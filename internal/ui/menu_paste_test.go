package ui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/wertasy/json-schema-editor/internal/jsondoc"
	"github.com/wertasy/json-schema-editor/internal/smodel"
)

type pasteRecorder struct {
	text string
	err  error
}

func (p *pasteRecorder) read() (string, error) { return p.text, p.err }

func newPasteModel(t *testing.T, root *smodel.FormNode, doc *jsondoc.Node, rec *pasteRecorder) *listModel {
	t.Helper()
	m := newListModel(root, doc, Options{Paste: rec.read})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return m
}

// objKeys returns the document member order at path.
func objKeys(t *testing.T, doc *jsondoc.Node, path ...any) []string {
	t.Helper()
	n, ok := doc.Get(path)
	if !ok || n == nil || n.Object == nil {
		t.Fatalf("no object at %v", path)
	}
	keys := make([]string, 0, len(*n.Object))
	for _, mem := range *n.Object {
		keys = append(keys, mem.Key)
	}
	return keys
}

func wantKeys(t *testing.T, got []string, want string) {
	t.Helper()
	if strings.Join(got, " ") != want {
		t.Fatalf("member order = %q, want %q", strings.Join(got, " "), want)
	}
}

func mountSrc(t *testing.T, doc *jsondoc.Node, i int) string {
	t.Helper()
	n, ok := doc.Get([]any{"mounts", i, "src"})
	if !ok || n == nil || n.Str == nil {
		t.Fatalf("no src at mounts[%d]", i)
	}
	return *n.Str
}

func TestPastePartialKeysIntoPageObject(t *testing.T) {
	rec := &pasteRecorder{text: `"x":1,"y":"2",`}
	m := newPasteModel(t, testModel(), testDoc(), rec) // cursor on Name(0)
	press(m, "p")
	if m.notice != "pasted 2 keys into $" {
		t.Fatalf("notice = %q, want %q", m.notice, "pasted 2 keys into $")
	}
	wantKeys(t, objKeys(t, m.doc), "name x y server mounts")
	if got := numAt(t, m.doc, "x"); got != "1" {
		t.Fatalf("x = %s, want 1", got)
	}
	if got := strAt(t, m.doc, "y"); got != "2" {
		t.Fatalf("y = %s, want 2", got)
	}
	// the whole paste is one undo step
	pre := testDoc()
	press(m, "u")
	if !nodesEqual(pre, m.doc) {
		t.Fatal("undo must restore the pre-paste document")
	}
}

func TestPastePartialKeysNoTrailingComma(t *testing.T) {
	rec := &pasteRecorder{text: `"a":1,"b":2`}
	m := newPasteModel(t, testModel(), testDoc(), rec)
	press(m, "p")
	if m.notice != "pasted 2 keys into $" {
		t.Fatalf("notice = %q, want %q", m.notice, "pasted 2 keys into $")
	}
	wantKeys(t, objKeys(t, m.doc), "name a b server mounts")
	if got := numAt(t, m.doc, "a"); got != "1" {
		t.Fatalf("a = %s, want 1", got)
	}
	if got := numAt(t, m.doc, "b"); got != "2" {
		t.Fatalf("b = %s, want 2", got)
	}
}

func TestPasteFullObjectIntoDrillRow(t *testing.T) {
	rec := &pasteRecorder{text: `{"port":9090,"ratio":0.5}`}
	m := newPasteModel(t, testModel(), testDoc(), rec)
	press(m, "down", "down") // cursor on Server(2)
	press(m, "P")            // the row's own object
	if m.notice != "pasted 2 keys into $.server" {
		t.Fatalf("notice = %q, want %q", m.notice, "pasted 2 keys into $.server")
	}
	// port keeps its position, only the value is replaced; ratio appends
	wantKeys(t, objKeys(t, m.doc, "server"), "port host debug ratio")
	if got := numAt(t, m.doc, "server", "port"); got != "9090" {
		t.Fatalf("port = %s, want 9090", got)
	}
	if got := numAt(t, m.doc, "server", "ratio"); got != "0.5" {
		t.Fatalf("ratio = %s, want 0.5", got)
	}
}

func TestPasteFullObjectOntoPageRoot(t *testing.T) {
	rec := &pasteRecorder{text: `{"port":9090}`}
	m := newPasteModel(t, testModel(), testDoc(), rec) // cursor on Name: no container row
	press(m, "p")
	if m.notice != "pasted 1 keys into $" {
		t.Fatalf("notice = %q, want %q", m.notice, "pasted 1 keys into $")
	}
	wantKeys(t, objKeys(t, m.doc), "name port server mounts")
	if got := numAt(t, m.doc, "port"); got != "9090" {
		t.Fatalf("port = %s, want 9090", got)
	}
}

func twoMountDoc(t *testing.T) *jsondoc.Node {
	t.Helper()
	doc := testDoc()
	doc.Set([]any{"mounts"}, tarr(
		tobj(tmem("src", tstr("/a")), tmem("dst", tstr("/1"))),
		tobj(tmem("src", tstr("/b")), tmem("dst", tstr("/2"))),
	))
	return doc
}

func TestPasteArrayItemsAtCursor(t *testing.T) {
	rec := &pasteRecorder{text: `1, 2, "three"`}
	m := newPasteModel(t, testModel(), twoMountDoc(t), rec)
	press(m, "down", "down", "down", "down", "enter") // → mounts items menu, cursor on < Add item >
	press(m, "down", "down")                          // cursor on the second item (idx 1)
	press(m, "p")
	if m.notice != "pasted 3 items into $.mounts" {
		t.Fatalf("notice = %q, want %q", m.notice, "pasted 3 items into $.mounts")
	}
	if got := docArrayLen(m.doc, []any{"mounts"}); got != 5 {
		t.Fatalf("mounts length = %d, want 5", got)
	}
	if got := mountSrc(t, m.doc, 0); got != "/a" {
		t.Fatalf("mounts[0].src = %q, want /a", got)
	}
	if got := mountSrc(t, m.doc, 1); got != "/b" {
		t.Fatalf("mounts[1].src = %q, want /b", got)
	}
	if got := numAt(t, m.doc, "mounts", 2); got != "1" {
		t.Fatalf("mounts[2] = %s, want 1", got)
	}
	if got := numAt(t, m.doc, "mounts", 3); got != "2" {
		t.Fatalf("mounts[3] = %s, want 2", got)
	}
	if got := strAt(t, m.doc, "mounts", 4); got != "three" {
		t.Fatalf("mounts[4] = %q, want three", got)
	}
}

func TestPasteFullArrayOntoArrayPage(t *testing.T) {
	rec := &pasteRecorder{text: `[4,5]`}
	m := newPasteModel(t, testModel(), twoMountDoc(t), rec)
	press(m, "down", "down", "down", "down", "enter") // items menu, cursor on < Add item >
	press(m, "p")                                     // not on an item row: append
	if m.notice != "pasted 2 items into $.mounts" {
		t.Fatalf("notice = %q, want %q", m.notice, "pasted 2 items into $.mounts")
	}
	if got := docArrayLen(m.doc, []any{"mounts"}); got != 4 {
		t.Fatalf("mounts length = %d, want 4", got)
	}
	if got := numAt(t, m.doc, "mounts", 2); got != "4" {
		t.Fatalf("mounts[2] = %s, want 4", got)
	}
	if got := numAt(t, m.doc, "mounts", 3); got != "5" {
		t.Fatalf("mounts[3] = %s, want 5", got)
	}
}

// p and P differ on a container row: p targets the menu's container,
// P the row's own object.
func TestPasteMenuVsCursorTarget(t *testing.T) {
	rec := &pasteRecorder{text: `{"x":1}`}
	m := newPasteModel(t, testModel(), testDoc(), rec)
	press(m, "down", "down") // cursor on Server(2)
	press(m, "p")            // breadcrumb target: the root object
	if m.notice != "pasted 1 keys into $" {
		t.Fatalf("notice = %q, want %q", m.notice, "pasted 1 keys into $")
	}
	// x lands right after the cursor member (server)
	wantKeys(t, objKeys(t, m.doc), "name server x mounts")
	if got := numAt(t, m.doc, "x"); got != "1" {
		t.Fatalf("x = %s, want 1", got)
	}

	// P pastes into the row's own object instead
	rec = &pasteRecorder{text: `{"y":2}`}
	m = newPasteModel(t, testModel(), testDoc(), rec)
	press(m, "down", "down") // cursor on Server(2)
	press(m, "P")
	if m.notice != "pasted 1 keys into $.server" {
		t.Fatalf("notice = %q, want %q", m.notice, "pasted 1 keys into $.server")
	}
	wantKeys(t, objKeys(t, m.doc, "server"), "port host debug y")
	if got := numAt(t, m.doc, "server", "y"); got != "2" {
		t.Fatalf("server.y = %s, want 2", got)
	}
}

func TestPasteIntoUnsetDrillRow(t *testing.T) {
	rec := &pasteRecorder{text: `"levels":["debug"]`}
	m := newPasteModel(t, testModel(), testDoc(), rec)
	press(m, "down", "down", "down") // cursor on Logging(3), no document value
	press(m, "P")                    // the row's own object (auto-created)
	if m.notice != "pasted 1 keys into $.logging" {
		t.Fatalf("notice = %q, want %q", m.notice, "pasted 1 keys into $.logging")
	}
	if got := strAt(t, m.doc, "logging", "levels", 0); got != "debug" {
		t.Fatalf("logging.levels[0] = %q, want debug", got)
	}
}

func TestPasteMismatchNotices(t *testing.T) {
	// key-value pairs pasted onto an array target are refused
	rec := &pasteRecorder{text: `"a":1`}
	m := newPasteModel(t, testModel(), twoMountDoc(t), rec)
	press(m, "down", "down", "down", "down", "enter") // mounts items menu
	press(m, "p")
	if m.notice != "clipboard holds key-value pairs, the target is an array" {
		t.Fatalf("notice = %q", m.notice)
	}
	if got := docArrayLen(m.doc, []any{"mounts"}); got != 2 {
		t.Fatalf("mounts length = %d, want 2", got)
	}

	// array items pasted onto a mapping target are refused
	rec = &pasteRecorder{text: `1,2`}
	m = newPasteModel(t, testModel(), testDoc(), rec)
	press(m, "p")
	if m.notice != "clipboard holds array items, the target is a mapping" {
		t.Fatalf("notice = %q", m.notice)
	}
	if !nodesEqual(testDoc(), m.doc) {
		t.Fatal("refused paste must not touch the document")
	}
}

func TestPasteUnparseable(t *testing.T) {
	for _, text := range []string{`{"a":`, `garbage`} {
		rec := &pasteRecorder{text: text}
		m := newPasteModel(t, testModel(), testDoc(), rec)
		press(m, "p")
		if m.notice != "clipboard is not JSON keys or items" {
			t.Fatalf("clipboard %q: notice = %q", text, m.notice)
		}
		if !nodesEqual(testDoc(), m.doc) {
			t.Fatalf("clipboard %q: failed paste must not touch the document", text)
		}
	}
}

func TestPasteEmptyClipboard(t *testing.T) {
	for _, text := range []string{"", ","} {
		rec := &pasteRecorder{text: text}
		m := newPasteModel(t, testModel(), testDoc(), rec)
		press(m, "p")
		if m.notice != "clipboard is empty" {
			t.Fatalf("clipboard %q: notice = %q", text, m.notice)
		}
		if !nodesEqual(testDoc(), m.doc) {
			t.Fatalf("clipboard %q: failed paste must not touch the document", text)
		}
	}
}

func TestPasteClipboardError(t *testing.T) {
	rec := &pasteRecorder{err: errors.New("no clipboard tool")}
	m := newPasteModel(t, testModel(), testDoc(), rec)
	press(m, "p")
	if m.notice != "paste failed: no clipboard tool" {
		t.Fatalf("notice = %q", m.notice)
	}
	if !nodesEqual(testDoc(), m.doc) {
		t.Fatal("failed read must not touch the document")
	}
}

func TestPasteUppercaseP(t *testing.T) {
	rec := &pasteRecorder{text: `"x":1,"y":"2",`}
	m := newPasteModel(t, testModel(), testDoc(), rec)
	press(m, "P")
	if m.notice != "pasted 2 keys into $" {
		t.Fatalf("notice = %q, want %q", m.notice, "pasted 2 keys into $")
	}
	wantKeys(t, objKeys(t, m.doc), "name x y server mounts")
}

func TestPasteDuplicateKeyInClipboard(t *testing.T) {
	rec := &pasteRecorder{text: `"a":1,"a":2`}
	m := newPasteModel(t, testModel(), testDoc(), rec)
	press(m, "p")
	if m.notice != "pasted 2 keys into $" {
		t.Fatalf("notice = %q", m.notice)
	}
	wantKeys(t, objKeys(t, m.doc), "name a server mounts") // last wins, single key
	if got := numAt(t, m.doc, "a"); got != "2" {
		t.Fatalf("a = %s, want 2", got)
	}
}

func TestPasteNoChanges(t *testing.T) {
	rec := &pasteRecorder{text: `{"name":"webapp"}`}
	m := newPasteModel(t, testModel(), testDoc(), rec)
	press(m, "p")
	if m.notice != "paste made no changes" {
		t.Fatalf("notice = %q, want %q", m.notice, "paste made no changes")
	}
	if !nodesEqual(testDoc(), m.doc) {
		t.Fatal("no-change paste must leave the document as it was")
	}
}

// newYAMLPasteModel builds a paste model for a YAML-document session (the
// YAML raw codec is what marks the session as YAML).
func newYAMLPasteModel(t *testing.T, root *smodel.FormNode, doc *jsondoc.Node, rec *pasteRecorder) *listModel {
	t.Helper()
	m := newListModel(root, doc, Options{Paste: rec.read, Raw: yamlTestCodec()})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return m
}

func TestPasteYAMLBlockPairs(t *testing.T) {
	rec := &pasteRecorder{text: "x: 1\ny: \"2\""}
	m := newYAMLPasteModel(t, testModel(), testDoc(), rec)
	press(m, "p")
	if m.notice != "pasted 2 keys into $" {
		t.Fatalf("notice = %q, want %q", m.notice, "pasted 2 keys into $")
	}
	wantKeys(t, objKeys(t, m.doc), "name x y server mounts")
	if got := numAt(t, m.doc, "x"); got != "1" {
		t.Fatalf("x = %s, want 1", got)
	}
	if got := strAt(t, m.doc, "y"); got != "2" {
		t.Fatalf("y = %q, want 2", got)
	}
}

func TestPasteYAMLFlowMapping(t *testing.T) {
	rec := &pasteRecorder{text: `{x: 1}`} // not strict JSON (unquoted key)
	m := newYAMLPasteModel(t, testModel(), testDoc(), rec)
	press(m, "p")
	if m.notice != "pasted 1 keys into $" {
		t.Fatalf("notice = %q, want %q", m.notice, "pasted 1 keys into $")
	}
	wantKeys(t, objKeys(t, m.doc), "name x server mounts")
	if got := numAt(t, m.doc, "x"); got != "1" {
		t.Fatalf("x = %s, want 1", got)
	}
}

func TestPasteYAMLBlockItems(t *testing.T) {
	rec := &pasteRecorder{text: "- 1\n- 2\n- three"}
	m := newYAMLPasteModel(t, testModel(), twoMountDoc(t), rec)
	press(m, "down", "down", "down", "down", "enter") // mounts items menu
	press(m, "down", "down")                          // second item (idx 1)
	press(m, "p")
	if m.notice != "pasted 3 items into $.mounts" {
		t.Fatalf("notice = %q, want %q", m.notice, "pasted 3 items into $.mounts")
	}
	if got := docArrayLen(m.doc, []any{"mounts"}); got != 5 {
		t.Fatalf("mounts length = %d, want 5", got)
	}
	if got := numAt(t, m.doc, "mounts", 2); got != "1" {
		t.Fatalf("mounts[2] = %s, want 1", got)
	}
	if got := numAt(t, m.doc, "mounts", 3); got != "2" {
		t.Fatalf("mounts[3] = %s, want 2", got)
	}
	if got := strAt(t, m.doc, "mounts", 4); got != "three" {
		t.Fatalf("mounts[4] = %q, want three", got)
	}
}

func TestPasteYAMLKeepsJSONWorking(t *testing.T) {
	rec := &pasteRecorder{text: `"a":1,"b":2`}
	m := newYAMLPasteModel(t, testModel(), testDoc(), rec)
	press(m, "p")
	if m.notice != "pasted 2 keys into $" {
		t.Fatalf("notice = %q, want %q", m.notice, "pasted 2 keys into $")
	}
	wantKeys(t, objKeys(t, m.doc), "name a b server mounts")
}

func TestPasteYAMLDetachesAnchors(t *testing.T) {
	rec := &pasteRecorder{text: "x: &a 1\ny: *a"}
	m := newYAMLPasteModel(t, testModel(), testDoc(), rec)
	press(m, "p")
	if m.notice != "pasted 2 keys into $" {
		t.Fatalf("notice = %q, want %q", m.notice, "pasted 2 keys into $")
	}
	// the alias pastes as a detached copy of the anchor's value: same
	// content, no anchor identity, no shared references
	x, _ := m.doc.Get([]any{"x"})
	y, _ := m.doc.Get([]any{"y"})
	if x == nil || y == nil || x.Num == nil || y.Num == nil ||
		x.Num.String() != "1" || y.Num.String() != "1" {
		t.Fatalf("x/y = %s/%s, want 1/1", nodeNum(t, x), nodeNum(t, y))
	}
	if x.Anchor != "" || y.Anchor != "" {
		t.Fatalf("pasted anchors must dissolve, got %q/%q", x.Anchor, y.Anchor)
	}
}

func nodeNum(t *testing.T, n *jsondoc.Node) string {
	t.Helper()
	if n == nil || n.Num == nil {
		return "<nil>"
	}
	return n.Num.String()
}

func TestPasteYAMLScalar(t *testing.T) {
	// in a YAML document a bare scalar is a single value: p refuses (it
	// only merges into containers), P replaces the cursor row's value
	rec := &pasteRecorder{text: "hello"}
	m := newYAMLPasteModel(t, testModel(), testDoc(), rec)
	press(m, "p")
	if m.notice != "a single value replaces a row's value — press P on the row" {
		t.Fatalf("p notice = %q", m.notice)
	}
	if !nodesEqual(testDoc(), m.doc) {
		t.Fatal("refused paste must not touch the document")
	}
	press(m, "P")
	if m.notice != "pasted value into $.name" {
		t.Fatalf("P notice = %q", m.notice)
	}
	if got := strAt(t, m.doc, "name"); got != "hello" {
		t.Fatalf("name = %q, want hello", got)
	}

	// a YAML document's clip is still refused in JSON documents
	rec = &pasteRecorder{text: "x: 1"}
	m = newPasteModel(t, testModel(), testDoc(), rec)
	press(m, "p")
	if m.notice != "clipboard is not JSON keys or items" {
		t.Fatalf("notice = %q", m.notice)
	}
	if !nodesEqual(testDoc(), m.doc) {
		t.Fatal("refused paste must not touch the document")
	}
}

func TestPasteValueOntoCursorRow(t *testing.T) {
	// a JSON number replaces the scalar field's value in one step
	rec := &pasteRecorder{text: `123`}
	m := newPasteModel(t, testModel(), testDoc(), rec) // cursor on Name(0)
	press(m, "P")
	if m.notice != "pasted value into $.name" {
		t.Fatalf("notice = %q, want %q", m.notice, "pasted value into $.name")
	}
	if got := numAt(t, m.doc, "name"); got != "123" {
		t.Fatalf("name = %s, want 123", got)
	}
	pre := testDoc()
	press(m, "u")
	if !nodesEqual(pre, m.doc) {
		t.Fatal("undo must restore the pre-paste document")
	}

	// a quoted JSON string is a single value too
	rec = &pasteRecorder{text: `"hello"`}
	m = newPasteModel(t, testModel(), testDoc(), rec)
	press(m, "P")
	if got := strAt(t, m.doc, "name"); got != "hello" {
		t.Fatalf("name = %q, want hello", got)
	}

	// array items: P on a scalar item replaces that item
	scalarItems := &smodel.FormNode{Key: "$", Title: "$", Kind: smodel.KObject,
		Children: []*smodel.FormNode{{Key: "items", Title: "Items", Kind: smodel.KArray,
			Items: &smodel.FormNode{Key: "items", Kind: smodel.KScalar, JSONType: "number"}}}}
	rec = &pasteRecorder{text: `99`}
	m = newPasteModel(t, scalarItems, tobj(tmem("items", tarr(tnum("1"), tnum("2")))), rec)
	press(m, "enter")        // items menu: Add(0), item0(1), item1(2)
	press(m, "down", "down") // cursor on item1
	press(m, "P")
	if m.notice != "pasted value into $.items[1]" {
		t.Fatalf("notice = %q", m.notice)
	}
	if got := numAt(t, m.doc, "items", 1); got != "99" {
		t.Fatalf("items[1] = %s, want 99", got)
	}
	if got := numAt(t, m.doc, "items", 0); got != "1" {
		t.Fatalf("items[0] = %s, want untouched 1", got)
	}

	// p and value-less rows refuse a single value without touching the doc
	rec = &pasteRecorder{text: `123`}
	m = newPasteModel(t, testModel(), testDoc(), rec)
	press(m, "p")
	if m.notice != "a single value replaces a row's value — press P on the row" {
		t.Fatalf("p notice = %q", m.notice)
	}
	if !nodesEqual(testDoc(), m.doc) {
		t.Fatal("refused paste must not touch the document")
	}
	press(m, "G") // < Edit raw JSON >: no value slot
	press(m, "P")
	if m.notice != "a single value replaces a row's value — press P on the row" {
		t.Fatalf("action-row notice = %q", m.notice)
	}
	if !nodesEqual(testDoc(), m.doc) {
		t.Fatal("refused paste must not touch the document")
	}
}

func TestBatchValuePaste(t *testing.T) {
	rec := &pasteRecorder{text: `123`}
	m := newPasteModel(t, testModel(), testDoc(), rec)

	// P writes the value into every selected row in one step; unset rows
	// gain the key (Mode had no document value)
	press(m, "m", "down", "m") // Name(0) + Mode(1)
	press(m, "P")
	if m.notice != "pasted value into 2 keys" {
		t.Fatalf("notice = %q, want %q", m.notice, "pasted value into 2 keys")
	}
	if got := numAt(t, m.doc, "name"); got != "123" {
		t.Fatalf("name = %s, want 123", got)
	}
	if got := numAt(t, m.doc, "mode"); got != "123" {
		t.Fatalf("mode = %s, want 123", got)
	}
	// one undo covers the whole batch
	pre := testDoc()
	press(m, "u")
	if !nodesEqual(pre, m.doc) {
		t.Fatal("one undo must restore the batch")
	}

	// the selection cleared: P is the plain cursor-row replace again
	press(m, "P") // cursor still on Mode
	if m.notice != "pasted value into $.mode" {
		t.Fatalf("notice = %q", m.notice)
	}
	if got := numAt(t, m.doc, "mode"); got != "123" {
		t.Fatalf("mode = %s, want 123", got)
	}
}
