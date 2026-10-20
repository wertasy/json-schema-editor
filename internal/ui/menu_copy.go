package ui

// Copy shortcuts: y yanks the selected entry's `"key": value` pair, Y yanks
// the value itself. Text follows the document syntax — compact JSON / JSON
// pairs in JSON documents, block YAML / `key: value` in YAML sessions;
// strings always copy verbatim. Both write to the system clipboard and leave
// a short notice; the copied text is never echoed, so secrets in the config
// do not leak onto the screen.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/atotto/clipboard"

	"github.com/wertasy/json-schema-editor/internal/jsondoc"
)

// copyRow copies the row under the cursor. pair selects the y form; rows
// without an object key (array elements, the root scalar) are refused for y.
// Value rows whose key is absent from the document have nothing to copy;
// explicit JSON null is a value and copies as "null" / "key: null".
func (m *listModel) copyRow(r row, pair bool) {
	text, ok, refusal := m.rowCopyText(r, pair, "y copies object keys only")
	if !ok {
		m.notice = refusal
		return
	}
	write := m.opts.Copy
	if write == nil {
		write = clipboard.WriteAll
	}
	if err := write(text); err != nil {
		m.notice = "copy failed: " + err.Error()
		return
	}
	if pair {
		m.notice = "Copied key-value pair"
	} else {
		m.notice = "Copied value"
	}
}

// rowCopyText builds the y/X clipboard text for the row: the pair (y/x) or
// the value (Y/X). ok=false carries the refusal notice; keyRefusal is the
// message for rows without an object key (worded per shortcut).
func (m *listModel) rowCopyText(r row, pair bool, keyRefusal string) (string, bool, string) {
	switch r.kind {
	case rowField, rowDrill, rowUnknown:
	default:
		return "", false, "nothing to copy"
	}
	if m.doc == nil {
		return "", false, "no value to copy"
	}
	v, ok := m.doc.Get(r.path)
	if !ok || v == nil {
		return "", false, "no value to copy"
	}
	if pair {
		key, ok := copyRowKey(r.path)
		if !ok {
			return "", false, keyRefusal
		}
		return m.copyPairText(key, v), true, ""
	}
	return m.copyValueText(v), true, ""
}

// selectionMembers collects the marked rows' key-value pairs in display
// order; rows without a document value are skipped.
func (m *listModel) selectionMembers() []jsondoc.Member {
	st := m.top()
	if st == nil {
		return nil
	}
	var members []jsondoc.Member
	for _, sel := range menuRows(m) {
		if len(sel.path) == 0 || !st.sel[selKey(sel.path)] {
			continue
		}
		key, ok := sel.path[len(sel.path)-1].(string)
		if !ok {
			continue
		}
		if v, ok := m.doc.Get(sel.path); ok && v != nil {
			members = append(members, jsondoc.Member{Key: key, Val: v})
		}
	}
	return members
}

// copyRowOrSelection is the y handler: with marked rows it copies every
// selected key-value pair as one object in the document syntax (display
// order, rows without a document value skipped) and clears the selection;
// without, it copies the cursor row's pair.
func (m *listModel) copyRowOrSelection(r row) {
	st := m.top()
	if st == nil || len(st.sel) == 0 {
		m.copyRow(r, true)
		return
	}
	members := m.selectionMembers()
	if len(members) == 0 {
		m.notice = "no value to copy"
		return
	}
	text := m.copyValueText(&jsondoc.Node{Object: &members})
	write := m.opts.Copy
	if write == nil {
		write = clipboard.WriteAll
	}
	if err := write(text); err != nil {
		m.notice = "copy failed: " + err.Error()
		return
	}
	m.notice = fmt.Sprintf("Copied %d key-value pairs", len(members))
	st.sel = nil
}

// cutRowOrSelection is the x/X handler: copy like y/Y, then delete — the
// marked rows in batch (x: one object/mapping, X: the values joined by
// newlines), otherwise the cursor row. The clipboard write happens before
// the delete, so a failed write leaves the document untouched; delete-side
// refusals (required merged keys) keep the row and their notice stands.
func (m *listModel) cutRowOrSelection(r row, pair bool) {
	st := m.top()
	var text string
	var count int
	batch := st != nil && len(st.sel) > 0
	if batch {
		if pair {
			members := m.selectionMembers()
			if len(members) == 0 {
				m.notice = "no value to copy"
				return
			}
			text = m.copyValueText(&jsondoc.Node{Object: &members})
			count = len(members)
		} else {
			var values []string
			for _, mem := range m.selectionMembers() {
				values = append(values, m.copyValueText(mem.Val))
				count++
			}
			text = strings.Join(values, "\n")
		}
	} else {
		t, ok, refusal := m.rowCopyText(r, pair, "x cuts object keys only")
		if !ok {
			m.notice = refusal
			return
		}
		text = t
		count = 1
	}
	write := m.opts.Copy
	if write == nil {
		write = clipboard.WriteAll
	}
	if err := write(text); err != nil {
		m.notice = "cut failed: " + err.Error()
		return
	}
	m.deleteRow(r) // clears the selection; refusals surface their notice
	if m.notice == "" {
		switch {
		case !batch && pair:
			m.notice = "Cut key-value pair"
		case !batch:
			m.notice = "Cut value"
		case pair:
			m.notice = fmt.Sprintf("Cut %d key-value pairs", count)
		default:
			m.notice = fmt.Sprintf("Cut %d values", count)
		}
	}
}

// copyValueText is the Y text: strings copy verbatim (what the row shows),
// everything else is written in the document's syntax — block YAML through
// the session codec in YAML documents, compact JSON otherwise (numbers keep
// their exact literal, containers keep document order).
func (m *listModel) copyValueText(v *jsondoc.Node) string {
	if v != nil && v.Str != nil {
		return *v.Str
	}
	if marshal := m.opts.Raw.MarshalValue; marshal != nil {
		var b bytes.Buffer
		if err := marshal(&b, v); err == nil {
			return strings.TrimRight(b.String(), "\n")
		}
	}
	return v.Compact()
}

// copyPairText is the y text: `"key": value` in JSON documents, `key: value`
// in YAML documents — a synthetic one-member object goes through the session
// codec, so container values come out as a `key:` block.
func (m *listModel) copyPairText(key string, v *jsondoc.Node) string {
	if marshal := m.opts.Raw.MarshalValue; marshal != nil {
		var b bytes.Buffer
		obj := &jsondoc.Node{Object: &[]jsondoc.Member{{Key: key, Val: v}}}
		if err := marshal(&b, obj); err == nil {
			return strings.TrimRight(b.String(), "\n")
		}
	}
	raw, _ := json.Marshal(key) // marshaling a string cannot fail
	return string(raw) + ": " + v.Compact()
}

// copyRowKey returns the row's object key: the last path segment when it is
// a string. Array indexes and the root path have no object key.
func copyRowKey(path []any) (string, bool) {
	if len(path) == 0 {
		return "", false
	}
	key, ok := path[len(path)-1].(string)
	return key, ok
}
