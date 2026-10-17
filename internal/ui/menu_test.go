package ui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"json-schema-editor/internal/icons"
	"json-schema-editor/internal/jsondoc"
	"json-schema-editor/internal/smodel"
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
		"↑/↓ move · enter edit/open · esc back · ctrl+s save · / search · ? help",
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

func TestMenuTextareaRaw(t *testing.T) {
	doc := testDoc()
	doc.Set([]any{"extra"}, tstr("old"))
	m := newListModel(testModel(), doc, Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	openRaw := func() {
		t.Helper()
		for i, r := range menuRows(m) {
			if r.kind == rowUnknown {
				m.top().cursor = i
			}
		}
		press(m, "enter")
	}
	// an unknown key opens the in-app raw editor for its value
	openRaw()
	if m.text == nil || !contains(m.View(), "Edit raw JSON") {
		t.Fatalf("raw editor should open:\n%s", m.View())
	}
	if !contains(m.View(), `"old"`) {
		t.Fatalf("raw editor prefill missing:\n%s", m.View())
	}
	// invalid JSON is rejected
	m.text.ta.SetValue("{oops")
	press(m, "ctrl+s")
	if m.text == nil || m.text.err == "" {
		t.Fatal("invalid JSON must keep the editor open with an error")
	}
	// valid JSON replaces the value
	m.text.ta.SetValue(`"new"`)
	press(m, "ctrl+s")
	if m.text != nil {
		t.Fatal("apply must close the editor")
	}
	if s := strAt(t, doc, "extra"); s != "new" {
		t.Fatalf("extra = %q, want new", s)
	}
	// empty removes the key
	openRaw()
	m.text.ta.SetValue("  ")
	press(m, "ctrl+s")
	if m.text != nil {
		t.Fatal("empty apply must close the editor")
	}
	if _, ok := doc.Get([]any{"extra"}); ok {
		t.Fatal("empty raw value should remove the key")
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
