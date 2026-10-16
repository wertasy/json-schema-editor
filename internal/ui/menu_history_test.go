package ui

// Undo (u) / redo (ctrl+r) history: one keypress = one undo step, list-mode
// only, redo forked by new edits, dangling navigation levels repaired.

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func historyModel(t *testing.T) (*listModel, interface{ String() string }) {
	t.Helper()
	doc := testDoc()
	m := newListModel(testModel(), doc, Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return m, doc
}

func TestUndoRedoScalarEdit(t *testing.T) {
	m, _ := historyModel(t)
	press(m, "enter", "!", "enter")
	if s := strAt(t, m.doc, "name"); s != "webapp!" {
		t.Fatalf("name after edit = %q, want webapp!", s)
	}
	press(m, "u")
	if s := strAt(t, m.doc, "name"); s != "webapp" {
		t.Fatalf("name after undo = %q, want webapp", s)
	}
	if m.notice != "undone" {
		t.Fatalf("notice = %q, want undone", m.notice)
	}
	press(m, "ctrl+r")
	if s := strAt(t, m.doc, "name"); s != "webapp!" {
		t.Fatalf("name after redo = %q, want webapp!", s)
	}
	if m.notice != "redone" {
		t.Fatalf("notice = %q, want redone", m.notice)
	}
}

func TestUndoNothingToUndo(t *testing.T) {
	m, _ := historyModel(t)
	press(m, "u")
	if m.notice != "nothing to undo" {
		t.Fatalf("notice = %q, want nothing to undo", m.notice)
	}
	if s := strAt(t, m.doc, "name"); s != "webapp" {
		t.Fatalf("name changed = %q", s)
	}
	press(m, "ctrl+r")
	if m.notice != "nothing to redo" {
		t.Fatalf("notice = %q, want nothing to redo", m.notice)
	}
}

func TestRedoClearedByNewEdit(t *testing.T) {
	m, _ := historyModel(t)
	press(m, "enter", "!", "enter") // webapp!
	press(m, "u")                   // webapp
	press(m, "enter", "?", "enter") // webapp?
	if s := strAt(t, m.doc, "name"); s != "webapp?" {
		t.Fatalf("name after second edit = %q, want webapp?", s)
	}
	press(m, "ctrl+r")
	if m.notice != "nothing to redo" {
		t.Fatalf("notice = %q, want nothing to redo", m.notice)
	}
	if s := strAt(t, m.doc, "name"); s != "webapp?" {
		t.Fatalf("redo forked timeline: name = %q", s)
	}
}

func TestUndoStepsReverseOrder(t *testing.T) {
	m, _ := historyModel(t)
	press(m, "enter", "!", "enter") // name edit
	press(m, "down", "right")       // Mode enum cycle: unset → grpc
	if s := strAt(t, m.doc, "mode"); s != "grpc" {
		t.Fatalf("mode after cycle = %q, want grpc", s)
	}
	press(m, "u") // revert the enum first
	if _, ok := m.doc.Get([]any{"mode"}); ok {
		t.Fatalf("mode should be gone after first undo")
	}
	if s := strAt(t, m.doc, "name"); s != "webapp!" {
		t.Fatalf("name must survive the first undo, got %q", s)
	}
	press(m, "u") // then the name edit
	if s := strAt(t, m.doc, "name"); s != "webapp" {
		t.Fatalf("name after second undo = %q, want webapp", s)
	}
}

func TestUndoPreservesDocumentOrder(t *testing.T) {
	orig := testDoc()
	m, _ := historyModel(t)
	press(m, "enter", "!", "enter")
	press(m, "u")
	if m.doc.String() != orig.String() {
		t.Fatalf("undo reordered the document:\n%s\n---\n%s", m.doc.String(), orig.String())
	}
}

func TestUndoLiteralInsideInputs(t *testing.T) {
	// in-place scalar input: u is a rune
	m, _ := historyModel(t)
	press(m, "enter")
	if m.edit == nil {
		t.Fatal("enter should open the in-place editor")
	}
	press(m, "u")
	if m.edit == nil {
		t.Fatal("u inside the input must not commit")
	}
	if v := m.edit.ti.Value(); v != "webappu" {
		t.Fatalf("input value = %q, want webappu", v)
	}
	if s := strAt(t, m.doc, "name"); s != "webapp" {
		t.Fatalf("doc changed inside input: %q", s)
	}
	press(m, "esc")
	press(m, "u")
	if m.notice != "nothing to undo" {
		t.Fatalf("notice = %q, want nothing to undo", m.notice)
	}

	// search overlay: u is a query rune
	m2, _ := historyModel(t)
	press(m2, "/", "u")
	if !m2.search.active {
		t.Fatal("u must not close the search overlay")
	}
	if v := m2.search.ti.Value(); v != "u" {
		t.Fatalf("search query = %q, want u", v)
	}
	press(m2, "esc")
	press(m2, "u")
	if m2.notice != "nothing to undo" {
		t.Fatalf("notice = %q, want nothing to undo", m2.notice)
	}
}

func TestUndoRepairsDanglingMenu(t *testing.T) {
	m, _ := historyModel(t)
	press(m, "down", "down", "down", "down", "enter") // root → Mounts items
	press(m, "enter")                                 // < Add item > → new item menu (no doc write)
	before := docArrayLen(m.doc, []any{"mounts"})
	press(m, "down", "enter", "s", "enter") // Source row: commit src="s" on the new item
	if n := docArrayLen(m.doc, []any{"mounts"}); n != before+1 {
		t.Fatalf("mounts len = %d, want %d", n, before+1)
	}
	depth := len(m.stack)
	press(m, "u")
	if n := docArrayLen(m.doc, []any{"mounts"}); n != before {
		t.Fatalf("mounts len after undo = %d, want %d", n, before)
	}
	if m.notice != "undone" {
		t.Fatalf("notice = %q, want undone", m.notice)
	}
	if len(m.stack) != depth-1 {
		t.Fatalf("stack = %d levels, want %d (dangling item menu popped)", len(m.stack), depth-1)
	}
	m.View() // must not panic
}

func TestUndoCap(t *testing.T) {
	m, _ := historyModel(t)
	for i := 0; i < 105; i++ {
		press(m, "enter", "x", "enter")
	}
	for i := 0; i < 100; i++ {
		press(m, "u")
		if m.notice == "nothing to undo" {
			t.Fatalf("undo %d hit the empty stack early", i+1)
		}
	}
	press(m, "u")
	if m.notice != "nothing to undo" {
		t.Fatalf("notice after 101 undos = %q, want nothing to undo", m.notice)
	}
	if s := strAt(t, m.doc, "name"); len(s) != len("webapp")+5 {
		t.Fatalf("name = %q, want 5 surviving x edits", s)
	}
}
