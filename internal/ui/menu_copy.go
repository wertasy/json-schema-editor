package ui

// Copy shortcuts: y yanks the selected entry's value (strings verbatim, all
// other values as compact JSON), Y yanks a single-line `"key": value` pair.
// Both write to the system clipboard and leave a short notice; the copied
// text is never echoed, so secrets in the config do not leak onto the screen.

import (
	"encoding/json"

	"github.com/atotto/clipboard"

	"github.com/wertasy/json-schema-editor/internal/jsondoc"
)

// copyRow copies the row under the cursor. pair selects the Y form; rows
// without an object key (array elements, the root scalar) are refused for Y.
// Value rows whose key is absent from the document have nothing to copy;
// explicit JSON null is a value and copies as "null".
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
	text := copyValueText(v)
	if pair {
		key, ok := copyRowKey(r.path)
		if !ok {
			m.notice = "Y copies object keys only"
			return
		}
		raw, err := json.Marshal(key)
		if err != nil {
			m.notice = "copy failed: " + err.Error()
			return
		}
		text = string(raw) + ": " + v.Compact()
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

// copyValueText is the y text: strings copy verbatim (what the row shows),
// every other value uses compact JSON (numbers keep their exact literal,
// containers keep document order). Y always uses the JSON form.
func copyValueText(v *jsondoc.Node) string {
	if v != nil && v.Str != nil {
		return *v.Str
	}
	return v.Compact()
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
