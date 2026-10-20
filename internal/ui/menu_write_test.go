package ui

// W write-to-path: the input row at the menu's end, the copy write, and the
// validation gate.

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/wertasy/json-schema-editor/internal/jsondoc"
)

type writeRecorder struct {
	paths []string
	err   error
}

func (w *writeRecorder) write(path string, _ *jsondoc.Node) error {
	if w.err != nil {
		return w.err
	}
	w.paths = append(w.paths, path)
	return nil
}

func newWriteModel(t *testing.T, rec *writeRecorder, saves *[]bool) *listModel {
	t.Helper()
	save := func(*jsondoc.Node) error {
		if saves != nil {
			*saves = append(*saves, true)
		}
		return nil
	}
	m := newListModel(testModel(), testDoc(), Options{
		Path:    "/origin/path/to/file.ext",
		Save:    save,
		WriteTo: rec.write,
	})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return m
}

func TestWriteToOpensPrefilledRow(t *testing.T) {
	rec := &writeRecorder{}
	m := newWriteModel(t, rec, nil)
	press(m, "W")
	if m.write == nil {
		t.Fatal("W must open the write-to input")
	}
	if got := m.write.ti.Value(); got != "/origin/path/to/file.ext" {
		t.Fatalf("prefill = %q, want the session path", got)
	}
	v := m.View()
	if !strings.Contains(v, "Write to") || !strings.Contains(v, "/origin/path/to/file.ext") {
		t.Fatalf("menu must show the Write to row:\n%s", v)
	}
	// the input sits at the menu's end: below the last action row
	if strings.Index(v, "Edit raw JSON") > strings.LastIndex(v, "Write to") {
		t.Fatalf("Write to row must come after the action rows:\n%s", v)
	}

	// typing edits the path; esc cancels without writing
	press(m, "ctrl+u")
	press(m, "w", "e", "i")
	if got := m.write.ti.Value(); got != "wei" {
		t.Fatalf("typed path = %q", got)
	}
	press(m, "esc")
	if m.write != nil {
		t.Fatal("esc must close the input")
	}
	if len(rec.paths) != 0 {
		t.Fatalf("esc must not write, wrote %v", rec.paths)
	}

}

// ctrl+s / w save the session file and the bottom bar shows the write
// target instead of a bare "Saved".
func TestSaveNoticeShowsTarget(t *testing.T) {
	rec := &writeRecorder{}
	var saves []bool
	m := newWriteModel(t, rec, &saves)
	press(m, "w")
	if m.write != nil {
		t.Fatal("w must save directly, not open the write-to input")
	}
	if len(saves) != 1 {
		t.Fatalf("saves = %v, want 1 save", saves)
	}
	if m.notice != "Write to /origin/path/to/file.ext" {
		t.Fatalf("notice = %q, want the write target", m.notice)
	}
	if !m.saved {
		t.Fatal("the session must be marked saved")
	}
	press(m, "ctrl+s")
	if len(saves) != 2 || m.notice != "Write to /origin/path/to/file.ext" {
		t.Fatalf("ctrl+s: saves=%d notice=%q", len(saves), m.notice)
	}
}

// enter on the unedited prefilled path (the session file itself) is a
// plain save: opts.Save runs, the baseline refreshes.
func TestWriteToOwnPathSavesSession(t *testing.T) {
	rec := &writeRecorder{}
	var saves []bool
	m := newWriteModel(t, rec, &saves)
	m.doc.Set([]any{"name"}, tstr("changed")) // dirty vs the baseline
	press(m, "W")
	press(m, "enter") // path untouched: the session file itself
	if m.write != nil {
		t.Fatal("enter must close the input")
	}
	if len(saves) != 1 {
		t.Fatalf("own path must save the session file, saves=%d", len(saves))
	}
	if len(rec.paths) != 0 {
		t.Fatalf("own-path write must not go through WriteTo, wrote %v", rec.paths)
	}
	if m.notice != "Write to /origin/path/to/file.ext" {
		t.Fatalf("notice = %q", m.notice)
	}
	if !nodesEqual(m.baseline, m.doc) {
		t.Fatal("saving the session file must refresh the change-mark baseline")
	}
	if !m.saved {
		t.Fatal("the session must be marked saved")
	}
}

func TestWriteToCommitsEditedPath(t *testing.T) {
	rec := &writeRecorder{}
	var saves []bool
	m := newWriteModel(t, rec, &saves)
	m.doc.Set([]any{"name"}, tstr("changed")) // dirty vs the baseline
	pre := m.baseline.Clone()
	press(m, "W")
	press(m, "ctrl+u")
	press(m, "a", "/", "b", ".", "j", "s", "o", "n")
	press(m, "enter")
	if m.write != nil {
		t.Fatal("enter must close the input")
	}
	if len(rec.paths) != 1 || rec.paths[0] != "a/b.json" {
		t.Fatalf("wrote %v, want [a/b.json]", rec.paths)
	}
	if m.notice != "Wrote to a/b.json" {
		t.Fatalf("notice = %q", m.notice)
	}
	if len(saves) != 0 {
		t.Fatal("W must not touch the session's own save target")
	}
	// W is a copy: the change markers keep comparing against the original
	// baseline and the session stays unsaved
	if !nodesEqual(m.baseline, pre) {
		t.Fatal("W must not refresh the change-mark baseline")
	}
	if m.saved {
		t.Fatal("W must not mark the session as saved")
	}

	// empty path is refused and keeps the input open
	press(m, "W")
	press(m, "ctrl+u")
	press(m, "enter")
	if m.write == nil || m.write.err != "path required" {
		t.Fatalf("empty path: write=%v err=%q", m.write, m.write.err)
	}
}

func TestWriteToValidationGatesAndForces(t *testing.T) {
	rec := &writeRecorder{}
	m := newListModel(testModel(), testDoc(), Options{
		Path:     "/origin/f.json",
		WriteTo:  rec.write,
		Validate: func(*jsondoc.Node) []string { return []string{"x is required"} },
		Force:    true,
	})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	press(m, "W")
	press(m, "ctrl+u") // point the row at a copy path
	press(m, "c", ".", "j", "s", "o", "n")
	press(m, "enter") // validation errors open the overlay, nothing written
	if m.errs == nil {
		t.Fatal("validation errors must open the errors overlay")
	}
	if m.errs.target != "c.json" {
		t.Fatalf("errs target = %q, want c.json", m.errs.target)
	}
	if len(rec.paths) != 0 {
		t.Fatalf("gated W must not write, wrote %v", rec.paths)
	}
	press(m, "y") // --force: writes to the W path, not the session's file
	if len(rec.paths) != 1 || rec.paths[0] != "c.json" {
		t.Fatalf("forced W wrote %v, want the W path", rec.paths)
	}
	if m.saved {
		t.Fatal("a forced W copy must not mark the session as saved")
	}
}

func TestWriteToErrorIsReported(t *testing.T) {
	rec := &writeRecorder{err: errors.New("disk full")}
	m := newWriteModel(t, rec, nil)
	press(m, "W")
	press(m, "ctrl+u")
	press(m, "c", ".", "j", "s", "o", "n")
	press(m, "enter")
	if m.write != nil {
		t.Fatal("enter must close the input even on failure")
	}
	if !strings.Contains(m.notice, "write failed: disk full") {
		t.Fatalf("notice = %q", m.notice)
	}
}

func TestWriteToUnavailable(t *testing.T) {
	// without WriteTo, w keeps its plain save meaning (no input row)
	saves := 0
	m := newListModel(testModel(), testDoc(), Options{
		Save: func(*jsondoc.Node) error { saves++; return nil },
	})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	press(m, "w")
	if m.write != nil {
		t.Fatal("without WriteTo the input must not open")
	}
	if saves != 1 || m.notice != "Saved" {
		t.Fatalf("w fallback save: saves=%d notice=%q", saves, m.notice)
	}
}
