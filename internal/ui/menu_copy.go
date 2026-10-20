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
	switch r.kind {
	case rowField, rowDrill, rowUnknown:
	default:
		m.notice = "nothing to copy"
		return
	}
	if m.doc == nil {
		m.notice = "no value to copy"
		return
	}
	v, ok := m.doc.Get(r.path)
	if !ok || v == nil {
		m.notice = "no value to copy"
		return
	}
	var text string
	if pair {
		key, ok := copyRowKey(r.path)
		if !ok {
			m.notice = "y copies object keys only"
			return
		}
		text = m.copyPairText(key, v)
	} else {
		text = m.copyValueText(v)
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
