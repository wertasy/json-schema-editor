package ui

import (
	"errors"
	"fmt"
	"strings"

	"github.com/atotto/clipboard"

	"github.com/wertasy/json-schema-editor/internal/jsondoc"
	"github.com/wertasy/json-schema-editor/internal/smodel"
	"github.com/wertasy/json-schema-editor/internal/yamldoc"
)

var (
	errPasteEmpty = errors.New("clipboard is empty")
	errPasteBad   = errors.New("clipboard is not JSON keys or items")
)

type pasteMember struct {
	key string
	val *jsondoc.Node
}

// pasteClipboard implements p/P smart paste: read the clipboard, classify it
// as object members or array items, pick the target container and merge the
// content in. underCursor selects the target: P pastes into the map/array
// the cursor row points at (falling back to the current menu's container),
// p always uses the container the current menu displays. Positionally,
// pasted keys/items land below (p) / above (P) the cursor row, whatever the
// clipboard shape — bare lists, a complete [..] array, or a complete {..}
// mapping (that one becomes a single item on an array page). All failure
// paths only set m.notice and leave the document untouched.
func (m *listModel) pasteClipboard(r row, underCursor bool) {
	if m.doc == nil {
		m.notice = "no document to paste into"
		return
	}
	read := m.opts.Paste
	if read == nil {
		read = clipboard.ReadAll
	}
	text, err := read()
	if err != nil {
		m.notice = "paste failed: " + err.Error()
		return
	}
	members, items, doc, err := parsePaste(text, m.opts.rawLabel() == "YAML")
	if err != nil {
		m.notice = err.Error()
		return
	}
	// a single value replaces values: with a selection, P writes it into
	// every selected row; without, into the row P sits on. p (menu target)
	// and rows without a value slot refuse it.
	if members == nil && items == nil {
		if underCursor {
			st := m.top()
			var targets []row
			if st != nil && len(st.sel) > 0 {
				for _, sel := range menuRows(m) {
					if st.sel[selKey(sel.path)] && pasteValueRowOK(sel) {
						targets = append(targets, sel)
					}
				}
			} else if pasteValueRowOK(r) {
				targets = []row{r}
			}
			if len(targets) > 0 {
				pre := m.doc.Clone()
				for _, t := range targets {
					m.doc.Set(t.path, doc.CloneDetached())
				}
				if st != nil && len(st.sel) > 0 {
					st.sel = nil
				}
				if nodesEqual(pre, m.doc) {
					m.notice = "paste made no changes"
				} else if len(targets) == 1 {
					m.notice = "pasted value into " + jsondoc.PathString(targets[0].path)
				} else {
					m.notice = fmt.Sprintf("pasted value into %d keys", len(targets))
				}
				return
			}
		}
		m.notice = "a single value replaces a row's value — press P on the row"
		return
	}
	path, isArray, notice := m.pasteTarget(r, underCursor)
	if notice != "" {
		m.notice = notice
		return
	}
	if members != nil && isArray {
		// a complete mapping lands in an items page as ONE object item
		// (p below / P above the cursor item); enum arrays keep the
		// refusal — their elements are enum scalars, not containers
		st := m.top()
		if st == nil || st.kind != menuItems {
			m.notice = "clipboard holds key-value pairs, the target is an array"
			return
		}
		pre := m.doc.Clone()
		m.pasteIntoArray(path, []*jsondoc.Node{membersAsItem(members)},
			m.pasteInsertIndex(r, path, true, underCursor))
		if nodesEqual(pre, m.doc) {
			m.notice = "paste made no changes"
			return
		}
		m.notice = fmt.Sprintf("pasted 1 items into %s", jsondoc.PathString(path))
		return
	}
	if items != nil && !isArray {
		m.notice = "clipboard holds array items, the target is a mapping"
		return
	}
	pre := m.doc.Clone()
	if members != nil {
		m.pasteIntoObject(path, members, m.pasteInsertIndex(r, path, false, underCursor))
		if nodesEqual(pre, m.doc) {
			m.notice = "paste made no changes"
			return
		}
		m.notice = fmt.Sprintf("pasted %d keys into %s", len(members), jsondoc.PathString(path))
		return
	}
	m.pasteIntoArray(path, items, m.pasteInsertIndex(r, path, true, underCursor))
	if nodesEqual(pre, m.doc) {
		m.notice = "paste made no changes"
		return
	}
	m.notice = fmt.Sprintf("pasted %d items into %s", len(items), jsondoc.PathString(path))
}

// parsePaste classifies the clipboard. Content starting with { or [ must be
// one complete JSON document (strict JSON, no comments/trailing commas);
// anything else that parses as one complete JSON document is a single value
// ("str", 123, true, null). Remaining text is a bare member/item list: one
// trailing comma is stripped, then the text is wrapped in {…} (object
// members) and […] (array items) in that order — the two forms cannot both
// parse, so no ambiguity exists. In YAML documents (yamlMode) content the
// JSON forms reject is decoded as YAML instead: block mappings/sequences,
// flow collections, single scalars. JSON stays valid YAML, so JSON clips
// keep working.
// On success doc is the full parsed document; exactly one of members/items
// is non-nil for collection content, both nil for a single scalar value.
func parsePaste(text string, yamlMode bool) (members []pasteMember, items []*jsondoc.Node, doc *jsondoc.Node, err error) {
	s := strings.TrimSpace(text)
	if s == "" {
		return nil, nil, nil, errPasteEmpty
	}
	if strings.HasPrefix(s, "{") || strings.HasPrefix(s, "[") {
		n, err := jsondoc.Parse(strings.NewReader(s))
		if err != nil {
			// one trailing comma before the closing bracket is tolerated:
			// {"a":1,} and [1,2,] parse as the containers they close
			if t, ok := stripTrailingComma(s); ok {
				if n2, err2 := jsondoc.Parse(strings.NewReader(t)); err2 == nil {
					return classifyParsed(n2)
				}
			}
			if yamlMode {
				return classifyPasteYAML(s)
			}
			return nil, nil, nil, errPasteBad
		}
		return classifyParsed(n)
	}
	// a complete JSON document that is neither {…} nor […] is one value
	if n, err := jsondoc.Parse(strings.NewReader(s)); err == nil {
		return classifyParsed(n)
	}
	if strings.HasSuffix(s, ",") {
		s = strings.TrimSpace(strings.TrimSuffix(s, ","))
	}
	if s == "" {
		return nil, nil, nil, errPasteEmpty
	}
	if n, err := jsondoc.Parse(strings.NewReader("{" + s + "}")); err == nil && n.Object != nil {
		return objectMembers(n), nil, n, nil
	}
	if n, err := jsondoc.Parse(strings.NewReader("[" + s + "]")); err == nil && n.Array != nil {
		return nil, *n.Array, n, nil
	}
	if yamlMode {
		return classifyPasteYAML(s)
	}
	return nil, nil, nil, errPasteBad
}

// stripTrailingComma drops one comma sitting right before the closing
// bracket (`{"a":1,}` → `{"a":1}`, `[1,2,]` → `[1,2]`); ok reports whether
// the text changed.
func stripTrailingComma(s string) (string, bool) {
	if strings.HasSuffix(s, ",}") {
		return s[:len(s)-2] + "}", true
	}
	if strings.HasSuffix(s, ",]") {
		return s[:len(s)-2] + "]", true
	}
	return s, false
}

// classifyParsed splits a fully parsed document into members or items; a
// scalar root is a single value (members and items both nil).
func classifyParsed(n *jsondoc.Node) ([]pasteMember, []*jsondoc.Node, *jsondoc.Node, error) {
	if n.Object != nil {
		return objectMembers(n), nil, n, nil
	}
	if n.Array != nil {
		return nil, *n.Array, n, nil
	}
	return nil, nil, n, nil
}

// classifyPasteYAML decodes clipboard text as YAML for YAML documents;
// scalar roots come back as a single value.
func classifyPasteYAML(s string) ([]pasteMember, []*jsondoc.Node, *jsondoc.Node, error) {
	n, _, err := yamldoc.Parse([]byte(s))
	if err != nil {
		return nil, nil, nil, errPasteBad
	}
	return classifyParsed(n)
}

// objectMembers copies an object node into the paste member list.
func objectMembers(n *jsondoc.Node) []pasteMember {
	members := make([]pasteMember, 0, len(*n.Object))
	for _, mem := range *n.Object {
		members = append(members, pasteMember{key: mem.Key, val: mem.Val})
	}
	return members
}

// pasteValueRowOK reports whether the cursor row is a value slot a pasted
// single value can replace: field, schema-less and array-item rows, plus
// container rows (replacing a container with a scalar is an explicit
// action). oneOf rows are excluded — a value would guess the variant — and
// action rows have no value.
func pasteValueRowOK(r row) bool {
	switch r.kind {
	case rowField, rowUnknown:
		return len(r.path) > 0
	case rowDrill:
		if len(r.path) == 0 {
			return false
		}
		if _, isIdx := r.path[len(r.path)-1].(int); isIdx {
			return true // a scalar or object array-item row
		}
		return r.fn != nil && r.fn.Kind != smodel.KOneOf
	}
	return false
}

// pasteTarget resolves the container the paste merges into: with
// underCursor, first the map/array row under the cursor, then the map/array
// the current menu displays; without, always the menu's container. A
// non-empty notice refuses the paste.
func (m *listModel) pasteTarget(r row, underCursor bool) (path []any, isArray bool, notice string) {
	st := m.top()
	if st == nil {
		return nil, false, "nothing to paste into here"
	}
	// drill rows of object/map/array fields take the paste even when the
	// field has no document value yet (the container auto-creates); a value
	// of the wrong shape refuses instead of silently falling through.
	if underCursor && r.kind == rowDrill && r.fn != nil {
		// a drill row inside an items menu is an array item, not a
		// container: paste targets the parent array (the item schema's
		// own kind says nothing about the target shape).
		if st.kind == menuItems && len(r.path) > 0 {
			if _, isIdx := r.path[len(r.path)-1].(int); isIdx {
				parent := clonePath(r.path[:len(r.path)-1])
				if !m.pasteContainerOK(parent, true) {
					return nil, false, "the value under the cursor is not an array"
				}
				return parent, true, ""
			}
		}
		switch r.fn.Kind {
		case smodel.KObject, smodel.KMap:
			if !m.pasteContainerOK(r.path, false) {
				return nil, false, "the value under the cursor is not a map"
			}
			return clonePath(r.path), false, ""
		case smodel.KArray:
			if !m.pasteContainerOK(r.path, true) {
				return nil, false, "the value under the cursor is not an array"
			}
			return clonePath(r.path), true, ""
		}
		// oneOf drill rows are not paste targets: they fall through to the
		// menu container below.
	}
	// schema-less keys: only a container value is a paste target.
	if underCursor && r.kind == rowUnknown && len(r.path) > 0 {
		if n, ok := m.doc.Get(r.path); ok && n != nil {
			if n.Object != nil {
				return clonePath(r.path), false, ""
			}
			if n.Array != nil {
				return clonePath(r.path), true, ""
			}
		}
	}
	switch st.kind {
	case menuObject:
		if m.pasteContainerOK(st.path, false) {
			return clonePath(st.path), false, ""
		}
		return nil, false, "the current menu is not a map"
	case menuItems, menuEnumArray:
		if m.pasteContainerOK(st.path, true) {
			return clonePath(st.path), true, ""
		}
		return nil, false, "the current menu is not an array"
	case menuOneOf:
		// paste into a oneOf value only once a variant is set (a container
		// exists); never auto-create, which would guess the variant.
		if n, ok := m.doc.Get(st.path); ok && n != nil {
			if n.Object != nil {
				return clonePath(st.path), false, ""
			}
			if n.Array != nil {
				return clonePath(st.path), true, ""
			}
		}
		return nil, false, "a oneOf value must be set before pasting into it"
	}
	return nil, false, "nothing to paste into here"
}

// pasteContainerOK reports whether the document value at path can receive
// container content of the given shape: a missing/null/empty value converts
// automatically (InsertMember/Insert create the container), a container of
// the right shape is fine, any other value refuses so the insert helpers
// never panic.
func (m *listModel) pasteContainerOK(path []any, array bool) bool {
	n, ok := m.doc.Get(path)
	if !ok || n == nil || pasteEmptyNode(n) {
		return true
	}
	if array {
		return n.Array != nil
	}
	return n.Object != nil
}

// pasteEmptyNode mirrors jsondoc's own (unexported) emptiness test: null or
// no value at all converts to a container.
func pasteEmptyNode(n *jsondoc.Node) bool {
	return n.Null || (n.Object == nil && n.Array == nil && n.Str == nil &&
		n.Num == nil && n.Bool == nil)
}

// pasteInsertIndex is where the contiguous block of pasted content starts:
// member pages put it below (p) / above (P) the cursor member, array pages
// below (p) / above (P) the cursor item; a cursor not on a member/item row
// appends. Reuses memberInsertIndex (menu_keys.go) for the page-object case.
func (m *listModel) pasteInsertIndex(r row, path []any, array, underCursor bool) int {
	st := m.top()
	if st == nil || !pathEq(st.path, path) {
		// pasting into a container under the cursor: no interior cursor
		// position exists, append.
		if array {
			return docArrayLen(m.doc, path)
		}
		if cur, ok := m.doc.Get(path); ok && cur != nil && cur.Object != nil {
			return len(*cur.Object)
		}
		return 0
	}
	if array {
		if (r.kind == rowField || r.kind == rowDrill) && r.idx >= 0 {
			if underCursor { // P: above the cursor item
				return r.idx
			}
			return r.idx + 1 // p: below it
		}
		return docArrayLen(m.doc, path)
	}
	if underCursor { // P: above the cursor member
		return m.memberInsertIndex(r, false)
	}
	return m.memberInsertIndex(r, true) // p: below it
}

// pasteIntoObject merges the members: keys already in the target keep their
// position and only their value is replaced (InsertMember semantics, which
// also writes through YAML-anchored values); new keys are inserted as one
// contiguous block in clipboard order starting at idx — an in-place
// overwrite does not advance idx, so keys that follow the overwritten one
// land right after the block's last truly new key.
func (m *listModel) pasteIntoObject(path []any, members []pasteMember, idx int) {
	for _, mem := range members {
		cur, _ := m.doc.Get(path)
		exists := cur != nil && cur.Object != nil && pasteHasKey(*cur.Object, mem.key)
		m.doc.InsertMember(path, mem.key, idx, mem.val.CloneDetached())
		if !exists {
			idx++
		}
	}
}

// pasteIntoArray splices the items as one contiguous block in clipboard
// order starting at idx.
func (m *listModel) pasteIntoArray(path []any, items []*jsondoc.Node, idx int) {
	for _, it := range items {
		m.doc.Insert(path, idx, it.CloneDetached())
		idx++
	}
}

// membersAsItem wraps pasted members into one detached object node, so a
// complete mapping can land in an array as a single item.
func membersAsItem(members []pasteMember) *jsondoc.Node {
	mem := make([]jsondoc.Member, 0, len(members))
	for _, m := range members {
		mem = append(mem, jsondoc.Member{Key: m.key, Val: m.val.CloneDetached()})
	}
	return &jsondoc.Node{Object: &mem}
}

func pasteHasKey(members []jsondoc.Member, key string) bool {
	for i := range members {
		if members[i].Key == key {
			return true
		}
	}
	return false
}
