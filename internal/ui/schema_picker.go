package ui

// The schema picker: an interactive search over the SchemaStore catalog,
// opened when no schema is configured and the file name does not uniquely
// determine one. Two modes sharing one filter:
//
//   - full-screen TUI (a bubbletea program mirroring the menu search:
//     type to filter, up/down to move, enter to select, esc to abort)
//   - accessible line protocol (search term, then a numbered list)

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/wertasy/json-schema-editor/internal/catalog"
)

// PickOptions configures PickSchema.
type PickOptions struct {
	Accessible bool      // line protocol; false = full-screen TUI
	Filename   string    // config file name shown in the header
	Query      string    // initial search term (the file name stem)
	In         io.Reader // accessible stdin override (tests); nil = os.Stdin
	Out        io.Writer // accessible stdout override (tests); nil = os.Stdout
}

// PickSchema lets the user choose one catalog entry; ok=false when the user
// aborts or the input is exhausted.
func PickSchema(entries []catalog.Entry, opts PickOptions) (catalog.Entry, bool) {
	if opts.Accessible {
		return pickAccessible(entries, opts)
	}
	return pickTUI(entries, opts)
}

// filterEntries returns the indexes of entries whose Name or Description
// contains query (case-insensitive); an empty query selects everything.
func filterEntries(entries []catalog.Entry, query string) []int {
	q := strings.ToLower(query)
	idx := make([]int, 0, len(entries))
	for i, e := range entries {
		if q == "" ||
			strings.Contains(strings.ToLower(e.Name), q) ||
			strings.Contains(strings.ToLower(e.Description), q) {
			idx = append(idx, i)
		}
	}
	return idx
}

// --- full-screen TUI ------------------------------------------------------

type pickerModel struct {
	entries  []catalog.Entry
	filename string
	ti       textinput.Model
	filtered []int // indexes into entries
	cursor   int
	offset   int
	width    int
	height   int
	chosen   *catalog.Entry
	aborted  bool
	stale    bool // the preset query is untouched: the first text key replaces it
}

func newPickerModel(entries []catalog.Entry, opts PickOptions) pickerModel {
	ti := textinput.New()
	ti.Prompt = ""
	ti.Focus()
	ti.SetValue(opts.Query)
	ti.CursorEnd()
	m := pickerModel{
		entries:  entries,
		filename: opts.Filename,
		ti:       ti,
		width:    80,
		height:   24,
		stale:    opts.Query != "",
	}
	m.refilter()
	return m
}

// refilter reapplies the search term; the cursor restarts at the top, like
// the menu search.
func (m *pickerModel) refilter() {
	m.filtered = filterEntries(m.entries, m.ti.Value())
	m.cursor = 0
	m.offset = 0
}

// modifiesText reports whether the key changes the search input (as opposed
// to moving around or committing).
func modifiesText(msg tea.KeyMsg) bool {
	switch msg.Type {
	case tea.KeyRunes, tea.KeySpace, tea.KeyBackspace, tea.KeyDelete:
		return true
	}
	return false
}

func (m pickerModel) Init() tea.Cmd { return nil }

func (m pickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	changed := false
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.aborted = true
			return m, tea.Quit
		case "up", "ctrl+k", "shift+tab":
			m.cursor = wrapCursor(m.cursor, -1, len(m.filtered))
			return m, nil
		case "down", "ctrl+j", "tab":
			m.cursor = wrapCursor(m.cursor, 1, len(m.filtered))
			return m, nil
		case "enter":
			if len(m.filtered) > 0 {
				e := m.entries[m.filtered[m.cursor]]
				m.chosen = &e
				return m, tea.Quit
			}
			return m, nil
		}
		if m.stale && modifiesText(msg) {
			// The preset stem behaves like selected text: typing or editing
			// starts a fresh search over it instead of appending to it.
			m.stale = false
			m.ti.SetValue("")
			changed = true
		}
	}
	var cmd tea.Cmd
	before := m.ti.Value()
	m.ti, cmd = m.ti.Update(msg)
	// Only a query change re-filters (and resets the cursor). Other messages
	// — cursor blink ticks, key presses that merely move inside the input —
	// must leave the selection where the user put it.
	if changed || before != m.ti.Value() {
		m.refilter()
	}
	return m, cmd
}

func (m pickerModel) View() string {
	var b strings.Builder
	b.WriteString(mTitle.Render("Select a schema") + "\n")
	b.WriteString(mStatus.Render(fmt.Sprintf("for %s — %d of %d",
		m.filename, len(m.filtered), len(m.entries))) + "\n")
	b.WriteString(mHelp.Render("search: ") + m.ti.View() + "\n")
	height := m.height - 4 // title, status, search, help
	if height < 1 {
		height = 1
	}
	// cursor-anchored scrolling window (same offset rules as writeRows)
	if m.offset > m.cursor {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+height {
		m.offset = m.cursor - height + 1
	}
	if maxOff := len(m.filtered) - height; m.offset > maxOff {
		m.offset = maxOff
	}
	if m.offset < 0 {
		m.offset = 0
	}
	for i := m.offset; i < len(m.filtered) && i < m.offset+height; i++ {
		e := m.entries[m.filtered[i]]
		prefix := cursorPrefix(i == m.cursor)
		avail := max(m.width-2, 0)
		name := clipRunes(e.Name, avail)
		desc := ""
		if rem := avail - len([]rune(name)) - 2; rem > 0 {
			desc = clipRunes(e.Description, rem)
		}
		sep := ""
		if desc != "" {
			sep = "  "
		}
		if i == m.cursor {
			b.WriteString(mSel.Render(prefix + name + sep + desc))
		} else {
			b.WriteString(prefix + name + sep + mCrumb.Render(desc))
		}
		b.WriteByte('\n')
	}
	// The help stays on the terminal's last line; blank rows fill the gap
	// under a short result list.
	lines := strings.Count(b.String(), "\n")
	for lines < m.height-1 {
		b.WriteByte('\n')
		lines++
	}
	b.WriteString(clipDisplay(mHelp.Render("type to search · ↑/↓ or tab/shift+tab move · enter select · esc cancel"), m.width-1))
	return b.String()
}

func pickTUI(entries []catalog.Entry, opts PickOptions) (catalog.Entry, bool) {
	out, err := tea.NewProgram(newPickerModel(entries, opts), tea.WithAltScreen()).Run()
	if err != nil {
		return catalog.Entry{}, false
	}
	pm, ok := out.(pickerModel)
	if !ok || pm.chosen == nil {
		return catalog.Entry{}, false
	}
	return *pm.chosen, true
}

// --- accessible line protocol ----------------------------------------------

// pickAccessible drives the picker as line prompts over In/Out:
//
//	Select a schema for <Filename>
//	? Schema search [<Query>]: <empty line = keep the current query>
//	  1) <Name> — <description>
//	  …  (at most 15 rows; a longer list ends with "… N more — refine the search",
//	      an empty result prints "  no matches" and returns to the search prompt)
//	? Number (0 = new search, enter = abort): <row number>
//
// An invalid or out-of-range number re-issues the number prompt; an empty
// line there aborts. EOF at any prompt aborts, so an exhausted script
// cannot spin.
func pickAccessible(entries []catalog.Entry, opts PickOptions) (catalog.Entry, bool) {
	in := opts.In
	if in == nil {
		in = os.Stdin
	}
	out := opts.Out
	if out == nil {
		out = os.Stdout
	}
	br := bufio.NewReader(newLineReader(in))
	query := opts.Query
	fmt.Fprintf(out, "Select a schema for %s\n", opts.Filename)
search:
	for {
		fmt.Fprintf(out, "? Schema search [%s]: ", query)
		line, ok := pickLine(br)
		if !ok {
			return catalog.Entry{}, false
		}
		if q := strings.TrimSpace(line); q != "" {
			query = q
		}
		idx := filterEntries(entries, query)
		if len(idx) == 0 {
			fmt.Fprintln(out, "  no matches")
			continue search
		}
		shown := min(len(idx), 15)
		for i := range shown {
			e := entries[idx[i]]
			if e.Description != "" {
				fmt.Fprintf(out, "  %d) %s — %s\n", i+1, e.Name, e.Description)
			} else {
				fmt.Fprintf(out, "  %d) %s\n", i+1, e.Name)
			}
		}
		if more := len(idx) - shown; more > 0 {
			fmt.Fprintf(out, "  … %d more — refine the search\n", more)
		}
		for {
			fmt.Fprint(out, "? Number (0 = new search, enter = abort): ")
			line, ok = pickLine(br)
			if !ok {
				return catalog.Entry{}, false
			}
			if strings.TrimSpace(line) == "" { // enter = abort
				return catalog.Entry{}, false
			}
			n, err := strconv.Atoi(strings.TrimSpace(line))
			switch {
			case err != nil || n < 0 || n > len(idx):
				fmt.Fprintln(out, "! invalid number") // re-issue the prompt
			case n == 0: // new search
				continue search
			default:
				return entries[idx[n-1]], true
			}
		}
	}
}

// pickLine reads one line; ok=false on EOF. lineReader serves one line per
// Read, so sequential prompts stay in lockstep with piped answer scripts.
func pickLine(br *bufio.Reader) (string, bool) {
	s, err := br.ReadString('\n')
	if s == "" && err != nil {
		return "", false
	}
	return strings.TrimRight(s, "\r\n"), true
}
