package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"json-schema-editor/internal/jsondoc"
	"json-schema-editor/internal/smodel"
)

// TestSpliceAddEditsInlineOnTheNewRow: a/A splice a placeholder element and
// the editor renders on that row instead of a separate input line; the
// append-only < Add item > flow labels its standalone input key[N], the
// address the element will take once committed.
func TestSpliceAddEditsInlineOnTheNewRow(t *testing.T) {
	mk := func() (*listModel, *jsondoc.Node) {
		headers := &smodel.FormNode{Key: "headers", Title: "Headers", Kind: smodel.KMap,
			Items: &smodel.FormNode{Key: "headers", Title: "headers", Kind: smodel.KOneOf, Variants: []*smodel.FormNode{
				{Key: "headers", Title: "string", Kind: smodel.KScalar, JSONType: "string"},
				{Key: "headers", Title: "list", Kind: smodel.KArray,
					Items: &smodel.FormNode{Key: "headers", Kind: smodel.KScalar, JSONType: "string"}},
			}}}
		root := &smodel.FormNode{Key: "$", Title: "$", Kind: smodel.KObject,
			Children: []*smodel.FormNode{headers}}
		doc := tobj(tmem("headers", tobj(tmem("Host", tarr(tstr("canhui.wang"))))))
		m := newListModel(root, doc, Options{})
		m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
		press(m, "enter") // headers menu
		press(m, "enter") // Host -> list editor
		return m, doc
	}

	// a inserts before the cursor item; the placeholder row carries the input
	m, doc := mk()
	press(m, "down", "a")
	if m.edit == nil || !m.edit.isNew {
		t.Fatal("a on a scalar item must open the new-item editor")
	}
	v := m.View()
	if !contains(v, "> Host[0]") || contains(v, "New item") {
		t.Fatalf("spliced item must edit inline on its row:\n%s", v)
	}
	// row order: unselected < Add item >, the inline editor on Host[0],
	// then the shifted existing element
	if !contains(v, "  < Add item >") || !contains(v, "  Host[1]") {
		t.Fatalf("rows around the spliced item are wrong:\n%s", v)
	}
	if strings.Index(v, "< Add item >") > strings.Index(v, "> Host[0]") {
		t.Fatalf("the inline editor must come after < Add item >:\n%s", v)
	}
	press(m, "x", "enter")
	if got := strArrAt(t, doc, "headers", "Host"); !eqStrs(got, "x", "canhui.wang") {
		t.Fatalf("a before item 0 = %v, want x canhui.wang", got)
	}

	// A inserts after the cursor item: the row below hosts the input
	m, doc = mk()
	press(m, "down", "A")
	v = m.View()
	if !contains(v, "> Host[1]") || contains(v, "New item") {
		t.Fatalf("A-spliced item must edit inline on its row:\n%s", v)
	}
	press(m, "y", "enter")
	if got := strArrAt(t, doc, "headers", "Host"); !eqStrs(got, "canhui.wang", "y") {
		t.Fatalf("A after item 0 = %v, want canhui.wang y", got)
	}

	// < Add item > writes nothing until a field is set: no row exists, so
	// the standalone input is labeled with the address it will take (len=1)
	m, doc = mk()
	press(m, "enter")
	v = m.View()
	if !contains(v, "> Host[1]") || contains(v, "New item") {
		t.Fatalf("traceless append must label the input Host[1]:\n%s", v)
	}
	press(m, "z", "enter")
	if got := strArrAt(t, doc, "headers", "Host"); !eqStrs(got, "canhui.wang", "z") {
		t.Fatalf("< Add item > append = %v, want canhui.wang z", got)
	}
	// and the committed row takes that label
	if v = m.View(); !contains(v, "Host[1]") || !contains(v, "z") {
		t.Fatalf("committed element must appear as Host[1]:\n%s", v)
	}
}
