package ui

// Macro recording/playback tests: q arms capture, @ replays the last macro
// through the normal dispatch path, literal q/@ inside text inputs are
// recorded (and replayed) as characters.

import (
	"fmt"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestMacroRecordPlayback(t *testing.T) {
	m := newListModel(testModel(), testDoc(), Options{})

	press(m, "q")
	if !m.recording {
		t.Fatalf("q should arm recording, notice=%q", m.notice)
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

	press(m, "q")
	if m.recording {
		t.Fatal("q should stop recording")
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
	press(m, "@")
	if got := m.top().cursor; got != 4 {
		t.Fatalf("cursor after playback = %d, want 4", got)
	}
	if !contains(m.notice, "played 2 keys") {
		t.Fatalf("notice = %q", m.notice)
	}
	press(m, "@")
	if got := m.top().cursor; got != 6 {
		t.Fatalf("cursor after second playback = %d, want 6", got)
	}
}

func TestMacroReplaysValueEdit(t *testing.T) {
	m := newListModel(testModel(), testDoc(), Options{})

	// Record "j" (Name → Mode) plus "l" (enum http → grpc).
	press(m, "q", "j", "l", "q")
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
	press(m, "@")
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

	press(m, "q")     // start recording on the Name row
	press(m, "enter") // open the in-place editor
	if m.edit == nil {
		t.Fatalf("editor did not open, notice=%q", m.notice)
	}
	press(m, "q", "@") // literal characters inside the input
	if !m.recording {
		t.Fatal("q inside a text input must not stop recording")
	}
	if got := m.edit.ti.Value(); got != "webappq@" {
		t.Fatalf("input value = %q, want webappq@", got)
	}
	press(m, "enter") // commit the edit
	press(m, "q")     // stop recording

	if m.recording {
		t.Fatal("q in plain list mode should stop recording")
	}
	if len(m.macro) != 4 {
		t.Fatalf("macro = %d keys, want 4 (enter, q, @, enter)", len(m.macro))
	}
	if got := strAt(t, m.doc, "name"); got != "webappq@" {
		t.Fatalf("name after recording = %q, want webappq@", got)
	}

	press(m, "@") // replay the typed characters into the same input
	if m.edit != nil {
		t.Fatal("the replayed enter must have committed the edit")
	}
	if got := strAt(t, m.doc, "name"); got != "webappq@q@" {
		t.Fatalf("name after playback = %q, want webappq@q@", got)
	}
	if !contains(m.notice, "played 4 keys") {
		t.Fatalf("notice = %q", m.notice)
	}
}

func TestMacroPlaybackStopsAtSessionEnd(t *testing.T) {
	var saves int
	m := newListModel(testModel(), testDoc(), countSave(&saves))
	m.macro = []tea.KeyMsg{key("ctrl+s"), key("j")}

	_, cmd := m.Update(key("@"))
	if saves != 1 || !m.saved {
		t.Fatal("ctrl+s in the macro must write through Save")
	}
	if got := m.top().cursor; got != 0 {
		t.Fatalf("cursor = %d, want 0 (second key must not run)", got)
	}
	if !contains(m.notice, "stopped at key 1/2") {
		t.Fatalf("notice = %q", m.notice)
	}
	if cmd != nil {
		t.Fatal("a plain ctrl+s writes and does not quit")
	}
}

func TestMacroEmptyAndCancel(t *testing.T) {
	m := newListModel(testModel(), testDoc(), Options{})

	press(m, "@")
	if !contains(m.notice, "no macro recorded") {
		t.Fatalf("notice = %q", m.notice)
	}
	if got := m.top().cursor; got != 0 {
		t.Fatalf("cursor = %d, want 0", got)
	}

	press(m, "q", "q") // arm and immediately stop: cancels
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
	press(m, "q", "j", "q")
	press(m, "q")
	press(m, "q")
	if m.recording || m.macro == nil || len(m.macro) != 1 {
		t.Fatalf("recording = %v, macro = %v, want false and the 1-key macro", m.recording, m.macro)
	}
}

func TestMacroKeyConflicts(t *testing.T) {
	m := newListModel(testModel(), testDoc(), Options{LoadErrors: []string{"at '/name': invalid"}})

	// (a) not recording: q keeps the issue overlay's own binding (close).
	press(m, "enter")
	if m.issues == nil {
		t.Fatalf("status row should open the issue list:\n%s", m.View())
	}
	press(m, "q")
	if m.issues != nil {
		t.Fatal("q must still close the issue list when not recording")
	}
	if m.recording {
		t.Fatal("q in an overlay must not start recording")
	}

	// (b) recording: q stops recording in every non-typing context, so the
	// overlay keeps its state.
	press(m, "q")
	if !m.recording {
		t.Fatalf("q should arm recording, notice=%q", m.notice)
	}
	press(m, "enter")
	if m.issues == nil {
		t.Fatal("enter should open the issue list while recording")
	}
	press(m, "q")
	if m.recording {
		t.Fatal("q must stop recording")
	}
	if m.issues == nil {
		t.Fatal("the issue list must stay open while recording")
	}
}

func TestMacroCap(t *testing.T) {
	m := newListModel(testModel(), testDoc(), Options{})
	press(m, "q")
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
