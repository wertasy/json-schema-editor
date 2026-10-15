package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"json-schema-editor/internal/jsondoc"
)

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
