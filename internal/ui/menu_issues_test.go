package ui

// Tests for validator-issue surfacing: message parsing, the jumpable status
// row, field marks, and schema-unknown document keys shown as repairable rows.

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"json-schema-editor/internal/jsondoc"
)

func TestParseIssue(t *testing.T) {
	cases := []struct {
		msg  string
		want string // path rendered as a compact string
		ok   bool
	}{
		{"at '/server/port': minimum value is 1024", "server/port", true},
		{"at '': missing property 'port'", "port", true},
		{"at '/inner': additional properties 'extra_key' not allowed", "inner/extra_key", true},
		{"at '': additional properties 'a', 'b' not allowed", "a", true},
		{"at '/a/0/b': got number, want string", "a/0/b", true},
		{"at '/a~1b': unexpected", "a/b", true},
		{"at '': got string, want object", "", true},
		{"jsonschema validation failed", "", false},
	}
	for _, tc := range cases {
		path, ok := parseIssue(tc.msg)
		if ok != tc.ok {
			t.Errorf("%q: ok = %v, want %v", tc.msg, ok, tc.ok)
			continue
		}
		if got := renderPath(path); got != tc.want {
			t.Errorf("%q: path = %q, want %q", tc.msg, got, tc.want)
		}
	}
}

func renderPath(path []any) string {
	s := ""
	for i, seg := range path {
		if i > 0 {
			s += "/"
		}
		switch v := seg.(type) {
		case string:
			s += v
		case int:
			s += string(rune('0' + v))
		}
	}
	return s
}

func TestUnknownKeyRows(t *testing.T) {
	doc := testDoc()
	doc.Set([]any{"extra_key"}, tstr("x"))
	doc.Set([]any{"server", "legacy"}, tnum("1"))
	m := newListModel(testModel(), doc, Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	// root: the schema-unknown key is visible and flagged
	rows := menuRows(m)
	idx := -1
	for i, r := range rows {
		if r.kind == rowUnknown && r.label == "extra_key" {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatalf("unknown key row missing: %+v", rows)
	}
	if v := m.View(); !contains(v, "(not in schema)") || !contains(v, "extra_key") {
		t.Fatalf("unknown row should be marked:\n%s", v)
	}

	// enter opens the raw JSON editor with the repair hint
	m.top().cursor = idx
	press(m, "enter")
	if m.text == nil {
		t.Fatal("unknown key should open the raw editor")
	}
	if v := m.View(); !contains(v, "not in schema") {
		t.Fatalf("raw editor should explain the repair:\n%s", v)
	}
	press(m, "esc")

	// nested unknown key shows in the sub menu
	for i, r := range menuRows(m) {
		if r.fn != nil && r.fn.Key == "server" {
			m.top().cursor = i
		}
	}
	press(m, "enter") // Server menu
	found := false
	for _, r := range menuRows(m) {
		if r.kind == rowUnknown && r.label == "legacy" {
			found = true
		}
	}
	if !found {
		t.Fatalf("nested unknown key missing:\n%s", m.View())
	}
	press(m, "esc")

	// delete removes the root unknown key
	for i, r := range menuRows(m) {
		if r.kind == rowUnknown {
			m.top().cursor = i
		}
	}
	press(m, "delete")
	if _, ok := doc.Get([]any{"extra_key"}); ok {
		t.Fatal("delete must remove the unknown key")
	}
}

func TestLoadIssuesRowAndJump(t *testing.T) {
	doc := testDoc()
	doc.Set([]any{"extra_key"}, tstr("x"))
	msgs := []string{
		"at '/server/port': minimum value is 1024",
		"at '': additional properties 'extra_key' not allowed",
	}
	m := newListModel(testModel(), doc, Options{LoadErrors: msgs})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	// jumpable status row first, field marks on the affected rows
	rows := menuRows(m)
	if len(rows) == 0 || rows[0].kind != rowIssues {
		t.Fatalf("status row should come first: %+v", rows)
	}
	if !contains(rows[0].label, "2 validation errors") {
		t.Fatalf("status label = %q", rows[0].label)
	}
	v := m.View()
	if !contains(v, "⚠ 2 validation errors") {
		t.Fatalf("status row not rendered:\n%s", v)
	}
	if !strings.Contains(issueLine(v), "--->") {
		t.Fatalf("status row should offer the drill arrow:\n%s", v)
	}
	if !contains(v, "⚠") {
		t.Fatalf("affected rows should carry the warning mark:\n%s", v)
	}

	// open the list and jump to the offending field
	press(m, "enter")
	if m.issues == nil || len(m.issues.items) != 2 {
		t.Fatalf("issues overlay should list both messages: %+v", m.issues)
	}
	if v := m.View(); !contains(v, "minimum value is 1024") || !contains(v, "additional properties") {
		t.Fatalf("issues view:\n%s", v)
	}
	press(m, "enter") // jump to /server/port
	if m.issues != nil {
		t.Fatal("jump should close the overlay")
	}
	top := m.top()
	if top.node.Key != "server" {
		t.Fatalf("jump should land in the Server menu, got %q", top.node.Key)
	}
	if r := rowAtCursor(m); !pathEq(r.path, []any{"server", "port"}) {
		t.Fatalf("cursor should sit on the port row, got %v", r.path)
	}

	// jumping to a schema-unknown key lands on its row at the root; the first
	// esc returns to the issue list, the second closes it, the third pops
	press(m, "esc")
	if m.issues == nil {
		t.Fatal("esc at the jump target should return to the issues list")
	}
	press(m, "esc") // close the list
	press(m, "esc") // pop to the root menu
	if len(m.stack) != 1 {
		t.Fatalf("stack = %d, want the root menu", len(m.stack))
	}
	m.top().cursor = 0
	press(m, "enter", "down", "enter") // status row → issues → second entry → jump
	if m.issues != nil {
		t.Fatal("jump should close the overlay")
	}
	if r := rowAtCursor(m); r.kind != rowUnknown || r.label != "extra_key" {
		t.Fatalf("cursor should sit on the unknown-key row, got %+v", r)
	}

	// the overlay recomputes against the current doc when a validator exists
	m.opts.Validate = func(*jsondoc.Node) []string {
		return []string{"at '/name': got number, want string"}
	}
	m.top().cursor = 0
	press(m, "enter")
	if m.issues == nil || len(m.issues.items) != 1 || !contains(m.issues.items[0].msg, "/name") {
		t.Fatalf("overlay should recompute from the validator: %+v", m.issues)
	}
	press(m, "esc")
	if m.issues != nil {
		t.Fatal("esc should close the overlay")
	}
}

// issueLine extracts the rendered validation-status row.
func issueLine(view string) string {
	for _, l := range strings.Split(view, "\n") {
		if strings.Contains(l, "validation errors") {
			return l
		}
	}
	return ""
}
