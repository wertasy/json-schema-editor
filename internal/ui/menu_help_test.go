package ui

// The "?" keyboard-help overlay and the schema-description line rendered
// above the help bar for the highlighted row.

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestHelpOverlay(t *testing.T) {
	m := newListModel(testModel(), testDoc(), Options{})
	m.Update(tea.WindowSizeMsg{Width: 170, Height: 30})

	if v := m.View(); !contains(v, "? help") {
		t.Fatalf("help bar should advertise ? help:\n%s", v)
	}
	press(m, "?")
	if v := m.View(); !contains(v, "Keyboard help") || !contains(v, "record / play a macro") {
		t.Fatalf("? should open the key reference:\n%s", v)
	}

	// no list keys leak through the overlay: navigation and history stay put
	press(m, "j", "u")
	if v := m.View(); !contains(v, "Keyboard help") {
		t.Fatalf("help overlay must swallow list keys:\n%s", v)
	}
	if s := strAt(t, m.doc, "name"); s != "webapp" {
		t.Fatalf("name = %q, keys must not reach the doc while help is open", s)
	}

	// q closes the overlay instead of aliasing to esc's quit confirm
	press(m, "q")
	if m.help || m.confirm != nil {
		t.Fatalf("q must close help without a quit confirm, help=%v confirm=%v",
			m.help, m.confirm != nil)
	}

	press(m, "?") // re-open, esc closes
	press(m, "esc")
	if m.help {
		t.Fatal("esc must close the help overlay")
	}
	if v := m.View(); !contains(v, "Name") {
		t.Fatalf("list must be back after closing help:\n%s", v)
	}
}

func TestSchemaDescLine(t *testing.T) {
	root := testModel()
	root.Children[0].Desc = "service name, lowercase" // Name row
	m := newListModel(root, testDoc(), Options{})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})

	if v := m.View(); !contains(v, "service name, lowercase") {
		t.Fatalf("description of the focused row should show:\n%s", v)
	}

	press(m, "down") // Mode has no description
	if v := m.View(); contains(v, "service name, lowercase") {
		t.Fatalf("description must not linger after leaving the row:\n%s", v)
	}

	// drilling keeps the feature: Server menu has no descriptions, then Port
	press(m, "down", "enter")
	if v := m.View(); contains(v, "service name, lowercase") {
		t.Fatal("parent's description must not show inside the submenu")
	}
}
