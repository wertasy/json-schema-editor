package ui

// menuconfig row model: the pure logic behind the menu TUI — row lists per
// menu state, value summaries, variant signatures and navigation-stack
// reconstruction. No bubbletea imports here so it stays unit-testable.

import (
	"fmt"
	"strconv"
	"strings"

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
// at path disambiguates.
type menuState struct {
	kind           menuKind
	node           *smodel.FormNode
	path           []any
	cursor, offset int
	variantOf      *smodel.FormNode
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
		noun := "validation errors"
		if len(lm.issuesNow) == 1 {
			noun = "validation error"
		}
		label := fmt.Sprintf("⚠ %d %s", len(lm.issuesNow), noun)
		rows = append([]row{{kind: rowIssues, idx: -1, label: label}}, rows...)
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
		return objectSummary(v)
	case smodel.KArray:
		if v.Array == nil {
			return "—"
		}
		switch n := len(*v.Array); n {
		case 0:
			return "empty"
		case 1:
			return "1 item"
		default:
			return fmt.Sprintf("%d items", n)
		}
	case smodel.KOneOf:
		idx := currentVariant(fn, v)
		if idx < 0 {
			return "—"
		}
		variant := fn.Variants[idx]
		switch variant.Kind {
		case smodel.KObject:
			// the variant title gives the object context
			return variant.Title + " " + objectSummary(v)
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
			if v.Array == nil {
				return "—"
			}
			switch n := len(*v.Array); n {
			case 0:
				return "empty"
			case 1:
				return "1 item"
			default:
				return fmt.Sprintf("%d items", n)
			}
		case smodel.KScalar:
			// leaf variants show their value, like any scalar row
			if s := scalarText(v); s != "" {
				return valueClip(s)
			}
		}
		return variant.Title
	default: // KRaw
		return "json " + clipRunes(smodel.EncodeValue(v), 20)
	}
}

// objectSummary shows the first two object members as k=v (nested values as
// {…}/[N]), then " +N" for the rest; "{}" when empty.
func objectSummary(n *jsondoc.Node) string {
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
	parts := make([]string, 0, 3)
	for i := 0; i < len(mem) && i < 2; i++ {
		m := mem[i]
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
					node: variant, path: clonePath(curPath)})
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
	return stack
}

// searchIndex walks the schema tree (object children, array item subtrees
// and oneOf variant subtrees included) collecting searchable rows whose
// label is "Title chain / Title  signature".
func searchIndex(root *smodel.FormNode) []row {
	var out []row
	var walk func(fn *smodel.FormNode, path []any, chain string)
	walk = func(fn *smodel.FormNode, path []any, chain string) {
		switch fn.Kind {
		case smodel.KObject:
			for _, c := range fn.Children {
				cpath := pathFor(path, c.Key)
				out = append(out, row{kind: rowField, fn: c, path: cpath, idx: -1,
					label: chain + c.Title + "  " + VariantSignature(c)})
				walk(c, cpath, chain+c.Title+" / ")
			}
		case smodel.KOneOf:
			for _, v := range fn.Variants {
				out = append(out, row{kind: rowVariant, fn: v, variant: v,
					path: clonePath(path), idx: -1,
					label: chain + v.Title + "  " + VariantSignature(v)})
				walk(v, clonePath(path), chain+v.Title+" / ")
			}
		case smodel.KArray:
			if fn.Items == nil || fn.Items.Kind == smodel.KEnum {
				return
			}
			ipath := pathIdx(path, 0)
			out = append(out, row{kind: rowDrill, fn: fn.Items, path: ipath, idx: -1,
				label: chain + fn.Items.Title + "  " + VariantSignature(fn.Items)})
			walk(fn.Items, ipath, chain+fn.Items.Title+" / ")
		}
	}
	walk(root, nil, "")
	return out
}

// filterSearch narrows the index to rows whose Title or Key contains q
// (case-insensitive); an empty query keeps everything.
func filterSearch(all []row, q string) []row {
	q = strings.ToLower(q)
	if q == "" {
		return all
	}
	var out []row
	for _, r := range all {
		if r.fn == nil {
			continue
		}
		if strings.Contains(strings.ToLower(r.fn.Title+" "+r.fn.Key), q) {
			out = append(out, r)
		}
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
