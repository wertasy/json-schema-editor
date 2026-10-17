package ui

// Macro recording/playback tests: @ arms capture, . replays the last macro
// through the normal dispatch path, literal @/. inside text inputs are
// recorded (and replayed) as characters, and q ≡ esc outside text inputs
// (literal inside, where it never touches macro state); in non-typing
// contexts it is recorded as esc and replays as esc.

import (
	"fmt"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestMacroRecordPlayback(t *testing.T) {
	m := newListModel(testModel(), testDoc(), Options{})

	press(m, "@")
	if !m.recording {
		t.Fatalf("@ should arm recording, notice=%q", m.notice)
	}
	if !contains(m.View(), "● REC 0") {
		t.Fatalf("recording indicator missing:\n%s", m.View())
	}

	press(m, "j", "j")
	if len(m.recBuf) != 2 {
		t.Fatalf("recBuf = %d keys, want 2", len(m.recBuf))
	}
	if !contains(m.View(), "● REC 2") {
		t.Fatalf("recording indicator should count 2:\n%s", m.View())
	}

	press(m, "@")
	if m.recording {
		t.Fatal("@ should stop recording")
	}
	if len(m.macro) != 2 {
		t.Fatalf("macro = %d keys, want 2", len(m.macro))
	}
	if !contains(m.notice, "macro recorded: 2 keys") {
		t.Fatalf("notice = %q", m.notice)
	}
	if contains(m.View(), "● REC") {
		t.Fatalf("indicator must clear after recording:\n%s", m.View())
	}

	if got := m.top().cursor; got != 2 {
		t.Fatalf("cursor after recording = %d, want 2", got)
	}
	press(m, ".")
	if got := m.top().cursor; got != 4 {
		t.Fatalf("cursor after playback = %d, want 4", got)
	}
	if !contains(m.notice, "played 2 keys") {
		t.Fatalf("notice = %q", m.notice)
	}
	press(m, ".")
	if got := m.top().cursor; got != 6 {
		t.Fatalf("cursor after second playback = %d, want 6", got)
	}
}

func TestMacroReplaysValueEdit(t *testing.T) {
	m := newListModel(testModel(), testDoc(), Options{})

	// Record "j" (Name → Mode) plus "l" (enum http → grpc).
	press(m, "@", "j", "l", "@")
	if m.recording || len(m.macro) != 2 {
		t.Fatalf("recording = %v, macro = %d keys, want false/2", m.recording, len(m.macro))
	}
	if got := strAt(t, m.doc, "mode"); got != "grpc" {
		t.Fatalf("mode after recording = %q, want grpc", got)
	}

	press(m, "k") // back to the Name row
	if got := m.top().cursor; got != 0 {
		t.Fatalf("cursor = %d, want 0", got)
	}
	press(m, ".")
	if got := m.top().cursor; got != 1 {
		t.Fatalf("cursor after playback = %d, want 1", got)
	}
	if r := rowAtCursor(m); r.fn == nil || r.fn.Key != "mode" {
		t.Fatalf("cursor should sit on the Mode row, got %+v", r)
	}
	if got := strAt(t, m.doc, "mode"); got != "tcp" {
		t.Fatalf("mode after playback = %q, want tcp", got)
	}
}

func TestMacroLiteralInInput(t *testing.T) {
	m := newListModel(testModel(), testDoc(), Options{})

	press(m, "@")     // start recording on the Name row
	press(m, "enter") // open the in-place editor
	if m.edit == nil {
		t.Fatalf("editor did not open, notice=%q", m.notice)
	}
	press(m, "@", ".") // literal characters inside the input
	if !m.recording {
		t.Fatal("@ inside a text input must not stop recording")
	}
	if got := m.edit.ti.Value(); got != "webapp@." {
		t.Fatalf("input value = %q, want webapp@.", got)
	}
	press(m, "enter") // commit the edit
	press(m, "@")     // stop recording

	if m.recording {
		t.Fatal("@ in plain list mode should stop recording")
	}
	if len(m.macro) != 4 {
		t.Fatalf("macro = %d keys, want 4 (enter, @, ., enter)", len(m.macro))
	}
	if got := strAt(t, m.doc, "name"); got != "webapp@." {
		t.Fatalf("name after recording = %q, want webapp@.", got)
	}

	press(m, ".") // replay the typed characters into the same input
	if m.edit != nil {
		t.Fatal("the replayed enter must have committed the edit")
	}
	if got := strAt(t, m.doc, "name"); got != "webapp@.@." {
		t.Fatalf("name after playback = %q, want webapp@.@.", got)
	}
	if !contains(m.notice, "played 4 keys") {
		t.Fatalf("notice = %q", m.notice)
	}
}

func TestMacroPlaybackStopsAtSessionEnd(t *testing.T) {
	// plain ctrl+s writes mid-macro and playback continues
	var saves int
	m := newListModel(testModel(), testDoc(), countSave(&saves))
	m.macro = []tea.KeyMsg{key("ctrl+s"), key("j")}

	_, cmd := m.Update(key("."))
	if saves != 1 || !m.saved {
		t.Fatal("ctrl+s in the macro must write through Save")
	}
	if got := m.top().cursor; got != 1 {
		t.Fatalf("cursor = %d, want 1 (playback continues after a plain save)", got)
	}
	if !contains(m.notice, "played 2 keys") {
		t.Fatalf("notice = %q", m.notice)
	}
	if cmd != nil {
		t.Fatal("a plain ctrl+s must not quit; only Save and exit ends the session")
	}

	// a macro containing Save and exit still stops at the session end
	var exits int
	m2 := newListModel(testModel(), testDoc(), countSave(&exits))
	m2.macro = []tea.KeyMsg{
		key("down"), key("down"), key("down"), key("down"), key("down"), key("down"),
		key("enter"), // Save and exit
		key("j"),
	}
	_, cmd = m2.Update(key("."))
	if exits != 1 || !m2.saved {
		t.Fatal("Save and exit in the macro must write through Save")
	}
	if got := m2.top().cursor; got != 6 {
		t.Fatalf("cursor = %d, want 6 (playback stopped on the Save and exit row, j must not run)", got)
	}
	if !contains(m2.notice, "stopped at key 7/8") {
		t.Fatalf("notice = %q", m2.notice)
	}
	if cmd == nil {
		t.Fatal("playback must keep the quit command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("cmd = %T, want tea.QuitMsg", cmd())
	}
}

func TestMacroEmptyAndCancel(t *testing.T) {
	m := newListModel(testModel(), testDoc(), Options{})

	press(m, ".")
	if !contains(m.notice, "no macro recorded") {
		t.Fatalf("notice = %q", m.notice)
	}
	if got := m.top().cursor; got != 0 {
		t.Fatalf("cursor = %d, want 0", got)
	}

	press(m, "@", "@") // arm and immediately stop: cancels
	if m.recording {
		t.Fatal("empty stop should end recording")
	}
	if m.macro != nil {
		t.Fatalf("macro = %v, want nil (previous macro semantics: none existed)", m.macro)
	}
	if !contains(m.notice, "cancelled") {
		t.Fatalf("notice = %q", m.notice)
	}
	if got := m.top().cursor; got != 0 {
		t.Fatalf("cursor = %d, want 0", got)
	}

	// A cancel keeps the previous macro: record one, then arm and cancel.
	press(m, "@", "j", "@")
	press(m, "@")
	press(m, "@")
	if m.recording || m.macro == nil || len(m.macro) != 1 {
		t.Fatalf("recording = %v, macro = %v, want false and the 1-key macro", m.recording, m.macro)
	}
}

func TestMacroKeyConflicts(t *testing.T) {
	m := newListModel(testModel(), testDoc(), Options{LoadErrors: []string{"at '/name': invalid"}})

	// (a) not recording: @ arms recording on the issue overlay too; the list
	// stays open (esc/q is the close binding).
	press(m, "enter")
	if m.issues == nil {
		t.Fatalf("status row should open the issue list:\n%s", m.View())
	}
	press(m, "@")
	if !m.recording {
		t.Fatalf("@ should arm recording on the issue list, notice=%q", m.notice)
	}
	if m.issues == nil {
		t.Fatal("arming recording must not close the issue list")
	}
	if !contains(m.View(), "● REC 0") {
		t.Fatalf("recording indicator missing on the issue list:\n%s", m.View())
	}
	// (b) recording: @ stops recording in every non-typing context, so the
	// overlay keeps its state; . replays against the open overlay; esc/q closes.
	press(m, "down")
	press(m, "@")
	if m.recording {
		t.Fatal("@ must stop recording")
	}
	if m.issues == nil {
		t.Fatal("the issue list must stay open while recording")
	}
	press(m, ".")
	if !contains(m.notice, "played 1 keys") {
		t.Fatalf("playback on the overlay: notice = %q", m.notice)
	}
	if m.issues == nil {
		t.Fatal("playback must not close the issue list")
	}
	press(m, "q")
	if m.issues != nil {
		t.Fatal("q should close the issue list like esc")
	}
}

func TestMacroCap(t *testing.T) {
	m := newListModel(testModel(), testDoc(), Options{})
	press(m, "@")
	for i := 0; i < maxMacroKeys; i++ {
		press(m, "j")
	}
	if m.recording {
		t.Fatal("reaching the cap should stop recording")
	}
	if len(m.macro) != maxMacroKeys {
		t.Fatalf("macro = %d keys, want %d", len(m.macro), maxMacroKeys)
	}
	want := fmt.Sprintf("macro limit reached (%d keys)", maxMacroKeys)
	if !contains(m.notice, want) {
		t.Fatalf("notice = %q, want %q", m.notice, want)
	}

	// Keys pressed after the cap are not appended to the macro.
	for i := 0; i < 5; i++ {
		press(m, "j")
	}
	if m.recording || len(m.macro) != maxMacroKeys {
		t.Fatalf("recording = %v, macro = %d keys, want false/%d",
			m.recording, len(m.macro), maxMacroKeys)
	}
}

// TestQIsEscapeAlias pins q ≡ esc outside text inputs — clean quit / save
// prompt at the root, pop a menu — while inside an input q is an ordinary
// character and esc cancels; q never touches macro state in non-typing
// contexts: it is recorded as esc and replays as esc.
func TestQIsEscapeAlias(t *testing.T) {
	m := newListModel(testModel(), testDoc(), Options{})
	press(m, "q")
	if m.confirm != nil || !m.exiting || m.aborted {
		t.Fatalf("q at a clean root must quit like esc: confirm=%v exiting=%v aborted=%v",
			m.confirm != nil, m.exiting, m.aborted)
	}

	// a changed document makes q open the same save prompt as esc
	dirty := newListModel(testModel(), testDoc(), Options{})
	press(dirty, "enter", "ctrl+u", "x", "enter", "q")
	if dirty.confirm == nil || !contains(dirty.View(), "Save changes?") {
		t.Fatalf("q at a changed root must ask to save like esc:\n%s", dirty.View())
	}

	press(m, "enter") // in-place editor on Name
	if m.edit == nil {
		t.Fatal("enter should open the in-place editor")
	}
	press(m, "x", "q")
	if m.edit == nil {
		t.Fatal("q must not cancel the in-place edit — it is a literal character there")
	}
	if got := m.edit.ti.Value(); got != "webappxq" {
		t.Fatalf("input = %q, want webappxq (q typed literally)", got)
	}
	press(m, "esc") // esc cancels
	if m.edit != nil {
		t.Fatal("esc must cancel the in-place edit")
	}
	if s := strAt(t, m.doc, "name"); s != "webapp" {
		t.Fatalf("name = %q, want the untouched webapp", s)
	}

	press(m, "down", "down", "enter") // into the Server menu
	press(m, "@")                     // arm recording inside the Server menu
	press(m, "q")                     // esc semantics: pops back to the root, recorded as esc
	if !m.recording {
		t.Fatal("q must not stop recording")
	}
	if len(m.stack) != 1 {
		t.Fatalf("stack = %d levels, want 1 (q must pop like esc)", len(m.stack))
	}
	if len(m.recBuf) != 1 || m.recBuf[0].String() != "esc" {
		t.Fatalf("recBuf = %+v, want [esc]", m.recBuf)
	}
	press(m, "@") // stop recording
	if m.recording || len(m.macro) != 1 {
		t.Fatalf("recording = %v, macro = %v, want false/[esc]", m.recording, m.macro)
	}

	// the recorded q replays as esc: at the clean root it quits directly
	press(m, ".")
	if m.confirm != nil || !m.exiting || m.aborted {
		t.Fatalf("replayed q must act as esc: confirm=%v exiting=%v aborted=%v",
			m.confirm != nil, m.exiting, m.aborted)
	}
}
