// Package smodel converts a JSON Schema document into a form model tree
// consumed by the UI. Only a practical subset of keywords is form-ized;
// anything unsupported degrades to a raw JSON editor and is still enforced
// by save-time validation (internal/validate).
package smodel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"strings"

	"json-schema-editor/internal/fetch"
	"json-schema-editor/internal/icons"
	"json-schema-editor/internal/jsondoc"
)

// Kind classifies a schema node for the UI.
type Kind int

const (
	KScalar  Kind = iota // string/integer/number input
	KEnum                // select (single value from options)
	KBoolean             // confirm
	KObject              // section form with children
	KArray               // array manager
	KMap                 // string-keyed map (additionalProperties): rows are doc keys
	KOneOf               // variant picker
	KRaw                 // raw JSON textarea fallback
)

func (k Kind) String() string {
	switch k {
	case KScalar:
		return "scalar"
	case KEnum:
		return "enum"
	case KBoolean:
		return "boolean"
	case KObject:
		return "object"
	case KArray:
		return "array"
	case KMap:
		return "map"
	case KOneOf:
		return "oneOf"
	}
	return "raw"
}

// EnumOpt is one selectable option. Encoded is the canonical JSON literal
// of the value (numbers "3", booleans "true", strings "\"a\""), so mixed
// type enums stay collision-free.
type EnumOpt struct {
	Display string
	Encoded string
	Val     *jsondoc.Node
}

// FormNode is one editable schema node.
type FormNode struct {
	Key, Title, Desc string // Title fallback: title → key; root: "$"
	Kind             Kind
	JSONType         string // KScalar: "string"|"integer"|"number"

	Min, Max, MultipleOf       *float64
	ExclusiveMin, ExclusiveMax bool
	MinLen, MaxLen             *int
	Pattern                    string
	Enum                       []EnumOpt
	Default                    *jsondoc.Node
	Placeholder                string
	Required                   bool
	Children                   []*FormNode // KObject, in schema order
	Items                      *FormNode   // KArray elements, KMap values
	MinItems, MaxItems         int
	Variants                   []*FormNode // KOneOf
	TagRef                     string      // x-tag-reference: tag lookup domain (e.g. "http_client")
}

const maxDepth = 32

// refCtx tracks the document currently being normalized and its canonical
// location (for relative $ref resolution).
type refCtx struct {
	doc *jsondoc.Node
	loc string
}

type parser struct {
	docs   map[string]*jsondoc.Node
	memo   map[*jsondoc.Node]*FormNode
	onPath map[*jsondoc.Node]bool
}

// Load parses the schema at loc (local path, http(s):// or file:// URL) into
// a form model; it also returns the raw schema document.
func Load(loc string) (*FormNode, *jsondoc.Node, error) {
	canon, err := fetch.CanonicalLoc(loc)
	if err != nil {
		return nil, nil, err
	}
	p := &parser{
		docs:   map[string]*jsondoc.Node{},
		memo:   map[*jsondoc.Node]*FormNode{},
		onPath: map[*jsondoc.Node]bool{},
	}
	doc, err := p.cachedLoad(canon)
	if err != nil {
		return nil, nil, err
	}
	ctx := &refCtx{doc: doc, loc: canon}
	root := p.schema(doc, ctx, "$", false, 0)
	return root, doc, nil
}

// EncodeValue returns the canonical JSON literal of v as a string; it is
// the identity used to match select options against current values.
func EncodeValue(v *jsondoc.Node) string { return encodeValue(v) }

func (p *parser) cachedLoad(loc string) (*jsondoc.Node, error) {
	if d, ok := p.docs[loc]; ok {
		return d, nil
	}
	data, err := fetch.Load(loc)
	if err != nil {
		return nil, err
	}
	doc, err := jsondoc.Parse(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", loc, err)
	}
	p.docs[loc] = doc
	return doc, nil
}

// docBase returns the base URI of ctx: its $id if present (resolved against
// the fetch location), else the fetch location itself.
func docBase(ctx *refCtx) string {
	if idNode := field(ctx.doc, "$id"); idNode != nil && idNode.Str != nil && *idNode.Str != "" {
		if bu, err := url.Parse(ctx.loc); err == nil {
			if iu, err := url.Parse(*idNode.Str); err == nil {
				return bu.ResolveReference(iu).String()
			}
		}
		return *idNode.Str
	}
	return ctx.loc
}

// resolveRef resolves a $ref value against ctx, loading remote documents
// through the parser cache.
func (p *parser) resolveRef(ref string, ctx *refCtx, depth int) (*jsondoc.Node, *refCtx, error) {
	if strings.HasPrefix(ref, "#") {
		target, ok := ctx.doc.ResolveJSONPointer(ref)
		if !ok {
			return nil, nil, fmt.Errorf("unresolvable pointer %s", ref)
		}
		return target, ctx, nil
	}
	if depth > maxDepth {
		return nil, nil, fmt.Errorf("ref chain too deep")
	}
	baseU, err := url.Parse(docBase(ctx))
	if err != nil {
		return nil, nil, err
	}
	refU, err := url.Parse(ref)
	if err != nil {
		return nil, nil, err
	}
	abs := baseU.ResolveReference(refU)
	frag := abs.EscapedFragment()
	abs.Fragment = ""
	abs.RawFragment = ""
	docLoc := abs.String()
	doc, err := p.cachedLoad(docLoc)
	if err != nil {
		return nil, nil, err
	}
	target, ok := doc.ResolveJSONPointer(frag)
	if !ok {
		return nil, nil, fmt.Errorf("unresolvable pointer %s in %s", frag, docLoc)
	}
	return target, &refCtx{doc: doc, loc: docLoc}, nil
}

// schema normalizes one schema node into a FormNode.
func (p *parser) schema(n *jsondoc.Node, ctx *refCtx, key string, required bool, depth int) *FormNode {
	if depth > maxDepth {
		return &FormNode{Key: key, Title: titleFallback(key), Kind: KRaw, Desc: icons.Warn + " max schema depth exceeded, edit as JSON"}
	}
	if n == nil || n.Null || !(n.Object != nil || n.Bool != nil) {
		// Boolean schemas (true/false) and null: no form semantics.
		return &FormNode{Key: key, Title: titleFallback(key), Kind: KRaw}
	}
	if n.Bool != nil {
		// schema `true`/`false`: anything / nothing allowed.
		return &FormNode{Key: key, Title: titleFallback(key), Kind: KRaw, Desc: "boolean schema, edit as JSON"}
	}
	if p.onPath[n] {
		return &FormNode{Key: key, Title: titleFallback(key), Kind: KRaw, Desc: icons.Warn + " recursive $ref, edit as JSON"}
	}
	if fn, ok := p.memo[n]; ok {
		return forkNode(fn, key, required)
	}
	p.onPath[n] = true
	defer delete(p.onPath, n)

	fn := p.build(n, ctx, key, required, depth)
	// Memoize before applying the key fallback: the fallback title belongs
	// to the property that references this node, not to the node itself.
	// The same $ref target (e.g. sing-box's shared duration definitions) is
	// reachable under several property keys; storing the first key's
	// fallback would leak it as the title of every other referencing key.
	p.memo[n] = fn
	return forkNode(fn, key, required)
}

// forkNode returns a memoized node as one referencing property sees it: its
// key, fallback title and required flag are that property's, and a combo also
// re-keys its variants (nested combos and array items included) — a variant
// is a value of the referencing member, so it must not keep the key of the
// first property that happened to build the shared node.
func forkNode(fn *FormNode, key string, required bool) *FormNode {
	cp := *fn
	cp.Key = key
	cp.Required = required
	if cp.Title == "" {
		cp.Title = titleFallback(key)
	}
	cp.Variants = rekeyVariants(cp.Variants, key)
	return &cp
}

// rekeyVariants copies the variants of a shared combo for the referencing
// property; the slice is reused when every key already matches.
func rekeyVariants(vs []*FormNode, key string) []*FormNode {
	if !variantsNeedKey(vs, key) {
		return vs
	}
	out := make([]*FormNode, len(vs))
	for i, v := range vs {
		c := *v
		c.Key = key
		c.Variants = rekeyVariants(v.Variants, key)
		if c.Items != nil && c.Items.Key != key {
			it := *c.Items
			it.Key = key
			c.Items = &it
		}
		out[i] = &c
	}
	return out
}

// variantsNeedKey reports whether rekeyVariants would change anything.
func variantsNeedKey(vs []*FormNode, key string) bool {
	for _, v := range vs {
		if v.Key != key || (v.Items != nil && v.Items.Key != key) || variantsNeedKey(v.Variants, key) {
			return true
		}
	}
	return false
}

func titleFallback(key string) string {
	if key == "" || key == "$" {
		return "$"
	}
	return key
}

func (p *parser) build(n *jsondoc.Node, ctx *refCtx, key string, required bool, depth int) *FormNode {
	// 1. $ref: resolve, recurse, apply sibling title/description overrides.
	if refNode := field(n, "$ref"); refNode != nil && refNode.Str != nil {
		target, tctx, err := p.resolveRef(*refNode.Str, ctx, depth)
		if err != nil {
			return &FormNode{
				Key: key, Kind: KRaw, Required: required,
				Desc: appendWarn(descOf(n), icons.Warn+" ref unreachable: "+*refNode.Str),
			}
		}
		merged := p.schema(target, tctx, key, required, depth+1)
		if t := titleOf(n); t != "" {
			merged.Title = t
		}
		if d := descOf(n); d != "" {
			merged.Desc = d
		}
		return merged
	}

	// 2. allOf: shallow-merge into one synthetic schema, then normalize.
	if subs := arrayOf(n, "allOf"); len(subs) > 0 {
		merged := p.mergeAllOf(n, subs, ctx, depth)
		return p.schema(merged, ctx, key, required, depth+1)
	}

	fn := &FormNode{
		Key:      key,
		Title:    titleOf(n),
		Desc:     descOf(n),
		Required: required,
	}
	if d := field(n, "default"); d != nil && !d.Null {
		fn.Default = d
	}
	if ref := field(n, "x-tag-reference"); ref != nil && ref.Str != nil {
		fn.TagRef = *ref.Str
	}
	p.applyConstraints(n, fn)

	t, nullable := typeOf(n)
	hasProps := field(n, "properties") != nil && field(n, "properties").Object != nil
	hasItems := field(n, "items") != nil
	hasAdditional := field(n, "additionalProperties") != nil
	combo := arrayOf(n, "oneOf")
	if len(combo) == 0 {
		combo = arrayOf(n, "anyOf")
	}

	switch {
	case enumOf(n) != nil:
		fn.Kind = KEnum
		fn.Enum = enumOf(n)
	case t == "boolean":
		fn.Kind = KBoolean
	case len(combo) > 0 && !hasProps && t == "":
		fn.Kind, fn.Enum, fn.Variants = p.buildVariants(combo, ctx, key, depth)
	case t == "object" || hasProps || hasAdditional:
		if !hasProps && hasAdditional && usableAdditional(n) {
			// string-keyed map (sing-box HTTPHeader et al): rows come from
			// the document, every value edits against the value schema
			fn.Kind = KMap
			fn.Items = p.schema(field(n, "additionalProperties"), ctx, key, false, depth+1)
			break
		}
		fn.Kind = KObject
		fn.Children = p.buildChildren(n, ctx, depth)
	case t == "array" || hasItems:
		fn.Kind = KArray
		if items := field(n, "items"); items != nil {
			it := p.schema(items, ctx, key, false, depth+1)
			if it.Kind == KArray {
				it = &FormNode{Key: key, Title: it.Title, Kind: KRaw,
					Desc: appendWarn(it.Desc, "nested arrays not supported, edit as JSON")}
			}
			fn.Items = it
		}
		fn.MinItems = intOf(n, "minItems")
		fn.MaxItems = intOf(n, "maxItems")
	case t == "string" || t == "integer" || t == "number":
		fn.Kind = KScalar
		fn.JSONType = t
	default:
		fn.Kind = KRaw
	}
	if nullable && fn.Kind != KRaw {
		fn.Desc = appendWarn(fn.Desc, "may be omitted")
	}
	return fn
}

// usableAdditional reports whether additionalProperties shapes a map: an
// object schema (values built against it) or schema `true` (values of any
// shape). `false` closes the object instead — no map semantics.
func usableAdditional(n *jsondoc.Node) bool {
	ap := field(n, "additionalProperties")
	if ap == nil {
		return false
	}
	if ap.Bool != nil {
		return *ap.Bool
	}
	return ap.Object != nil
}

// buildVariants handles oneOf/anyOf: if every variant is a const or single
// value enum the whole thing collapses into one enum select; otherwise the
// UI gets a variant picker.
func (p *parser) buildVariants(combo []*jsondoc.Node, ctx *refCtx, key string, depth int) (Kind, []EnumOpt, []*FormNode) {
	var opts []EnumOpt
	var variants []*FormNode
	for _, vn := range combo {
		fn := p.schema(vn, ctx, key, false, depth+1)
		if fn.Kind != KEnum || len(fn.Enum) != 1 {
			variants = nil
			break
		}
		opts = append(opts, fn.Enum[0])
		variants = append(variants, fn)
	}
	if variants != nil && len(opts) > 0 {
		return KEnum, opts, nil
	}
	variants = nil
	for _, vn := range combo {
		fn := p.schema(vn, ctx, key, false, depth+1)
		if titleOf(vn) == "" {
			// no schema title: clear the key fallback too — the UI shows
			// the structural signature instead of a meaningless "variant N"
			fn.Title = ""
		}
		variants = append(variants, fn)
	}
	return KOneOf, nil, variants
}

func (p *parser) buildChildren(n *jsondoc.Node, ctx *refCtx, depth int) []*FormNode {
	props := field(n, "properties")
	if props == nil || props.Object == nil {
		return nil
	}
	reqSet := map[string]bool{}
	for _, r := range arrayOf(n, "required") {
		if r.Str != nil {
			reqSet[*r.Str] = true
		}
	}
	var children []*FormNode
	seen := map[string]bool{}
	for _, m := range *props.Object {
		// machine-generated schemas repeat keys inside one properties
		// object (older sing-box schemas repeat the dial-fields tables);
		// the document value always resolves to the first member, so later
		// duplicates would be dead rows — keep the first occurrence only
		if seen[m.Key] {
			continue
		}
		seen[m.Key] = true
		children = append(children, p.schema(m.Val, ctx, m.Key, reqSet[m.Key], depth+1))
	}
	return children
}

func (p *parser) applyConstraints(n *jsondoc.Node, fn *FormNode) {
	if v, ok := numField(n, "minimum"); ok {
		fn.Min = new(v)
	}
	if v, ok := numField(n, "maximum"); ok {
		fn.Max = new(v)
	}
	// exclusiveMinimum/Maximum: numeric (2020-12) acts as its own bound,
	// boolean (draft-07) modifies the sibling minimum/maximum.
	if v, ok := numField(n, "exclusiveMinimum"); ok {
		if fn.Min == nil || v > *fn.Min {
			fn.Min = new(v)
		}
		fn.ExclusiveMin = true
	} else if boolOf(n, "exclusiveMinimum") {
		fn.ExclusiveMin = true
	}
	if v, ok := numField(n, "exclusiveMaximum"); ok {
		if fn.Max == nil || v < *fn.Max {
			fn.Max = new(v)
		}
		fn.ExclusiveMax = true
	} else if boolOf(n, "exclusiveMaximum") {
		fn.ExclusiveMax = true
	}
	if v, ok := numField(n, "multipleOf"); ok {
		fn.MultipleOf = new(v)
	}
	if v, ok := intField(n, "minLength"); ok {
		fn.MinLen = new(v)
	}
	if v, ok := intField(n, "maxLength"); ok {
		fn.MaxLen = new(v)
	}
	if s := field(n, "pattern"); s != nil && s.Str != nil {
		fn.Pattern = *s.Str
	}
	if ex := arrayOf(n, "examples"); len(ex) > 0 && ex[0] != nil {
		switch {
		case ex[0].Str != nil:
			fn.Placeholder = *ex[0].Str
		case ex[0].Num != nil:
			fn.Placeholder = ex[0].Num.String()
		case ex[0].Bool != nil:
			fn.Placeholder = fmt.Sprintf("%v", *ex[0].Bool)
		}
	}
}

// mergeAllOf shallow-merges the node's own keywords with every allOf sub
// schema (sub $refs resolved first, nested allOf expanded) into one
// synthetic schema node.
func (p *parser) mergeAllOf(n *jsondoc.Node, subs []*jsondoc.Node, ctx *refCtx, depth int) *jsondoc.Node {
	if depth > maxDepth {
		return n
	}
	list := []*jsondoc.Node{n}
	for _, sub := range subs {
		cur, cctx := sub, ctx
		for hops := 0; cur != nil && cur.Object != nil; hops++ {
			if hops > maxDepth {
				break
			}
			refNode := field(cur, "$ref")
			if refNode == nil || refNode.Str == nil {
				break
			}
			target, tctx, err := p.resolveRef(*refNode.Str, cctx, depth+hops)
			if err != nil {
				cur = nil
				break
			}
			cur, cctx = target, tctx
		}
		if cur != nil && cur.Object != nil {
			if inner := arrayOf(cur, "allOf"); len(inner) > 0 {
				cur = p.mergeAllOf(cur, inner, cctx, depth+1)
			}
			list = append(list, cur)
		}
	}
	return p.mergeParts(list, depth)
}

// maxComboProduct caps the cartesian product of the oneOf/anyOf lists that
// meet inside an allOf merge; beyond it the combo stays a plain member.
const maxComboProduct = 128

// mergeParts shallow-merges the member maps of object schemas: properties
// concatenate (later parts win key conflicts), required unions, numeric
// bounds keep the stricter side, enums intersect, every other keyword takes
// the first non-empty value. oneOf/anyOf members are withheld from the
// plain merge and distributed over their variants when the shapes are
// object-compatible — rest ∧ (v1 ∨ v2 …) ≡ (rest ∧ v1) ∨ (rest ∧ v2) … —
// which is how sing-box composes Rule = matchers allOf RuleAction; the UI
// flattens the resulting nested variants (dynamicVariants). A combo that
// cannot be distributed (scalar variant against an object rest, bare $ref
// variant, cartesian explosion, depth cap) stays a plain member exactly as
// before: save-time validation remains the backstop.
func (p *parser) mergeParts(parts []*jsondoc.Node, depth int) *jsondoc.Node {
	members := []jsondoc.Member{}
	set := func(k string, v *jsondoc.Node) {
		for i := range members {
			if members[i].Key == k {
				members[i].Val = v
				return
			}
		}
		members = append(members, jsondoc.Member{Key: k, Val: v})
	}
	get := func(k string) *jsondoc.Node {
		for i := range members {
			if members[i].Key == k {
				return members[i].Val
			}
		}
		return nil
	}
	setIfAbsent := func(k string, v *jsondoc.Node) {
		if get(k) == nil {
			set(k, v)
		}
	}

	type combo struct {
		key  string
		vals []*jsondoc.Node
	}
	var combos []combo
	for _, part := range parts {
		for _, m := range *part.Object {
			switch m.Key {
			case "allOf", "$schema", "$id", "$comment":
				// dropped
			case "oneOf", "anyOf":
				if m.Val.Array != nil && len(*m.Val.Array) > 0 {
					combos = append(combos, combo{key: m.Key, vals: *m.Val.Array})
				}
			case "properties":
				var base []jsondoc.Member
				if old := get("properties"); old != nil && old.Object != nil {
					base = *old.Object
				}
				if m.Val.Object != nil {
					for _, pm := range *m.Val.Object {
						replaced := false
						for i := range base {
							if base[i].Key == pm.Key {
								base[i].Val = pm.Val // later wins
								replaced = true
								break
							}
						}
						if !replaced {
							base = append(base, pm)
						}
					}
				}
				set("properties", &jsondoc.Node{Object: &base})
			case "required":
				var base []*jsondoc.Node
				if old := get("required"); old != nil && old.Array != nil {
					base = *old.Array
				}
				if m.Val.Array != nil {
					for _, rv := range *m.Val.Array {
						found := false
						for _, b := range base {
							if b.Str != nil && rv.Str != nil && *b.Str == *rv.Str {
								found = true
								break
							}
						}
						if !found {
							base = append(base, rv)
						}
					}
				}
				set("required", &jsondoc.Node{Array: &base})
			case "minimum", "minLength", "exclusiveMinimum":
				merged := stricterNum(get(m.Key), m.Val, m.Key)
				if merged != nil {
					set(m.Key, merged)
				}
			case "maximum", "maxLength", "exclusiveMaximum":
				merged := stricterNum(get(m.Key), m.Val, m.Key)
				if merged != nil {
					set(m.Key, merged)
				}
			case "enum":
				old := get("enum")
				if old == nil {
					set("enum", m.Val)
					break
				}
				inter := intersectEnum(old, m.Val)
				set("enum", inter)
			case "type", "title", "description", "default", "examples", "pattern":
				if old := get(m.Key); old == nil || isEmptyNode(old) {
					set(m.Key, m.Val)
				}
			default:
				setIfAbsent(m.Key, m.Val)
			}
		}
	}
	base := &jsondoc.Node{Object: &members}
	if len(combos) == 0 {
		return base
	}
	keep := func() *jsondoc.Node {
		setIfAbsent(combos[0].key, &jsondoc.Node{Array: &combos[0].vals})
		return base
	}
	distributable := field(base, "properties") == nil // an unshaped rest merges into anything
	if !distributable {
		distributable = true
		for _, c := range combos {
			for _, v := range c.vals {
				if !objectish(v) {
					distributable = false
				}
			}
		}
	}
	if depth >= maxDepth || !distributable {
		return keep()
	}
	tuples := [][]*jsondoc.Node{{}}
	for _, c := range combos {
		var next [][]*jsondoc.Node
		for _, t := range tuples {
			for _, v := range c.vals {
				next = append(next, append(append([]*jsondoc.Node{}, t...), v))
			}
		}
		tuples = next
		if len(tuples) > maxComboProduct {
			return keep()
		}
	}
	variants := make([]*jsondoc.Node, 0, len(tuples))
	for _, t := range tuples {
		variants = append(variants, p.mergeParts(append([]*jsondoc.Node{base}, t...), depth+1))
	}
	return &jsondoc.Node{Object: &[]jsondoc.Member{
		{Key: combos[0].key, Val: &jsondoc.Node{Array: &variants}},
	}}
}

// objectish reports whether n shapes an object value the way build's
// object branch does: declared properties or an object type entry.
func objectish(n *jsondoc.Node) bool {
	if n == nil || n.Object == nil {
		return false
	}
	if field(n, "properties") != nil {
		return true
	}
	t := field(n, "type")
	if t == nil {
		return false
	}
	if t.Str != nil && *t.Str == "object" {
		return true
	}
	if t.Array != nil {
		for _, e := range *t.Array {
			if e.Str != nil && *e.Str == "object" {
				return true
			}
		}
	}
	return false
}

func isEmptyNode(n *jsondoc.Node) bool {
	return n == nil || (n.Object != nil && len(*n.Object) == 0) ||
		(n.Array != nil && len(*n.Array) == 0) ||
		(n.Str != nil && *n.Str == "") || n.Null
}

// stricterNum keeps the stricter of two numeric constraints (bigger min,
// smaller max); booleans OR together via numeric encoding 1/0.
func stricterNum(old, cur *jsondoc.Node, key string) *jsondoc.Node {
	if old == nil {
		return cur
	}
	if cur == nil {
		return old
	}
	ov, ok1 := nodeNum(old)
	cv, ok2 := nodeNum(cur)
	if !ok1 {
		return cur
	}
	if !ok2 {
		return old
	}
	switch key {
	case "minimum", "minLength", "exclusiveMinimum":
		if cv > ov {
			return cur
		}
		return old
	case "maximum", "maxLength", "exclusiveMaximum":
		if cv < ov {
			return cur
		}
		return old
	}
	return old
}

func intersectEnum(a, b *jsondoc.Node) *jsondoc.Node {
	var out []*jsondoc.Node
	if a.Array == nil || b.Array == nil {
		return &jsondoc.Node{Array: &out}
	}
	for _, av := range *a.Array {
		for _, bv := range *b.Array {
			if sameValue(av, bv) {
				out = append(out, av)
				break
			}
		}
	}
	return &jsondoc.Node{Array: &out}
}

func sameValue(a, b *jsondoc.Node) bool {
	return encodeValue(a) == encodeValue(b)
}

// --- jsondoc accessors -------------------------------------------------

func field(n *jsondoc.Node, key string) *jsondoc.Node {
	if n == nil || n.Object == nil {
		return nil
	}
	for _, m := range *n.Object {
		if m.Key == key {
			return m.Val
		}
	}
	return nil
}

func arrayOf(n *jsondoc.Node, key string) []*jsondoc.Node {
	if a := field(n, key); a != nil && a.Array != nil {
		return *a.Array
	}
	return nil
}

func titleOf(n *jsondoc.Node) string {
	if s := field(n, "title"); s != nil && s.Str != nil {
		return *s.Str
	}
	return ""
}

func descOf(n *jsondoc.Node) string {
	if s := field(n, "description"); s != nil && s.Str != nil {
		return *s.Str
	}
	return ""
}

func boolOf(n *jsondoc.Node, key string) bool {
	if b := field(n, key); b != nil && b.Bool != nil {
		return *b.Bool
	}
	return false
}

func intOf(n *jsondoc.Node, key string) int {
	if v, ok := intField(n, key); ok {
		return v
	}
	return 0
}

func numField(n *jsondoc.Node, key string) (float64, bool) {
	if v := field(n, key); v != nil {
		return nodeNum(v)
	}
	return 0, false
}

func intField(n *jsondoc.Node, key string) (int, bool) {
	f, ok := numField(n, key)
	if !ok {
		return 0, false
	}
	if f != math.Trunc(f) {
		return 0, false
	}
	return int(f), true
}

func nodeNum(n *jsondoc.Node) (float64, bool) {
	if n == nil {
		return 0, false
	}
	if n.Num != nil {
		f, err := n.Num.Float64()
		return f, err == nil
	}
	if n.Bool != nil {
		if *n.Bool {
			return 1, true
		}
		return 0, true
	}
	return 0, false
}

// typeOf returns the effective scalar type name (first non-null entry when
// type is an array) and whether null is allowed.
func typeOf(n *jsondoc.Node) (string, bool) {
	t := field(n, "type")
	if t == nil {
		return "", false
	}
	if t.Str != nil {
		return *t.Str, false
	}
	if t.Array == nil {
		return "", false
	}
	name, nullable := "", false
	for _, e := range *t.Array {
		if e.Str == nil {
			continue
		}
		if *e.Str == "null" {
			nullable = true
			continue
		}
		if name == "" {
			name = *e.Str
		}
	}
	return name, nullable
}

// enumOf returns options for enum/const schemas, nil if not an enum.
func enumOf(n *jsondoc.Node) []EnumOpt {
	if e := field(n, "enum"); e != nil && e.Array != nil {
		var opts []EnumOpt
		for _, v := range *e.Array {
			opts = append(opts, EnumOpt{
				Display: displayValue(v),
				Encoded: encodeValue(v),
				Val:     v,
			})
		}
		return opts
	}
	if c := field(n, "const"); c != nil {
		return []EnumOpt{{Display: displayValue(c), Encoded: encodeValue(c), Val: c}}
	}
	return nil
}

func displayValue(v *jsondoc.Node) string {
	switch {
	case v == nil:
		return "null"
	case v.Str != nil:
		return *v.Str
	case v.Num != nil:
		return v.Num.String()
	case v.Bool != nil:
		if *v.Bool {
			return "true"
		}
		return "false"
	default:
		return encodeValue(v)
	}
}

// encodeValue is the canonical JSON literal of a value as a string.
func encodeValue(v *jsondoc.Node) string {
	if v == nil {
		return "null"
	}
	b, err := json.Marshal(v.ToAny())
	if err != nil {
		return ""
	}
	return string(b)
}

func appendWarn(desc, warn string) string {
	if desc == "" {
		return warn
	}
	return desc + " " + warn
}
