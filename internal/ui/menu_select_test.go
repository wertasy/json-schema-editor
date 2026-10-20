package ui

import (
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func TestMultiSelectToggleRenderAndRefusals(t *testing.T) {
	forceANSI(t)
	m := newListModel(testModel(), testDoc(), Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	// m marks the cursor row (Name); the mark renders as a red ">" once the
	// cursor moves on (the focused row keeps its inverse highlight)
	press(m, "m")
	r := rowAtCursor(m)
	if !m.rowSelected(r) {
		t.Fatal("m must mark the cursor row")
	}
	// cursor on a selected row: the > keeps the plain white cursor prefix
	// and the key is bold bright red (the required * keeps its own red)
	line := lineOf(t, m, "Name")
	if strings.Contains(line, mMark.Render("> ")) {
		t.Fatalf("cursor row must keep its white > prefix:\n%s", line)
	}
	if !strings.Contains(line, "\x1b[1m\x1b[91mName") {
		t.Fatalf("cursor key on a selected row must be bold red:\n%s", line)
	}
	press(m, "down")
	// a selected row off the cursor always carries the bright red marker
	line = lineOf(t, m, "Name")
	if !strings.Contains(line, mMark.Render("> ")) {
		t.Fatalf("selected row must carry the red > marker:\n%s", line)
	}
	if v := m.View(); strings.Contains(stripANSI(v), "\n* ") {
		t.Fatalf("the old * marker must be gone:\n%s", stripANSI(v))
	}
	press(m, "up", "m")
	if m.rowSelected(r) {
		t.Fatal("m must unmark the marked row")
	}

	// marks survive scrolling
	press(m, "m", "down", "up")
	if !m.rowSelected(r) {
		t.Fatal("marks must survive cursor movement")
	}

	// marks survive a drill down and back (cursor memory restores the row)
	press(m, "down", "down") // Server drill row
	press(m, "m")
	press(m, "enter") // into Server
	press(m, "esc")   // back out
	if !m.rowSelected(rowAtCursor(m)) {
		t.Fatal("marks must survive back/enter round-trips")
	}

	// action rows are refused
	press(m, "G") // < Edit raw JSON >
	press(m, "m")
	if m.notice != "m marks key-value rows" {
		t.Fatalf("notice = %q", m.notice)
	}
	press(m, "g", "g")

	// array items are refused
	press(m, "down", "down", "down", "down", "enter") // mounts items menu
	press(m, "down")
	press(m, "m")
	if m.notice != "m marks key-value pairs, not array items" {
		t.Fatalf("notice = %q", m.notice)
	}
}

func TestMultiSelectCopyAndDeleteJSON(t *testing.T) {
	rec := &clipboardRecorder{}
	doc := testDoc()
	m := newListModel(testModel(), doc, Options{Copy: rec.write})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	// mark Name(0) and Server(2), y copies both pairs as one object
	press(m, "m", "down", "down", "m")
	press(m, "y")
	want := `{"name":"webapp","server":{"port":8080,"host":"0.0.0.0","debug":true}}`
	if len(rec.texts) != 1 || rec.texts[0] != want {
		t.Fatalf("batch y copied %q, want %q", rec.texts, want)
	}
	if m.notice != "Copied 2 key-value pairs" {
		t.Fatalf("notice = %q", m.notice)
	}

	// the selection cleared: y is the plain cursor-row pair again
	press(m, "y")
	if len(rec.texts) != 2 || rec.texts[1] != `"server": {"port":8080,"host":"0.0.0.0","debug":true}` {
		t.Fatalf("post-batch y copied %q", rec.texts[1:])
	}

	// batch d removes both keys in one undo step
	press(m, "m")       // mark Server (cursor)
	press(m, "g", "g")  // top row: Name
	press(m, "m")       // mark Name
	press(m, "d")
	if _, ok := doc.Get([]any{"name"}); ok {
		t.Fatal("batch d must remove the marked name pair")
	}
	if _, ok := doc.Get([]any{"server"}); ok {
		t.Fatal("batch d must remove the marked server pair")
	}
	press(m, "u")
	if s := strAt(t, doc, "name"); s != "webapp" {
		t.Fatalf("one undo must restore the batch, name = %q", s)
	}
	if _, ok := doc.Get([]any{"server"}); !ok {
		t.Fatal("one undo must restore the batch, server missing")
	}
}

func TestMultiSelectCopyYAML(t *testing.T) {
	rec := &clipboardRecorder{}
	m := newListModel(testModel(), testDoc(), Options{Copy: rec.write, Raw: yamlTestCodec()})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	press(m, "m", "down", "down", "m")
	press(m, "y")
	want := "name: webapp\nserver:\n  port: 8080\n  host: 0.0.0.0\n  debug: true"
	if len(rec.texts) != 1 || rec.texts[0] != want {
		t.Fatalf("batch y copied %q, want %q", rec.texts, want)
	}
}

// lineOf returns the first View line containing text.
func lineOf(t *testing.T, m *listModel, text string) string {
	t.Helper()
	for _, line := range strings.Split(m.View(), "\n") {
		if strings.Contains(line, text) {
			return line
		}
	}
	t.Fatalf("no line containing %q in:\n%s", text, m.View())
	return ""
}

// stripANSI removes lipgloss escape sequences for plain-text assertions.
func stripANSI(s string) string {
	return ansiRe.ReplaceAllString(s, "")
}

var ansiRe = regexp.MustCompile("\x1b\\[[0-9;]*m")

// forceANSI makes lipgloss emit real escape sequences so assertions can
// check colors (the default renderer is Ascii without a TTY).
func forceANSI(t *testing.T) {
	t.Helper()
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI)
	t.Cleanup(func() { lipgloss.SetColorProfile(old) })
}

func TestVisualSelectMarksPassedRows(t *testing.T) {
	forceANSI(t)
	rec := &clipboardRecorder{}
	doc := testDoc()
	m := newListModel(testModel(), doc, Options{Copy: rec.write})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	// rows: Name(0) Mode(1) Server(2) Logging(3) Mounts(4) Auth(5) Save(6) Raw(7)

	// esc (q aliases it) leaves visual select without leaving the menu
	press(m, "v")
	press(m, "esc")
	if m.top().visual || m.exiting || len(m.stack) != 1 {
		t.Fatalf("esc must end visual select in place: visual=%v exiting=%v depth=%d",
			m.top().visual, m.exiting, len(m.stack))
	}
	press(m, "v")
	press(m, "q")
	if m.top().visual || m.exiting || len(m.stack) != 1 {
		t.Fatalf("q must end visual select in place: visual=%v exiting=%v depth=%d",
			m.top().visual, m.exiting, len(m.stack))
	}

	press(m, "v")
	if !m.top().visual {
		t.Fatal("v must turn visual select on")
	}
	if !m.rowSelected(rowAtCursor(m)) {
		t.Fatal("v must mark the starting row")
	}
	// in visual select the cursor row takes the mark colors too: the > is
	// bright red and the key is bold bright red
	press(m, "j")
	line := lineOf(t, m, "Mode")
	if !strings.Contains(line, mMark.Render("> ")) {
		t.Fatalf("cursor row must carry the red > marker in visual select:\n%s", line)
	}
	if !strings.Contains(line, mBold.Render(mMark.Render("Mode"))) {
		t.Fatalf("cursor key must be bold red in visual select:\n%s", line)
	}
	// the other selected rows carry the bright red > while visual is on
	if line := lineOf(t, m, "Name"); !strings.HasPrefix(line, mMark.Render("> ")) {
		t.Fatalf("marked row must carry the bright red > during visual select:\n%s", line)
	}

	// j passes over and marks every key-value row; action rows skip
	press(m, "j", "j") // Server, Logging → range Name..Logging
	if got := len(m.top().sel); got != 4 {
		t.Fatalf("selection = %d rows, want 4 (Name Mode Server Logging)", got)
	}

	// moving back shrinks the range: rows below the cursor leave it
	press(m, "k", "k") // back to Name
	if got := len(m.top().sel); got != 2 {
		t.Fatalf("selection = %d rows, want 2 after shrinking", got)
	}
	if _, ok := m.top().sel[selKey([]any{"server"})]; ok {
		t.Fatal("server must leave the selection when the range shrinks")
	}
	press(m, "j", "j", "j") // Server, Logging, Mounts
	if got := len(m.top().sel); got != 5 {
		t.Fatalf("selection = %d rows, want 5 (through Mounts)", got)
	}
	press(m, "j") // Auth
	if got := len(m.top().sel); got != 6 {
		t.Fatalf("selection = %d rows, want 6", got)
	}

	// batch copy over the visual selection: rows with values copy (Mode,
	// Logging and Auth are unset and skipped)
	press(m, "y")
	want := `{"name":"webapp","server":{"port":8080,"host":"0.0.0.0","debug":true},"mounts":[{"src":"/data","dst":"/mnt"}]}`
	if len(rec.texts) != 1 || rec.texts[0] != want {
		t.Fatalf("visual y copied %q, want %q", rec.texts, want)
	}
	if m.notice != "Copied 3 key-value pairs" {
		t.Fatalf("notice = %q", m.notice)
	}
	if len(m.top().sel) != 0 {
		t.Fatal("batch copy must clear the selection")
	}

	// visual stays on after the batch: movement re-marks from the anchor.
	// j wraps past the action rows (Raw skipped) to Name, then Mode; the
	// anchor is still Name, so the range is Name..Mode.
	press(m, "j", "j", "j", "j")
	if got := len(m.top().sel); got != 2 {
		t.Fatalf("selection = %d rows, want 2 after re-marking", got)
	}
	press(m, "v")
	if m.top().visual {
		t.Fatal("v must turn visual select off")
	}
	press(m, "down", "down")
	if got := len(m.top().sel); got != 2 {
		t.Fatalf("movement after v must not mark, selection = %d", got)
	}

	// cursor back on a selected row after closing visual: the same colors
	// as during visual select — white cursor > and bold red key
	press(m, "k", "k") // Mode
	line = lineOf(t, m, "Mode")
	if strings.Contains(line, mMark.Render("> ")) {
		t.Fatalf("cursor row must keep its white > prefix after closing:\n%s", line)
	}
	if !strings.Contains(line, mBold.Render(mMark.Render("Mode"))) {
		t.Fatalf("cursor key on a selected row must stay bold red:\n%s", line)
	}
	// selected rows off the cursor stay bright red after closing visual
	if line := lineOf(t, m, "Name"); !strings.Contains(line, mMark.Render("> ")) {
		t.Fatalf("selected row must keep the red > marker after closing:\n%s", line)
	}

	// drilling ends visual select: the menu drilled out of drops the flag
	press(m, "down")  // Server
	press(m, "v")     // visual on; the starting row joins the selection
	press(m, "enter") // into Server
	if m.top().visual {
		t.Fatal("a fresh child menu must not open in visual select")
	}
	press(m, "j")
	if len(m.top().sel) != 0 {
		t.Fatal("movement in the child menu must not mark")
	}
	press(m, "esc")
	if m.top().visual {
		t.Fatal("returning from a drill must end visual select")
	}
}

func TestMultiSelectDeleteSkipsRequiredMerged(t *testing.T) {
	m := requiredMergeModel(t)
	press(m, "down", "enter") // child menu, merged name (required)
	press(m, "m")             // mark the merged required row
	if !m.rowSelected(rowAtCursor(m)) {
		t.Fatal("merged key-value rows must be selectable")
	}
	press(m, "d")
	if _, ok := m.doc.Get([]any{"base", "name"}); !ok {
		t.Fatal("required merged row must survive batch d")
	}
	if !contains(m.View(), "Name is required") {
		t.Fatalf("required refusal missing:\n%s", m.View())
	}
	// the batch cleared the selection: the next d is the plain refusal again
	press(m, "down") // clear the notice
	press(m, "d")
	if _, ok := m.doc.Get([]any{"base", "name"}); !ok {
		t.Fatal("plain d must still refuse the required merged key")
	}
}
