package ui

// menuconfig row model: the pure logic behind the menu TUI — row lists per
// menu state, value summaries, variant signatures and navigation-stack
// reconstruction. No bubbletea imports here so it stays unit-testable.

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"json-schema-editor/internal/icons"
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
	rowIssues                 // "<err|warn> N validation errors" status row (root)
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
		if st.node.Kind == smodel.KMap {
			// string-keyed map (additionalProperties): one row per document
			// key, values edited against the map's value schema
			if lm.doc != nil {
				if cur, ok := lm.doc.Get(st.path); ok && cur != nil && cur.Object != nil {
					for _, mem := range *cur.Object {
						rows = append(rows, mapItemRow(st.node, st.path, mem.Key))
					}
				}
			}
		} else if st.node.Kind == smodel.KObject {
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

// valueRow reports whether r is a document entry whose value is present and
// not JSON null. Fields, containers, map members and schema-less unknown
// keys qualify; synthetic action rows, oneOf variant choices and enum-array
// toggles are not keys and never do.
func (m *listModel) valueRow(r row) bool {
	switch r.kind {
	case rowField, rowDrill, rowUnknown:
	default:
		return false
	}
	if m.doc == nil {
		return false
	}
	v, ok := m.doc.Get(r.path) // a nil path is the root scalar itself
	return ok && v != nil && !v.Null
}

// moveToValue moves the cursor to the next (delta > 0) or previous
// (delta < 0) row with a non-null value, wrapping at both ends. Rows without
// such a value are skipped; the cursor stays put when the menu has none.
func (m *listModel) moveToValue(rows []row, delta int) {
	if len(rows) == 0 {
		return
	}
	cur := m.top().cursor
	for step := 1; step <= len(rows); step++ {
		i := wrapCursor(cur, delta*step, len(rows))
		if m.valueRow(rows[i]) {
			m.top().cursor = i
			return
		}
	}
}

// childRow maps one object child to a field or drill row.
func childRow(st *menuState, c *smodel.FormNode) row {
	p := pathFor(st.path, c.Key)
	if menuLeaf(c) {
		return row{kind: rowField, fn: c, path: p, idx: -1}
	}
	return row{kind: rowDrill, fn: c, path: p, idx: -1}
}

// mapItemRow maps one document key of a string-keyed map (KMap) to an
// editable or drillable row against the map's value schema. The doc key
// becomes the row label; the shared value node provides kind and editing.
func mapItemRow(node *smodel.FormNode, parentPath []any, key string) row {
	p := pathFor(parentPath, key)
	items := node.Items
	if items == nil {
		// value schema unknown: fall back to raw editing of the value
		items = &smodel.FormNode{Key: node.Key, Title: node.Title, Kind: smodel.KRaw}
	}
	label := key
	if menuLeaf(items) {
		return row{kind: rowField, fn: items, path: p, idx: -1, label: label}
	}
	return row{kind: rowDrill, fn: items, path: p, idx: -1, label: label}
}

// itemRow maps array element i to an editable or drillable row. The row
// label is "<key>[i]" (the document key owns the list), so element rows
// read like addresses rather than repeating the shared item schema title.
func itemRow(node *smodel.FormNode, parentPath []any, i int) row {
	p := pathIdx(parentPath, i)
	items := node.Items
	if items == nil {
		// items schema unknown: fall back to raw editing of the element
		items = &smodel.FormNode{Key: node.Key, Title: node.Title, Kind: smodel.KRaw}
	}
	key := node.Key
	if n := len(parentPath); n > 0 {
		if s, ok := parentPath[n-1].(string); ok {
			key = s
		}
	}
	label := fmt.Sprintf("%s[%d]", key, i)
	if menuLeaf(items) {
		return row{kind: rowField, fn: items, path: p, idx: i, label: label}
	}
	return row{kind: rowDrill, fn: items, path: p, idx: i, label: label}
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
	case smodel.KObject, smodel.KMap:
		return objectSummary(fn, v)
	case smodel.KArray:
		return arraySummary(v)
	case smodel.KOneOf:
		// sing-box nests oneOf groups (e.g. DNSRule: matcher rules beside
		// logical rules): descend to the matched leaf, not the group
		variant := matchedLeafVariant(fn, v)
		if variant == nil {
			return "—"
		}
		switch variant.Kind {
		case smodel.KObject, smodel.KMap:
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

// matchedLeafVariant resolves the variant matching val, following nested
// oneOf groups (sing-box rule schemas nest matcher and logical groups) down
// to the leaf; nil when no level matches.
func matchedLeafVariant(node *smodel.FormNode, val *jsondoc.Node) *smodel.FormNode {
	for node != nil && node.Kind == smodel.KOneOf {
		idx := currentVariant(node, val)
		if idx < 0 {
			return nil
		}
		node = node.Variants[idx]
	}
	return node
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
			} else if cur.Kind == smodel.KMap && cur.Items != nil {
				// any doc key resolves against the map's value schema
				if v, ok := doc.Get(pathFor(curPath, s)); ok && v != nil {
					child = cur.Items
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
// are "<path>  <overview>" using the same value column the menus show,
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
					label: jsondoc.PathString(cpath) + "  " + summary(lm, c, cpath)})
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
					label: jsondoc.PathString(path) + "  " + VariantSignature(v)})
				for _, c := range v.Children {
					if known[c.Key] {
						continue
					}
					known[c.Key] = true
					cpath := pathFor(path, c.Key)
					out = append(out, row{kind: rowField, fn: c, path: cpath, idx: -1,
						label: jsondoc.PathString(cpath) + "  " + summary(lm, c, cpath)})
					walk(c, cpath)
				}
			}
			out = append(out, unknownRows(lm, path, known, seen)...)
		case smodel.KMap:
			// every document key is a known member; values index against the
			// map's value schema
			if cur, ok := lm.doc.Get(path); ok && cur != nil && cur.Object != nil {
				vn := fn.Items
				if vn == nil {
					vn = &smodel.FormNode{Key: fn.Key, Title: fn.Title, Kind: smodel.KRaw}
				}
				for _, mem := range *cur.Object {
					cpath := pathFor(path, mem.Key)
					out = append(out, row{kind: rowField, fn: vn, path: cpath, idx: -1,
						label: jsondoc.PathString(cpath) + "  " + summary(lm, vn, cpath)})
					walk(vn, cpath)
				}
			}
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
					label: jsondoc.PathString(ipath) + "  " + summary(lm, fn.Items, ipath)})
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
		if k := jsondoc.PathString(p); !seen[k] {
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
		label: jsondoc.PathString(p) + "  " + lm.rawValueSummary(p)}
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
			if k := jsondoc.PathString(p); !seen[k] {
				seen[k] = true
				out = append(out, unknownRow(lm, p))
				out = append(out, unknownValueRows(lm, p, mem.Val, seen)...)
			}
		}
	}
	if v.Array != nil {
		for i, item := range *v.Array {
			p := pathIdx(path, i)
			if k := jsondoc.PathString(p); !seen[k] {
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
		out = append(out, issue{
			msg:  fmt.Sprintf("at '%s': unknown key %q (not in the schema)", jsondoc.PathString(p), key),
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
			// defines counts as known; nested oneOf groups (the allOf
			// distribution builds them) flatten to their object leaves
			// first — a leafless list keeps the raw variants
			variants := dynamicVariants(fn)
			if variants == nil {
				variants = fn.Variants
			}
			known := map[string]bool{}
			for _, v := range variants {
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
		case smodel.KMap:
			// every document key is defined by the map's additionalProperties
			if fn.Items != nil && val.Object != nil {
				for _, mem := range *val.Object {
					walk(fn.Items, pathFor(path, mem.Key), mem.Val)
				}
			}
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

// rowScalarValue reports whether the row's own value is a scalar: its
// document value, or the schema default when the key is unset. Container rows
// (object, array, oneOf roll-up) and unset rows without a default answer
// false.
func rowScalarValue(lm *listModel, r row) bool {
	n, ok := lm.doc.Get(r.path)
	if !ok || n == nil || n.Null {
		if r.fn == nil || r.fn.Default == nil {
			return false
		}
		n = r.fn.Default
	}
	return n.Str != nil || n.Num != nil || n.Bool != nil
}

// rowSearchText is the text-mode match target of a row: "Title Key <value>"
// for a row carrying a scalar value, "key <value>" for a schema-less scalar
// row. A row whose value is a container matches its title/key only: its
// overview summarizes child rows, which match the same text themselves, so
// matching the roll-up would list every child hit twice — once as the parent,
// once as the child. ok=false when the row never matches in text mode.
func rowSearchText(lm *listModel, r row) (string, bool) {
	if r.fn == nil {
		if r.kind != rowUnknown {
			return "", false
		}
		return unknownMatchText(r, rowScalarValue(lm, r)), true
	}
	text := r.fn.Title + " " + r.fn.Key
	if rowScalarValue(lm, r) {
		if s := summary(lm, r.fn, r.path); s != "—" {
			text += " " + s
		}
	}
	return text, true
}

// filterSearch narrows the index to rows matching q. A query starting with
// "$" — alone, "$.", or "$[" — is a document-path query in the rendered path
// style ("$.a.b[0]"): the row path is the only match target (title/key/value
// matching is suspended) and the match is anchored at the document root, with
// prefix semantics so a partially typed last segment still matches. Any other
// query is ordinary text: title and key, plus the value for rows carrying a
// scalar value (container overviews are roll-ups of child rows and never
// match); fold=true compares case-insensitively, fold=false requires an
// exact-case match. An empty query keeps everything. Hits whose full path
// equals the query (path mode) or whose leaf key equals q (text mode) rank
// first; ties keep document order. Variant rows match schema identity only:
// their jump target is the picker row. Schema-less rows (unknown keys) match
// their key and value overview.
//
// When re != nil the query is a regular expression. Path queries still match
// the path body: refilter compiles them via pathPattern (leading "$" stripped,
// [N] index brackets escaped), so a pasted breadcrumb stays literal. Full
// path matches rank first.
func filterSearch(lm *listModel, all []row, q string, fold bool, re *regexp.Regexp) []row {
	if q == "" {
		return all
	}
	if isPathQuery(q) {
		if re != nil {
			var out []row
			for _, r := range all {
				body := pathBody(r.path)
				if loc := re.FindStringIndex(body); loc != nil && loc[0] == 0 {
					out = append(out, r)
				}
			}
			full := func(r row) bool {
				body := pathBody(r.path)
				loc := re.FindStringIndex(body)
				return loc != nil && loc[0] == 0 && loc[1] == len(body)
			}
			sort.SliceStable(out, func(i, j int) bool { return full(out[i]) && !full(out[j]) })
			return out
		}
		needle := q
		if fold {
			needle = strings.ToLower(needle)
		}
		var out []row
		for _, r := range all {
			p := jsondoc.PathString(r.path)
			if fold {
				p = strings.ToLower(p)
			}
			if strings.HasPrefix(p, needle) {
				out = append(out, r)
			}
		}
		exact := func(r row) bool {
			p := jsondoc.PathString(r.path)
			if fold {
				p = strings.ToLower(p)
			}
			return p == needle
		}
		sort.SliceStable(out, func(i, j int) bool { return exact(out[i]) && !exact(out[j]) })
		return out
	}
	var out []row
	if re != nil {
		for _, r := range all {
			text, ok := rowSearchText(lm, r)
			if !ok {
				continue
			}
			if re.MatchString(text) {
				out = append(out, r)
			}
		}
		full := func(r row) bool {
			target := rowKey(r)
			loc := re.FindStringIndex(target)
			return loc != nil && loc[0] == 0 && loc[1] == len(target)
		}
		sort.SliceStable(out, func(i, j int) bool { return full(out[i]) && !full(out[j]) })
		return out
	}
	if fold {
		q = strings.ToLower(q)
	}
	for _, r := range all {
		var ok bool
		var text string
		if text, ok = rowSearchText(lm, r); !ok {
			continue
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
		if eq(rowKey(r), q) {
			return 0 // exact leaf key
		}
		return 1
	}
	sort.SliceStable(out, func(i, j int) bool { return score(out[i]) < score(out[j]) })
	return out
}

// isPathQuery reports whether q is a document-path query: "$", "$.", or "$["
// prefix. Requiring the "$" keeps ordinary text searches for values containing
// dots or slashes working.
func isPathQuery(q string) bool {
	return q == "$" || strings.HasPrefix(q, "$.") || strings.HasPrefix(q, "$[")
}

// pathBody is a row path as a path-query match subject: the rendered path
// without its leading "$" ("$.a.b[0]" → ".a.b[0]", root "$" → "").
func pathBody(path []any) string {
	return jsondoc.PathString(path)[1:]
}

// pathPattern turns a document-path query into a regex over pathBody: the
// leading "$" is dropped and JSON-path index brackets are escaped, so a pasted
// breadcrumb stays literal in the default regex mode ("$.tags[0]" matches
// ".tags[0]", not ".tags0"). A bracket still being typed is escaped as soon as
// it is followed only by digits, keeping the incremental pattern valid; other
// brackets are left to the regex engine (character classes).
func pathPattern(q string) string {
	body := strings.TrimPrefix(q, "$")
	var b strings.Builder
	for i := 0; i < len(body); {
		if body[i] != '[' {
			b.WriteByte(body[i])
			i++
			continue
		}
		if k := strings.IndexByte(body[i+1:], ']'); k >= 0 {
			k += i + 1
			if allDigits(body[i+1 : k]) {
				b.WriteString(`\[` + body[i+1:k] + `\]`)
				i = k + 1
				continue
			}
		} else if rest := body[i+1:]; rest == "" || allDigits(rest) {
			// an index still being typed: escape so the regex stays valid
			b.WriteString(`\[` + rest)
			i = len(body)
			continue
		}
		b.WriteByte(body[i])
		i++
	}
	return b.String()
}

// allDigits reports whether s consists of ASCII digits and is non-empty.
func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
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

// unknownMatchText is the match text of a schema-less row: its key (the last
// path segment) plus, for a scalar value, the value overview carried in the
// label. A container value contributes no text — its members are indexed as
// rows of their own.
func unknownMatchText(r row, withValue bool) string {
	key := ""
	if n := len(r.path); n > 0 {
		key = fmt.Sprint(r.path[n-1])
	}
	if !withValue {
		return key
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
// string and the number of occurrences replaced. With re != nil the query is
// a regular expression instead: replacement is leftmost non-overlapping and
// rep expands $1 capture groups.
func replaceAll(s, old, rep string, fold bool, re *regexp.Regexp) (string, int) {
	if re != nil {
		return re.ReplaceAllString(s, rep), len(re.FindAllStringIndex(s, -1))
	}
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
// "<path>  <old> → <new>" so the result list doubles as the replacement
// preview. An empty query lists every string value with its original label
// (mirroring filterSearch's keep-everything rule). Duplicate paths — oneOf
// variants walking the same location — are kept once.
func replaceHits(lm *listModel, all []row, q, rep string, fold bool, re *regexp.Regexp) []row {
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
			new, c := replaceAll(*n.Str, q, rep, fold, re)
			if c == 0 {
				continue
			}
			r2.label = jsondoc.PathString(r.path) + "  " + valueClip(*n.Str) + " → " + valueClip(new)
		}
		out = append(out, r2)
	}
	return out
}

// splitIssue extracts the quoted document location from a validator message
// like "at '$.inner.x': got number, want string" and returns the detail after
// it.
func splitIssue(msg string) (loc []any, detail string, ok bool) {
	const at = "at '"
	i := strings.Index(msg, at)
	if i < 0 {
		return nil, "", false
	}
	rest := msg[i+len(at):]
	j := strings.Index(rest, "'")
	if j < 0 {
		return nil, "", false
	}
	loc, ok = jsondoc.ParsePath(rest[:j])
	if !ok {
		return nil, "", false
	}
	return loc, rest[j:], true
}

// parseIssue extracts the jump target from a validator message: the location
// itself, extended by the property name for "missing property 'p'" and by the
// first name of "additional properties 'p', … not allowed". ok is false when
// the message carries no location or the location is not a document path.
func parseIssue(msg string) (path []any, ok bool) {
	loc, detail, ok := splitIssue(msg)
	if !ok {
		return nil, false
	}
	if name, ok := issuePropName(detail, "missing property '"); ok {
		return pathFor(loc, name), true
	}
	if name, ok := issuePropName(detail, "additional properties '"); ok {
		return pathFor(loc, name), true
	}
	return loc, true
}

// issueFlagPaths returns every location a validator message refers to.
// additionalProperties errors list all offending keys in the validator's map
// iteration order; flagging only the first made unknown-key warning counts
// depend on that order and flicker between refreshes.
func issueFlagPaths(msg string) [][]any {
	loc, detail, ok := splitIssue(msg)
	if !ok {
		return nil
	}
	if i := strings.Index(detail, "additional propert"); i >= 0 {
		if names := quotedNames(detail[i:]); len(names) > 0 {
			out := make([][]any, 0, len(names))
			for _, name := range names {
				out = append(out, pathFor(loc, name))
			}
			return out
		}
	}
	if name, ok := issuePropName(detail, "missing property '"); ok {
		return [][]any{pathFor(loc, name)}
	}
	return [][]any{loc}
}

// issuePropName reads the property name after marker (which includes its
// opening quote) up to the closing unescaped quote.
func issuePropName(detail, marker string) (string, bool) {
	k := strings.Index(detail, marker)
	if k < 0 {
		return "", false
	}
	s := detail[k+len(marker):]
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '\\' && i+1 < len(s) && s[i+1] == '\'':
			b.WriteByte('\'')
			i++
		case s[i] == '\'':
			return b.String(), true
		default:
			b.WriteByte(s[i])
		}
	}
	return "", false
}

// quotedNames returns every single-quoted token in s, resolving the \'
// escape the library uses inside property names.
func quotedNames(s string) []string {
	var out []string
	for i := 0; i < len(s); i++ {
		if s[i] != '\'' {
			continue
		}
		var b strings.Builder
		j := i + 1
		for ; j < len(s); j++ {
			if s[j] == '\\' && j+1 < len(s) && s[j+1] == '\'' {
				b.WriteByte('\'')
				j++
				continue
			}
			if s[j] == '\'' {
				break
			}
			b.WriteByte(s[j])
		}
		if j >= len(s) {
			break
		}
		out = append(out, b.String())
		i = j
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

// issueStatusLabel composes the root status row label: validation errors and
// unknown-key warnings are counted separately and carry their severity glyph,
// e.g. "<err> 2 validation errors", "<err> 1 validation error · <warn> 1 warning",
// "<warn> 2 warnings".
func issueStatusLabel(errs, warns int) string {
	plural := func(n int) string {
		if n == 1 {
			return ""
		}
		return "s"
	}
	switch {
	case errs > 0 && warns > 0:
		return fmt.Sprintf("%s %d validation error%s · %s %d warning%s",
			icons.Err, errs, plural(errs), icons.Warn, warns, plural(warns))
	case errs > 0:
		return fmt.Sprintf("%s %d validation error%s", icons.Err, errs, plural(errs))
	default:
		return fmt.Sprintf("%s %d warning%s", icons.Warn, warns, plural(warns))
	}
}
