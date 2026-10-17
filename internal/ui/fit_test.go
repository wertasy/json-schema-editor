package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"json-schema-editor/internal/jsondoc"
	"json-schema-editor/internal/smodel"
)

func TestWrapText(t *testing.T) {
	cases := []struct {
		in    string
		width int
		want  []string
	}{
		{"hello world", 20, []string{"hello world"}},
		{"hello world", 8, []string{"hello", "world"}},
		{"abcdefghij", 4, []string{"abcd", "efgh", "ij"}},
		{"中文描述测试", 4, []string{"中文", "描述", "测试"}},
		{"line1\nline2", 10, []string{"line1", "line2"}},
		{"line1\n", 10, []string{"line1", ""}},
	}
	for _, c := range cases {
		got := wrapText(c.in, c.width)
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("wrapText(%q, %d) = %q, want %q", c.in, c.width, got, c.want)
		}
		for _, line := range got {
			if w := lipgloss.Width(line); w > c.width {
				t.Errorf("wrapText(%q, %d) line %q is %d cells", c.in, c.width, line, w)
			}
		}
	}
}

func TestMenuDescriptionWraps(t *testing.T) {
	root := &smodel.FormNode{Key: "$", Title: "T", Kind: smodel.KObject,
		Children: []*smodel.FormNode{{Key: "a", Title: "A", Kind: smodel.KScalar,
			JSONType: "string",
			Desc:     "alpha bravo charlie delta echo foxtrot golf"}}}
	m := newListModel(root, tobj(), Options{})
	m.Update(tea.WindowSizeMsg{Width: 20, Height: 12})
	v := m.View()
	if !contains(v, "alpha") || !contains(v, "golf") {
		t.Fatalf("the description was truncated:\n%s", v)
	}
	lines := strings.Split(v, "\n")
	if len(lines) != 12 {
		t.Fatalf("view has %d lines, want 12 with the help pinned", len(lines))
	}
	if !contains(lines[len(lines)-1], "↑/↓ move") {
		t.Fatalf("help bar is not on the last row:\n%s", v)
	}
	for _, l := range lines {
		if w := lipgloss.Width(l); w > 20 {
			t.Fatalf("line over the width: %d cells: %q", w, l)
		}
	}
	alphaLine, golfLine := -1, -1
	for i, l := range lines {
		if strings.Contains(l, "alpha") {
			alphaLine = i
		}
		if strings.Contains(l, "golf") {
			golfLine = i
		}
	}
	if alphaLine < 0 || golfLine <= alphaLine {
		t.Fatalf("description did not wrap across lines: alpha=%d golf=%d\n%s",
			alphaLine, golfLine, v)
	}
}

// TestMenuLinesFitTerminalCells pins the wcwidth accounting: rows carry
// ambiguous-width glyphs (severity icons, "●", "—") that lipgloss.Width
// counts as one cell while a CJK terminal gives them two. Every rendered
// line must stay within width-1 cells so the renderer's end-of-line erase
// cannot cut half a glyph (the icon flicker).
func TestMenuLinesFitTerminalCells(t *testing.T) {
	doc := testDoc()
	doc.Set([]any{"name"}, tstr(strings.Repeat("x", 80)))
	m := newListModel(testModel(), doc, Options{LoadErrors: []string{
		"at '$.server.port': minimum value is 1024",
		"at '$.name': got number, want string",
	}})
	m.Update(tea.WindowSizeMsg{Width: 50, Height: 20})
	lines := strings.Split(m.View(), "\n")
	if len(lines) != 20 {
		t.Fatalf("view has %d lines, want 20", len(lines))
	}
	for _, l := range lines {
		if w := displayWidth(l); w > 49 {
			t.Fatalf("line is %d cells, want <= 49: %q", w, l)
		}
	}
	if !strings.Contains(lines[len(lines)-1], "↑/↓ move") {
		t.Fatalf("help bar is not on the last row:\n%s", m.View())
	}
}

// TestViewLineWidths guards against line overflow: with ANSI colors active,
// no rendered line of the list view or its overlays may exceed the terminal
// width (narrow and wide sizes).
func TestViewLineWidths(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) }) // don't leak into other tests
	members := []jsondoc.Member{}
	for _, width := range []int{40, 68, 88, 108} {
		root, _, _ := inferModel()
		m := newListModel(root, &jsondoc.Node{Object: &members}, Options{})
		m.Update(tea.WindowSizeMsg{Width: width, Height: 20})
		press(m, "enter", "enter") // Inbounds → items → Add: merged menu
		if m.top().variantOf == nil {
			t.Fatalf("w=%d: dynamic menu did not open", width)
		}
		checkWidth := func(name, view string) {
			t.Helper()
			for _, l := range strings.Split(strings.TrimSuffix(view, "\n"), "\n") {
				if w := lipgloss.Width(l); w > width {
					t.Errorf("w=%d %s: line width %d: %q", width, name, w, l)
				}
			}
		}
		checkWidth("list", m.View())
		press(m, "down", "enter") // Type row → enum overlay
		if m.sel == nil {
			t.Fatalf("w=%d: enum overlay did not open", width)
		}
		checkWidth("overlay", m.View())
	}
}
