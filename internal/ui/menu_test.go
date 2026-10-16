package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"json-schema-editor/internal/icons"
	"json-schema-editor/internal/jsondoc"
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
	m.Update(tea.WindowSizeMsg{Width: 150, Height: 30}) // full help bar fits
	v := m.View()
	for _, want := range []string{
		"Server Configuration", "$", "Name", "webapp", "Mode", "http",
		"Server", "port=8080 host=0.0.0.0 +1", "—",
		"Name*", "Server*", // required star shows regardless of a set value
		"Mounts", "1 item", "Auth", "Save and exit", "Edit raw JSON",
		"↑/↓ · gg/G · enter edit/open · space · del delete · a/A add · u undo · ctrl+r redo · esc · ctrl+s save · / search · r replace · ? help · ctrl+c quit",
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

func TestMenuDrillAndRestoreCursor(t *testing.T) {
	m := newListModel(testModel(), testDoc(), Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	// move to row 2 (Server), drill in, come back: cursor must survive
	press(m, "down", "down", "enter")
	if len(m.stack) != 2 || !contains(m.View(), "$/server") {
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
	if len(m.stack) != 2 || !contains(m.View(), "$/logging") {
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

func TestMenuRootEscQuitConfirm(t *testing.T) {
	m := newListModel(testModel(), testDoc(), Options{})
	press(m, "esc")
	if m.confirm == nil || !contains(m.View(), "Quit without saving?") {
		t.Fatalf("root esc must ask:\n%s", m.View())
	}
	press(m, "n")
	if m.confirm != nil || m.aborted {
		t.Fatal("n must dismiss without quitting")
	}
	press(m, "esc", "y")
	if !m.aborted {
		t.Fatal("y must abort")
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
	m := newListModel(testModel(), doc, Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	openRaw := func() {
		t.Helper()
		for i, r := range menuRows(m) {
			if r.kind == rowRaw {
				m.top().cursor = i
			}
		}
		press(m, "enter")
	}
	// root Edit raw JSON row
	openRaw()
	if m.text == nil || !contains(m.View(), "Edit raw JSON") {
		t.Fatalf("raw editor should open:\n%s", m.View())
	}
	if !contains(m.View(), `"name":"webapp"`) {
		t.Fatalf("raw editor prefill missing:\n%s", m.View())
	}
	// invalid JSON is rejected
	m.text.ta.SetValue("{oops")
	press(m, "ctrl+s")
	if m.text == nil || m.text.err == "" {
		t.Fatal("invalid JSON must keep the editor open with an error")
	}
	// valid JSON replaces the whole document
	m.text.ta.SetValue(`{"name":"changed"}`)
	press(m, "ctrl+s")
	if m.text != nil {
		t.Fatal("apply must close the editor")
	}
	if s := strAt(t, doc, "name"); s != "changed" {
		t.Fatalf("doc name = %q, want changed", s)
	}
	// empty at the root is refused
	openRaw()
	m.text.ta.SetValue("  ")
	press(m, "ctrl+s")
	if m.text == nil || !contains(m.View(), "cannot be empty") {
		t.Fatalf("empty root raw must be refused:\n%s", m.View())
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
		"at '/server/port': minimum value is 1024",
		"at '/name': got number, want string",
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
		return []string{"at '/name': got number, want string", "at '/server/port': minimum value is 1024", "at '/mode': bad mode"}
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
