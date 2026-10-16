package ui

// menuconfig row model: the pure logic behind the menu TUI — row lists per
// menu state, value summaries, variant signatures and navigation-stack
// reconstruction. No bubbletea imports here so it stays unit-testable.

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"json-schema-editor/internal/jsondoc"
	"json-schema-editor/internal/smodel"
)

// menuKind classifies one screen of the menu tree.
type menuKind int

const (
	menuObject    menuKind = iota // KObject: field + drill rows
	menuItems                     // KArray: add + per-item rows
	menuEnumArray                 // KArray with enum items: toggle rows
	menuOneOf                     // KOneOf: variant rows
)

// menuState is one level of the navigation stack; cursor/offset are
// remembered so popping restores the exact view. variantOf is non-nil when
// this is the dynamic item menu of an all-object oneOf: node then switches
// between the merged union menu and the matched variant's menu as the value
// at path disambiguates. oneOf is non-nil on the top-level editor menu of a
// classic oneOf variant (object/array): ctrl+t offers the type switch.
type menuState struct {
	kind           menuKind
	node           *smodel.FormNode
	path           []any
	cursor, offset int
	variantOf      *smodel.FormNode
	oneOf          *smodel.FormNode // classic variant editor: ctrl+t type switch
}

// rowKind classifies one rendered line inside a menu.
type rowKind int

const (
	rowField   rowKind = iota // scalar/enum/bool/raw: edit in place
	rowDrill                  // object/array/oneOf: push child menu
	rowToggle                 // enum-array member checkbox
	rowAdd                    // "< Add item >"
	rowRemove                 // "< Remove this item >"
	rowSave                   // "< Save and exit >"
	rowRaw                    // "< Edit raw JSON >"
	rowVariant                // oneOf variant picker line
	rowNoItems                // placeholder for empty menus
	rowUnknown                // document key the schema does not define
	rowIssues                 // "⚠ N validation errors" status row (root)
)

// row is one line of a menu. fn/path drive editing and drill-down; idx is
// the array index for item rows and the option index for toggle rows (-1
// otherwise); variant points at the FormNode of a oneOf variant; label is
// the fixed text of synthetic rows (and the display text of search hits).
type row struct {
	kind    rowKind
	fn      *smodel.FormNode
	path    []any
	idx     int
	variant *smodel.FormNode
	label   string
}

// menuKindFor returns the menu kind a container node drills into.
func menuKindFor(fn *smodel.FormNode) menuKind {
	switch fn.Kind {
	case smodel.KOneOf:
		return menuOneOf
	case smodel.KArray:
		if fn.Items != nil && fn.Items.Kind == smodel.KEnum {
			return menuEnumArray
		}
		return menuItems
	default:
		return menuObject
	}
}

// menuLeaf reports whether a node edits in place (no child menu of its own).
func menuLeaf(fn *smodel.FormNode) bool {
	switch fn.Kind {
	case smodel.KScalar, smodel.KEnum, smodel.KBoolean, smodel.KRaw:
		return true
	case smodel.KArray:
		return fn.Items == nil || fn.Items.Kind == smodel.KRaw
	}
	return false
}

// menuRows builds the row list for the current (top) menu state.
func menuRows(lm *listModel) []row { return menuRowsFor(lm, lm.top()) }

// menuRowsFor builds the row list for one menu state: the top one in
// menuRows, any state for cursor bookkeeping during variant flips.
func menuRowsFor(lm *listModel, st *menuState) []row {
	if st == nil {
		return nil
	}
	var rows []row
	// A menu for an array item (path ends with an index) offers removal.
	if len(st.path) > 0 && st.kind != menuEnumArray {
		if _, ok := st.path[len(st.path)-1].(int); ok {
			rows = append(rows, row{kind: rowRemove, path: clonePath(st.path),
				idx: -1, label: "< Remove this item >"})
		}
	}
	switch st.kind {
	case menuObject:
		if st.node.Kind == smodel.KObject {
			known := map[string]bool{}
			for _, c := range st.node.Children {
				known[c.Key] = true
				rows = append(rows, childRow(st, c))
			}
			// document members the schema does not define stay visible (raw
			// JSON editable) so they can be repaired or removed
			if lm.doc != nil {
				if cur, ok := lm.doc.Get(st.path); ok && cur != nil && cur.Object != nil {
					for _, mem := range *cur.Object {
						if !known[mem.Key] {
							rows = append(rows, row{kind: rowUnknown, idx: -1,
								path: pathFor(st.path, mem.Key), label: mem.Key})
						}
					}
				}
			}
		} else {
			// scalar/enum/bool/raw root schema: the root itself is the field
			rows = append(rows, row{kind: rowField, fn: st.node,
				path: clonePath(st.path), idx: -1})
		}
	case menuItems:
		rows = append(rows, row{kind: rowAdd, label: "< Add item >", idx: -1})
		if cur, ok := lm.doc.Get(st.path); ok && cur.Array != nil {
			for i := range *cur.Array {
				rows = append(rows, itemRow(st.node, st.path, i))
			}
		}
	case menuEnumArray:
		if st.node.Items == nil || len(st.node.Items.Enum) == 0 {
			rows = append(rows, row{kind: rowNoItems, idx: -1,
				label: "( no options )"})
		} else {
			for i := range st.node.Items.Enum {
				rows = append(rows, row{kind: rowToggle, fn: st.node,
					path: clonePath(st.path), idx: i})
			}
		}
	case menuOneOf:
		for _, v := range st.node.Variants {
			rows = append(rows, row{kind: rowVariant, variant: v,
				path: clonePath(st.path), idx: -1})
		}
	}
	if len(rows) == 0 {
		rows = append(rows, row{kind: rowNoItems, idx: -1, label: "( nothing here )"})
	}
	if len(lm.stack) > 0 && lm.stack[0] == st && len(lm.issuesNow) > 0 {
		errs, warns := 0, 0
		for _, it := range lm.issuesNow {
			if it.warn {
				warns++
			} else {
				errs++
			}
		}
		rows = append([]row{{kind: rowIssues, idx: -1, label: issueStatusLabel(errs, warns)}}, rows...)
	}
	if len(lm.stack) > 0 && lm.stack[0] == st {
		rows = append(rows,
			row{kind: rowSave, label: "< Save and exit >", idx: -1},
			row{kind: rowRaw, label: "< Edit raw JSON >", idx: -1})
	}
	return rows
}

// childRow maps one object child to a field or drill row.
func childRow(st *menuState, c *smodel.FormNode) row {
	p := pathFor(st.path, c.Key)
	if menuLeaf(c) {
		return row{kind: rowField, fn: c, path: p, idx: -1}
	}
	return row{kind: rowDrill, fn: c, path: p, idx: -1}
}

// itemRow maps array element i to an editable or drillable row.
func itemRow(node *smodel.FormNode, parentPath []any, i int) row {
	p := pathIdx(parentPath, i)
	items := node.Items
	if items == nil {
		// items schema unknown: fall back to raw editing of the element
		items = &smodel.FormNode{Key: node.Key, Title: node.Title, Kind: smodel.KRaw}
	}
	if menuLeaf(items) {
		return row{kind: rowField, fn: items, path: p, idx: i}
	}
	return row{kind: rowDrill, fn: items, path: p, idx: i}
}

// summary renders the right-hand value column for fn at path: the current
// doc value, or the effective default when unset, "—" when neither exists.
func summary(lm *listModel, fn *smodel.FormNode, path []any) string {
	v, ok := lm.doc.Get(path)
	if !ok || v == nil || v.Null {
		if fn.Default != nil && !fn.Default.Null {
			v = fn.Default
		} else {
			return "—"
		}
	}
	switch fn.Kind {
	case smodel.KScalar:
		switch {
		case v.Str != nil:
			return valueClip(*v.Str)
		case v.Num != nil:
			return v.Num.String()
		case v.Bool != nil:
			return fmt.Sprintf("%v", *v.Bool)
		}
		return "—"
	case smodel.KBoolean:
		if v.Bool != nil && *v.Bool {
			return "[y]"
		}
		return "[n]"
	case smodel.KEnum:
		enc := smodel.EncodeValue(v)
		for _, o := range fn.Enum {
			if o.Encoded == enc {
				return o.Display
			}
		}
		if s := scalarText(v); s != "" {
			return s
		}
		return "—"
	case smodel.KObject:
		return objectSummary(fn, v)
	case smodel.KArray:
		return arraySummary(v)
	case smodel.KOneOf:
		idx := currentVariant(fn, v)
		if idx < 0 {
			return "—"
		}
		variant := fn.Variants[idx]
		switch variant.Kind {
		case smodel.KObject:
			// a real title gives the object context; untitled variants
			// show the members alone (no meaningless fallback label)
			if variant.Title != "" {
				return variant.Title + " " + objectSummary(variant, v)
			}
			return objectSummary(variant, v)
		case smodel.KEnum:
			enc := smodel.EncodeValue(v)
			for _, o := range variant.Enum {
				if o.Encoded == enc {
					return o.Display
				}
			}
		case smodel.KBoolean:
			if v.Bool != nil && !*v.Bool {
				return "[n]"
			}
			return "[y]"
		case smodel.KArray:
			return arraySummary(v)
		case smodel.KScalar:
			// leaf variants show their value, like any scalar row
			if s := scalarText(v); s != "" {
				return valueClip(s)
			}
		}
		if variant.Title != "" {
			return variant.Title
		}
		return "—"
	default: // KRaw
		return "json " + clipRunes(smodel.EncodeValue(v), 20)
	}
}

// objectSummary shows the first two object members as k=v (nested values as
// {…}/[N]), then " +N" for the rest; "{}" when empty. Members render in the
// schema's declared order (fn.Children) so every row of a list summarizes
// alike regardless of how the document ordered its keys; keys the schema
// does not know keep document order after the known ones. fn may be nil
// (schema-less values): plain document order then.
func objectSummary(fn *smodel.FormNode, n *jsondoc.Node) string {
	if n == nil {
		return "{}"
	}
	if n.Object == nil {
		return clipRunes(smodel.EncodeValue(n), 24)
	}
	mem := *n.Object
	if len(mem) == 0 {
		return "{}"
	}
	rank := map[string]int{}
	if fn != nil {
		for i, c := range fn.Children {
			if _, ok := rank[c.Key]; !ok {
				rank[c.Key] = i
			}
		}
	}
	ordered := append([]jsondoc.Member(nil), mem...) // never reorder the doc
	sort.SliceStable(ordered, func(i, j int) bool {
		ri, ok := rank[ordered[i].Key]
		if !ok {
			ri = len(rank)
		}
		rj, ok := rank[ordered[j].Key]
		if !ok {
			rj = len(rank)
		}
		return ri < rj
	})
	parts := make([]string, 0, 3)
	for i := 0; i < len(ordered) && i < 2; i++ {
		m := ordered[i]
		switch {
		case m.Val == nil || m.Val.Null:
			parts = append(parts, m.Key+"=null")
		case m.Val.Object != nil:
			parts = append(parts, m.Key+"={…}")
		case m.Val.Array != nil:
			parts = append(parts, fmt.Sprintf("%s=[%d]", m.Key, len(*m.Val.Array)))
		case m.Val.Str != nil:
			parts = append(parts, m.Key+"="+valueClip(*m.Val.Str))
		case m.Val.Num != nil:
			parts = append(parts, m.Key+"="+m.Val.Num.String())
		case m.Val.Bool != nil:
			parts = append(parts, fmt.Sprintf("%s=%v", m.Key, *m.Val.Bool))
		}
	}
	s := strings.Join(parts, " ")
	if len(mem) > 2 {
		s += fmt.Sprintf(" +%d", len(mem)-2)
	}
	return s
}

// valueClip truncates a scalar value for the summary column (24 runes,
// ellipsis included).
func valueClip(s string) string { return clipRunes(s, 24) }

// arraySummary renders an array value column: any list that is not purely
// object-valued previews its members ("[4] a 1 {…} +1" — bracketed count,
// first values space-separated, "+N" for the rest; objects as {…}, nested
// arrays as [N]), pure object arrays just count ("N items"), "empty" when
// unset.
func arraySummary(v *jsondoc.Node) string {
	if v == nil || v.Array == nil {
		return "—"
	}
	arr := *v.Array
	if len(arr) == 0 {
		return "empty"
	}
	label := fmt.Sprintf("%d item", len(arr))
	if len(arr) != 1 {
		label += "s"
	}
	pureObjects := true
	for _, it := range arr {
		if it == nil || it.Object == nil {
			pureObjects = false
			break
		}
	}
	if pureObjects {
		return label
	}
	const preview = 3
	parts := make([]string, 0, preview)
	for i := 0; i < len(arr) && i < preview; i++ {
		parts = append(parts, arrayItemBrief(arr[i]))
	}
	s := fmt.Sprintf("[%d] ", len(arr)) + strings.Join(parts, " ")
	if len(arr) > preview {
		s += fmt.Sprintf(" +%d", len(arr)-preview)
	}
	return s
}

// arrayItemBrief renders one array member for the preview column.
func arrayItemBrief(n *jsondoc.Node) string {
	switch {
	case n == nil || n.Null:
		return "null"
	case n.Object != nil:
		return "{…}"
	case n.Array != nil:
		return fmt.Sprintf("[%d]", len(*n.Array))
	case n.Str != nil:
		return clipRunes(*n.Str, 16)
	case n.Num != nil:
		return n.Num.String()
	case n.Bool != nil:
		return fmt.Sprintf("%v", *n.Bool)
	}
	return clipRunes(smodel.EncodeValue(n), 16)
}

// clipRunes truncates s to max runes, the last being an ellipsis.
func clipRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}

// VariantSignature renders the structural signature shown on oneOf variant
// rows and search hits: "{a, b*}" for objects (a required const child shows
// its fixed value instead, e.g. "type=direct"), type names for scalars,
// "a |b |c" option lists for enums, "yes/no", "list of <sig>" and "json".
func VariantSignature(fn *smodel.FormNode) string {
	if fn == nil {
		return "json"
	}
	switch fn.Kind {
	case smodel.KObject:
		names := make([]string, 0, len(fn.Children))
		for _, c := range fn.Children {
			var n string
			switch {
			case c.Required && c.Kind == smodel.KEnum && len(c.Enum) == 1:
				n = c.Key + "=" + c.Enum[0].Display
			case c.Required:
				n = c.Key + "*"
			default:
				n = c.Key
			}
			names = append(names, n)
		}
		if len(names) == 0 {
			return "{}"
		}
		if len(names) > 4 {
			return "{" + strings.Join(names[:4], ", ") + fmt.Sprintf(", +%d}", len(names)-4)
		}
		return "{" + strings.Join(names, ", ") + "}"
	case smodel.KScalar:
		if fn.JSONType == "" {
			return "string"
		}
		return fn.JSONType
	case smodel.KEnum:
		ds := make([]string, 0, len(fn.Enum))
		for _, o := range fn.Enum {
			ds = append(ds, o.Display)
		}
		if len(ds) == 0 {
			return "enum"
		}
		if len(ds) > 3 {
			return strings.Join(ds[:3], " |") + " …"
		}
		return strings.Join(ds, " |")
	case smodel.KBoolean:
		return "yes/no"
	case smodel.KArray:
		if fn.Items == nil {
			return "list of json"
		}
		return "list of " + VariantSignature(fn.Items)
	default:
		return "json"
	}
}

// currentVariant returns the index of the first variant the value at path
// matches, or -1. Matching is value-aware: enum variants match by encoded
// value, object variants when every shared key's value conforms to the
// variant's field schema (variants that require nothing match any object).
func currentVariant(node *smodel.FormNode, val *jsondoc.Node) int {
	if val == nil || val.Null || node == nil {
		return -1
	}
	for i, v := range node.Variants {
		if variantMatches(v, val) {
			return i
		}
	}
	return -1
}

// seedVariantCursor points a freshly built oneOf picker state at the variant
// matching the current value (the ● row); unset values stay on row 0.
func seedVariantCursor(st *menuState, doc *jsondoc.Node) {
	if st == nil || st.kind != menuOneOf || doc == nil {
		return
	}
	v, ok := doc.Get(st.path)
	if !ok {
		return
	}
	if idx := currentVariant(st.node, v); idx >= 0 {
		st.cursor = idx
	}
}

// displayTitle is the header/label for a node whose schema gave it no title
// (untitled oneOf variants): the structural signature stands in, matching
// what variant rows already show.
func displayTitle(fn *smodel.FormNode) string {
	if fn.Title != "" {
		return fn.Title
	}
	return VariantSignature(fn)
}

// dynamicVariants flattens oneOf variant groups (nested oneOfs, as in
// sing-box's inbound schema) into their object leaves; nil when any leaf is
// not an object — the classic variant picker applies instead.
func dynamicVariants(node *smodel.FormNode) []*smodel.FormNode {
	if node == nil || node.Kind != smodel.KOneOf || len(node.Variants) == 0 {
		return nil
	}
	var out []*smodel.FormNode
	var add func(vs []*smodel.FormNode) bool
	add = func(vs []*smodel.FormNode) bool {
		for _, v := range vs {
			switch v.Kind {
			case smodel.KObject:
				out = append(out, v)
			case smodel.KOneOf:
				if !add(v.Variants) {
					return false
				}
			default:
				return false
			}
		}
		return true
	}
	if !add(node.Variants) {
		return nil
	}
	return out
}

// matchingVariants returns the candidate variants for val: all variants
// while the value is unset or an empty object, otherwise those the value
// conforms to (see variantMatches).
func matchingVariants(node *smodel.FormNode, val *jsondoc.Node) []*smodel.FormNode {
	if node == nil || node.Kind != smodel.KOneOf {
		return nil
	}
	return matchVariantList(node.Variants, val)
}

// matchVariantList is matchingVariants over an explicit variant list
// (dynamic menus pass the flattened leaves).
func matchVariantList(vs []*smodel.FormNode, val *jsondoc.Node) []*smodel.FormNode {
	if vs == nil {
		return nil
	}
	if val == nil || val.Null || (val.Object != nil && len(*val.Object) == 0) {
		return append([]*smodel.FormNode{}, vs...)
	}
	var out []*smodel.FormNode
	for _, v := range vs {
		if variantMatches(v, val) {
			out = append(out, v)
		}
	}
	return out
}

// variantMatches reports whether val fits one variant: enums by encoded
// value; objects when the variant requires nothing (any object matches) or
// at least one key overlaps and every overlapping key's value conforms;
// nested oneOf groups match when any sub-variant does. Keys the variant
// does not define are ignored.
func variantMatches(v *smodel.FormNode, val *jsondoc.Node) bool {
	switch v.Kind {
	case smodel.KOneOf:
		return len(matchVariantList(v.Variants, val)) > 0
	case smodel.KEnum:
		enc := smodel.EncodeValue(val)
		for _, o := range v.Enum {
			if o.Encoded == enc {
				return true
			}
		}
		return false
	case smodel.KObject:
		if val == nil || val.Object == nil {
			return false
		}
		defs := map[string]*smodel.FormNode{}
		required := false
		for _, c := range v.Children {
			defs[c.Key] = c
			if c.Required {
				required = true
			}
		}
		if !required {
			return true
		}
		overlap := false
		for _, m := range *val.Object {
			def, ok := defs[m.Key]
			if !ok {
				continue
			}
			overlap = true
			if !valueConforms(def, m.Val) {
				return false
			}
		}
		return overlap
	case smodel.KScalar, smodel.KBoolean, smodel.KArray, smodel.KRaw:
		// leaf variants (e.g. a plain string alternative) match by shape
		return valueConforms(v, val)
	default:
		return false
	}
}

// valueConforms reports whether the value fits the field definition at the
// kind level: enums by encoded option, scalars/booleans/objects/arrays by
// JSON shape. Raw definitions accept anything.
func valueConforms(def *smodel.FormNode, v *jsondoc.Node) bool {
	if def == nil {
		return true
	}
	switch def.Kind {
	case smodel.KEnum:
		enc := smodel.EncodeValue(v)
		for _, o := range def.Enum {
			if o.Encoded == enc {
				return true
			}
		}
		return false
	case smodel.KBoolean:
		return v != nil && v.Bool != nil
	case smodel.KObject:
		return v != nil && v.Object != nil
	case smodel.KArray:
		return v != nil && v.Array != nil
	case smodel.KScalar:
		if v == nil {
			return false
		}
		switch def.JSONType {
		case "integer", "number":
			return v.Num != nil
		case "string":
			return v.Str != nil
		default:
			return v.Str != nil || v.Num != nil || v.Bool != nil
		}
	default:
		return true
	}
}

// variantFieldDefs collects every field key's definitions across the
// variant list, in first-appearance order.
func variantFieldDefs(variants []*smodel.FormNode) (order []string, defs map[string][]*smodel.FormNode) {
	defs = map[string][]*smodel.FormNode{}
	for _, v := range variants {
		for _, c := range v.Children {
			if _, seen := defs[c.Key]; !seen {
				order = append(order, c.Key)
			}
			defs[c.Key] = append(defs[c.Key], c)
		}
	}
	return order, defs
}

// unionField merges one key's definitions across variants. Const fields
// (single-option enums) with differing values become a union enum marked as
// a discriminator; otherwise same-kind definitions take the first, and kind
// conflicts drop the key (nil).
func unionField(key string, ds []*smodel.FormNode) (fld *smodel.FormNode, disc bool) {
	allConst, differs := true, false
	for i, d := range ds {
		if d.Kind != smodel.KEnum || len(d.Enum) != 1 {
			allConst = false
			break
		}
		if i > 0 && d.Enum[0].Encoded != ds[0].Enum[0].Encoded {
			differs = true
		}
	}
	if allConst && differs {
		union := &smodel.FormNode{Kind: smodel.KEnum, Key: key, Title: ds[0].Title, Required: true}
		seen := map[string]bool{}
		for _, d := range ds {
			o := d.Enum[0]
			if !seen[o.Encoded] {
				seen[o.Encoded] = true
				union.Enum = append(union.Enum, o)
			}
			union.Required = union.Required && d.Required
		}
		return union, true
	}
	for _, d := range ds[1:] {
		if d.Kind != ds[0].Kind {
			return nil, false
		}
	}
	return ds[0], false
}

// mergedOneOfMenu synthesizes the union-menu node over the variant list:
// the merge of every variant's fields (discriminator keys as union enums,
// same-kind keys as the first definition, conflicting keys dropped).
func mergedOneOfMenu(node *smodel.FormNode, variants []*smodel.FormNode) *smodel.FormNode {
	merged := &smodel.FormNode{Kind: smodel.KObject, Key: node.Key, Title: node.Title}
	order, defs := variantFieldDefs(variants)
	for _, k := range order {
		if fld, _ := unionField(k, defs[k]); fld != nil {
			merged.Children = append(merged.Children, fld)
		}
	}
	return merged
}

// pinnedOneOfMenu returns the variant's node with discriminator keys
// replaced by the union enum, so the variant can be switched in place.
func pinnedOneOfMenu(variants []*smodel.FormNode, variant *smodel.FormNode) *smodel.FormNode {
	_, defs := variantFieldDefs(variants)
	out := &smodel.FormNode{Kind: smodel.KObject, Key: variant.Key, Title: variant.Title}
	for _, c := range variant.Children {
		if fld, disc := unionField(c.Key, defs[c.Key]); disc && fld != nil {
			out.Children = append(out.Children, fld)
		} else {
			out.Children = append(out.Children, c)
		}
	}
	return out
}

// rebuildStack reconstructs the navigation stack from the root down to the
// menu that contains the row for path (leaf segments are not pushed: the
// row lives in its parent menu). String segments resolve against object
// children; when the current node is a oneOf the current variant's subtree
// (per the doc value) continues the walk. int segments land on the array
// item menu. Unresolvable segments end the walk early.
func rebuildStack(root *smodel.FormNode, doc *jsondoc.Node, path []any) []*menuState {
	stack := []*menuState{{kind: menuKindFor(root), node: root, path: nil}}
	cur, curPath := root, []any{}
	for _, seg := range path {
		switch s := seg.(type) {
		case string:
			if cur.Kind == smodel.KOneOf {
				v, _ := doc.Get(curPath)
				idx := currentVariant(cur, v)
				if idx < 0 {
					return stack
				}
				variant := cur.Variants[idx]
				if menuLeaf(variant) {
					return stack
				}
				stack = append(stack, &menuState{kind: menuKindFor(variant),
					node: variant, path: clonePath(curPath), oneOf: cur})
				cur = variant
			}
			var child *smodel.FormNode
			if cur.Kind == smodel.KObject {
				for _, c := range cur.Children {
					if c.Key == s {
						child = c
						break
					}
				}
			}
			if child == nil {
				continue // extra key without schema: skip the segment
			}
			cpath := pathFor(curPath, s)
			if child.Kind == smodel.KOneOf {
				// an existing value edits its variant directly; the picker
				// stays one ctrl+t away
				if v, ok := doc.Get(cpath); ok && v != nil && !v.Null {
					if vi := currentVariant(child, v); vi >= 0 && !menuLeaf(child.Variants[vi]) {
						stack = append(stack, &menuState{kind: menuKindFor(child.Variants[vi]),
							node: child.Variants[vi], path: cpath, oneOf: child})
						cur, curPath = child.Variants[vi], cpath
						continue
					}
				}
			}
			if !menuLeaf(child) {
				stack = append(stack, &menuState{kind: menuKindFor(child),
					node: child, path: cpath})
			} else {
				return stack // leaf row lives in the current top menu
			}
			cur, curPath = child, cpath
		case int:
			if cur.Kind != smodel.KArray || cur.Items == nil {
				return stack
			}
			if menuLeaf(cur.Items) {
				return stack // scalar item rows live in the array menu
			}
			ipath := pathIdx(curPath, s)
			if cur.Items.Kind == smodel.KOneOf {
				if variants := dynamicVariants(cur.Items); variants != nil {
					ival, _ := doc.Get(ipath)
					var inode *smodel.FormNode
					if cands := matchVariantList(variants, ival); len(cands) == 1 {
						inode = pinnedOneOfMenu(variants, cands[0])
					} else {
						inode = mergedOneOfMenu(cur.Items, variants)
					}
					stack = append(stack, &menuState{kind: menuObject, node: inode,
						path: ipath, variantOf: cur.Items})
					cur, curPath = cur.Items, ipath
					continue
				}
			}
			stack = append(stack, &menuState{kind: menuKindFor(cur.Items),
				node: cur.Items, path: ipath})
			cur, curPath = cur.Items, ipath
		}
	}
	for i := range stack {
		seedVariantCursor(stack[i], doc)
	}
	return stack
}

// searchIndex walks the schema subtree below st — the menu the breadcrumb
// points at — collecting object children, the actual array items and oneOf
// variant subtrees, plus the document keys the schema does not define
// (nested values included) so they stay searchable and replaceable. Labels
// are "<docPath>  <overview>" using the same value column the menus show,
// so hits read as jump targets; variant rows carry their structural
// signature instead of fallback titles like "variant 2", which carry no
// meaning for the user.
func searchIndex(lm *listModel, st *menuState) []row {
	var out []row
	seen := map[string]bool{} // unknown doc paths, deduped across variants
	var walk func(fn *smodel.FormNode, path []any)
	walk = func(fn *smodel.FormNode, path []any) {
		switch fn.Kind {
		case smodel.KObject:
			known := map[string]bool{}
			for _, c := range fn.Children {
				known[c.Key] = true
				cpath := pathFor(path, c.Key)
				out = append(out, row{kind: rowField, fn: c, path: cpath, idx: -1,
					label: docPath(cpath) + "  " + summary(lm, c, cpath)})
				walk(c, cpath)
			}
			out = append(out, unknownRows(lm, path, known, seen)...)
		case smodel.KOneOf:
			// first-definition field rows across variants (shared keys like
			// a discriminator would otherwise repeat per variant), then the
			// members no variant defines
			known := map[string]bool{}
			for _, v := range fn.Variants {
				out = append(out, row{kind: rowVariant, fn: v, variant: v,
					path: clonePath(path), idx: -1,
					label: docPath(path) + "  " + VariantSignature(v)})
				for _, c := range v.Children {
					if known[c.Key] {
						continue
					}
					known[c.Key] = true
					cpath := pathFor(path, c.Key)
					out = append(out, row{kind: rowField, fn: c, path: cpath, idx: -1,
						label: docPath(cpath) + "  " + summary(lm, c, cpath)})
					walk(c, cpath)
				}
			}
			out = append(out, unknownRows(lm, path, known, seen)...)
		case smodel.KArray:
			if fn.Items == nil || fn.Items.Kind == smodel.KEnum {
				return
			}
			// index every existing item so values are searchable; an
			// unset/empty array still indexes the item schema once
			n := max(docArrayLen(lm.doc, path), 1)
			for i := range n {
				ipath := pathIdx(path, i)
				out = append(out, row{kind: rowDrill, fn: fn.Items, path: ipath, idx: -1,
					label: docPath(ipath) + "  " + summary(lm, fn.Items, ipath)})
				walk(fn.Items, ipath)
			}
		}
	}
	if st != nil {
		walk(st.node, st.path)
	}
	return out
}

// unknownRows indexes the members of the object at path that the schema
// does not define (known), as rowUnknown with raw value overviews. seen
// dedupes paths, e.g. a key unknown to one oneOf variant but defined in
// another is indexed once.
func unknownRows(lm *listModel, path []any, known map[string]bool, seen map[string]bool) []row {
	if lm.doc == nil {
		return nil
	}
	cur, ok := lm.doc.Get(path)
	if !ok || cur == nil || cur.Object == nil {
		return nil
	}
	var out []row
	for _, mem := range *cur.Object {
		if known[mem.Key] {
			continue
		}
		p := pathFor(path, mem.Key)
		if k := docPath(p); !seen[k] {
			seen[k] = true
			out = append(out, unknownRow(lm, p))
			out = append(out, unknownValueRows(lm, p, mem.Val, seen)...)
		}
	}
	return out
}

// unknownRow builds one schema-less index row.
func unknownRow(lm *listModel, p []any) row {
	return row{kind: rowUnknown, idx: -1, path: p,
		label: docPath(p) + "  " + lm.rawValueSummary(p)}
}

// unknownValueRows indexes everything nested inside a schema-less value:
// object members and array items are unknown in their own right, and
// strings among them are replace targets like any other.
func unknownValueRows(lm *listModel, path []any, v *jsondoc.Node, seen map[string]bool) []row {
	if v == nil {
		return nil
	}
	var out []row
	if v.Object != nil {
		for _, mem := range *v.Object {
			p := pathFor(path, mem.Key)
			if k := docPath(p); !seen[k] {
				seen[k] = true
				out = append(out, unknownRow(lm, p))
				out = append(out, unknownValueRows(lm, p, mem.Val, seen)...)
			}
		}
	}
	if v.Array != nil {
		for i, item := range *v.Array {
			p := pathIdx(path, i)
			if k := docPath(p); !seen[k] {
				seen[k] = true
				out = append(out, unknownRow(lm, p))
				out = append(out, unknownValueRows(lm, p, item, seen)...)
			}
		}
	}
	return out
}

// unknownKeyIssues collects the document keys no schema node defines —
// the same keys the menus flag "(not in schema)" — as editor-level
// warnings for the issues list. JSON Schema permits them (unless
// additionalProperties: false), so they never gate saving. Only the
// outermost unknown key is reported: everything nested inside shares its
// fate and is edited as raw JSON. The walk is value-aware: under a oneOf
// the matched variant's fields define what is known.
func unknownKeyIssues(model *smodel.FormNode, doc *jsondoc.Node) []issue {
	if model == nil || doc == nil {
		return nil
	}
	var out []issue
	emit := func(path []any, key string) {
		p := pathFor(path, key)
		ptr := ""
		for _, seg := range p {
			ptr += "/" + fmt.Sprint(seg)
		}
		out = append(out, issue{
			msg:  fmt.Sprintf("at '%s': unknown key %q (not in the schema)", ptr, key),
			path: p,
			warn: true,
		})
	}
	unknownOf := func(val *jsondoc.Node, known map[string]bool, path []any) {
		if val == nil || val.Object == nil {
			return
		}
		for _, mem := range *val.Object {
			if len(path) == 0 && mem.Key == "$schema" {
				continue // the editor's own schema pointer, not a user mistake
			}
			if !known[mem.Key] {
				emit(path, mem.Key)
			}
		}
	}
	var walk func(fn *smodel.FormNode, path []any, val *jsondoc.Node)
	walk = func(fn *smodel.FormNode, path []any, val *jsondoc.Node) {
		if fn == nil || val == nil {
			return
		}
		switch fn.Kind {
		case smodel.KObject:
			known := map[string]bool{}
			for _, c := range fn.Children {
				known[c.Key] = true
				if m := memberOf(val, c.Key); m != nil {
					walk(c, pathFor(path, c.Key), m)
				}
			}
			unknownOf(val, known, path)
		case smodel.KOneOf:
			if cands := matchVariantList(fn.Variants, val); len(cands) == 1 {
				walk(cands[0], path, val)
				return
			}
			// value unset, ambiguous or unmatched: any key any variant
			// defines counts as known
			known := map[string]bool{}
			for _, v := range fn.Variants {
				for _, c := range v.Children {
					if known[c.Key] {
						continue
					}
					known[c.Key] = true
					if m := memberOf(val, c.Key); m != nil {
						walk(c, pathFor(path, c.Key), m)
					}
				}
			}
			unknownOf(val, known, path)
		case smodel.KArray:
			if fn.Items != nil && val.Array != nil {
				for i, item := range *val.Array {
					walk(fn.Items, pathIdx(path, i), item)
				}
			}
		}
	}
	walk(model, nil, doc)
	return out
}

// memberOf returns the value of key in an object node, nil when absent.
func memberOf(n *jsondoc.Node, key string) *jsondoc.Node {
	if n == nil || n.Object == nil {
		return nil
	}
	for _, mem := range *n.Object {
		if mem.Key == key {
			return mem.Val
		}
	}
	return nil
}

// filterSearch narrows the index to rows matching q. A query containing "/"
// is a document-path query: the row path ($/a/b/0) is the only match target —
// title/key/value matching is suspended (search values with a slash-free
// keyword instead). A leading "$" or "/" anchors the match at the document
// root; without it the path matches anywhere. Otherwise Title, Key or value
// overview must contain q; fold=true compares case-insensitively (the
// historic behavior), fold=false requires an exact-case match. An empty query
// keeps everything. Hits whose leaf key equals q (text mode) or whose full
// path equals the query path (path mode) rank first; ties keep document
// order. Variant rows match schema identity only: their jump target is the
// picker row. Schema-less rows (unknown keys) match their key and value
// overview.
func filterSearch(lm *listModel, all []row, q string, fold bool) []row {
	if q == "" {
		return all
	}
	pathQ, anchored, isPath := parsePathQuery(q)
	if fold && !isPath {
		q = strings.ToLower(q)
	}
	var out []row
	for _, r := range all {
		if isPath {
			p := docPath(r.path)
			if fold {
				p = strings.ToLower(p)
			}
			needle := "/" + pathQ
			if fold {
				needle = strings.ToLower(needle)
			}
			keep := true // bare "/", "$/" or "$//": every row
			if pathQ != "" {
				if anchored {
					keep = strings.HasPrefix(p, "$"+needle)
				} else {
					keep = strings.Contains(p, needle)
				}
			}
			if keep {
				out = append(out, r)
			}
			continue
		}
		var text string
		if r.fn == nil {
			if r.kind != rowUnknown {
				continue
			}
			text = unknownMatchText(r)
		} else {
			text = r.fn.Title + " " + r.fn.Key
			if r.kind != rowVariant {
				if s := summary(lm, r.fn, r.path); s != "—" {
					text += " " + s
				}
			}
		}
		if fold {
			text = strings.ToLower(text)
		}
		if strings.Contains(text, q) {
			out = append(out, r)
		}
	}
	eq := func(a, b string) bool {
		if fold {
			return strings.EqualFold(a, b)
		}
		return a == b
	}
	score := func(r row) int {
		if isPath && pathQ != "" && eq(docPath(r.path), "$/"+pathQ) {
			return 0 // exact full path (path mode)
		}
		if !isPath && eq(rowKey(r), q) {
			return 0 // exact leaf key (text mode)
		}
		return 1
	}
	sort.SliceStable(out, func(i, j int) bool { return score(out[i]) < score(out[j]) })
	return out
}

// parsePathQuery classifies a query containing "/" as a document-path query:
// the row path ($/a/b/0) is then the only match target — title/key/value
// matching is suspended (search values with a slash-free keyword instead).
// A leading "$" or "/" anchors the match at the document root; without it
// the path matches anywhere. norm drops the "$"/"/" prefix and a trailing
// "/" (partial last segments still match: prefix semantics).
func parsePathQuery(q string) (norm string, anchored, isPath bool) {
	isPath = strings.Contains(q, "/")
	anchored = strings.HasPrefix(q, "$") || strings.HasPrefix(q, "/")
	norm = strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(q, "$"), "/"), "/")
	return norm, anchored, isPath
}

// rowKey returns a row's leaf key for exact-key ranking: the last path
// segment when it is a string, else the schema node's key (array-item drill
// rows end in an index and rank by their array key).
func rowKey(r row) string {
	if n := len(r.path); n > 0 {
		if s, ok := r.path[n-1].(string); ok {
			return s
		}
	}
	if r.fn != nil {
		return r.fn.Key
	}
	return ""
}

// unknownMatchText is the match text of a schema-less row: its key (the
// last path segment) plus the value overview carried in the label.
func unknownMatchText(r row) string {
	key := ""
	if n := len(r.path); n > 0 {
		key = fmt.Sprint(r.path[n-1])
	}
	overview := r.label
	if i := strings.Index(r.label, "  "); i >= 0 {
		overview = r.label[i+2:]
	}
	return key + " " + overview
}

// replaceAll replaces every occurrence of old in s with rep, scanning left
// to right without re-scanning replacements (a rep containing old is not
// replaced again). fold=true matches case-insensitively. It returns the new
// string and the number of occurrences replaced.
func replaceAll(s, old, rep string, fold bool) (string, int) {
	if old == "" {
		return s, 0
	}
	if !fold {
		n := strings.Count(s, old)
		if n == 0 {
			return s, 0
		}
		return strings.ReplaceAll(s, old, rep), n
	}
	sr, or := []rune(s), []rune(old)
	var b strings.Builder
	n := 0
	for i := 0; i < len(sr); {
		if i+len(or) <= len(sr) && runesFoldEqual(sr[i:i+len(or)], or) {
			b.WriteString(rep)
			n++
			i += len(or)
			continue
		}
		b.WriteRune(sr[i])
		i++
	}
	return b.String(), n
}

func runesFoldEqual(a, b []rune) bool {
	for i := range a {
		if unicode.ToLower(a[i]) != unicode.ToLower(b[i]) {
			return false
		}
	}
	return true
}

// replaceHits narrows the index to rows whose document value is a string
// containing q (per fold), relabeling each hit
// "<docPath>  <old> → <new>" so the result list doubles as the replacement
// preview. An empty query lists every string value with its original label
// (mirroring filterSearch's keep-everything rule). Duplicate paths — oneOf
// variants walking the same location — are kept once.
func replaceHits(lm *listModel, all []row, q, rep string, fold bool) []row {
	var out []row
	var seen [][]any
	for _, r := range all {
		dup := false
		for _, p := range seen {
			if pathEq(p, r.path) {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		n, ok := lm.doc.Get(r.path)
		if !ok || n == nil || n.Str == nil {
			continue
		}
		seen = append(seen, r.path)
		r2 := r
		if q != "" {
			new, c := replaceAll(*n.Str, q, rep, fold)
			if c == 0 {
				continue
			}
			r2.label = docPath(r.path) + "  " + valueClip(*n.Str) + " → " + valueClip(new)
		}
		out = append(out, r2)
	}
	return out
}

// parseIssue extracts the instance location from a validator message like
// "at '/inner/x': got number, want string": the JSON-pointer path, extended
// by the property name for "missing property 'p'" and by the first name of
// "additional properties 'p', … not allowed". ok is false when the message
// carries no location.
func parseIssue(msg string) (path []any, ok bool) {
	const at = "at '"
	i := strings.Index(msg, at)
	if i < 0 {
		return nil, false
	}
	rest := msg[i+len(at):]
	j := strings.Index(rest, "'")
	if j < 0 {
		return nil, false
	}
	path = pointerPath(rest[:j])
	detail := rest[j:]
	for _, marker := range []string{"missing property '", "additional propert"} {
		k := strings.Index(detail, marker)
		if k < 0 {
			continue
		}
		sub := detail[k:]
		if q := strings.Index(sub, "'"); q >= 0 {
			if r := strings.Index(sub[q+1:], "'"); r >= 0 {
				if name := sub[q+1 : q+1+r]; name != "" {
					return pathFor(path, name), true
				}
			}
		}
		return path, true
	}
	return path, true
}

// pointerPath converts a JSON Pointer ("" or "/a/b/0") to a document path;
// numeric segments become array indices, ~1/~0 escapes are resolved.
func pointerPath(ptr string) []any {
	if ptr == "" {
		return nil
	}
	var out []any
	for _, seg := range strings.Split(strings.Trim(ptr, "/"), "/") {
		if seg == "" {
			continue
		}
		seg = strings.ReplaceAll(strings.ReplaceAll(seg, "~1", "/"), "~0", "~")
		if n, err := strconv.Atoi(seg); err == nil {
			out = append(out, n)
		} else {
			out = append(out, seg)
		}
	}
	return out
}

func clonePath(p []any) []any {
	q := make([]any, len(p))
	copy(q, p)
	return q
}

// docArrayLen returns the current length of the array at path (0 if absent).
func docArrayLen(doc *jsondoc.Node, path []any) int {
	n, ok := doc.Get(path)
	if !ok || n.Array == nil {
		return 0
	}
	return len(*n.Array)
}

// issueStatusLabel composes the root status row label: validation errors
// and unknown-key warnings are counted separately, e.g.
// "⚠ 2 validation errors", "⚠ 1 validation error · 1 warning",
// "⚠ 2 warnings".
func issueStatusLabel(errs, warns int) string {
	plural := func(n int) string {
		if n == 1 {
			return ""
		}
		return "s"
	}
	switch {
	case errs > 0 && warns > 0:
		return fmt.Sprintf("⚠ %d validation error%s · %d warning%s", errs, plural(errs), warns, plural(warns))
	case errs > 0:
		return fmt.Sprintf("⚠ %d validation error%s", errs, plural(errs))
	default:
		return fmt.Sprintf("⚠ %d warning%s", warns, plural(warns))
	}
}

// docPath renders a document path as the breadcrumb string ("$/a/b/0",
// "$" for the root). Keys and array indices both use "/" segments.
func docPath(path []any) string {
	var b strings.Builder
	b.WriteByte('$')
	for _, seg := range path {
		switch s := seg.(type) {
		case string:
			b.WriteByte('/')
			b.WriteString(s)
		case int:
			fmt.Fprintf(&b, "/%d", s)
		}
	}
	return b.String()
}
