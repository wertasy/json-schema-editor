package ui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/wertasy/json-schema-editor/internal/icons"
	"github.com/wertasy/json-schema-editor/internal/jsondoc"
	"github.com/wertasy/json-schema-editor/internal/smodel"
	"github.com/wertasy/json-schema-editor/internal/validate"
)

// Model-level tests for the menuconfig TUI: key-message injection plus
// View/doc assertions, mirroring the plan's verification list.

func key(k string) tea.KeyMsg {
	switch k {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "space":
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	case "ctrl+j":
		return tea.KeyMsg{Type: tea.KeyCtrlJ}
	case "ctrl+k":
		return tea.KeyMsg{Type: tea.KeyCtrlK}
	case "ctrl+s":
		return tea.KeyMsg{Type: tea.KeyCtrlS}
	case "ctrl+r":
		return tea.KeyMsg{Type: tea.KeyCtrlR}
	case "ctrl+n":
		return tea.KeyMsg{Type: tea.KeyCtrlN}
	case "ctrl+p":
		return tea.KeyMsg{Type: tea.KeyCtrlP}
	case "alt+i":
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i"), Alt: true}
	case "alt+x":
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x"), Alt: true}
	case "ctrl+u":
		return tea.KeyMsg{Type: tea.KeyCtrlU}
	case "home":
		return tea.KeyMsg{Type: tea.KeyHome}
	case "end":
		return tea.KeyMsg{Type: tea.KeyEnd}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
	}
}

func press(m *listModel, keys ...string) {
	for _, k := range keys {
		m.Update(key(k))
	}
}

// finishExternalValue writes content to the pending $EDITOR temp file and
// delivers the completion message, as tea.ExecProcess would.
func finishExternalValue(t *testing.T, m *listModel, content string) {
	t.Helper()
	if m.ext == nil || m.ext.path == "" {
		t.Fatal("no pending external value edit")
	}
	if err := os.WriteFile(m.ext.path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	m.Update(rawEditorDoneMsg{path: m.ext.path})
}

// countSave returns Options whose Save counts write calls (a successful
// no-op sink for save-path tests).
func countSave(calls *int) Options {
	return Options{Save: func(*jsondoc.Node) error { *calls++; return nil }}
}

func numAt(t *testing.T, doc *jsondoc.Node, path ...any) string {
	t.Helper()
	n, ok := doc.Get(path)
	if !ok || n == nil || n.Num == nil {
		return "<missing>"
	}
	return n.Num.String()
}

func TestMenuRootView(t *testing.T) {
	m := newListModel(testModel(), testDoc(), Options{})
	m.Update(tea.WindowSizeMsg{Width: 170, Height: 30}) // full help bar fits
	v := m.View()
	for _, want := range []string{
		"Server Configuration", "$", "Name", "webapp", "Mode", "http",
		"Server", "port=8080 host=0.0.0.0 +1", "—",
		"Name*", "Server*", // required star shows regardless of a set value
		"Mounts", "1 item", "Auth", "Save and exit", "Edit raw JSON",
		"↑/↓ move · enter edit/open · esc back · ctrl+s/w save · / search · ? help",
	} {
		if !contains(v, want) {
			t.Errorf("root view missing %q:\n%s", want, v)
		}
	}
	if contains(v, "validation errors") {
		t.Error("status line must be hidden without opts.Status")
	}

	// status line shows on the root screen only
	m2 := newListModel(testModel(), testDoc(), Options{Status: icons.Warn + " schema compile failed, save-time validation disabled: 2 validation errors"})
	if v := m2.View(); !contains(v, icons.Warn+" schema compile failed") {
		t.Errorf("status line missing:\n%s", v)
	}
	press(m2, "down", "down", "enter")
	if v := m2.View(); contains(v, "validation errors") {
		t.Error("status line must not render below the root screen")
	}
}

// TestMenuStatusAboveHelpBar pins the session status block to the bottom: it
// renders directly above the hint bar (below the transient notice's slot),
// not in the header.
func TestMenuStatusAboveHelpBar(t *testing.T) {
	m := newListModel(testModel(), testDoc(), Options{Status: "yaml: 1 alias kept\nschema from SchemaStore: GitHub Workflow"})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	lines := strings.Split(m.View(), "\n")
	if len(lines) != 30 {
		t.Fatalf("view has %d lines, want 30 with the status inside the frame:\n%s",
			len(lines), m.View())
	}
	if !contains(lines[len(lines)-3], "alias kept") ||
		!contains(lines[len(lines)-2], "SchemaStore") {
		t.Fatalf("status must sit directly above the help bar:\n%s", m.View())
	}
	if !contains(lines[len(lines)-1], "? help") {
		t.Fatalf("the help bar must stay on the last row:\n%s", m.View())
	}
	if contains(lines[0], "alias kept") || contains(lines[1], "alias kept") {
		t.Fatalf("status must not render in the header:\n%s", m.View())
	}
}

func TestMenuHelpPinnedToBottom(t *testing.T) {
	m := newListModel(testModel(), testDoc(), Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	lines := strings.Split(m.View(), "\n")
	if len(lines) != 30 {
		t.Fatalf("view has %d lines, want 30 with the help on the last row", len(lines))
	}
	if !contains(lines[len(lines)-1], "? help") {
		t.Fatalf("last row is not the help bar:\n%s", m.View())
	}

	// a short menu pads the gap instead of floating the bar under the rows
	root := &smodel.FormNode{Key: "$", Title: "Tiny", Kind: smodel.KObject,
		Children: []*smodel.FormNode{
			{Key: "a", Title: "A", Kind: smodel.KScalar, JSONType: "string"},
		}}
	short := newListModel(root, tobj(), Options{})
	short.Update(tea.WindowSizeMsg{Width: 100, Height: 12})
	lines = strings.Split(short.View(), "\n")
	if len(lines) != 12 {
		t.Fatalf("short view has %d lines, want 12", len(lines))
	}
	if !contains(lines[len(lines)-1], "? help") {
		t.Fatalf("short view help bar not pinned:\n%s", short.View())
	}
}

func TestMenuDrillAndRestoreCursor(t *testing.T) {
	m := newListModel(testModel(), testDoc(), Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	// move to row 2 (Server), drill in, come back: cursor must survive
	press(m, "down", "down", "enter")
	if len(m.stack) != 2 || !contains(m.View(), "$.server") {
		t.Fatalf("breadcrumb after drill:\n%s", m.View())
	}
	if !contains(m.View(), "> Port") {
		t.Fatalf("server menu cursor should be on Port:\n%s", m.View())
	}
	press(m, "esc")
	if len(m.stack) != 1 {
		t.Fatalf("esc should pop, stack = %d", len(m.stack))
	}
	// one more down lands on row 3 (Logging): proves cursor was restored to 2
	press(m, "down", "enter")
	if len(m.stack) != 2 || !contains(m.View(), "$.logging") {
		t.Fatalf("cursor restore failed, breadcrumb:\n%s", m.View())
	}
}

func TestMenuInlineEditScalar(t *testing.T) {
	doc := testDoc()
	m := newListModel(testModel(), doc, Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	press(m, "down", "down", "enter") // into Server
	press(m, "enter")                 // edit Port

	// invalid: below minimum → red error, doc untouched, still editing
	press(m, "ctrl+u", "1", "enter")
	if !contains(m.View(), "must be at least") {
		t.Fatalf("missing validation error:\n%s", m.View())
	}
	if got := numAt(t, doc, "server", "port"); got != "8080" {
		t.Fatalf("doc changed on invalid input: %s", got)
	}
	if m.edit == nil {
		t.Fatal("edit state must survive a failed confirm")
	}
	// valid: writes through and closes the editor
	press(m, "ctrl+u", "9050", "enter")
	if got := numAt(t, doc, "server", "port"); got != "9050" {
		t.Fatalf("doc port = %s, want 9050", got)
	}
	if m.edit != nil {
		t.Fatal("edit state must close after success")
	}

	// esc cancels without writing
	press(m, "enter", "ctrl+u", "1234", "esc")
	if m.edit != nil {
		t.Fatal("esc must close the editor")
	}
	if got := numAt(t, doc, "server", "port"); got != "9050" {
		t.Fatalf("esc must not write, port = %s", got)
	}

	// empty optional input removes the key (omit rule)
	press(m, "down", "down", "down", "enter", "ctrl+u", "enter") // Ratio: no default → removed
	if _, ok := doc.Get([]any{"server", "ratio"}); ok {
		t.Fatal("empty optional input should remove the key")
	}
}

func TestMenuEnumCycle(t *testing.T) {
	doc := testDoc()
	m := newListModel(testModel(), doc, Options{})
	press(m, "down") // Mode row
	press(m, "right")
	if s := strAt(t, doc, "mode"); s != "grpc" {
		t.Fatalf("mode after right = %q, want grpc", s)
	}
	press(m, "right")
	if s := strAt(t, doc, "mode"); s != "tcp" {
		t.Fatalf("mode after right×2 = %q, want tcp", s)
	}
	press(m, "left")
	if s := strAt(t, doc, "mode"); s != "grpc" {
		t.Fatalf("mode after left = %q, want grpc", s)
	}

}

func strAt(t *testing.T, doc *jsondoc.Node, path ...any) string {
	t.Helper()
	n, ok := doc.Get(path)
	if !ok || n == nil || n.Str == nil {
		return "<missing>"
	}
	return *n.Str
}

func TestMenuEnumOverlay(t *testing.T) {
	doc := testDoc()
	m := newListModel(testModel(), doc, Options{})
	press(m, "down", "enter") // open enum picker on Mode
	if m.sel == nil || !contains(m.View(), "http") || !contains(m.View(), "grpc") {
		t.Fatalf("enum overlay:\n%s", m.View())
	}
	press(m, "down", "enter") // pick grpc
	if m.sel != nil {
		t.Fatal("picker must close on enter")
	}
	if s := strAt(t, doc, "mode"); s != "grpc" {
		t.Fatalf("mode = %q, want grpc", s)
	}
	if !contains(m.View(), "grpc") {
		t.Fatalf("summary should show grpc:\n%s", m.View())
	}
	// esc cancels
	press(m, "enter", "down", "esc")
	if m.sel != nil {
		t.Fatal("esc must close the picker")
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

func TestMenuBoolToggle(t *testing.T) {
	doc := testDoc() // debug=true
	m := newListModel(testModel(), doc, Options{})
	press(m, "down", "down", "enter", "down", "down") // Server → Debug row
	press(m, "space")
	if b := boolAt(t, doc, "server", "debug"); b {
		t.Fatal("space should flip debug to false")
	}
	if !contains(m.View(), "[n]") {
		t.Fatalf("summary should show [n]:\n%s", m.View())
	}
	press(m, "enter") // enter on a bool row toggles too
	if b := boolAt(t, doc, "server", "debug"); !b {
		t.Fatal("enter should toggle bool rows")
	}
}

func boolAt(t *testing.T, doc *jsondoc.Node, path ...any) bool {
	t.Helper()
	n, ok := doc.Get(path)
	if !ok || n == nil || n.Bool == nil {
		t.Fatal("missing bool at " + pathKey(path))
	}
	return *n.Bool
}

func pathKey(path []any) string {
	s := ""
	for _, p := range path {
		s += "/" + p.(string)
	}
	return s
}

func TestMenuRootEscQuit(t *testing.T) {
	// unchanged: root esc ends the session immediately, without a prompt
	clean := newListModel(testModel(), testDoc(), Options{})
	_, cmd := clean.Update(key("esc"))
	if clean.confirm != nil || !clean.exiting || clean.aborted || clean.saved {
		t.Fatalf("clean root esc must quit directly: confirm=%v exiting=%v aborted=%v saved=%v",
			clean.confirm != nil, clean.exiting, clean.aborted, clean.saved)
	}
	if cmd == nil {
		t.Fatal("clean root esc must return a quit command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("clean root esc cmd = %T, want tea.QuitMsg", cmd())
	}

	// changed: esc asks whether to save instead of whether to quit unsaved
	var saves int
	discardDoc := testDoc()
	discard := newListModel(testModel(), discardDoc, countSave(&saves))
	press(discard, "enter", "ctrl+u", "x", "enter")
	if !discard.dirty() {
		t.Fatal("editing Name must dirty the document")
	}
	discard.Update(key("esc"))
	if discard.confirm == nil || !contains(discard.View(), "Save changes?") {
		t.Fatalf("dirty root esc must ask to save:\n%s", discard.View())
	}
	if !contains(discard.View(), "n discard & exit") {
		t.Fatalf("save prompt must explain n:\n%s", discard.View())
	}
	_, cmd = discard.Update(key("n"))
	if discard.confirm != nil || !discard.aborted || discard.exiting || saves != 0 {
		t.Fatalf("n must discard and exit: confirm=%v aborted=%v exiting=%v saves=%d",
			discard.confirm != nil, discard.aborted, discard.exiting, saves)
	}
	if cmd == nil {
		t.Fatal("n must return a quit command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("n cmd = %T, want tea.QuitMsg", cmd())
	}

	// changed: esc dismisses the prompt and stays in the session
	stay := newListModel(testModel(), testDoc(), Options{})
	press(stay, "enter", "ctrl+u", "x", "enter", "esc")
	if stay.confirm == nil {
		t.Fatal("esc on a dirty root must open the save prompt")
	}
	press(stay, "esc")
	if stay.confirm != nil || stay.aborted || stay.exiting {
		t.Fatalf("esc must dismiss the prompt and stay: confirm=%v aborted=%v exiting=%v",
			stay.confirm != nil, stay.aborted, stay.exiting)
	}

	// changed: y saves and exits
	saves = 0
	save := newListModel(testModel(), testDoc(), countSave(&saves))
	press(save, "enter", "ctrl+u", "x", "enter", "esc")
	_, cmd = save.Update(key("y"))
	if saves != 1 || !save.saved || !save.exiting || save.aborted || save.dirty() {
		t.Fatalf("y must save and exit: saves=%d saved=%v exiting=%v aborted=%v dirty=%v",
			saves, save.saved, save.exiting, save.aborted, save.dirty())
	}
	if cmd == nil {
		t.Fatal("y must return a quit command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("y cmd = %T, want tea.QuitMsg", cmd())
	}
}

func TestMenuCtrlC(t *testing.T) {
	m := newListModel(testModel(), testDoc(), Options{})
	press(m, "down", "enter", "ctrl+c")
	if !m.aborted || m.saved {
		t.Fatal("ctrl+c must abort anywhere")
	}
}

func TestUnknownKeyExternalEdit(t *testing.T) {
	doc := testDoc()
	doc.Set([]any{"extra"}, tobj(tmem("old", tstr("value"))))
	m := newListModel(testModel(), doc, Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	open := func() {
		t.Helper()
		for i, r := range menuRows(m) {
			if r.kind == rowUnknown {
				m.top().cursor = i
			}
		}
		press(m, "enter")
		if m.text != nil {
			t.Fatal("unknown key must not open the in-app raw editor")
		}
		if m.ext == nil {
			t.Fatal("unknown object value must launch the external editor")
		}
	}
	// The temp file is prefilled with the current value as JSON.
	open()
	data, err := os.ReadFile(m.ext.path)
	if err != nil {
		t.Fatal(err)
	}
	pre, err := jsondoc.Parse(strings.NewReader(string(data)))
	if err != nil {
		t.Fatalf("editor prefill is not valid JSON: %v\n%s", err, data)
	}
	if old, ok := pre.Get([]any{"old"}); !ok || old.Str == nil || *old.Str != "value" {
		t.Fatalf("editor prefill = %q, want the current object", data)
	}

	// Invalid JSON leaves the value untouched and closes the editor.
	finishExternalValue(t, m, "{oops")
	if _, ok := doc.Get([]any{"extra", "old"}); !ok {
		t.Fatal("invalid JSON must leave the object value untouched")
	}
	if !strings.Contains(m.notice, "invalid JSON") {
		t.Fatalf("notice = %q, want an invalid-JSON error", m.notice)
	}

	// Valid JSON replaces the value as one undo step.
	open()
	finishExternalValue(t, m, `{"new":"value"}`)
	if s := strAt(t, doc, "extra", "new"); s != "value" {
		t.Fatalf("extra.new = %q, want value", s)
	}
	press(m, "u")
	if old, ok := doc.Get([]any{"extra", "old"}); !ok || old.Str == nil || *old.Str != "value" {
		t.Fatal("undo must restore the original object")
	}

	// An empty file removes the key.
	open()
	finishExternalValue(t, m, "  ")
	if _, ok := doc.Get([]any{"extra"}); ok {
		t.Fatal("empty external value should remove the key")
	}
}

// TestUnknownKeyScalarInlineEdit: schema-less scalars edit in place by their
// own JSON type; missing values and containers still use $EDITOR.
func TestUnknownKeyScalarInlineEdit(t *testing.T) {
	doc := testDoc()
	doc.Set([]any{"text"}, tstr("old"))
	doc.Set([]any{"count"}, tnum("42"))
	doc.Set([]any{"flag"}, tbool(true))
	doc.Set([]any{"nothing"}, &jsondoc.Node{Null: true})
	doc.Set([]any{"objval"}, tobj(tmem("a", tnum("1"))))
	doc.Set([]any{"arrval"}, tarr(tnum("1")))
	m := newListModel(testModel(), doc, Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	selectRow := func(label string) {
		t.Helper()
		for i, r := range menuRows(m) {
			if r.kind == rowUnknown && r.label == label {
				m.top().cursor = i
				return
			}
		}
		t.Fatalf("unknown row %q not found:\n%s", label, m.View())
	}

	// string → text input prefilled with the current value
	selectRow("text")
	press(m, "enter")
	if m.edit == nil || m.ext != nil || m.text != nil {
		t.Fatalf("string value must edit in place, edit=%v ext=%v text=%v",
			m.edit != nil, m.ext != nil, m.text != nil)
	}
	if m.edit.fn.JSONType != "string" || m.edit.ti.Value() != "old" {
		t.Fatalf("string editor = type %q value %q, want string/old",
			m.edit.fn.JSONType, m.edit.ti.Value())
	}
	press(m, "ctrl+u", "n", "e", "w", "enter")
	if s := strAt(t, doc, "text"); s != "new" {
		t.Fatalf("text = %q, want new", s)
	}

	// number → numeric input; invalid text is rejected without writing
	selectRow("count")
	press(m, "enter")
	if m.edit == nil || m.edit.fn.JSONType != "number" || m.edit.ti.Value() != "42" {
		t.Fatalf("number editor = %+v, want type number value 42", m.edit)
	}
	press(m, "ctrl+u", "n", "o", "p", "enter")
	if m.edit == nil || m.edit.err != "must be a number" {
		t.Fatalf("invalid number must stay in the editor, edit=%+v", m.edit)
	}
	press(m, "ctrl+u", "4", "3", "enter")
	if got := numAt(t, doc, "count"); got != "43" {
		t.Fatalf("count = %q, want 43", got)
	}

	// boolean → enter and space toggle it
	selectRow("flag")
	press(m, "enter")
	if m.edit != nil || m.ext != nil {
		t.Fatalf("boolean value must toggle, edit=%v ext=%v", m.edit != nil, m.ext != nil)
	}
	if n, _ := doc.Get([]any{"flag"}); n == nil || n.Bool == nil || *n.Bool {
		t.Fatalf("enter must toggle true to false, got %+v", n)
	}
	press(m, "space")
	if n, _ := doc.Get([]any{"flag"}); n == nil || n.Bool == nil || !*n.Bool {
		t.Fatalf("space must toggle false to true, got %+v", n)
	}

	// null, object and array values keep the external editor
	for _, tc := range []struct{ label, value string }{
		{"nothing", "null"},
		{"objval", `{"a":2}`},
		{"arrval", "[2]"},
	} {
		selectRow(tc.label)
		press(m, "enter")
		if m.ext == nil || m.edit != nil || m.text != nil {
			t.Fatalf("%s value must use the external editor, edit=%v ext=%v text=%v",
				tc.label, m.edit != nil, m.ext != nil, m.text != nil)
		}
		finishExternalValue(t, m, tc.value)
	}
	if n, _ := doc.Get([]any{"nothing"}); n == nil || !n.Null {
		t.Fatalf("nothing = %+v, want null", n)
	}
	if n, _ := doc.Get([]any{"objval", "a"}); n == nil || n.Num == nil || n.Num.String() != "2" {
		t.Fatalf("objval.a = %+v, want 2", n)
	}
	if n, _ := doc.Get([]any{"arrval", 0}); n == nil || n.Num == nil || n.Num.String() != "2" {
		t.Fatalf("arrval[0] = %+v, want 2", n)
	}
}

func TestExternalRawEdit(t *testing.T) {
	m := newListModel(testModel(), testDoc(), Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	apply := func(path string, runErr error) *listModel {
		t.Helper()
		nm, _ := m.Update(rawEditorDoneMsg{path: path, err: runErr})
		return nm.(*listModel)
	}

	// A successful edit replaces the document as one undo step.
	path := filepath.Join(t.TempDir(), "raw.json")
	if err := os.WriteFile(path, []byte(`{"name":"edited"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	m = apply(path, nil)
	if s := strAt(t, m.doc, "name"); s != "edited" {
		t.Fatalf("doc name = %q, want edited", s)
	}
	if !m.dirty() {
		t.Fatal("external edit must mark the document dirty")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("temp file not removed: %v", err)
	}
	press(m, "u")
	if s := strAt(t, m.doc, "name"); s != "webapp" {
		t.Fatalf("undo name = %q, want webapp", s)
	}

	// Invalid JSON keeps the document and reports the parse error.
	before := m.doc.Clone()
	bad := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(bad, []byte("{oops"), 0o600); err != nil {
		t.Fatal(err)
	}
	m = apply(bad, nil)
	if !nodesEqual(before, m.doc) {
		t.Fatal("invalid JSON must not replace the document")
	}
	if !contains(m.notice, "invalid JSON") {
		t.Fatalf("notice = %q, want an invalid JSON error", m.notice)
	}

	// An editor failure keeps the document too.
	m = apply("missing", errors.New("boom"))
	if !contains(m.notice, "boom") {
		t.Fatalf("notice = %q, want the editor error", m.notice)
	}
}

func TestMenuSaveFlow(t *testing.T) {
	// clean ctrl+s: writes through Save and stays in the session
	var saves int
	m := newListModel(testModel(), testDoc(), countSave(&saves))
	_, cmd := m.Update(key("ctrl+s"))
	if saves != 1 || !m.saved || m.withErrors {
		t.Fatal("ctrl+s must save when validation passes (nil validator)")
	}
	if cmd != nil {
		t.Fatal("ctrl+s must not quit; only the Save and exit row ends the session")
	}

	// w is a plain alias for ctrl+s in list mode
	var savesW int
	mw := newListModel(testModel(), testDoc(), countSave(&savesW))
	_, cmd = mw.Update(key("w"))
	if savesW != 1 || !mw.saved || mw.withErrors {
		t.Fatal("w must save when validation passes")
	}
	if cmd != nil {
		t.Fatal("w must stay in the session like ctrl+s")
	}

	// validation errors block, then dismiss
	validate := func(*jsondoc.Node) []string { return []string{"bad thing A", "bad thing B"} }
	m2 := newListModel(testModel(), testDoc(), Options{Validate: validate})
	press(m2, "ctrl+s")
	if m2.saved || m2.errs == nil {
		t.Fatal("errors must block saving")
	}
	if v := m2.View(); !contains(v, "bad thing A") || !contains(v, "fix fields and save again, or rerun with --force") {
		t.Fatalf("error overlay:\n%s", v)
	}
	press(m2, "esc")
	if m2.errs != nil {
		t.Fatal("esc must dismiss the error overlay")
	}

	// force path: y writes with errors and stays in the session
	m3 := newListModel(testModel(), testDoc(), Options{Validate: validate, Force: true, Save: func(*jsondoc.Node) error { return nil }})
	press(m3, "ctrl+s")
	if v := m3.View(); !contains(v, "y save anyway · n back") {
		t.Fatalf("force footer:\n%s", v)
	}
	_, cmd = m3.Update(key("y"))
	if !m3.saved || !m3.withErrors {
		t.Fatal("force y must save with errors")
	}
	if cmd != nil {
		t.Fatal("force ctrl+s must not quit")
	}

	// force path: n goes back
	m4 := newListModel(testModel(), testDoc(), Options{Validate: validate, Force: true})
	press(m4, "ctrl+s", "n")
	if m4.saved || m4.errs != nil {
		t.Fatal("force n must return to the list unsaved")
	}

	// the Save and exit row writes through Save and quits
	var exits int
	m5 := newListModel(testModel(), testDoc(), countSave(&exits))
	press(m5, "down", "down", "down", "down", "down", "down")
	_, cmd = m5.Update(key("enter")) // Save and exit
	if exits != 1 || !m5.saved {
		t.Fatal("Save and exit row must write through Save")
	}
	if cmd == nil {
		t.Fatal("Save and exit must quit")
	}
}

func TestJumpKeys(t *testing.T) {
	m := newListModel(testModel(), testDoc(), Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	last := len(menuRows(m)) - 1

	// G and End jump to the last row; gg and Home back to the first
	press(m, "G")
	if m.top().cursor != last {
		t.Fatalf("G: cursor = %d, want %d", m.top().cursor, last)
	}
	press(m, "end")
	if m.top().cursor != last {
		t.Fatalf("End: cursor = %d, want %d", m.top().cursor, last)
	}
	press(m, "g", "g")
	if m.top().cursor != 0 {
		t.Fatalf("gg: cursor = %d, want 0", m.top().cursor)
	}
	press(m, "home")
	if m.top().cursor != 0 {
		t.Fatalf("Home: cursor = %d, want 0", m.top().cursor)
	}

	// a lone g arms but does not move; a different key disarms it
	press(m, "j") // leave the top row
	press(m, "g")
	if m.top().cursor != 1 {
		t.Fatalf("lone g moved the cursor: %d", m.top().cursor)
	}
	press(m, "j")
	if m.top().cursor != 2 {
		t.Fatalf("key after a lone g should move normally: %d", m.top().cursor)
	}

	// an armed g still fires after an intervening jump key
	press(m, "G")
	press(m, "g")
	press(m, "g")
	if m.top().cursor != 0 {
		t.Fatalf("gg after G: cursor = %d, want 0", m.top().cursor)
	}

	// the rows stay addressable after the jumps (no offset drift)
	if v := m.View(); !contains(v, "Save and exit") {
		t.Fatalf("last row must render after a jump:\n%s", v)
	}
}

func TestJumpKeysOverlays(t *testing.T) {
	// issues overlay
	msgs := []string{
		"at '$.server.port': minimum value is 1024",
		"at '$.name': got number, want string",
	}
	m := newListModel(testModel(), testDoc(), Options{LoadErrors: msgs})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	press(m, "enter") // status row → issues
	if m.issues == nil {
		t.Fatal("issues overlay should open")
	}
	press(m, "G")
	if m.issues.cursor != 1 {
		t.Fatalf("issues G: cursor = %d, want 1", m.issues.cursor)
	}
	press(m, "end")
	if m.issues.cursor != 1 {
		t.Fatalf("issues End: cursor = %d, want 1", m.issues.cursor)
	}
	press(m, "g", "g")
	if m.issues.cursor != 0 {
		t.Fatalf("issues gg: cursor = %d, want 0", m.issues.cursor)
	}
	press(m, "home")
	if m.issues.cursor != 0 {
		t.Fatalf("issues Home: cursor = %d, want 0", m.issues.cursor)
	}
	press(m, "esc")

	// enum select overlay: Mode row → options
	press(m, "down", "down") // issues row, Name row, Mode row
	press(m, "enter")
	if m.sel == nil {
		t.Fatal("enum select overlay should open")
	}
	press(m, "G")
	if m.sel.cursor != len(m.sel.opts)-1 {
		t.Fatalf("sel G: cursor = %d, want %d", m.sel.cursor, len(m.sel.opts)-1)
	}
	press(m, "g", "g")
	if m.sel.cursor != 0 {
		t.Fatalf("sel gg: cursor = %d, want 0", m.sel.cursor)
	}
	press(m, "esc")

	// save-error overlay
	validate := func(*jsondoc.Node) []string {
		return []string{"at '$.name': got number, want string", "at '$.server.port': minimum value is 1024", "at '$.mode': bad mode"}
	}
	m2 := newListModel(testModel(), testDoc(), Options{
		Validate: validate, Force: true, Save: func(*jsondoc.Node) error { return nil },
	})
	m2.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	press(m2, "ctrl+s")
	if m2.errs == nil {
		t.Fatal("error overlay should open")
	}
	press(m2, "G")
	if m2.errs.cursor != 2 {
		t.Fatalf("errs G: cursor = %d, want 2", m2.errs.cursor)
	}
	press(m2, "g", "g")
	if m2.errs.cursor != 0 {
		t.Fatalf("errs gg: cursor = %d, want 0", m2.errs.cursor)
	}
	press(m2, "home")
	if m2.errs.cursor != 0 {
		t.Fatalf("errs Home: cursor = %d, want 0", m2.errs.cursor)
	}
	press(m2, "end")
	if m2.errs.cursor != 2 {
		t.Fatalf("errs End: cursor = %d, want 2", m2.errs.cursor)
	}
}

func TestMenuNextPrevNonNull(t *testing.T) {
	doc := testDoc()
	m := newListModel(testModel(), doc, Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	cursor := func() int { return m.top().cursor }

	// rows: Name(0) Mode(1, unset) Server(2) Logging(3, unset)
	//       Mounts(4) Auth(5, unset) Save and exit(6) Edit raw JSON(7)
	press(m, "n")
	if cursor() != 2 {
		t.Fatalf("n from Name = %d, want Server (2)", cursor())
	}
	press(m, "n")
	if cursor() != 4 {
		t.Fatalf("n from Server = %d, want Mounts (4)", cursor())
	}
	press(m, "n") // wraps past the action rows to the first value
	if cursor() != 0 {
		t.Fatalf("n from Mounts should wrap to Name (0), got %d", cursor())
	}
	press(m, "p") // wraps backwards to the last value
	if cursor() != 4 {
		t.Fatalf("p from Name should wrap to Mounts (4), got %d", cursor())
	}

	// explicit null and schema-less null keys are skipped; a set unknown key
	// is a value like any other
	doc.Set([]any{"mode"}, tnull())
	doc.Set([]any{"null_extra"}, tnull())
	doc.Set([]any{"extra"}, tnum("1"))
	m = newListModel(testModel(), doc, Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	// the two unknown keys add a warning row at index 0:
	// issues(0) Name(1) Mode(2,null) Server(3) Logging(4) Mounts(5)
	// Auth(6) null_extra(7,null) extra(8) Save(9) Raw(10)
	press(m, "n") // issues row → Name
	if cursor() != 1 {
		t.Fatalf("n from the issues row = %d, want Name (1)", cursor())
	}
	press(m, "n") // Name → Server: Mode is explicit null
	if cursor() != 3 {
		t.Fatalf("n must skip an explicit null, cursor = %d", cursor())
	}
	press(m, "n") // Server → Mounts: Logging/Auth are unset
	if cursor() != 5 {
		t.Fatalf("n from Server = %d, want Mounts (5)", cursor())
	}
	press(m, "n") // Mounts → extra: null_extra is skipped
	if r := rowAtCursor(m); r.kind != rowUnknown || !pathEq(r.path, []any{"extra"}) {
		t.Fatalf("n from Mounts should land on the set unknown key, got %+v", r)
	}
	press(m, "n") // wraps past the action rows to Name
	if cursor() != 1 {
		t.Fatalf("n from the last value should wrap to Name, got %d", cursor())
	}

	// no non-null key: n/p leave the cursor where it is
	root := &smodel.FormNode{Key: "$", Title: "$", Kind: smodel.KObject,
		Children: []*smodel.FormNode{
			{Key: "a", Title: "A", Kind: smodel.KScalar, JSONType: "string"},
			{Key: "b", Title: "B", Kind: smodel.KScalar, JSONType: "string"},
		}}
	empty := newListModel(root, tobj(), Options{})
	empty.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	press(empty, "n")
	if empty.top().cursor != 0 {
		t.Fatalf("n without values moved the cursor to %d", empty.top().cursor)
	}
	press(empty, "p")
	if empty.top().cursor != 0 {
		t.Fatalf("p without values moved the cursor to %d", empty.top().cursor)
	}

	// array items are entries too: null placeholders are skipped
	itemsRoot := &smodel.FormNode{Key: "$", Title: "$", Kind: smodel.KObject,
		Children: []*smodel.FormNode{{Key: "items", Title: "Items", Kind: smodel.KArray,
			Items: &smodel.FormNode{Key: "items", Kind: smodel.KObject,
				Children: []*smodel.FormNode{
					{Key: "x", Title: "X", Kind: smodel.KScalar, JSONType: "string"}}}}}}
	im := newListModel(itemsRoot, tobj(tmem("items", tarr(tnull(),
		tobj(tmem("x", tstr("v"))), tobj(tmem("x", tstr("w")))))), Options{})
	im.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	press(im, "enter") // items menu: Add(0), item0 null(1), item1(2), item2(3)
	press(im, "n")     // Add → item1, skipping the null placeholder
	if im.top().cursor != 2 {
		t.Fatalf("n must skip a null array item, cursor = %d", im.top().cursor)
	}
	press(im, "n")
	if im.top().cursor != 3 {
		t.Fatalf("n from item1 = %d, want item2 (3)", im.top().cursor)
	}
	press(im, "p")
	if im.top().cursor != 2 {
		t.Fatalf("p from item2 = %d, want item1 (2)", im.top().cursor)
	}
}

func TestMenuNextPrevCtrlJK(t *testing.T) {
	m := newListModel(testModel(), testDoc(), Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	cursor := func() int { return m.top().cursor }

	// ctrl+j is the same as n: Name(0) → Server(2), skipping unset Mode(1).
	press(m, "ctrl+j")
	if cursor() != 2 {
		t.Fatalf("ctrl+j from Name = %d, want Server (2)", cursor())
	}
	press(m, "ctrl+j")
	if cursor() != 4 {
		t.Fatalf("ctrl+j from Server = %d, want Mounts (4)", cursor())
	}
	// ctrl+k is the same as p.
	press(m, "ctrl+k")
	if cursor() != 2 {
		t.Fatalf("ctrl+k from Mounts = %d, want Server (2)", cursor())
	}

	root := &smodel.FormNode{Key: "$", Title: "$", Kind: smodel.KObject,
		Children: []*smodel.FormNode{
			{Key: "a", Title: "A", Kind: smodel.KScalar, JSONType: "string"},
			{Key: "b", Title: "B", Kind: smodel.KScalar, JSONType: "string"},
		}}
	empty := newListModel(root, tobj(), Options{})
	empty.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	press(empty, "ctrl+j", "ctrl+k")
	if empty.top().cursor != 0 {
		t.Fatalf("ctrl+j/k without values moved the cursor to %d", empty.top().cursor)
	}
}

func TestSearchViewInputsAboveResults(t *testing.T) {
	m := newListModel(testModel(), testDoc(), Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	press(m, "/")
	lines := strings.Split(m.View(), "\n")
	if len(lines) != 30 {
		t.Fatalf("search view has %d lines, want 30:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	if !strings.Contains(lines[2], "search:") {
		t.Fatalf("search input not above the results:\n%s", strings.Join(lines, "\n"))
	}
	if !strings.Contains(lines[29], "enter jump") {
		t.Fatalf("search help not last:\n%s", strings.Join(lines, "\n"))
	}
	found := false
	for i := 3; i < 29; i++ {
		if strings.Contains(lines[i], "$.") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("result rows not below the input:\n%s", strings.Join(lines, "\n"))
	}

	// Replace mode stacks two inputs above the results.
	press(m, "esc", "r")
	lines = strings.Split(m.View(), "\n")
	if len(lines) != 30 {
		t.Fatalf("replace view has %d lines, want 30:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	if !strings.Contains(lines[2], "search:") || !strings.Contains(lines[3], "replace:") ||
		!strings.Contains(lines[29], "enter jump") {
		t.Fatalf("replace inputs not above the results:\n%s", strings.Join(lines, "\n"))
	}
}

func TestParseIssueMissingProperties(t *testing.T) {
	const plural = "at '$': missing properties 'name', 'description', 'runs'"
	p, ok := parseIssue(plural)
	if !ok || jsondoc.PathString(p) != "$.name" {
		t.Fatalf("plural parseIssue = %v, %v", p, ok)
	}
	var got []string
	for _, f := range issueFlagPaths(plural) {
		got = append(got, jsondoc.PathString(f))
	}
	if strings.Join(got, ",") != "$.name,$.description,$.runs" {
		t.Fatalf("issueFlagPaths = %v", got)
	}

	// Singular required errors keep their old behavior.
	const single = "at '$.server': missing property 'port'"
	p, ok = parseIssue(single)
	if !ok || jsondoc.PathString(p) != "$.server.port" {
		t.Fatalf("singular parseIssue = %v, %v", p, ok)
	}
	if got := issueFlagPaths(single); len(got) != 1 || jsondoc.PathString(got[0]) != "$.server.port" {
		t.Fatalf("singular issueFlagPaths = %v", got)
	}
}

func TestSaveErrorJumpRealMultipleRequired(t *testing.T) {
	dir := t.TempDir()
	schemaPath := filepath.Join(dir, "multi.schema.json")
	const schema = `{"type":"object","properties":{
		"name":{"type":"string","title":"Name"},
		"description":{"type":"string","title":"Description"},
		"runs":{"type":"integer","title":"Runs"}},
		"required":["name","description","runs"]}`
	if err := os.WriteFile(schemaPath, []byte(schema), 0o644); err != nil {
		t.Fatal(err)
	}
	v, err := validate.New(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	model, _, err := smodel.Load(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	doc := tobj()
	errs := v.Check(doc)
	if len(errs) != 1 || !strings.Contains(errs[0], "missing properties") {
		t.Fatalf("validator messages = %q", errs)
	}
	m := newListModel(model, doc, Options{
		Validate: v.Check,
		Save:     func(*jsondoc.Node) error { return nil },
	})
	m.width, m.height = 120, 30
	press(m, "ctrl+s")
	if m.errs == nil {
		t.Fatal("save errors did not open")
	}
	press(m, "enter")
	if m.errs != nil {
		t.Fatalf("enter did not jump on %q", errs[0])
	}
	rows := menuRows(m)
	if m.top().cursor < 0 || m.top().cursor >= len(rows) {
		t.Fatalf("cursor %d out of %d rows", m.top().cursor, len(rows))
	}
	if got := jsondoc.PathString(rows[m.top().cursor].path); got != "$.name" {
		t.Fatalf("jumped to %q, want $.name", got)
	}
}

func TestSaveErrorJumpMissingProperties(t *testing.T) {
	cases := []struct {
		name string
		msg  string
		top  string
		row  string
	}{
		{
			name: "root plural",
			msg:  "at '$': missing properties 'name', 'description', 'runs'",
			top:  "$",
			row:  "$.name",
		},
		{
			name: "nested plural",
			msg:  "at '$.server': missing properties 'port', 'host'",
			top:  "$.server",
			row:  "$.server.port",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			validate := func(*jsondoc.Node) []string { return []string{tc.msg} }
			m := newListModel(testModel(), testDoc(), Options{
				Validate: validate,
				Save:     func(*jsondoc.Node) error { return nil },
			})
			m.width, m.height = 120, 30
			press(m, "ctrl+s")
			if m.errs == nil {
				t.Fatal("save errors did not open")
			}
			press(m, "enter")
			if m.errs != nil {
				t.Fatalf("enter did not jump: paths=%v", m.errs.paths)
			}
			if got := jsondoc.PathString(m.top().path); got != tc.top {
				t.Fatalf("top path = %q, want %q", got, tc.top)
			}
			rows := menuRows(m)
			if m.top().cursor < 0 || m.top().cursor >= len(rows) {
				t.Fatalf("cursor %d out of %d rows", m.top().cursor, len(rows))
			}
			if got := jsondoc.PathString(rows[m.top().cursor].path); got != tc.row {
				t.Fatalf("cursor row = %q, want %q", got, tc.row)
			}
		})
	}
}
