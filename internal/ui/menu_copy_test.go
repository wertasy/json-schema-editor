package ui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"json-schema-editor/internal/jsondoc"
	"json-schema-editor/internal/smodel"
)

type clipboardRecorder struct {
	texts []string
	err   error
}

func (c *clipboardRecorder) write(s string) error {
	if c.err != nil {
		return c.err
	}
	c.texts = append(c.texts, s)
	return nil
}

func newCopyModel(t *testing.T, root *smodel.FormNode, doc *jsondoc.Node, rec *clipboardRecorder) *listModel {
	t.Helper()
	m := newListModel(root, doc, Options{Copy: rec.write})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return m
}

func TestCopyValueAndPair(t *testing.T) {
	doc := testDoc()
	doc.Set([]any{"server", "port"}, tnum("1.0")) // exact literal must survive
	rec := &clipboardRecorder{}
	m := newCopyModel(t, testModel(), doc, rec)

	// Name: scalar string copies verbatim; Y uses the JSON literal.
	press(m, "y")
	if len(rec.texts) != 1 || rec.texts[0] != "webapp" {
		t.Fatalf("y copied %q, want the raw string", rec.texts)
	}
	if m.notice != "Copied value" {
		t.Fatalf("notice = %q", m.notice)
	}
	press(m, "Y")
	if len(rec.texts) != 2 || rec.texts[1] != `"name": "webapp"` {
		t.Fatalf("Y copied %q, want the JSON pair", rec.texts)
	}
	if m.notice != "Copied key-value pair" {
		t.Fatalf("notice = %q", m.notice)
	}

	// Server: container keeps document order and the numeric literal.
	press(m, "down", "down")
	press(m, "y")
	want := `{"port":1.0,"host":"0.0.0.0","debug":true}`
	if got := rec.texts[len(rec.texts)-1]; got != want {
		t.Fatalf("container y = %q, want %q", got, want)
	}
	press(m, "Y")
	want = `"server": ` + want
	if got := rec.texts[len(rec.texts)-1]; got != want {
		t.Fatalf("container Y = %q, want %q", got, want)
	}
}

func TestCopyArrayElementAndNull(t *testing.T) {
	root := &smodel.FormNode{Key: "$", Title: "T", Kind: smodel.KObject,
		Children: []*smodel.FormNode{
			{Key: "items", Title: "Items", Kind: smodel.KArray,
				Items: &smodel.FormNode{Key: "items", Kind: smodel.KObject,
					Children: []*smodel.FormNode{
						{Key: "x", Title: "X", Kind: smodel.KScalar, JSONType: "string"},
					}}},
			{Key: "nothing", Title: "Nothing", Kind: smodel.KScalar, JSONType: "string"},
		}}
	doc := tobj(
		tmem("items", tarr(tobj(tmem("x", tstr("v"))))),
		tmem("nothing", tnull()),
	)
	rec := &clipboardRecorder{}
	m := newCopyModel(t, root, doc, rec)

	press(m, "enter") // items menu: Add(0), item0(1)
	press(m, "down")
	press(m, "y")
	if got := rec.texts[len(rec.texts)-1]; got != `{"x":"v"}` {
		t.Fatalf("array element y = %q", got)
	}
	before := len(rec.texts)
	press(m, "Y")
	if len(rec.texts) != before {
		t.Fatal("Y must refuse array elements")
	}
	if m.notice != "Y copies object keys only" {
		t.Fatalf("notice = %q", m.notice)
	}

	press(m, "esc") // back to the root menu
	press(m, "down")
	press(m, "y")
	if got := rec.texts[len(rec.texts)-1]; got != "null" {
		t.Fatalf("explicit null y = %q", got)
	}
	press(m, "Y")
	if got := rec.texts[len(rec.texts)-1]; got != `"nothing": null` {
		t.Fatalf("explicit null Y = %q", got)
	}
}

func TestCopyRefusesMissingAndSyntheticRows(t *testing.T) {
	root := &smodel.FormNode{Key: "$", Title: "T", Kind: smodel.KObject,
		Children: []*smodel.FormNode{
			{Key: "gone", Title: "Gone", Kind: smodel.KScalar, JSONType: "string"},
			{Key: "set", Title: "Set", Kind: smodel.KScalar, JSONType: "string"},
		}}
	rec := &clipboardRecorder{}
	m := newCopyModel(t, root, tobj(tmem("set", tstr("v"))), rec)

	press(m, "y") // absent key
	if len(rec.texts) != 0 || m.notice != "no value to copy" {
		t.Fatalf("absent y: copied=%v notice=%q", rec.texts, m.notice)
	}
	press(m, "Y")
	if len(rec.texts) != 0 || m.notice != "no value to copy" {
		t.Fatalf("absent Y: copied=%v notice=%q", rec.texts, m.notice)
	}

	press(m, "down", "down") // < Save and exit >
	press(m, "y")
	if len(rec.texts) != 0 || m.notice != "nothing to copy" {
		t.Fatalf("action row y: copied=%v notice=%q", rec.texts, m.notice)
	}
}

func TestCopyUnknownKeyEscapesJSON(t *testing.T) {
	root := &smodel.FormNode{Key: "$", Title: "T", Kind: smodel.KObject}
	doc := tobj(tmem(`weird"key`, tnum("1")))
	rec := &clipboardRecorder{}
	m := newCopyModel(t, root, doc, rec)
	m.top().cursor = rowIndexAtPath(m, `weird"key`)

	press(m, "y")
	if got := rec.texts[0]; got != "1" {
		t.Fatalf("value = %q", got)
	}
	press(m, "Y")
	if got := rec.texts[1]; got != `"weird\"key": 1` {
		t.Fatalf("escaped pair = %q", got)
	}
}

func TestCopyErrorIsReported(t *testing.T) {
	rec := &clipboardRecorder{err: errors.New("no clipboard tool")}
	m := newCopyModel(t, testModel(), testDoc(), rec)
	press(m, "y")
	if !strings.Contains(m.notice, "copy failed: no clipboard tool") {
		t.Fatalf("notice = %q", m.notice)
	}
}

func TestCopyKeyIsLiteralWhileEditing(t *testing.T) {
	rec := &clipboardRecorder{}
	m := newCopyModel(t, testModel(), testDoc(), rec)
	press(m, "enter")
	if m.edit == nil {
		t.Fatal("enter must open the in-place editor")
	}
	press(m, "y", "Y")
	if len(rec.texts) != 0 {
		t.Fatalf("copy keys inside an editor must not write the clipboard: %q", rec.texts)
	}
	if got := m.edit.ti.Value(); !strings.HasSuffix(got, "yY") {
		t.Fatalf("editor value = %q, want the typed yY", got)
	}
}

func TestCopyDocumentedInHelp(t *testing.T) {
	rec := &clipboardRecorder{}
	m := newCopyModel(t, testModel(), testDoc(), rec)
	press(m, "?")
	if v := m.View(); !contains(v, `copy value / "key": value (JSON) to the clipboard`) {
		t.Fatalf("help overlay must document y/Y:\n%s", v)
	}
}
