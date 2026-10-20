package ui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/wertasy/json-schema-editor/internal/jsondoc"
	"github.com/wertasy/json-schema-editor/internal/smodel"
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

	// Name: y uses the JSON pair; Y copies the raw string.
	press(m, "y")
	if len(rec.texts) != 1 || rec.texts[0] != `"name": "webapp"` {
		t.Fatalf("y copied %q, want the JSON pair", rec.texts)
	}
	if m.notice != "Copied key-value pair" {
		t.Fatalf("notice = %q", m.notice)
	}
	press(m, "Y")
	if len(rec.texts) != 2 || rec.texts[1] != "webapp" {
		t.Fatalf("Y copied %q, want the raw string", rec.texts)
	}
	if m.notice != "Copied value" {
		t.Fatalf("notice = %q", m.notice)
	}

	// Server: container keeps document order and the numeric literal.
	press(m, "down", "down")
	press(m, "Y")
	want := `{"port":1.0,"host":"0.0.0.0","debug":true}`
	if got := rec.texts[len(rec.texts)-1]; got != want {
		t.Fatalf("container Y = %q, want %q", got, want)
	}
	press(m, "y")
	want = `"server": ` + want
	if got := rec.texts[len(rec.texts)-1]; got != want {
		t.Fatalf("container y = %q, want %q", got, want)
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
	press(m, "y") // array pages: y copies the item's value like Y
	if len(rec.texts) != 1 || rec.texts[0] != `{"x":"v"}` {
		t.Fatalf("array element y = %q", rec.texts)
	}
	press(m, "Y")
	if len(rec.texts) != 2 || rec.texts[1] != `{"x":"v"}` {
		t.Fatalf("array element Y = %q", rec.texts)
	}

	press(m, "esc") // back to the root menu
	press(m, "down")
	press(m, "y")
	if got := rec.texts[len(rec.texts)-1]; got != `"nothing": null` {
		t.Fatalf("explicit null y = %q", got)
	}
	press(m, "Y")
	if got := rec.texts[len(rec.texts)-1]; got != "null" {
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
	if got := rec.texts[0]; got != `"weird\"key": 1` {
		t.Fatalf("escaped pair = %q", got)
	}
	press(m, "Y")
	if got := rec.texts[1]; got != "1" {
		t.Fatalf("value = %q", got)
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
	if v := m.View(); !contains(v, `copy the "key": value pair / the value (JSON or YAML per document)`) {
		t.Fatalf("help overlay must document y/Y:\n%s", v)
	}
}

func TestCopyYAMLFormatFollowsDocument(t *testing.T) {
	rec := &clipboardRecorder{}
	m := newListModel(testModel(), testDoc(), Options{Copy: rec.write, Raw: yamlTestCodec()})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	// y: YAML pair; Y: the value (strings verbatim, scalars as literals)
	press(m, "y")
	if len(rec.texts) != 1 || rec.texts[0] != "name: webapp" {
		t.Fatalf("y copied %q, want the YAML pair", rec.texts)
	}
	press(m, "Y")
	if len(rec.texts) != 2 || rec.texts[1] != "webapp" {
		t.Fatalf("Y copied %q, want the raw string", rec.texts)
	}

	// containers come out as YAML blocks; the pair indents under its key
	press(m, "down", "down") // Server
	press(m, "Y")
	if got := rec.texts[2]; got != "port: 8080\nhost: 0.0.0.0\ndebug: true" {
		t.Fatalf("container Y = %q", got)
	}
	press(m, "y")
	if got := rec.texts[3]; got != "server:\n  port: 8080\n  host: 0.0.0.0\n  debug: true" {
		t.Fatalf("container y = %q", got)
	}
}

func TestCutRow(t *testing.T) {
	rec := &clipboardRecorder{}
	doc := testDoc()
	m := newListModel(testModel(), doc, Options{Copy: rec.write})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	// x: copy the JSON pair, then remove the key — one undo step
	press(m, "x")
	if len(rec.texts) != 1 || rec.texts[0] != `"name": "webapp"` {
		t.Fatalf("x copied %q, want the JSON pair", rec.texts)
	}
	if m.notice != "Cut key-value pair" {
		t.Fatalf("notice = %q", m.notice)
	}
	if _, ok := doc.Get([]any{"name"}); ok {
		t.Fatal("x must remove the cut key")
	}
	press(m, "u")
	if s := strAt(t, doc, "name"); s != "webapp" {
		t.Fatalf("one undo must restore the cut pair, name = %q", s)
	}

	// X on a container: copy the value, remove the key
	press(m, "down", "down") // Server
	press(m, "X")
	want := `{"port":8080,"host":"0.0.0.0","debug":true}`
	if got := rec.texts[1]; got != want {
		t.Fatalf("X copied %q, want %q", got, want)
	}
	if m.notice != "Cut value" {
		t.Fatalf("notice = %q", m.notice)
	}
	if _, ok := doc.Get([]any{"server"}); ok {
		t.Fatal("X must remove the cut key")
	}

	// x cuts the array item like X: the item's value, then the entry
	press(m, "u")
	press(m, "g", "g")
	press(m, "down", "down", "down", "down", "enter") // mounts items menu
	press(m, "down")
	before := len(rec.texts)
	press(m, "x")
	if len(rec.texts) != before+1 || rec.texts[before] != `{"src":"/data","dst":"/mnt"}` {
		t.Fatalf("array element x: copied=%v", rec.texts[before:])
	}
	if m.notice != "Cut value" {
		t.Fatalf("notice = %q", m.notice)
	}
	if _, ok := doc.Get([]any{"mounts", 0}); ok {
		t.Fatal("array element x must remove the item")
	}
}

// On an array page y≡Y and x≡X: every form operates on the item's value
// (array items have no object key), and x also deletes the entry.
func TestArrayPageLowercaseMatchesUppercase(t *testing.T) {
	mk := func() (*listModel, *jsondoc.Node, *clipboardRecorder) {
		rec := &clipboardRecorder{}
		doc := twoMountDoc(t)
		m := newListModel(testModel(), doc, Options{Copy: rec.write})
		m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
		press(m, "down", "down", "down", "down", "enter") // mounts items menu
		press(m, "down")                                  // cursor on item0
		return m, doc, rec
	}

	my, _, ry := mk()
	press(my, "y")
	press(my, "Y")
	if len(ry.texts) != 2 || ry.texts[0] != ry.texts[1] {
		t.Fatalf("y/Y copied %q, want identical values", ry.texts)
	}

	mx, dx, rx := mk()
	press(mx, "x")
	mX, dX, rX := mk()
	press(mX, "X")
	if len(rx.texts) != 1 || len(rX.texts) != 1 || rx.texts[0] != rX.texts[0] {
		t.Fatalf("x/X copied %q vs %q", rx.texts, rX.texts)
	}
	if !nodesEqual(dx, dX) {
		t.Fatal("x and X must leave identical documents")
	}
	if got := docArrayLen(dx, []any{"mounts"}); got != 1 {
		t.Fatalf("mounts length after x = %d, want 1", got)
	}
}

func TestCutSelection(t *testing.T) {
	rec := &clipboardRecorder{}
	doc := testDoc()
	m := newListModel(testModel(), doc, Options{Copy: rec.write})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	// batch x: one object with the marked pairs, then all marked keys gone
	press(m, "m", "down", "down", "m") // Name + Server
	press(m, "x")
	want := `{"name":"webapp","server":{"port":8080,"host":"0.0.0.0","debug":true}}`
	if len(rec.texts) != 1 || rec.texts[0] != want {
		t.Fatalf("batch x copied %q, want %q", rec.texts, want)
	}
	if m.notice != "Cut 2 key-value pairs" {
		t.Fatalf("notice = %q", m.notice)
	}
	if _, ok := doc.Get([]any{"name"}); ok {
		t.Fatal("batch x must remove the marked name key")
	}
	if _, ok := doc.Get([]any{"server"}); ok {
		t.Fatal("batch x must remove the marked server key")
	}
	press(m, "u")
	if _, ok := doc.Get([]any{"server"}); !ok {
		t.Fatal("one undo must restore the batch")
	}

	// batch X: the values joined by newlines
	press(m, "m")      // Server (cursor)
	press(m, "g", "g") // Name
	press(m, "m")
	press(m, "X")
	want = "webapp\n" + `{"port":8080,"host":"0.0.0.0","debug":true}`
	if got := rec.texts[1]; got != want {
		t.Fatalf("batch X copied %q, want %q", got, want)
	}
	if m.notice != "Cut 2 values" {
		t.Fatalf("notice = %q", m.notice)
	}
	press(m, "u")

	// a required merged row refuses the delete side: the row stays and the
	// refusal notice stands — the cut degraded to a plain copy
	m2 := requiredMergeModel(t)
	rec2 := &clipboardRecorder{}
	m2.opts.Copy = rec2.write
	press(m2, "down", "enter") // child menu, merged name (required)
	press(m2, "x")
	if len(rec2.texts) != 1 || rec2.texts[0] != `"name": "from-base"` {
		t.Fatalf("refused cut clipboard = %q", rec2.texts)
	}
	if _, ok := m2.doc.Get([]any{"base", "name"}); !ok {
		t.Fatal("refused cut must keep the required merged key")
	}
	if !contains(m2.View(), "Name is required") {
		t.Fatalf("required refusal missing:\n%s", m2.View())
	}
}

func TestCutClipboardErrorKeepsRow(t *testing.T) {
	rec := &clipboardRecorder{err: errors.New("no clipboard tool")}
	doc := testDoc()
	m := newListModel(testModel(), doc, Options{Copy: rec.write})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	press(m, "x")
	if !strings.Contains(m.notice, "cut failed: no clipboard tool") {
		t.Fatalf("notice = %q", m.notice)
	}
	if s := strAt(t, doc, "name"); s != "webapp" {
		t.Fatalf("failed cut must keep the row, name = %q", s)
	}
}
