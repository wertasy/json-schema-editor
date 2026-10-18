package ui

// Raw editors follow the document syntax: JSON by default, YAML when the
// caller supplies a YAML codec.

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/wertasy/json-schema-editor/internal/jsondoc"
	"github.com/wertasy/json-schema-editor/internal/smodel"
	"github.com/wertasy/json-schema-editor/internal/yamldoc"
)

func yamlTestCodec() RawCodec {
	return RawCodec{
		Label: "YAML",
		Ext:   ".yaml",
		MarshalValue: func(w io.Writer, n *jsondoc.Node) error {
			return yamldoc.Write(w, n, nil)
		},
		UnmarshalValue: func(data []byte) (*jsondoc.Node, error) {
			d, _, err := yamldoc.Parse(data)
			return d, err
		},
		UnmarshalDoc: func(data []byte) (*jsondoc.Node, error) {
			d, _, err := yamldoc.Parse(data)
			return d, err
		},
	}
}

func TestYAMLRawTextarea(t *testing.T) {
	root := &smodel.FormNode{Key: "$", Title: "T", Kind: smodel.KObject,
		Children: []*smodel.FormNode{{Key: "raw", Kind: smodel.KRaw}}}
	doc := tobj(tmem("raw", tobj(tmem("k", tstr("v")))))
	m := newListModel(root, doc, Options{Raw: yamlTestCodec()})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if v := m.View(); !contains(v, "< Edit raw YAML >") {
		t.Fatalf("root raw row must say YAML:\n%s", v)
	}

	press(m, "enter") // raw field
	if m.text == nil {
		t.Fatal("raw field must open the textarea")
	}
	if got := strings.TrimSpace(m.text.ta.Value()); got != "k: v" {
		t.Fatalf("prefill = %q, want YAML", got)
	}
	m.text.ta.SetValue("k: changed")
	press(m, "ctrl+s")
	if m.text != nil {
		t.Fatalf("apply must close the editor, err=%q", m.text.err)
	}
	if got := strAt(t, doc, "raw", "k"); got != "changed" {
		t.Fatalf("raw.k = %q, want changed", got)
	}
}

func TestYAMLExternalValueEdit(t *testing.T) {
	doc := testDoc()
	doc.Set([]any{"extra"}, tobj(tmem("k", tstr("v"))))
	m := newListModel(testModel(), doc, Options{Raw: yamlTestCodec()})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	m.editValueExternal([]any{"extra"}, -1)
	if m.ext == nil || filepath.Ext(m.ext.path) != ".yaml" {
		t.Fatalf("pending edit = %+v, want a .yaml temp file", m.ext)
	}
	finishExternalValue(t, m, "k: changed")
	if got := strAt(t, doc, "extra", "k"); got != "changed" {
		t.Fatalf("extra.k = %q, want changed", got)
	}
}

func TestYAMLExternalDocApply(t *testing.T) {
	doc := testDoc()
	m := newListModel(testModel(), doc, Options{Raw: yamlTestCodec()})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	path := filepath.Join(t.TempDir(), "doc.yaml")
	if err := os.WriteFile(path, []byte("name: edited\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m.Update(rawEditorDoneMsg{path: path})
	if got := strAt(t, doc, "name"); got != "edited" {
		t.Fatalf("name = %q, want edited", got)
	}
	if !strings.Contains(m.notice, "Raw YAML edited") {
		t.Fatalf("notice = %q, want Raw YAML edited", m.notice)
	}
}
