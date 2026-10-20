package ui

// Quick move (f/F): press f or F, type one character, and the cursor jumps
// to the next/previous key starting with it (case-insensitive, wrapping at
// the ends, skipping array items and action rows). esc cancels while
// waiting; any other key cancels and is handled as usual.

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type findState struct {
	backward bool // F: search upward
}

// startFind arms the quick-move prompt (f: downward, F: upward).
func (m *listModel) startFind(backward bool) {
	m.find = &findState{backward: backward}
}

// findKey handles a key while quick move waits for a character: a rune
// moves the cursor and ends the find, esc cancels it in place. handled=
// false passes any other key through — the find ends and the key works as
// usual.
func (m *listModel) findKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	switch {
	case msg.Type == tea.KeyEsc:
		m.find = nil
		m.notice = "quick move cancelled"
		return nil, true
	case msg.Type == tea.KeyRunes && len(msg.Runes) > 0:
		ch := msg.Runes[0]
		backward := m.find.backward
		m.find = nil
		m.findMove(ch, backward)
		return nil, true
	}
	return nil, false
}

// findMove moves the cursor to the nearest row after (or, backward, before)
// the cursor whose key starts with ch, wrapping at the ends; the cursor row
// itself is skipped.
func (m *listModel) findMove(ch rune, backward bool) {
	st := m.top()
	if st == nil {
		return
	}
	rows := menuRows(m)
	want := string(ch)
	for i := 1; i <= len(rows); i++ {
		idx := st.cursor
		if backward {
			idx -= i
		} else {
			idx += i
		}
		idx %= len(rows)
		if idx < 0 {
			idx += len(rows)
		}
		key, ok := findRowKey(rows[idx])
		if ok && strings.HasPrefix(key, want) {
			st.cursor = idx
			return
		}
	}
	m.notice = fmt.Sprintf("no key starts with %q", string(ch))
}

// findRowKey returns the key a row matches by: the schema key, or the
// document key (last path segment) for schema-less rows. Only key-value
// rows match — array items and action rows are skipped.
func findRowKey(r row) (string, bool) {
	if !selectableRow(r) {
		return "", false
	}
	if r.fn != nil && r.fn.Key != "" {
		return r.fn.Key, true
	}
	if len(r.path) == 0 {
		return "", false
	}
	key, ok := r.path[len(r.path)-1].(string)
	return key, ok && key != ""
}
