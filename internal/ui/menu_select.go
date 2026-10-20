package ui

// Row multi-selection: m marks/unmarks the key-value row under the cursor
// (rendered as a "*" in the cursor column); y copies every marked pair as
// one object and d deletes them in one step (the keypress is one undo
// step). The selection lives on the menu state, survives scrolling and
// back/enter round-trips, and clears when a batch action runs.

import (
	"strconv"
	"strings"
)

// selectableRow reports whether a row can carry a selection mark: the same
// key-value rows m accepts (object-menu fields, container rows and
// schema-less keys — no array items, no action rows).
func selectableRow(r row) bool {
	switch r.kind {
	case rowField, rowDrill, rowUnknown:
	default:
		return false
	}
	if len(r.path) == 0 {
		return false
	}
	if _, isIdx := r.path[len(r.path)-1].(int); isIdx {
		return false
	}
	return true
}

// toggleSelect flips the cursor row's membership in the menu's selection.
// Only key-value rows qualify: array items and action rows are refused.
func (m *listModel) toggleSelect(r row) {
	if !selectableRow(r) {
		if (r.kind == rowField || r.kind == rowDrill) && len(r.path) > 0 {
			if _, isIdx := r.path[len(r.path)-1].(int); isIdx {
				m.notice = "m marks key-value pairs, not array items"
				return
			}
		}
		m.notice = "m marks key-value rows"
		return
	}
	st := m.top()
	if st == nil {
		return
	}
	if st.sel == nil {
		st.sel = map[string]bool{}
	}
	k := selKey(r.path)
	if st.sel[k] {
		delete(st.sel, k)
	} else {
		st.sel[k] = true
	}
}

// visualSync rebuilds the selection while visual select (v) is on: exactly
// the selectable rows between the anchor (where v was pressed) and the
// cursor — moving back shrinks the range, so rows leave the selection.
// Non-selectable rows (array items, action rows) inside the range are
// skipped. Without visual select it does nothing.
func (m *listModel) visualSync(st *menuState, rows []row) {
	if st == nil || !st.visual {
		return
	}
	lo, hi := st.vanchor, st.cursor
	if lo > hi {
		lo, hi = hi, lo
	}
	sel := map[string]bool{}
	for i := lo; i <= hi && i < len(rows); i++ {
		if i < 0 || !selectableRow(rows[i]) {
			continue
		}
		sel[selKey(rows[i].path)] = true
	}
	st.sel = sel
}

// rowSelected reports whether the row is in the current menu's selection.
func (m *listModel) rowSelected(r row) bool {
	st := m.top()
	return st != nil && len(r.path) > 0 && st.sel[selKey(r.path)]
}

// visualOn reports whether the current menu is in visual select (v): the
// cursor row's marker is bright red while it is on, plain white when off.
func (m *listModel) visualOn() bool {
	st := m.top()
	return st != nil && st.visual
}

// selKey renders a path as the selection map key; the s:/i: tags keep
// string keys and array indexes distinct.
func selKey(path []any) string {
	var b strings.Builder
	for _, seg := range path {
		switch v := seg.(type) {
		case string:
			b.WriteString("s:")
			b.WriteString(v)
		case int:
			b.WriteString("i:")
			b.WriteString(strconv.Itoa(v))
		}
		b.WriteByte(0)
	}
	return b.String()
}
