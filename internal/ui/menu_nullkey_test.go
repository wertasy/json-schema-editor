package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/wertasy/json-schema-editor/internal/jsondoc"
	"github.com/wertasy/json-schema-editor/internal/smodel"
)

func nullKeyModel(t *testing.T) (*listModel, *jsondoc.Node) {
	t.Helper()
	headers := &smodel.FormNode{Key: "headers", Title: "Headers", Kind: smodel.KMap,
		Items: &smodel.FormNode{Key: "headers", Title: "headers", Kind: smodel.KOneOf, Variants: []*smodel.FormNode{
			{Key: "headers", Title: "string", Kind: smodel.KScalar, JSONType: "string"},
			{Key: "headers", Title: "list", Kind: smodel.KArray,
				Items: &smodel.FormNode{Key: "headers", Kind: smodel.KScalar, JSONType: "string"}},
		}}}
	root := &smodel.FormNode{Key: "$", Title: "$", Kind: smodel.KObject,
		Children: []*smodel.FormNode{headers}}
	doc := tobj(tmem("headers", tobj()))
	m := newListModel(root, doc, Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	press(m, "enter") // headers menu
	return m, doc
}

// addNullStringKey walks a → key name → picker → string variant and leaves
// the in-place editor open on the null-seeded key.
func addNullStringKey(t *testing.T, m *listModel, key string) {
	t.Helper()
	press(m, "a")
	m.add.ti.SetValue(key)
	m.keyAdd(tea.KeyMsg{Type: tea.KeyEnter}) // seeds null + opens the picker
	press(m, "enter")                        // string variant (cursor on string)
	if m.edit == nil {
		t.Fatalf("string variant should open the in-place editor")
	}
}

// TestEmptyStringVariantKeepsNullKey: committing the string variant with an
// empty input must keep the created key as null instead of deleting it.
func TestEmptyStringVariantKeepsNullKey(t *testing.T) {
	m, doc := nullKeyModel(t)
	addNullStringKey(t, m, "Host")
	press(m, "enter") // empty commit
	if m.edit != nil {
		t.Fatal("edit should close")
	}
	v, ok := doc.Get([]any{"headers", "Host"})
	if !ok || v == nil || !v.Null {
		t.Fatalf("Host = %v (ok=%v), want the key kept as null", v, ok)
	}
	if view := m.View(); !contains(view, "Host") {
		t.Fatalf("key must stay visible:\n%s", view)
	}
}

// TestEscStringVariantKeepsNullKey: cancelling the editor keeps the seeded
// key as null too.
func TestEscStringVariantKeepsNullKey(t *testing.T) {
	m, doc := nullKeyModel(t)
	addNullStringKey(t, m, "Host")
	press(m, "esc")
	v, ok := doc.Get([]any{"headers", "Host"})
	if !ok || v == nil || !v.Null {
		t.Fatalf("Host = %v (ok=%v), want the key kept as null", v, ok)
	}
}
