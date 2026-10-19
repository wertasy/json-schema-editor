package ui

// Row layout: the value column follows the widest title in the menu, bounded
// to [titleColMin, titleColMax]. Longer titles are shown in full.

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/wertasy/json-schema-editor/internal/smodel"
)

func TestMenuTitleColWidthAdapts(t *testing.T) {
	cases := []struct {
		name     string
		title    string
		required bool
		want     int
	}{
		{"short uses minimum", "A", false, titleColMin},
		{"adaptive middle", strings.Repeat("x", 20), false, 20},
		{"exact maximum", strings.Repeat("x", titleColMax), false, titleColMax},
		{"over maximum clamps", strings.Repeat("x", titleColMax+10), false, titleColMax},
		{"required marker counts", strings.Repeat("x", titleColMin), true, titleColMin + 1},
	}
	for _, c := range cases {
		root := &smodel.FormNode{Key: "$", Title: "Root", Kind: smodel.KObject,
			Children: []*smodel.FormNode{{
				Key: "field", Title: c.title, Kind: smodel.KScalar,
				JSONType: "string", Required: c.required,
			}}}
		doc := tobj(tmem("field", tstr("v")))
		m := newListModel(root, doc, Options{})
		if got := m.menuTitleColWidth(menuRows(m)); got != c.want {
			t.Errorf("%s: width = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestShortTitlesUseMinimumColumn(t *testing.T) {
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.Ascii)
	t.Cleanup(func() { lipgloss.SetColorProfile(old) })

	root := &smodel.FormNode{Key: "$", Title: "Root", Kind: smodel.KObject,
		Children: []*smodel.FormNode{
			{Key: "a", Title: "A", Kind: smodel.KScalar, JSONType: "string"},
			{Key: "b", Title: "B", Kind: smodel.KScalar, JSONType: "string"},
		}}
	m := newListModel(root, tobj(tmem("a", tstr("one")), tmem("b", tstr("two"))), Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	lines := strings.Split(m.View(), "\n")
	want := 2 + titleColMin + 1 // cursor prefix + padded title + separator
	for _, value := range []string{"one", "two"} {
		if got := valueColumn(lines, value); got != want {
			t.Errorf("value %q starts at cell %d, want %d:\n%s", value, got, want, m.View())
		}
	}
}

func TestLongTitlesShowInFullWithoutEllipsis(t *testing.T) {
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.Ascii)
	t.Cleanup(func() { lipgloss.SetColorProfile(old) })

	mid := strings.Repeat("m", 20)
	long := strings.Repeat("l", 60)
	root := &smodel.FormNode{Key: "$", Title: "Root", Kind: smodel.KObject,
		Children: []*smodel.FormNode{
			{Key: "short", Title: "Short", Kind: smodel.KScalar, JSONType: "string"},
			{Key: "mid", Title: mid, Kind: smodel.KScalar, JSONType: "string"},
			{Key: "long", Title: long, Kind: smodel.KScalar, JSONType: "string", Required: true},
		}}
	doc := tobj(
		tmem("short", tstr("one")),
		tmem("mid", tstr("med")),
		tmem("long", tstr("two")),
	)
	m := newListModel(root, doc, Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	lines := strings.Split(m.View(), "\n")
	shortCol := 2 + titleColMax + 1 // clamped column
	if got := valueColumn(lines, "one"); got != shortCol {
		t.Errorf("short value starts at cell %d, want %d:\n%s", got, shortCol, m.View())
	}
	if got := valueColumn(lines, "med"); got != shortCol {
		t.Errorf("mid value starts at cell %d, want %d:\n%s", got, shortCol, m.View())
	}

	// The 60-cell title exceeds the maximum: it is not cut, so its value
	// starts further right instead of aligning with the shorter rows.
	longLine := lineWithValue(lines, "two")
	if got, want := valueColumn(lines, "two"), 2+len(long)+1+1; got != want {
		t.Errorf("long value starts at cell %d, want %d:\n%s", got, want, m.View())
	}
	if strings.Contains(longLine, "…") {
		t.Fatalf("long title must not be ellipsized: %q", longLine)
	}
	if !strings.Contains(longLine, long+"*") {
		t.Fatalf("long required title missing its full text or marker: %q", longLine)
	}
}

func lineWithValue(lines []string, value string) string {
	for _, line := range lines {
		if strings.Contains(line, value) {
			return line
		}
	}
	return ""
}

func valueColumn(lines []string, value string) int {
	line := lineWithValue(lines, value)
	if line == "" {
		return -1
	}
	i := strings.Index(line, value)
	if i < 0 {
		return -1
	}
	return displayWidth(line[:i])
}
