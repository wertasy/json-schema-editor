package ui

// Confirm-overlay enter/space = yes, and the a add-schema-less-key flow:
// key-name input → raw-JSON value editor → doc write, with refusals for
// schema-defined/duplicate keys and non-object menus.

import (
	"testing"
)

// TestConfirmEnterSpaceYes: enter and space mean Yes in the quit confirm and
// the remove-item confirm.
func TestConfirmEnterSpaceYes(t *testing.T) {
	m := newListModel(testModel(), testDoc(), Options{})
	press(m, "esc") // root: asks to quit
	if m.confirm == nil || !contains(m.View(), "Quit without saving?") {
		t.Fatalf("esc at the root must ask to quit:\n%s", m.View())
	}
	press(m, "enter")
	if m.confirm != nil || !m.aborted {
		t.Fatalf("enter must confirm the quit, confirm=%v aborted=%v", m.confirm != nil, m.aborted)
	}

	// fresh model: remove a mounts item via the confirm, answered with space
	m = newListModel(testModel(), testDoc(), Options{})
	press(m, "down", "down", "down", "down", "enter") // → Mounts items menu
	press(m, "down", "enter")                         // → item menu
	press(m, "enter")                                 // → Remove this item?
	if m.confirm == nil || !contains(m.View(), "Remove this item?") {
		t.Fatalf("enter on the remove row must open the confirm:\n%s", m.View())
	}
	press(m, "space")
	if m.confirm != nil {
		t.Fatal("space must confirm the removal")
	}
	if _, ok := m.doc.Get([]any{"mounts", 0}); ok {
		t.Fatal("space = yes must remove the item")
	}
}

// TestAddUnknownKey: a opens the key input at the root, enter moves to the
// raw-JSON editor, ctrl+s writes the pair (yellow "(not in schema)" row),
// and u undoes the whole addition. Then the same flow nested under Server.
func TestAddUnknownKey(t *testing.T) {
	m := newListModel(testModel(), testDoc(), Options{})
	press(m, "a")
	if m.add == nil || !contains(m.View(), "New key") {
		t.Fatalf("a at an object menu must open the key input:\n%s", m.View())
	}
	press(m, "e", "x", "t", "r", "a", "enter")
	if m.add != nil || m.text == nil {
		t.Fatalf("enter must move to the raw editor, add=%v text=%v", m.add != nil, m.text != nil)
	}
	press(m, "4", "2", "ctrl+s")
	if m.text != nil {
		t.Fatalf("ctrl+s must apply the value, err=%v", m.text != nil && m.text.err != "")
	}
	n, ok := m.doc.Get([]any{"extra"})
	if !ok || n == nil || n.Num == nil || n.Num.String() != "42" {
		t.Fatalf("extra = %+v, want 42", n)
	}
	if v := m.View(); !contains(v, "extra") || !contains(v, "(not in schema)") {
		t.Fatalf("root view must show the unknown key:\n%s", v)
	}
	press(m, "u") // one undo removes the whole addition
	if n, ok := m.doc.Get([]any{"extra"}); ok && n != nil {
		t.Fatalf("undo must remove the added key, got %+v", n)
	}

	// nested object menu
	press(m, "down", "down", "enter") // → Server
	press(m, "a")
	if m.add == nil {
		t.Fatal("a must open the key input in the Server menu")
	}
	press(m, "n", "o", "t", "e", "enter")
	if m.add != nil || m.text == nil {
		t.Fatalf("enter must move to the raw editor, add=%v text=%v", m.add != nil, m.text != nil)
	}
	press(m, "\"", "h", "i", "\"", "ctrl+s")
	if s := strAt(t, m.doc, "server", "note"); s != "hi" {
		t.Fatalf("server.note = %q, want hi", s)
	}
}

// TestAddUnknownKeyRefusesExisting: schema-defined keys are refused with an
// inline error; esc cancels without touching the doc.
func TestAddUnknownKeyRefusesExisting(t *testing.T) {
	m := newListModel(testModel(), testDoc(), Options{})
	press(m, "a")
	press(m, "n", "a", "m", "e", "enter")
	if m.add == nil || m.add.err == "" {
		t.Fatalf("schema key must be refused with an error, add=%v err=%q",
			m.add != nil, mAddErr(m))
	}
	if _, ok := m.doc.Get([]any{"name"}); !ok {
		t.Fatal("refused add must not touch the doc")
	}
	press(m, "esc")
	if m.add != nil {
		t.Fatal("esc must close the key input")
	}
}

// TestAddUnknownKeyNonObject: a in a non-object menu (items list) is
// refused with a notice and opens nothing.
func TestAddUnknownKeyNonObject(t *testing.T) {
	m := newListModel(testModel(), testDoc(), Options{})
	press(m, "down", "down", "down", "down", "enter") // → Mounts items menu
	if m.top().kind != menuItems {
		t.Fatalf("navigation must land on the items menu, got %v", m.top().kind)
	}
	press(m, "a")
	if m.add != nil || m.notice == "" {
		t.Fatalf("a in a non-object menu must refuse: add=%v notice=%q", m.add != nil, m.notice)
	}
}

// mAddErr safely reads the add input's error for diagnostics.
func mAddErr(m *listModel) string {
	if m.add == nil {
		return ""
	}
	return m.add.err
}
