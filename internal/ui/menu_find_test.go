package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestRootHLeftDoNotExit(t *testing.T) {
	m := newListModel(testModel(), testDoc(), Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	// at the root h and left do nothing: no parent menu, no quit prompt
	press(m, "h", "left")
	if m.exiting || m.confirm != nil || len(m.stack) != 1 {
		t.Fatalf("h/left at the root must stay: exiting=%v confirm=%v depth=%d",
			m.exiting, m.confirm != nil, len(m.stack))
	}

	// inside a submenu h still goes back
	press(m, "down", "down", "enter") // Server menu
	press(m, "h")
	if len(m.stack) != 1 {
		t.Fatalf("h in a submenu must go back, depth = %d", len(m.stack))
	}

	// esc/q keep the root exit semantics (clean doc: exits directly)
	press(m, "esc")
	if !m.exiting {
		t.Fatal("esc at the root must still exit")
	}
}

func TestQuickMove(t *testing.T) {
	m := newListModel(testModel(), testDoc(), Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	cursor := func() int { return m.top().cursor }
	// rows: Name(0) Mode(1) Server(2) Logging(3) Mounts(4) Auth(5) Save(6) Raw(7)

	// f waits for one character, then jumps down to the key starting with it
	press(m, "f")
	if m.find == nil {
		t.Fatal("f must arm quick move")
	}
	// the bottom help bar shows the find prompt while waiting
	if v := m.View(); !contains(v, "Quick move: type a character to move down · esc cancels") {
		t.Fatalf("help bar must show the find prompt:\n%s", v)
	}
	press(m, "l")
	if m.find != nil {
		t.Fatal("the character must end quick move")
	}
	if v := m.View(); !contains(v, "↑/↓ move · enter edit/open · esc back · ctrl+s/w save · / search · ? help") {
		t.Fatalf("help bar must return after the find:\n%s", v)
	}
	if cursor() != 3 {
		t.Fatalf("cursor = %d, want Logging (3)", cursor())
	}

	// f wraps: from Logging the next key starting with "s" is Server above
	press(m, "f", "s")
	if cursor() != 2 {
		t.Fatalf("cursor = %d, want Server (2)", cursor())
	}

	// F moves upward
	press(m, "F", "n")
	if cursor() != 0 {
		t.Fatalf("cursor = %d, want Name (0)", cursor())
	}

	// matching is case-sensitive: uppercase "N" does not match the key
	// "name", and the cursor row itself is skipped
	press(m, "f", "N")
	if m.notice != `no key starts with "N"` {
		t.Fatalf("notice = %q (uppercase N must not match name)", m.notice)
	}
	if cursor() != 0 {
		t.Fatalf("failed find moved the cursor to %d", cursor())
	}
	press(m, "f", "n")
	if cursor() != 0 {
		t.Fatalf("cursor = %d, want Name (0) unchanged", cursor())
	}
	press(m, "j")      // Mode
	press(m, "f", "n") // wraps up to Name (0)
	if cursor() != 0 {
		t.Fatalf("cursor = %d, want Name (0)", cursor())
	}

	// repeated starting characters wrap to the earlier match
	press(m, "f", "m") // Name → mode (1)
	if cursor() != 1 {
		t.Fatalf("cursor = %d, want Mode (1)", cursor())
	}
	press(m, "f", "m") // Mode → mounts (4)
	if cursor() != 4 {
		t.Fatalf("cursor = %d, want Mounts (4)", cursor())
	}
	press(m, "f", "m") // wraps past Auth/Save/Raw/Name to Mode
	if cursor() != 1 {
		t.Fatalf("cursor = %d, want Mode (1)", cursor())
	}

	// no match: notice, cursor unchanged
	press(m, "f", "z")
	if m.find != nil {
		t.Fatal("the character must end quick move even without a match")
	}
	if m.notice != `no key starts with "z"` {
		t.Fatalf("notice = %q", m.notice)
	}
	if cursor() != 1 {
		t.Fatalf("failed find moved the cursor to %d", cursor())
	}

	// esc cancels without moving
	press(m, "f")
	press(m, "esc")
	if m.find != nil {
		t.Fatal("esc must cancel quick move")
	}
	if m.notice != "quick move cancelled" {
		t.Fatalf("notice = %q", m.notice)
	}
	if cursor() != 1 {
		t.Fatalf("cancelled find moved the cursor to %d", cursor())
	}

	// a non-rune key (arrows, enter, …) cancels the find and is handled as
	// usual; rune keys are the find character
	press(m, "f")
	press(m, "up")
	if m.find != nil {
		t.Fatal("a non-rune key must cancel quick move")
	}
	if cursor() != 0 {
		t.Fatalf("up after cancelling find moved the cursor to %d, want 0", cursor())
	}
}
