// Package ui renders smodel form trees as sequential huh screens with
// drill-down navigation. Each screen is one independent huh.Form.Run().
package ui

import (
	"fmt"
	"os"
	"strconv"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"

	"json-schema-editor/internal/jsondoc"
	"json-schema-editor/internal/smodel"
)

// Options configures a Run.
type Options struct {
	Accessible bool
	Force      bool
	// Validate checks a config document; nil disables save-time validation.
	Validate func(*jsondoc.Node) []string
	// Save writes the document to its destination. Every save action goes
	// through it (TUI ctrl+s, the Save-and-exit row, the accessible save);
	// nil makes saving impossible.
	Save func(*jsondoc.Node) error
	// Status is a warning line shown on the root screen (e.g. compile
	// failure).
	Status string
	// LoadErrors are the validation messages of the file as loaded; the root
	// menu shows them as a jumpable status row.
	LoadErrors []string
}

// Outcome reports how the session ended.
type Outcome struct {
	// Saved is true when at least one write succeeded during the session.
	Saved bool
	// WithErrors is true when the last write went through --force despite
	// validation errors.
	WithErrors bool
	// Aborted is true when the session ended via ctrl+c/EOF abort rather
	// than a save-and-exit or a clean quit.
	Aborted bool
	// Unchanged is true when the session ended cleanly without writing:
	// the document matched the loaded/last-saved state when the user quit.
	Unchanged bool
	Err       error
}

// result is the internal outcome of a screen.
type result int

const (
	resPop   result = iota // done with this screen, go back
	resSaved               // user saved; unwind everything
	resAbort               // aborted on the root screen; exit 130
)

const helpLine = "↑/↓ move · space toggle · enter select · tab next · esc back · ctrl+c quit"

type app struct {
	model *smodel.FormNode
	doc   *jsondoc.Node
	opts  Options
	// savedWithErrors records a --force save that skipped validation errors.
	savedWithErrors bool
	// saveErr carries a failed Save write out of saveFlow (which must return
	// resSaved to unwind the screen loop).
	saveErr error
	// in wraps stdin in accessible mode for one-line-at-a-time delivery.
	in *lineReader
}

// Run drives the whole interactive session.
func Run(model *smodel.FormNode, doc *jsondoc.Node, opts Options) (out Outcome) {
	// huh's accessible prompts can panic on malformed piped input (a stale
	// value at EOF); degrade to a clean error instead of a stack trace.
	defer func() {
		if r := recover(); r != nil {
			out = Outcome{Err: fmt.Errorf("session failed: %v", r)}
		}
	}()
	a := &app{model: model, doc: doc, opts: opts}
	if !opts.Accessible {
		return runMenu(model, doc, opts)
	}
	if opts.Accessible {
		a.in = newLineReader(os.Stdin)
	}
	var r result
	switch model.Kind {
	case smodel.KObject:
		r = a.section(model, nil, true)
	case smodel.KArray:
		r = a.arrayManager(model, nil, true)
	case smodel.KOneOf:
		r = a.variantRoot(model)
	default: // KScalar / KEnum / KBoolean / KRaw root
		r = a.simpleRoot(model)
	}
	if a.saveErr != nil {
		return Outcome{Err: a.saveErr}
	}
	if r == resSaved {
		return Outcome{Saved: true, WithErrors: a.savedWithErrors}
	}
	return Outcome{Aborted: true}
}

func (a *app) runForm(f *huh.Form) error {
	if a.opts.Accessible {
		f = f.WithAccessible(true).WithInput(a.in)
	}
	return f.WithProgramOptions(tea.WithAltScreen()).Run()
}

// runMenu drives the default menuconfig-style TUI and maps its end state
// to an Outcome.
func runMenu(model *smodel.FormNode, doc *jsondoc.Node, opts Options) (out Outcome) {
	defer func() {
		if r := recover(); r != nil {
			out = Outcome{Err: fmt.Errorf("session failed: %v", r)}
		}
	}()
	p := tea.NewProgram(newListModel(model, doc, opts), tea.WithAltScreen())
	fm, err := p.Run()
	if err != nil {
		return Outcome{Err: err}
	}
	if lm, ok := fm.(*listModel); ok {
		// Aborted stays true even after a mid-session ctrl+s: the abort
		// itself wrote nothing, but main must know a save already landed.
		// A clean root quit is neither an abort nor a write: Unchanged tells
		// main to exit 0 when nothing was saved during the session.
		return Outcome{
			Saved:      lm.saved,
			WithErrors: lm.withErrors,
			Aborted:    lm.aborted,
			Unchanged:  lm.exiting && !lm.saved,
		}
	}
	return Outcome{}
}

// stdinEOF reports that the (accessible) input script is exhausted; further
// prompts would just spin on defaults.
func (a *app) stdinEOF() bool { return a.in != nil && a.in.EOFSeen() }

// eofResult aborts on the root screen and pops otherwise.
func (a *app) eofResult(root bool) (result, bool) {
	if !a.stdinEOF() {
		return resPop, false
	}
	if root {
		return resAbort, true
	}
	return resPop, true
}

// arrayLen returns the current length of the array at path (0 if absent).
func (a *app) arrayLen(path []any) int {
	n, ok := a.doc.Get(path)
	if !ok || n.Array == nil {
		return 0
	}
	return len(*n.Array)
}

// choose runs a single-select screen and returns the chosen key; ok=false
// means the user aborted.
func (a *app) choose(title, desc string, opts []navOpt, def string) (string, bool) {
	var sel string
	hopts := make([]huh.Option[string], 0, len(opts))
	for _, o := range opts {
		hopts = append(hopts, huh.NewOption(o.label, o.key).Selected(o.key == def))
	}
	s := huh.NewSelect[string]().
		Title(title).
		Description(desc).
		Value(&sel).
		Options(hopts...)
	if len(opts) <= 5 {
		s = s.Inline(true)
	}
	form := huh.NewForm(huh.NewGroup(s).Description(helpLine))
	if err := a.runForm(form); err != nil {
		return "", false
	}
	return sel, true
}

type navOpt struct {
	key, label string
}

// pathFor returns a copy of path with key appended.
func pathFor(path []any, key string) []any {
	p := make([]any, len(path), len(path)+1)
	copy(p, path)
	return append(p, key)
}

// pathIdx returns a copy of path with index appended.
func pathIdx(path []any, idx int) []any {
	p := make([]any, len(path), len(path)+1)
	copy(p, path)
	return append(p, idx)
}

func itoa(i int) string { return strconv.Itoa(i) }
