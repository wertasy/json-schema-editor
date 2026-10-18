package ui

// The "?" keyboard-help overlay and the schema-description line rendered
// above the help bar for the highlighted row.

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func TestHelpOverlay(t *testing.T) {
	m := newListModel(testModel(), testDoc(), Options{})
	m.Update(tea.WindowSizeMsg{Width: 170, Height: 30})

	if v := m.View(); !contains(v, "? help") {
		t.Fatalf("help bar should advertise ? help:\n%s", v)
	}
	press(m, "?")
	if v := m.View(); !contains(v, "Keyboard help") || !contains(v, "record / play a macro") {
		t.Fatalf("? should open the key reference:\n%s", v)
	}

	// no list keys leak through the overlay: navigation and history stay put
	press(m, "j", "u")
	if v := m.View(); !contains(v, "Keyboard help") {
		t.Fatalf("help overlay must swallow list keys:\n%s", v)
	}
	if s := strAt(t, m.doc, "name"); s != "webapp" {
		t.Fatalf("name = %q, keys must not reach the doc while help is open", s)
	}

	// q closes the overlay instead of aliasing to esc's quit confirm
	press(m, "q")
	if m.help || m.confirm != nil {
		t.Fatalf("q must close help without a quit confirm, help=%v confirm=%v",
			m.help, m.confirm != nil)
	}

	press(m, "?") // re-open, esc closes
	press(m, "esc")
	if m.help {
		t.Fatal("esc must close the help overlay")
	}
	if v := m.View(); !contains(v, "Name") {
		t.Fatalf("list must be back after closing help:\n%s", v)
	}
}

func TestHelpOverlayFooterAtBottom(t *testing.T) {
	m := newListModel(testModel(), testDoc(), Options{})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	press(m, "?")
	lines := strings.Split(m.View(), "\n")
	if len(lines) != 40 {
		t.Fatalf("help view has %d lines, want 40:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	if !strings.Contains(lines[39], "esc/? close") {
		t.Fatalf("close hint not on the last line:\n%s", strings.Join(lines, "\n"))
	}
	if strings.Contains(lines[39], "\n") {
		t.Fatalf("close hint has a trailing newline")
	}
}

// TestNoticePinnedAboveHelp pins the bottom stack: schema description, then
// the transient notice, then the hint bar, regardless of how short the menu
// is. Previously the notice sat right under the rows and blank padding opened
// between it and the help bar.
func TestNoticePinnedAboveHelp(t *testing.T) {
	root := testModel()
	root.Children[0].Desc = "name help" // Name row description
	m := newListModel(root, testDoc(), Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.notice = "empty fields hidden · z shows them"

	lines := strings.Split(m.View(), "\n")
	if len(lines) != 30 {
		t.Fatalf("view has %d lines, want 30", len(lines))
	}
	if !contains(lines[len(lines)-1], "? help") {
		t.Fatalf("help bar is not on the last row:\n%s", m.View())
	}
	if !contains(lines[len(lines)-2], "empty fields hidden · z shows them") {
		t.Fatalf("notice is not directly above the help bar:\n%s", m.View())
	}
	if !contains(lines[len(lines)-3], "name help") {
		t.Fatalf("description is not above the notice:\n%s", m.View())
	}

	// Without a description the notice still hugs the help bar.
	m2 := newListModel(testModel(), testDoc(), Options{})
	m2.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m2.notice = "Saved"
	lines = strings.Split(m2.View(), "\n")
	if !contains(lines[len(lines)-2], "Saved") {
		t.Fatalf("notice is not directly above the help bar:\n%s", m2.View())
	}
}

// TestHelpOverlayKeyColumn pins the two-column help layout: descriptions
// start at the fixed title column (24 cells), and continuation lines align
// under them. On narrow terminals the padding collapses so the description
// still gets room.
func TestHelpOverlayKeyColumn(t *testing.T) {
	m := newListModel(testModel(), testDoc(), Options{})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	press(m, "?")

	lines := strings.Split(m.View(), "\n")
	find := func(needle string) string {
		t.Helper()
		for _, l := range lines {
			if strings.Contains(l, needle) {
				return l
			}
		}
		t.Fatalf("help line with %q missing:\n%s", needle, m.View())
		return ""
	}
	line := find("move (wraps")
	at := strings.Index(line, "move (wraps")
	if !strings.Contains(line[:at], "up / down / j / k") {
		t.Fatalf("key column lost its key:\n%s", line)
	}
	if got := displayWidth(line[:at]); got != titleColWidth {
		t.Fatalf("description starts at %d cells, want %d:\n%s", got, titleColWidth, line)
	}

	cont := find("at the root: quit")
	at = strings.Index(cont, "at the root: quit")
	if got := displayWidth(cont[:at]); got != titleColWidth {
		t.Fatalf("continuation starts at %d cells, want %d:\n%s", got, titleColWidth, cont)
	}

	// Narrow terminals collapse the padding instead of clipping the text away.
	m.Update(tea.WindowSizeMsg{Width: 30, Height: 40})
	narrow := m.View()
	if !contains(narrow, "move (wraps") {
		t.Fatalf("narrow help lost the description:\n%s", narrow)
	}
	for _, l := range strings.Split(strings.TrimSuffix(narrow, "\n"), "\n") {
		if got := displayWidth(l); got > 29 {
			t.Fatalf("narrow help line is %d cells, want <= 29: %q", got, l)
		}
	}
}

func TestSchemaDescLine(t *testing.T) {
	root := testModel()
	root.Children[0].Desc = "service name, lowercase" // Name row
	m := newListModel(root, testDoc(), Options{})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})

	if v := m.View(); !contains(v, "service name, lowercase") {
		t.Fatalf("description of the focused row should show:\n%s", v)
	}

	press(m, "down") // Mode has no description
	if v := m.View(); contains(v, "service name, lowercase") {
		t.Fatalf("description must not linger after leaving the row:\n%s", v)
	}

	// drilling keeps the feature: Server menu has no descriptions, then Port
	press(m, "down", "enter")
	if v := m.View(); contains(v, "service name, lowercase") {
		t.Fatal("parent's description must not show inside the submenu")
	}
}

// TestSchemaDescColorDistinctFromHelp: the row description is schema metadata
// and must not share the hint bar's color, so the two bottom rows read apart.
func TestSchemaDescColorDistinctFromHelp(t *testing.T) {
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(old) })

	root := testModel()
	root.Children[0].Desc = "service name, lowercase" // Name row
	m := newListModel(root, testDoc(), Options{})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	lines := strings.Split(m.View(), "\n")

	sgr := func(s string) string {
		i := strings.IndexByte(s, 0x1b)
		if i < 0 || i+1 >= len(s) || s[i+1] != '[' {
			return ""
		}
		if j := strings.IndexByte(s[i:], 'm'); j > 0 {
			return s[i : i+j+1]
		}
		return ""
	}

	var descLine string
	for i, l := range lines {
		if strings.Contains(l, "service name, lowercase") {
			descLine = l
			if hint := lines[i+1]; !strings.Contains(hint, "↑/↓ move") {
				t.Fatalf("row after the description must be the hint bar, got %q", hint)
			} else if sgr(hint) == sgr(l) || sgr(hint) == "" || sgr(l) == "" {
				t.Fatalf("description and hint bar must use different colors: desc=%q help=%q",
					sgr(l), sgr(hint))
			}
		}
	}
	if descLine == "" {
		t.Fatalf("description row missing:\n%s", m.View())
	}
	if want := sgr(mDesc.Render("x")); sgr(descLine) != want {
		t.Fatalf("description SGR = %q, want the mDesc color %q", sgr(descLine), want)
	}
}
