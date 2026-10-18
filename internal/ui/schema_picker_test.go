package ui

import (
	"bytes"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"json-schema-editor/internal/catalog"
)

func pickEntries() []catalog.Entry {
	return []catalog.Entry{
		{Name: "Server Config", Description: "web server settings", URL: "https://x/server.json"},
		{Name: "Kube Deployment", Description: "kubernetes workload", URL: "https://x/kube.json"},
		{Name: "Plain", Description: "", URL: "https://x/plain.json"},
	}
}

func TestFilterEntries(t *testing.T) {
	entries := pickEntries()

	all := filterEntries(entries, "")
	if len(all) != 3 {
		t.Fatalf("empty query = %v, want all", all)
	}

	if got := filterEntries(entries, "server"); len(got) != 1 || entries[got[0]].Name != "Server Config" {
		t.Fatalf("name hit: %v", got)
	}
	if got := filterEntries(entries, "KUBERNETES"); len(got) != 1 || entries[got[0]].Name != "Kube Deployment" {
		t.Fatalf("description hit is case-sensitive: %v", got)
	}
	if got := filterEntries(entries, "zzz"); len(got) != 0 {
		t.Fatalf("no-hit query: %v", got)
	}
}

func pkey(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "ctrl+j":
		return tea.KeyMsg{Type: tea.KeyCtrlJ}
	case "ctrl+k":
		return tea.KeyMsg{Type: tea.KeyCtrlK}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

func update(m pickerModel, keys ...string) pickerModel {
	for _, k := range keys {
		nm, _ := m.Update(pkey(k))
		m = nm.(pickerModel)
	}
	return m
}

func TestPickerTUIEnterSelects(t *testing.T) {
	m := update(newPickerModel(pickEntries(), PickOptions{Filename: "mystery.json", Query: "server"}), "enter")
	if m.chosen == nil || m.chosen.URL != "https://x/server.json" {
		t.Fatalf("enter chose %#v", m.chosen)
	}
	if m.aborted {
		t.Fatal("selection flagged as abort")
	}
}

func TestPickerTUIEscAborts(t *testing.T) {
	m := update(newPickerModel(pickEntries(), PickOptions{}), "esc")
	if m.aborted != true || m.chosen != nil {
		t.Fatalf("esc: chosen=%#v aborted=%v", m.chosen, m.aborted)
	}
	m = update(newPickerModel(pickEntries(), PickOptions{}), "ctrl+c")
	if m.aborted != true {
		t.Fatal("ctrl+c did not abort")
	}
}

func TestPickerTUICursorWraps(t *testing.T) {
	m := newPickerModel(pickEntries(), PickOptions{})
	if m.cursor != 0 {
		t.Fatalf("initial cursor = %d", m.cursor)
	}
	m = update(m, "up")
	if m.cursor != len(m.filtered)-1 {
		t.Fatalf("up from top = %d, want wrap to %d", m.cursor, len(m.filtered)-1)
	}
	m = update(m, "down")
	if m.cursor != 0 {
		t.Fatalf("down from bottom = %d, want wrap to 0", m.cursor)
	}
	m = update(m, "down", "down")
	if m.cursor != 2 {
		t.Fatalf("two downs = %d, want 2", m.cursor)
	}
	// ctrl+j/ctrl+k move like j/k-style list navigation and wrap
	m = update(m, "ctrl+j")
	if m.cursor != 0 {
		t.Fatalf("ctrl+j from bottom = %d, want wrap to 0", m.cursor)
	}
	m = update(m, "ctrl+k")
	if m.cursor != len(m.filtered)-1 {
		t.Fatalf("ctrl+k from top = %d, want wrap to %d", m.cursor, len(m.filtered)-1)
	}
	if m.ti.Value() != "" {
		t.Fatalf("ctrl+j/k must not edit the query, got %q", m.ti.Value())
	}
}

func TestPickerTUICursorSurvivesNonKeyMessages(t *testing.T) {
	m := update(newPickerModel(pickEntries(), PickOptions{}), "e") // two matches
	if len(m.filtered) != 2 {
		t.Fatalf("filtered = %v, want two entries", m.filtered)
	}
	m = update(m, "down")
	if m.cursor != 1 {
		t.Fatalf("cursor = %d, want 1", m.cursor)
	}
	// A cursor-blink tick (or any other non-key message) must not re-filter
	// and snap the selection back to the top.
	nm, _ := m.Update(struct{}{})
	m = nm.(pickerModel)
	if m.cursor != 1 {
		t.Fatalf("non-key message reset the cursor to %d", m.cursor)
	}
	// Keys that only move inside the input likewise keep the selection.
	m = update(m, "left", "right")
	if m.cursor != 1 {
		t.Fatalf("input cursor keys reset the selection to %d", m.cursor)
	}
	// Editing the query still restarts at the top.
	m = update(m, "backspace")
	if m.cursor != 0 || len(m.filtered) != 3 {
		t.Fatalf("query change must reset the cursor, got cursor=%d filtered=%v", m.cursor, m.filtered)
	}
}

func TestPickerTUIRefilter(t *testing.T) {
	m := update(newPickerModel(pickEntries(), PickOptions{}), "k", "u")
	if len(m.filtered) != 1 || m.ti.Value() != "ku" {
		t.Fatalf("typed filter: value=%q filtered=%v", m.ti.Value(), m.filtered)
	}
	if m.cursor != 0 {
		t.Fatalf("cursor not reset on refilter: %d", m.cursor)
	}
	// narrowing to nothing leaves the model consistent; enter is ignored
	m = update(m, "z")
	if len(m.filtered) != 0 {
		t.Fatalf("expected empty result: %v", m.filtered)
	}
	m = update(m, "enter")
	if m.chosen != nil || m.aborted {
		t.Fatalf("enter on empty list: chosen=%#v aborted=%v", m.chosen, m.aborted)
	}
}

func TestPickerTUIPresetReplacedOnFirstKey(t *testing.T) {
	m := newPickerModel(pickEntries(), PickOptions{Query: "mystery"})
	if len(m.filtered) != 0 {
		t.Fatalf("preset should filter to nothing: %v", m.filtered)
	}
	// typing replaces the stale preset instead of appending to it
	m = update(m, "s", "e", "r", "v")
	if m.ti.Value() != "serv" {
		t.Fatalf("value = %q, want %q", m.ti.Value(), "serv")
	}
	if len(m.filtered) != 1 || pickEntries()[m.filtered[0]].Name != "Server Config" {
		t.Fatalf("replaced query did not match: %v", m.filtered)
	}
	// once replaced, further keys edit normally (no second clear)
	m = update(m, "backspace", "t")
	if m.ti.Value() != "sert" {
		t.Fatalf("value = %q, want %q", m.ti.Value(), "sert")
	}
}

func TestPickerTUITabMovesCursor(t *testing.T) {
	m := newPickerModel(pickEntries(), PickOptions{})
	m = update(m, "tab")
	if m.cursor != 1 {
		t.Fatalf("tab cursor = %d, want 1", m.cursor)
	}
	m = update(m, "tab")
	if m.cursor != 2 {
		t.Fatalf("second tab cursor = %d, want 2", m.cursor)
	}
	m = update(m, "tab")
	if m.cursor != 0 {
		t.Fatalf("tab wrap cursor = %d, want 0", m.cursor)
	}
	m = update(m, "shift+tab")
	if m.cursor != 2 {
		t.Fatalf("shift+tab wrap cursor = %d, want 2", m.cursor)
	}
	m = update(m, "shift+tab")
	if m.cursor != 1 {
		t.Fatalf("shift+tab cursor = %d, want 1", m.cursor)
	}
	if m.ti.Value() != "" {
		t.Fatalf("tab keys must not edit the query, got %q", m.ti.Value())
	}
}

func TestPickerTUITabWithNoCandidates(t *testing.T) {
	m := newPickerModel(pickEntries(), PickOptions{Query: "zzz"})
	m = update(m, "tab", "shift+tab")
	if m.cursor != 0 || m.ti.Value() != "zzz" {
		t.Fatalf("tab with no matches: cursor=%d value=%q", m.cursor, m.ti.Value())
	}
}

func TestPickerViewInputAboveResults(t *testing.T) {
	m := newPickerModel(pickEntries(), PickOptions{Filename: "new.yml"})
	m.width, m.height = 100, 30
	lines := strings.Split(ansi.Strip(m.View()), "\n")
	if len(lines) != 30 {
		t.Fatalf("view has %d lines, want 30:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	if !strings.Contains(lines[2], "search:") {
		t.Fatalf("search input not above the results:\n%s", strings.Join(lines, "\n"))
	}
	if !strings.Contains(lines[29], "enter select") {
		t.Fatalf("help line not last:\n%s", strings.Join(lines, "\n"))
	}
	found := false
	for i := 3; i < 29; i++ {
		if strings.Contains(lines[i], "Server Config") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("result rows not below the input:\n%s", strings.Join(lines, "\n"))
	}
}

func TestPickerTUIWindowResize(t *testing.T) {
	m := newPickerModel(pickEntries(), PickOptions{})
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = nm.(pickerModel)
	if m.width != 100 || m.height != 30 {
		t.Fatalf("size = %dx%d", m.width, m.height)
	}
}

func TestPickerViewKeepsGapBeforeDescription(t *testing.T) {
	m := newPickerModel(pickEntries(), PickOptions{Filename: "new.yml"})
	m.width, m.height = 100, 30
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "> Server Config  web server settings") {
		t.Fatalf("selected row name and description not separated:\n%s", view)
	}
	if !strings.Contains(view, "  Kube Deployment  kubernetes workload") {
		t.Fatalf("row name and description not separated:\n%s", view)
	}
	if strings.Contains(view, "Deploymentkubernetes") {
		t.Fatalf("description glued to the name:\n%s", view)
	}
}

func TestPickAccessibleSelects(t *testing.T) {
	var out bytes.Buffer
	e, ok := PickSchema(pickEntries(), PickOptions{
		Accessible: true,
		Filename:   "mystery.json",
		Query:      "mystery",
		In:         strings.NewReader("server\n1\n"),
		Out:        &out,
	})
	if !ok {
		t.Fatalf("not selected, output:\n%s", out.String())
	}
	if e.URL != "https://x/server.json" {
		t.Fatalf("chose %#v", e)
	}
	s := out.String()
	for _, want := range []string{
		"Select a schema for mystery.json",
		"? Schema search [mystery]: ",
		"  1) Server Config — web server settings",
		"? Number (0 = new search, enter = abort): ",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("output missing %q:\n%s", want, s)
		}
	}
}

func TestPickAccessibleAbortOnEmptyNumber(t *testing.T) {
	var out bytes.Buffer
	e, ok := PickSchema(pickEntries(), PickOptions{
		Accessible: true,
		Filename:   "m.json",
		Query:      "server",
		In:         strings.NewReader("\n"),
		Out:        &out,
	})
	if ok || e.URL != "" {
		t.Fatal("empty number line must abort")
	}
}

func TestPickAccessibleAbortOnEOF(t *testing.T) {
	var out bytes.Buffer
	// default query "mystery" matches nothing; both lines keep the default,
	// then EOF must stop the loop instead of spinning.
	e, ok := PickSchema(pickEntries(), PickOptions{
		Accessible: true,
		Filename:   "mystery.json",
		Query:      "mystery",
		In:         strings.NewReader("\n\n"),
		Out:        &out,
	})
	if ok || e.URL != "" {
		t.Fatal("EOF must abort")
	}
	if strings.Count(out.String(), "  no matches") != 2 {
		t.Fatalf("no matches rounds = %d, want 2:\n%s",
			strings.Count(out.String(), "  no matches"), out.String())
	}
	// bare EOF at the first prompt
	if _, ok := PickSchema(pickEntries(), PickOptions{Accessible: true, In: strings.NewReader(""), Out: &out}); ok {
		t.Fatal("immediate EOF must abort")
	}
}

func TestPickAccessibleInvalidNumber(t *testing.T) {
	var out bytes.Buffer
	e, ok := PickSchema(pickEntries(), PickOptions{
		Accessible: true,
		Filename:   "m.json",
		Query:      "mystery",
		In:         strings.NewReader("server\nx\n99\n2\n1\n"),
		Out:        &out,
	})
	if !ok {
		t.Fatalf("not selected, output:\n%s", out.String())
	}
	// "server" matches Server Config only, so 99 and 2 are out of range;
	// each invalid line re-prompts until the single valid number wins.
	if e.URL != "https://x/server.json" {
		t.Fatalf("chose %#v", e)
	}
	if got := strings.Count(out.String(), "! invalid number"); got != 3 {
		t.Fatalf("invalid-number lines = %d, want 3:\n%s", got, out.String())
	}
}

func TestPickAccessibleZeroResearches(t *testing.T) {
	var out bytes.Buffer
	e, ok := PickSchema(pickEntries(), PickOptions{
		Accessible: true,
		Filename:   "m.json",
		Query:      "server",
		In:         strings.NewReader("0\nkube\n1\n"),
		Out:        &out,
	})
	if !ok || e.URL != "https://x/kube.json" {
		t.Fatalf("0 did not return to search: ok=%v e=%#v\n%s", ok, e, out.String())
	}
}

func TestPickAccessibleTruncatesTo15(t *testing.T) {
	entries := make([]catalog.Entry, 20)
	for i := range entries {
		entries[i] = catalog.Entry{Name: "S", Description: "", URL: "https://x/" + entries[i].Name}
		entries[i].Name = string(rune('A'+i)) + "-item"
		entries[i].URL = "https://x/" + entries[i].Name
	}
	var out bytes.Buffer
	e, ok := PickSchema(entries, PickOptions{
		Accessible: true,
		Filename:   "m.json",
		Query:      "",
		In:         strings.NewReader("\n15\n"),
		Out:        &out,
	})
	if !ok || e.Name != "O-item" {
		t.Fatalf("chose %#v ok=%v\n%s", e, ok, out.String())
	}
	s := out.String()
	if !strings.Contains(s, "  … 5 more — refine the search") {
		t.Fatalf("missing truncation line:\n%s", s)
	}
	if strings.Contains(s, "  16) ") {
		t.Fatalf("more than 15 rows listed:\n%s", s)
	}
}
