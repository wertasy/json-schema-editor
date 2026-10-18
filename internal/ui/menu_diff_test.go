package ui

// Tests for change marking: rows whose JSON differs from the loaded/last-saved
// baseline get a trailing "●", the title carries a global dirty dot, and
// saving clears both.

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/wertasy/json-schema-editor/internal/jsondoc"
)

func TestNodesEqual(t *testing.T) {
	obj := func(ms ...jsondoc.Member) *jsondoc.Node {
		o := []jsondoc.Member(ms)
		return &jsondoc.Node{Object: &o}
	}
	cases := []struct {
		name string
		a, b *jsondoc.Node
		want bool
	}{
		{"identical objects, different member order",
			obj(tmem("a", tstr("1")), tmem("b", tstr("2"))),
			obj(tmem("b", tstr("2")), tmem("a", tstr("1"))), true},
		{"value differs", obj(tmem("a", tstr("1"))), obj(tmem("a", tstr("2"))), false},
		{"key count differs", obj(tmem("a", tstr("1"))), obj(tmem("a", tstr("1")), tmem("b", tstr("2"))), false},
		{"array order matters", tarr(tstr("x"), tstr("y")), tarr(tstr("y"), tstr("x")), false},
		{"array equal", tarr(tstr("x")), tarr(tstr("x")), true},
		{"nil vs empty object", nil, tobj(), false},
		{"null equals null", tnull(), tnull(), true},
		{"number text", tnum("8080"), tnum("8080"), true},
		{"number text differs", tnum("8080"), tnum("8080.0"), false},
	}
	for _, tc := range cases {
		if got := nodesEqual(tc.a, tc.b); got != tc.want {
			t.Errorf("%s: nodesEqual = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestChangeMarkers(t *testing.T) {
	doc := testDoc()
	var saves int
	m := newListModel(testModel(), doc, countSave(&saves))
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	// freshly loaded: nothing marked, no dirty dot
	if m.dirty() || contains(m.View(), "●") {
		t.Fatalf("fresh load must be clean:\n%s", m.View())
	}

	// edit the root Name field in place
	press(m, "enter") // Name row
	if m.edit == nil {
		t.Fatal("name input did not open")
	}
	press(m, "!", "enter")
	if s := strAt(t, doc, "name"); s != "webapp!" {
		t.Fatalf("name = %q", s)
	}
	if !m.dirty() {
		t.Fatal("dirty must report the change")
	}
	m.clampCursor(menuRows(m))
	if v := m.View(); !contains(v, "webapp! ●") {
		t.Fatalf("changed row should carry the marker:\n%s", v)
	}
	if name, _ := doc.Get([]any{"name"}); nodesEqual(name, mustGet(t, m.baseline, "name")) {
		t.Fatal("baseline must keep the loaded value")
	}

	// nested change: drill into Server and touch the port → parent row marks
	press(m, "down", "down", "enter") // Server
	press(m, "enter")                 // Port input
	press(m, "ctrl+u", "9", "0", "5", "0", "enter")
	if v := m.View(); !contains(v, "9050 ●") {
		t.Fatalf("port row should show the new value with its marker:\n%s", v)
	}
	press(m, "esc") // back to the root menu
	if v := m.View(); !contains(v, "Server") || !contains(v, "●") {
		t.Fatalf("parent row should mark the nested change:\n%s", v)
	}
	rows := menuRows(m)
	serverRow := row{path: []any{"server"}}
	for _, r := range rows {
		if r.fn != nil && r.fn.Key == "server" {
			serverRow = r
		}
	}
	if !m.changed(serverRow.path) {
		t.Fatal("server subtree must register as changed")
	}
	if m.changed([]any{"logging"}) {
		t.Fatal("untouched sibling must stay clean")
	}

	// saving clears the baseline and keeps the session open
	m.Update(key("ctrl+s"))
	if m.confirm != nil {
		press(m, "y")
	}
	if saves != 1 || !m.saved {
		t.Fatal("ctrl+s must write through Save")
	}
	if m.dirty() || contains(m.View(), "●") {
		t.Fatalf("markers must clear after save:\n%s", m.View())
	}
}

func mustGet(t *testing.T, n *jsondoc.Node, path ...any) *jsondoc.Node {
	t.Helper()
	v, ok := n.Get(path)
	if !ok {
		t.Fatalf("baseline missing %v", path)
	}
	return v
}
