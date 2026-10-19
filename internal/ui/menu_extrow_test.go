package ui

// External value editing: e opens $EDITOR on the row's value, and multiline
// strings route enter to $EDITOR instead of the one-line input (they cannot
// be authored or displayed there).

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/wertasy/json-schema-editor/internal/jsondoc"
)

func TestMultilineStringOpensExternalEditor(t *testing.T) {
	doc := testDoc()
	multiline := "run: |\n  mkdir build\n  make"
	doc.Set([]any{"name"}, &jsondoc.Node{Str: &multiline})
	m := newListModel(testModel(), doc, Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	press(m, "enter") // Name is row 0 and holds a multiline string
	if m.edit != nil {
		t.Fatal("multiline string must not open the one-line input")
	}
	if m.ext == nil || m.ext.insertIdx != -1 || !pathEq(m.ext.target, []any{"name"}) {
		t.Fatalf("multiline enter must open $EDITOR on the value, ext = %+v", m.ext)
	}
	finishExternalValue(t, m, `"single line"`)
	if got, _ := doc.Get([]any{"name"}); got == nil || got.Str == nil || *got.Str != "single line" {
		s := ""
		if got != nil && got.Str != nil {
			s = *got.Str
		}
		t.Fatalf("applied value = %q, want single line", s)
	}

	// the applied value is single-line again: enter edits in place
	press(m, "enter")
	if m.edit == nil || m.ext != nil {
		t.Fatalf("single-line enter must edit in place (edit %v, ext %v)", m.edit != nil, m.ext != nil)
	}
}

func TestERowOpensExternalEditor(t *testing.T) {
	m := newListModel(testModel(), testDoc(), Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	// e on a scalar field row edits its value
	press(m, "e")
	if m.ext == nil || !pathEq(m.ext.target, []any{"name"}) {
		t.Fatalf("e on a field row must open $EDITOR, ext = %+v", m.ext)
	}
	finishExternalValue(t, m, `"renamed"`)
	if got, _ := m.doc.Get([]any{"name"}); got == nil || got.Str == nil || *got.Str != "renamed" {
		t.Fatal("e must write the edited value through")
	}

	// e on a container row edits the container's whole value
	press(m, "down", "down", "e")
	if m.ext == nil || !pathEq(m.ext.target, []any{"server"}) {
		t.Fatalf("e on a container row must open $EDITOR, ext = %+v", m.ext)
	}
	finishExternalValue(t, m, "{}\n")
	if _, ok := m.doc.Get([]any{"server"}); !ok {
		t.Fatal("container edit must keep the key")
	}

	// action rows carry no document value
	press(m, "G", "e")
	if m.notice != "no value to edit" || m.ext != nil {
		t.Fatalf("e on an action row: notice = %q, ext = %v", m.notice, m.ext != nil)
	}
}

// TestUnknownERowEditsValue: e works on schema-less rows too.
func TestUnknownERowEditsValue(t *testing.T) {
	doc := testDoc()
	doc.Set([]any{"extra_unknown_key"}, &jsondoc.Node{Str: strptr("v")})
	m := newListModel(testModel(), doc, Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	// root rows: status, name, mode, server, logging, mounts, auth, unknown
	// (the unknown key raises an editor-level warning → status row first)
	press(m, "down", "down", "down", "down", "down", "down", "down", "e")
	if m.ext == nil || !pathEq(m.ext.target, []any{"extra_unknown_key"}) {
		t.Fatalf("e on an unknown row must open $EDITOR, ext = %+v", m.ext)
	}
}

func strptr(s string) *string { return &s }
