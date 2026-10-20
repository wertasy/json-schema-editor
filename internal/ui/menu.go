package ui

// The menuconfig-style default TUI: one persistent bubbletea program
// (breadcrumb + scrolling list + help bar) over the smodel form tree.
// Editing writes through to the document immediately; there is no
// per-screen form submit. huh remains only in the --accessible path.

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/wertasy/json-schema-editor/internal/icons"
	"github.com/wertasy/json-schema-editor/internal/jsondoc"
	"github.com/wertasy/json-schema-editor/internal/smodel"
)

const menuHelp = "↑/↓ move · enter edit/open · esc back · ctrl+s/w save · / search · ? help"

// helpEntry is one row of the "?" key reference. keys is the left column;
// empty means a continuation line aligned under the descriptions.
type helpEntry struct {
	keys string
	desc string
}

// helpEntries is the full key reference shown by the "?" overlay. The render
// pads the key column instead of writing literal tabs: tabs are expanded by
// the terminal without the renderer knowing their width, which breaks the
// cell accounting used for clipping and the end-of-line erase.
var helpEntries = []helpEntry{
	{"up / down / j / k", "move (wraps at both ends; ctrl+j/k in search and issues)"},
	{"n / N / ctrl+j / ctrl+k", "next / previous key with a non-null value"},
	{"y / Y", "copy the \"key\": value pair / the value (JSON or YAML per document)"},
	{"x / X", "cut the \"key\": value pair / the value — y/Y plus delete, batch on marked rows"},
	{"m", "mark key-value rows: y copies, d deletes the marked ones (m toggles)"},
	{"v", "visual select: j/k (or arrows) mark rows between start and cursor; v/esc stops"},
	{"f / F", "quick move: type a character to jump to the next/previous key starting with it"},
	{"p / P", "smart paste clipboard JSON (YAML docs: YAML too) — p: this menu, P: the row (a single value replaces its value)"},
	{"z", "show/hide fields with no value (null/unset)"},
	{"[N] gg / G", "jump to first / last row; N gg in array menus: item N (Home/End too)"},
	{"ctrl+o / ctrl+i (tab)", "back / forward in the navigation history (submenus, jumps; esc pops count too)"},
	{"ga / gr", "jump to the row's anchor / its other uses — references and << merges (esc returns)"},
	{"enter / right / l", "edit field · open submenu"},
	{"tab / shift+tab", "complete next / previous candidate (ctrl+n / ctrl+p)"},
	{"left / h", "back one level"},
	{"esc / q", "cancel / back — q aliases esc outside inputs; a pending g combo clears first"},
	{"", "at the root: quit, asking to save when the doc changed"},
	{"space", "toggle boolean / enum member"},
	{"d / delete", "delete the selected pair (required merge keys refused)"},
	{"a / A", "add a schema-less key before / after the cursor"},
	{"e", "edit the row's value in $EDITOR (multiline strings always open here)"},
	{"E", "expand: materialize the referenced value (cursor row or menu) into a copy"},
	{"&", "anchor: set/rename the field's YAML anchor (empty removes; unreferenced only)"},
	{"*", "reference: point the field at an anchor (tab completes; empty removes)"},
	{"J / K", "move the item under the cursor down / up"},
	{"ctrl+t", "switch the variant type of a oneOf value"},
	{"u / ctrl+r", "undo / redo"},
	{"ctrl+s / w", "save (stay in the session)"},
	{"/", "search below the current path"},
	{"r", "search & replace string values"},
	{"alt+x", "regex toggle (default on): match the query as a regular expression ($1 groups in replace)"},
	{"@ / .", "record / play a macro"},
	{"?", "open/close this help"},
	{"ctrl+c", "quit without saving (exit 130)"},
	{"<", "merge keys: merge anchors (space/comma separated; tab completes; empty clears)"},
}

const overlayHelp = "enter select · esc cancel"

// maxMacroKeys caps a single recorded macro; reaching it stops recording and
// keeps the keys captured so far.
const maxMacroKeys = 500

var (
	mTitle  = lipgloss.NewStyle().Bold(true)
	mCrumb  = lipgloss.NewStyle().Faint(true)
	mStatus = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	mStar   = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	mErr    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	// mMark brightens the selection red: same hue family as mErr, one step
	// brighter so marked rows never read as validation errors.
	mMark = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	// mBold marks the key of a row the cursor sits on when that row is
	// selected: the tie to the selection without recoloring it.
	mBold = lipgloss.NewStyle().Bold(true)
	mWarn = lipgloss.NewStyle().Foreground(lipgloss.Color("3")) // unknown-key warnings: yellow, distinct from red errors
	mTail = lipgloss.NewStyle().Faint(true)
	mHelp = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "245", Dark: "248"})
	// schema descriptions (the row comment above the hint bar) are metadata,
	// not keys to press: violet-gray keeps them apart from the neutral gray
	// hint bar without colliding with any value color
	mDesc = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "97", Dark: "141"})
	mDim  = lipgloss.NewStyle().Faint(true)

	// panel chrome and value colors (adaptive: legible on light and dark)
	mAccent = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "27", Dark: "39"})
	// mAnchor colors YAML anchor names (&name): a violet apart from the
	// accent blue references (*name) use, so definitions and references
	// read differently at a glance.
	mAnchor  = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "126", Dark: "177"})
	mSel     = lipgloss.NewStyle().Bold(true).Background(lipgloss.AdaptiveColor{Light: "250", Dark: "236"})
	mValStr  = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "28", Dark: "114"})
	mValNum  = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "25", Dark: "51"})
	mValBool = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "127", Dark: "212"})
	mValEnum = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "166", Dark: "215"})
	mValObj  = lipgloss.NewStyle().Bold(true)
	// Schema-default previews: brighter than the hint bar's gray so they read
	// as values, while staying visually distinct from explicitly set values.
	mValDefault = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "251", Dark: "253"})
	mDiff       = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "161", Dark: "204"}) // changed values
)

// editState is an in-place scalar input on one field row.
type editState struct {
	fn     *smodel.FormNode
	path   []any
	ti     textinput.Model
	err    string
	isNew  bool             // editing a not-yet-appended array item; esc writes nothing
	cands  []string         // existing same-key values in the document (fixed at open)
	sugs   []string         // cands filtered by the current input prefix
	sugIdx int              // tab cycle position (-1 = none)
	oneOf  *smodel.FormNode // set for scalar variant edits: ctrl+t type switch
}

// addState is the key-name input overlay for a: add a schema-less
// key-value member under the current object menu.
type addState struct {
	path []any            // object the new member goes into
	node *smodel.FormNode // the menu's node; its Children are the known keys
	idx  int              // member insert position captured from the cursor (a/A)
	ti   textinput.Model
	err  string
}

// selState is the full-screen enum option picker.
type selState struct {
	fn     *smodel.FormNode
	path   []any
	opts   []smodel.EnumOpt
	cursor int
	oneOf  *smodel.FormNode // set for enum variant editors: ctrl+t type switch
}

// textState is the built-in raw textarea overlay for schema-defined fields
// that cannot be form-edited; its syntax follows Options.Raw (JSON or YAML).
// Schema-less scalar values edit in place; unset and container values use
// the external editor instead.
type textState struct {
	path []any
	fn   *smodel.FormNode // schema field edited as raw text
	ta   textarea.Model
	err  string
}

// issuesState is the load-time validation issues overlay: each entry can jump
// to the offending field.
type issuesState struct {
	items  []issue
	cursor int
}

// refPickState is the gr picker: the references to one anchor, enter jumps
// to the highlighted one (esc/q closes without moving).
type refPickState struct {
	name   string  // anchor name shown in the title
	paths  [][]any // candidate locations in document order
	cursor int
}

// issue is one validation message with its parsed instance location (nil when
// the message carries none).
type issue struct {
	msg  string
	path []any
	// flagged lists every location the message refers to. additionalProperties
	// messages name several keys in map iteration order; deduping only path
	// (the first name) made unknown-key warnings flicker in and out.
	flagged [][]any
	// marks lists the rows that carry the severity icon for this issue. It is
	// narrower than flagged for additionalProperties messages: names that are
	// valid fields in the edited value (errors from other oneOf branches) must
	// not be marked, and the selected names must not depend on map order.
	marks [][]any
	warn  bool // editor-level warning (schema-unknown key), not a validator error
}

// errsState is the save-time validation error overlay.
type errsState struct {
	lines  []string
	paths  [][]any
	cursor int
	exit   bool // end the session after the --force save (vs plain ctrl+s)
}

// confirmState is a y/n question overlay. onNo is optional: without it n is
// a plain dismissal; with it n performs an action (esc always dismisses).
type confirmState struct {
	title string
	hint  string // optional footer override
	onYes func(m *listModel) tea.Cmd
	onNo  func(m *listModel) tea.Cmd
}

// searchState is the "/" filter mode, scoped to the subtree below the
// current breadcrumb path.
type searchState struct {
	active   bool
	replace  bool // replace mode: show the replace input, results are previews
	foldCase bool // true = match case-insensitively (default)
	ti       textinput.Model
	rti      textinput.Model
	all      []row
	results  []row
	cursor   int
	regex    bool           // true = the query is an RE2 regular expression (alt+x)
	re       *regexp.Regexp // compiled query; nil unless regex && query compiled ok
	reErr    string         // regexp.Compile error message; "" when ok
	// A search jump parks the overlay instead of closing it: esc at the
	// landing depth (returnDepth) reopens it, and closing it always returns to
	// the menu the search was opened from (origin, captured when the jump
	// parks the overlay so every park cycle gets a fresh return point).
	parked      bool
	returnDepth int
	origin      []*menuState
}

type listModel struct {
	model         *smodel.FormNode
	doc           *jsondoc.Node
	opts          Options
	stack         []*menuState
	width, height int
	saved         bool // at least one successful write this session
	withErrors    bool // the last successful write skipped validation errors (--force)
	aborted       bool
	exiting       bool // the session ended cleanly (root quit or Save and exit)
	edit          *editState
	add           *addState  // key-name input for a schema-less member (a)
	find          *findState // f/F quick move: waiting for the character
	anc           *anchorState
	mrg           *mergeState // "<" merge-keys input for the current mapping
	sel           *selState
	text          *textState
	ext           *externalEdit // pending $EDITOR target for a schema-less value
	errs          *errsState
	confirm       *confirmState
	help          bool // "?" keyboard-help overlay
	hideEmpty     bool // z filter: hide object keys with no document value
	search        searchState
	menuCache     map[*smodel.FormNode]*smodel.FormNode
	notice        string          // transient list-mode message, cleared on the next key
	startup       string          // session info notices (aliases kept, schema source), dropped on the first key
	cmdBuf        []string        // pending multi-key command (vi-style: shown bottom-right, never cleared by a timer)
	recording     bool            // macro capture armed
	recBuf        []tea.KeyMsg    // keys captured so far
	macro         []tea.KeyMsg    // last completed macro
	undoStack     []*jsondoc.Node // doc snapshot before each change; u restores
	redoStack     []*jsondoc.Node // undone states; ctrl+r restores
	baseline      *jsondoc.Node   // loaded/last-saved snapshot for change marks
	issuesNow     []issue         // live validation problems (refreshed after writes)
	errPaths      [][]any         // validation error locations, marked on their rows
	warnPaths     [][]any         // unknown-key warning locations (yellow marks)
	issues        *issuesState    // open issues overlay
	issuesBack    *issuesState    // parked overlay: esc at the jump target returns here
	issuesReturn  int             // stack depth of the jump target
	issuesOrigin  []*menuState    // stack captured when the jump parked the list
	refPick       *refPickState   // open multi-reference picker (gr with 2+ targets)
	jumpOrigin    []*menuState    // stack captured when ga/gr jumped; nil when inactive
	jumpReturn    int             // stack depth of the ga/gr landing
	navBack       []navMark       // navigation trail: locations left behind (ctrl+o)
	navFwd        []navMark       // forward branch of the trail (ctrl+i)
}

// newMenuTextarea builds the raw editor sized for the current screen.
func newMenuTextarea(m *listModel) textarea.Model {
	ta := textarea.New()
	ta.ShowLineNumbers = false
	ta.Prompt = ""
	w := m.width - 4
	if w > 100 {
		w = 100
	}
	if w < 20 {
		w = 20
	}
	ta.SetWidth(w)
	h := m.height - 6
	if h > 24 {
		h = 24
	}
	if h < 3 {
		h = 3
	}
	ta.SetHeight(h)
	return ta
}

// newListModel builds the root menu for the form tree.
func newListModel(model *smodel.FormNode, doc *jsondoc.Node, opts Options) *listModel {
	stack := []*menuState{{kind: menuKindFor(model), node: model}}
	if doc != nil {
		seedVariantCursor(stack[0], doc) // oneOf root: open on the matched variant
	}
	m := &listModel{
		model:   model,
		doc:     doc,
		opts:    opts,
		width:   80,
		height:  24,
		stack:   stack,
		startup: opts.StartupNotice,
	}
	if doc != nil {
		// Clone keeps merge-key members structurally (FromAny would flatten
		// them away) while sharing nothing with the live document, so edits
		// through alias references keep counting as changes.
		m.baseline = doc.Clone()
	}
	m.setIssues(m.liveIssues(opts.LoadErrors))
	return m
}

// setIssues replaces the live validation problem list and its row marks.
func (m *listModel) setIssues(items []issue) {
	m.issuesNow = items
	m.errPaths = m.errPaths[:0]
	m.warnPaths = m.warnPaths[:0]
	for _, it := range items {
		marks := it.marks
		if len(marks) == 0 && it.path != nil {
			marks = [][]any{it.path}
		}
		for _, p := range marks {
			if p == nil { // document root: the status row carries the count
				continue
			}
			if it.warn {
				m.warnPaths = append(m.warnPaths, p)
			} else {
				m.errPaths = append(m.errPaths, p)
			}
		}
	}
}

// refreshIssues re-validates the document after a write so fixed problems
// leave the list, the counts and the row marks — and, just as important, new
// problems appear immediately: a document that loaded clean is re-validated
// on its first change too (an edit can introduce an unknown key or a value
// the schema rejects, e.g. by pointing a field at an alias whose content
// does not fit). Callers invoke it only when the document changed.
// Without a validator the load-time list stays as loaded.
func (m *listModel) refreshIssues() {
	if m.opts.Validate == nil {
		return
	}
	// The root menu gains or loses its issue status row with the problem
	// count; keep the cursor on the row it was on while the list shifts.
	var root *menuState
	var before []row
	if len(m.stack) > 0 {
		root = m.stack[0]
		before = menuRowsFor(m, root)
	}
	m.setIssues(m.liveIssues(m.opts.Validate(m.doc)))
	if root == nil {
		return
	}
	if after := menuRowsFor(m, root); len(after) != len(before) {
		remapStateCursor(root, before, after)
	}
}

// liveIssues combines validator messages with the editor's unknown-key
// warnings for the live issue list. A key the validator already flags
// (e.g. additionalProperties) is not warned about twice.
func (m *listModel) liveIssues(msgs []string) []issue {
	items := issuesFrom(msgs)
	warnings := unknownKeyIssues(m.model, m.doc)

	// An additionalProperties message names keys in map iteration order, and
	// a failed oneOf branch also names fields that are valid in the matched
	// variant. Mark only the names that really are schema-unknown keys in the
	// edited value; when none are, mark the object itself. Otherwise valid
	// siblings acquire error icons that move/disappear between validations.
	for i := range items {
		it := &items[i]
		if it.warn || !strings.Contains(it.msg, "additional properties ") {
			continue
		}
		loc, _, ok := splitIssue(it.msg)
		if !ok {
			continue
		}
		listed := map[string]bool{}
		for _, p := range it.flagged {
			listed[jsondoc.PathString(p)] = true
		}
		var actual [][]any
		for _, w := range warnings { // document order: stable
			if listed[jsondoc.PathString(w.path)] {
				actual = append(actual, w.path)
			}
		}
		if len(actual) == 0 {
			it.path = loc
			it.flagged = [][]any{loc}
			it.marks = [][]any{loc}
			continue
		}
		it.path = actual[0]
		it.flagged = actual
		it.marks = actual
	}

	flagged := map[string]bool{}
	for _, it := range items {
		for _, p := range it.flagged {
			flagged[jsondoc.PathString(p)] = true
		}
		if it.path != nil {
			flagged[jsondoc.PathString(it.path)] = true
		}
	}
	for _, w := range warnings {
		if !flagged[jsondoc.PathString(w.path)] {
			items = append(items, w)
		}
	}
	return items
}

// issuesFrom parses validator messages into jumpable issues.
func issuesFrom(msgs []string) []issue {
	if len(msgs) == 0 {
		return nil
	}
	out := make([]issue, 0, len(msgs))
	for _, s := range msgs {
		path, ok := parseIssue(s)
		if !ok {
			path = nil
		}
		out = append(out, issue{msg: s, path: path, flagged: issueFlagPaths(s)})
	}
	return out
}

// top returns the current navigation state.
func (m *listModel) top() *menuState {
	if len(m.stack) == 0 {
		return nil
	}
	return m.stack[len(m.stack)-1]
}

// cachedMenu memoizes synthesized menu nodes per schema node (merged menus
// keyed by the oneOf node, pinned menus by the variant node).
func (m *listModel) cachedMenu(key *smodel.FormNode, build func() *smodel.FormNode) *smodel.FormNode {
	if n, ok := m.menuCache[key]; ok {
		return n
	}
	n := build()
	if m.menuCache == nil {
		m.menuCache = map[*smodel.FormNode]*smodel.FormNode{}
	}
	m.menuCache[key] = n
	return n
}

// dynamicMenu builds the item menu of an all-object oneOf: the merged union
// menu while the value is unset or ambiguous, the matched variant's menu
// once exactly one candidate remains.
func (m *listModel) dynamicMenu(node *smodel.FormNode, path []any) *menuState {
	st := &menuState{kind: menuObject, path: path, variantOf: node}
	val, _ := m.doc.Get(path)
	variants := dynamicVariants(node)
	if cands := matchVariantList(variants, val); len(cands) == 1 {
		v := cands[0]
		st.node = m.cachedMenu(v, func() *smodel.FormNode { return pinnedOneOfMenu(variants, v) })
	} else {
		st.node = m.cachedMenu(node, func() *smodel.FormNode { return mergedOneOfMenu(node, variants) })
	}
	return st
}

// refreshVariantMenu re-runs variant inference on every dynamic item menu in
// the stack after each key press: the node flips between merged and pinned as
// the value disambiguates. The cursor follows the field it was on (matched by
// schema key), so flipping via the discriminator keeps it selected.
func (m *listModel) refreshVariantMenu() {
	for _, st := range m.stack {
		if st.variantOf == nil {
			continue
		}
		want := m.dynamicMenu(st.variantOf, st.path).node
		if want == st.node {
			continue
		}
		key := m.cursorKey(st)
		st.node = want
		if key != "" {
			if i := m.rowIndexByKey(st, key); i >= 0 {
				st.cursor = i
			}
		}
	}
}

// cursorKey returns the schema key of the row under the state's cursor (""
// for synthetic rows); the cursor stays on that key across variant flips.
func (m *listModel) cursorKey(st *menuState) string {
	rows := menuRowsFor(m, st)
	if st.cursor < 0 || st.cursor >= len(rows) {
		return ""
	}
	if fn := rows[st.cursor].fn; fn != nil {
		return fn.Key
	}
	return ""
}

// rowIndexByKey finds the first row of the state backed by the given key.
func (m *listModel) rowIndexByKey(st *menuState, key string) int {
	for i, r := range menuRowsFor(m, st) {
		if r.fn != nil && r.fn.Key == key {
			return i
		}
	}
	return -1
}

func (m *listModel) Init() tea.Cmd { return nil }

// dispatchKey routes one key to the active handler and runs the post-key
// refreshes (variant inference, live validation issues). Keys that change
// the document push the pre-key snapshot onto the undo stack, so one undo
// step reverts exactly one key's worth of changes. u/ctrl+r act only in
// list mode and bypass the snapshot wrap — restoring is not an edit.
func (m *listModel) dispatchKey(msg tea.KeyMsg) tea.Cmd {
	if m.listMode() && m.applyHistoryKey(msg) {
		m.cmdBuf = nil // a history key breaks any pending g combo
		m.refreshVariantMenu()
		m.refreshIssues()
		return nil
	}
	pre := m.doc.Clone()
	cmd := m.routeKey(msg)
	edited := !nodesEqual(pre, m.doc)
	if edited {
		m.pushUndo(pre)
	}
	m.refreshVariantMenu()
	if edited {
		m.refreshIssues()
	}
	return cmd
}

// routeKey is the overlay-priority dispatch: the innermost open surface
// consumes the key.
func (m *listModel) routeKey(msg tea.KeyMsg) tea.Cmd {
	switch {
	case m.help:
		return m.keyHelp(msg)
	case m.confirm != nil:
		return m.keyConfirm(msg)
	case m.issues != nil:
		return m.keyIssues(msg)
	case m.errs != nil:
		return m.keyErrs(msg)
	case m.refPick != nil:
		return m.keyRefPick(msg)
	case m.text != nil:
		return m.keyText(msg)
	case m.sel != nil:
		return m.keySel(msg)
	case m.search.active:
		return m.keySearch(msg)
	case m.edit != nil:
		return m.keyEdit(msg)
	case m.add != nil:
		return m.keyAdd(msg)
	case m.anc != nil:
		return m.keyAnchor(msg)
	case m.mrg != nil:
		return m.keyMerge(msg)
	}
	return m.keyList(msg)
}

func (m *listModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case rawEditorDoneMsg:
		return m.applyExternalEdit(msg)
	case tea.KeyMsg:
		// ctrl+c always quits immediately without writing (contract).
		if msg.Type == tea.KeyCtrlC {
			m.aborted = true
			return m, tea.Quit
		}
		// q is an esc alias in every non-typing context (exit/cancel
		// semantics); inside a focused text input it is an ordinary
		// character and esc does the cancelling. Rewriting here means
		// overlays, list mode and recorded macros all see esc; macros
		// need no extra handling — q is rewritten before recording only
		// outside inputs, so replays type/cancel exactly what was captured.
		if msg.Type == tea.KeyRunes && !msg.Alt && string(msg.Runes) == "q" && !m.typing() {
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		}
		m.notice = ""
		m.startup = "" // session info notices live until the first key press
		if cmd, consumed := m.macroControl(msg); consumed {
			return m, cmd
		}
		if m.recording {
			m.recBuf = append(m.recBuf, msg)
			if len(m.recBuf) >= maxMacroKeys {
				m.macro, m.recBuf, m.recording = m.recBuf, nil, false
				m.notice = fmt.Sprintf("macro limit reached (%d keys) — recording stopped", maxMacroKeys)
			}
		}
		return m, m.dispatchKey(msg)
	}
	// Non-key messages feed the active input widget.
	var cmd tea.Cmd
	switch {
	case m.edit != nil:
		m.edit.ti, cmd = m.edit.ti.Update(msg)
	case m.add != nil:
		m.add.ti, cmd = m.add.ti.Update(msg)
	case m.anc != nil:
		m.anc.ti, cmd = m.anc.ti.Update(msg)
	case m.text != nil:
		m.text.ta, cmd = m.text.ta.Update(msg)
	case m.search.active:
		m.search.ti, cmd = m.search.ti.Update(msg)
		var cmd2 tea.Cmd
		m.search.rti, cmd2 = m.search.rti.Update(msg)
		if cmd == nil {
			cmd = cmd2
		}
	}
	return m, cmd
}

func (m *listModel) View() string {
	switch {
	case m.help:
		return m.helpView()
	case m.confirm != nil:
		return m.confirmView()
	case m.issues != nil:
		return m.issuesView()
	case m.errs != nil:
		return m.errsView()
	case m.refPick != nil:
		return m.refPickView()
	case m.text != nil:
		return m.textView()
	case m.sel != nil:
		return m.selView()
	}
	return m.listView()
}

// --- list rendering ------------------------------------------------------

// breadcrumb renders the navigation context as a document path
// ("$.a.b[0]"). Levels that do not extend the path — oneOf pickers and
// variant editors — add no segment (the title line names them). A menu that
// drilled into a reference appends the anchor's location
// ("$.a.steps → $.jobs.build.steps"). While the cursor rests on a merged
// key, the arrow names the anchor the key came from instead — moving off a
// merged key clears the arrow and the path behind it. Overlong paths keep
// their tail, which is the informative end.
func (m *listModel) breadcrumb() string {
	st := m.top()
	if st == nil {
		return "$"
	}
	crumb := jsondoc.PathString(st.path)
	if _, ref, ok := m.anchorRole(st.path); ok && ref {
		n, _ := m.doc.Get(st.path)
		crumb += " → " + jsondoc.PathString(m.anchorHolderPath(n))
		return clipLeft(crumb, m.width)
	}
	if anchor := m.mergeCrumb(); anchor != nil {
		crumb += " → " + jsondoc.PathString(anchor)
	}
	return clipLeft(crumb, m.width)
}

// clipLeft keeps the tail of s within max runes, marking the cut with "…".
func clipLeft(s string, max int) string {
	if max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max == 1 {
		return "…"
	}
	return "…" + string(r[len(r)-(max-1):])
}

func (m *listModel) listView() string {
	var b strings.Builder
	st := m.top()
	titleText, summary := m.menuTitle(st)
	title := mTitle.Render(titleText)
	// The menu of an anchored value names the anchor on the title line:
	// "steps: &x" for the carrier, "steps: *x" for a reference, both violet.
	if name, ref, ok := m.anchorRole(st.path); ok {
		if ref {
			title += ": " + mAnchor.Render("*"+name)
		} else {
			title += ": " + mAnchor.Render("&"+name)
		}
	}
	// a mapping with merge keys names them on the title: "<<: *a *b"
	if merges := m.mergeNames(st.path); merges != "" {
		title += " " + mAnchor.Render(merges)
	}
	if m.dirty() {
		title += " " + mDiff.Render("●")
	}
	b.WriteString(clipDisplay(title, m.width-1))
	b.WriteByte('\n')
	b.WriteString(clipDisplay(mCrumb.Render(clipRunes(m.breadcrumb(), m.width)), m.width-1))
	b.WriteByte('\n')
	header := 2
	// the "<" merge-keys input edits the mapping itself, so it lives in the
	// header under the title/breadcrumb, not under a row
	if m.mrg != nil {
		lines := m.mergeInputLines()
		for _, line := range strings.Split(lines, "\n") {
			b.WriteString(clipDisplay(line, m.width-1))
			b.WriteByte('\n')
			header++
		}
	}

	if m.search.active {
		inputs := 1
		if m.search.replace {
			inputs++
		}
		status := m.statusLines()
		b.WriteString(clipDisplay(mHelp.Render("search: ")+m.search.ti.View(), m.width-1))
		b.WriteByte('\n')
		if m.search.replace {
			b.WriteString(clipDisplay(mHelp.Render("replace: ")+m.search.rti.View(), m.width-1))
			b.WriteByte('\n')
		}
		// Results fill the space between the inputs and the footer; a short
		// list is padded so the footer stays on the terminal's last line.
		h := m.height - header - inputs - len(status) - 1
		if h < 1 {
			h = 1
		}
		m.writeSearchRows(&b, h)
		lines := strings.Count(b.String(), "\n")
		for lines < m.height-1-len(status) {
			b.WriteByte('\n')
			lines++
		}
		caseTxt := "case-insensitive"
		if !m.search.foldCase {
			caseTxt = "case-sensitive"
		}
		if m.search.reErr != "" {
			b.WriteString(clipDisplay(mErr.Render(clipRunes("invalid regex: "+m.search.reErr+" · esc close", m.width)), m.width-1))
			return b.String()
		}
		modeTxt := "literal"
		if m.search.regex {
			modeTxt = "regex"
		}
		for _, line := range status {
			b.WriteString(clipDisplay(line, m.width-1))
			b.WriteByte('\n')
		}
		b.WriteString(clipDisplay(mHelp.Render("↑/↓ ctrl+j/k · enter jump/replace · tab replace · alt+i "+caseTxt+" · alt+x "+modeTxt), m.width-1))
		return b.String()
	}

	rows := menuRows(m)
	m.clampCursor(rows)
	desc := m.focusedDesc(rows)
	var descLines []string
	if desc != "" {
		descLines = wrapText(desc, m.width-1)
	}
	// a menu whose schema title is missing is labelled with its key; the
	// structural signature then moves here, above the hint bar
	var summaryLines []string
	if summary != "" {
		summaryLines = wrapText("variant: "+summary, m.width-1)
	}
	status := m.statusLines()
	h := m.height - header - 1 - len(descLines) - len(summaryLines) - len(status)
	if m.notice != "" {
		h--
	}
	if m.hideEmpty {
		h--
	}
	m.writeRows(&b, rows, h)
	if m.hideEmpty {
		b.WriteString(clipDisplay("  "+mDim.Render("[empty hidden]"), m.width-1))
		b.WriteByte('\n')
	}
	// Pin the bottom block (row description, session status, transient
	// notice) directly above the hint bar: fill the gap below the rows
	// first, so nothing floats in the middle of a short menu. No trailing
	// newline after the hint bar, or the renderer would treat it as an extra
	// line and scroll the title out of view.
	bottom := len(descLines) + len(summaryLines) + len(status)
	if m.notice != "" {
		bottom++
	}
	lines := strings.Count(b.String(), "\n")
	for lines < m.height-1-bottom {
		b.WriteByte('\n')
		lines++
	}
	for _, line := range descLines {
		b.WriteString(clipDisplay(mDesc.Render(line), m.width-1))
		b.WriteByte('\n')
	}
	for _, line := range summaryLines {
		b.WriteString(clipDisplay(mDim.Render(line), m.width-1))
		b.WriteByte('\n')
	}
	for _, line := range status {
		b.WriteString(clipDisplay(line, m.width-1))
		b.WriteByte('\n')
	}
	if m.notice != "" {
		b.WriteString(clipDisplay(mErr.Render(clipRunes(m.notice, m.width)), m.width-1))
		b.WriteByte('\n')
	}
	b.WriteString(m.helpLine())
	return b.String()
}

// statusLines renders the session status notices (schema source, offline
// fallback, compile failure, alias count): the root screen shows them as the
// block directly above the bottom hint bar. Informational notices
// (m.startup) lead the block and vanish on the first key press; warnings
// stay. Lines wrap to the terminal width (plain text first, styled after —
// wrapText counts display cells, not ANSI bytes) so the renderer's cell
// accounting stays intact.
func (m *listModel) statusLines() []string {
	if len(m.stack) != 1 {
		return nil
	}
	combined := m.opts.Status
	if m.startup != "" {
		if combined != "" {
			combined = m.startup + "\n" + combined
		} else {
			combined = m.startup
		}
	}
	if combined == "" {
		return nil
	}
	var out []string
	for _, line := range strings.Split(combined, "\n") {
		for _, w := range wrapText(line, m.width-1) {
			out = append(out, mStatus.Render(w))
		}
	}
	return out
}

// wrapText wraps s to max display cells per line, preserving explicit
// newlines. It breaks at the last space when that leaves a non-empty prefix;
// otherwise (CJK text, a single long token) it breaks between runes. A wide
// rune wider than max stays whole rather than producing an empty line.
func wrapText(s string, max int) []string {
	if max < 1 {
		return []string{s}
	}
	var out []string
	for _, para := range strings.Split(s, "\n") {
		line := ""
		for _, r := range para {
			cand := line + string(r)
			if displayWidth(cand) <= max {
				line = cand
				continue
			}
			if line == "" {
				line = string(r)
				continue
			}
			if i := strings.LastIndexByte(line, ' '); i > 0 {
				out = append(out, line[:i])
				line = line[i+1:] + string(r)
				continue
			}
			out = append(out, line)
			line = string(r)
		}
		out = append(out, line)
	}
	return out
}

// displayWidth returns the terminal cell width of s. It uses wcwidth
// semantics (go-runewidth, which honours the locale's East-Asian ambiguous
// setting) instead of the grapheme width lipgloss.Width reports: Nerd Font
// private-use glyphs and "●" are ambiguous, and a CJK terminal gives them two
// cells while uniseg sees one. The mismatch made the renderer's end-of-line
// erase land on half a glyph, which showed up as flicker while the cursor
// moved.
func displayWidth(s string) int { return ansi.StringWidthWc(s) }

// clipDisplay truncates s to max terminal cells, ANSI-aware. A reset closes
// any style left open by the cut.
func clipDisplay(s string, max int) string {
	if max < 1 {
		return ""
	}
	if displayWidth(s) <= max {
		return s
	}
	return ansi.TruncateWc(s, max, "") + "\x1b[0m"
}

// helpLine renders the bottom hint row via recHint. Special modes replace
// the standard hints with their own operation line, most specific first:
// quick move waiting, visual select, then the marked-rows selection.
func (m *listModel) helpLine() string {
	if m.find != nil {
		if m.find.backward {
			return m.recHint("Quick move: type a character to move up · esc cancels")
		}
		return m.recHint("Quick move: type a character to move down · esc cancels")
	}
	if st := m.top(); st != nil {
		if st.visual {
			return m.recHint("Visual select: j/k or arrows mark rows · y copy · d delete · v/esc stop")
		}
		if n := len(st.sel); n > 0 {
			rows := "rows"
			if n == 1 {
				rows = "row"
			}
			return m.recHint(fmt.Sprintf("Marked %d %s: y copy · d delete · m unmark", n, rows))
		}
	}
	return m.recHint(menuHelp)
}

// helpView renders the "?" overlay: the full key reference.
func (m *listModel) helpView() string {
	var b strings.Builder
	b.WriteString(mTitle.Render("Keyboard help"))
	b.WriteString("\n\n")
	for _, e := range helpEntries {
		var line string
		if m.width-1 < helpColWidth+12 {
			// Narrow terminal: do not waste most of the row on padding.
			line = e.keys
			if line != "" {
				line += " "
			}
			line += e.desc
		} else {
			line = m.helpCol(e.keys) + e.desc
		}
		b.WriteString(clipDisplay(mHelp.Render(line), m.width-1))
		b.WriteByte('\n')
	}
	// Keep the close hint on the terminal's last line; blank rows fill the
	// space under a reference that does not use the full screen.
	lines := strings.Count(b.String(), "\n")
	for lines < m.height-1 {
		b.WriteByte('\n')
		lines++
	}
	b.WriteString(m.recHint("esc/? close"))
	return b.String()
}

// focusedDesc returns the schema description of the highlighted row (shown
// in mDesc above the hint bar, so metadata reads differently from the keys
// to press): field/drill rows carry it in fn, oneOf variant rows in variant.
func (m *listModel) focusedDesc(rows []row) string {
	if len(rows) == 0 {
		return ""
	}
	r := rows[m.top().cursor]
	if r.fn != nil && r.fn.Desc != "" {
		return r.fn.Desc
	}
	if r.variant != nil {
		return r.variant.Desc
	}
	return ""
}

// recHint renders a hint row with a right-aligned tail: the pending
// multi-key command buffer (vi showcmd style — a lone g waits there with no
// timeout) and, while a macro capture runs, "● REC n". Both stay visible on
// every surface that renders hints.
func (m *listModel) recHint(hints string) string {
	limit := m.width - 1
	var parts []string
	if len(m.cmdBuf) > 0 {
		parts = append(parts, mStatus.Render(strings.Join(m.cmdBuf, "")))
	}
	if m.recording {
		parts = append(parts, mErr.Render(fmt.Sprintf("● REC %d", len(m.recBuf))))
	}
	if len(parts) == 0 {
		return mHelp.Render(clipDisplay(hints, limit))
	}
	right := strings.Join(parts, " ")
	w := displayWidth(right)
	if m.width < w+8 {
		return mHelp.Render(clipDisplay(hints, limit))
	}
	left := clipDisplay(hints, limit-w-1)
	pad := limit - displayWidth(left) - w
	if pad < 1 {
		pad = 1
	}
	return mHelp.Render(left+strings.Repeat(" ", pad)) + right
}

// writeCut clips s to the terminal width line by line (multiline row content:
// in-place input + completion hint + error) and writes it out.
func (m *listModel) writeCut(b *strings.Builder, s string) {
	for _, line := range strings.Split(s, "\n") {
		// Leave one cell of slack: the renderer measures with grapheme widths
		// and appends its end-of-line erase when it thinks the line is short,
		// so a line that fills the terminal exactly can lose the last cell.
		b.WriteString(clipDisplay(line, m.width-1))
		b.WriteByte('\n')
	}
}

// writeRows renders the visible slice of rows, scrolling around the cursor.
func (m *listModel) writeRows(b *strings.Builder, rows []row, height int) {
	if height < 1 {
		height = 1
	}
	st := m.top()
	if st.offset > st.cursor {
		st.offset = st.cursor
	}
	if st.cursor >= st.offset+height {
		st.offset = st.cursor - height + 1
	}
	if maxOff := len(rows) - height; st.offset > maxOff {
		st.offset = maxOff
	}
	if st.offset < 0 {
		st.offset = 0
	}
	titleWidth := m.menuTitleColWidth(rows)
	for i := st.offset; i < len(rows) && i < st.offset+height; i++ {
		m.writeCut(b, m.rowLine(rows[i], i == st.cursor, titleWidth))
		// the & / * name input sits on the line under the cursor
		if m.anc != nil && i == st.cursor {
			m.writeCut(b, m.anchorInputLines())
		}
	}
	// in-place input for a brand-new array item renders on its own row when
	// that row is already in the document (a/A splice the placeholder in);
	// the append-only < Add item > flow has no row yet, so it gets a line
	// labeled with the element index it will take
	if m.edit != nil && m.edit.isNew && !hasInlineEditRow(m, rows) {
		m.writeCut(b, m.editLines(m.titleCol(m.newItemTitle(), titleWidth)))
	}
	// key-name input for a schema-less member (a)
	if m.add != nil {
		m.writeCut(b, cursorPrefix(true)+m.titleCol("New key", titleWidth)+m.add.ti.View())
		if m.add.err != "" {
			m.writeCut(b, mErr.Render("  "+m.add.err))
		}
	}
}

// anchorInputLines renders the & / * name input under the cursor row: the
// prompt, the completion hint (reference mode), and any error. The prompt
// reads like the other prompts (lowercase, help-bar gray), indented two
// cells where the cursor arrow would sit, so its text lines up with the row
// titles.
func (m *listModel) anchorInputLines() string {
	prompt := "  anchor: " // two cells: the cursor arrow's former width
	if m.anc.mode == refSet {
		prompt = "  reference: "
	}
	lines := []string{mHelp.Render(prompt) + m.anc.ti.View()}
	if m.anc.mode == refSet && len(m.anchorFiltered()) > 0 {
		lines = append(lines, mHelp.Render("  tab: "+m.anchorSugView()))
	}
	if m.anc.err != "" {
		lines = append(lines, mErr.Render("  "+m.anc.err))
	}
	return strings.Join(lines, "\n")
}

// writeSearchRows renders the visible slice of search results.
func (m *listModel) writeSearchRows(b *strings.Builder, height int) {
	if height < 1 {
		height = 1
	}
	if m.search.cursor >= len(m.search.results) {
		m.search.cursor = len(m.search.results) - 1
	}
	if m.search.cursor < 0 {
		m.search.cursor = 0
	}
	off := m.search.cursor - height + 1
	if off < 0 {
		off = 0
	}
	for i := off; i < len(m.search.results) && i < off+height; i++ {
		line := cursorPrefix(i == m.search.cursor) + m.search.results[i].label
		if i == m.search.cursor {
			line = mSel.Render(line)
		}
		m.writeCut(b, line)
	}
}

// cursorPrefix marks the focused row, two cells wide.
func cursorPrefix(on bool) string {
	if on {
		return "> "
	}
	return "  "
}

// nodesEqual reports deep JSON equality: objects compare by key set (member
// order ignored), arrays order-sensitively, scalars by exact text. Merge
// specs compare by their referenced anchors' content, so retargeting,
// reordering or clearing a merge list counts as a change.
func nodesEqual(a, b *jsondoc.Node) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	// Anchor identity is part of the document: expanding a reference (or
	// renaming/removing an anchor) leaves the content identical but changes
	// what the YAML file carries, so it must count as an edit — undoable,
	// marked dirty, and written on save.
	if a.Anchor != b.Anchor {
		return false
	}
	if a.Merge != nil || b.Merge != nil {
		if a.Merge == nil || b.Merge == nil || len(a.Merge) != len(b.Merge) {
			return false
		}
		for i := range a.Merge {
			if !nodesEqual(a.Merge[i], b.Merge[i]) {
				return false
			}
		}
		return true
	}
	if a.Null || b.Null {
		return a.Null == b.Null
	}
	switch {
	case a.Str != nil || b.Str != nil:
		return a.Str != nil && b.Str != nil && *a.Str == *b.Str
	case a.Num != nil || b.Num != nil:
		return a.Num != nil && b.Num != nil && a.Num.String() == b.Num.String()
	case a.Bool != nil || b.Bool != nil:
		return a.Bool != nil && b.Bool != nil && *a.Bool == *b.Bool
	case a.Object != nil || b.Object != nil:
		if a.Object == nil || b.Object == nil || len(*a.Object) != len(*b.Object) {
			return false
		}
		by := map[string]*jsondoc.Node{}
		for _, mem := range *b.Object {
			by[mem.Key] = mem.Val
		}
		for _, mem := range *a.Object {
			bv, ok := by[mem.Key]
			if !ok || !nodesEqual(mem.Val, bv) {
				return false
			}
		}
		return true
	case a.Array != nil || b.Array != nil:
		if a.Array == nil || b.Array == nil || len(*a.Array) != len(*b.Array) {
			return false
		}
		for i := range *a.Array {
			if !nodesEqual((*a.Array)[i], (*b.Array)[i]) {
				return false
			}
		}
		return true
	}
	return true
}

// changeMark renders the trailing marker on rows whose value differs from the
// baseline ("●"), or "" when unchanged.
func (m *listModel) changeMark(path []any) string {
	if !m.changed(path) {
		return ""
	}
	return " " + mDiff.Render("●")
}

// changed reports whether the value at path differs from the loaded/last-saved
// baseline; without a baseline nothing is marked.
func (m *listModel) changed(path []any) bool {
	if m.baseline == nil || m.doc == nil {
		return false
	}
	cur, _ := m.doc.Get(path)
	base, _ := m.baseline.Get(path)
	return !nodesEqual(cur, base)
}

// dirty reports whether anything in the document differs from the baseline.
func (m *listModel) dirty() bool {
	if m.baseline == nil || m.doc == nil {
		return false
	}
	return !nodesEqual(m.doc, m.baseline)
}

const (
	// helpColWidth is the keyboard-help overlay's fixed description column.
	helpColWidth = 24
	// The menu's title column adapts to the widest key, within these bounds.
	titleColMin = 16
	titleColMax = 42
)

// helpCol pads the ? overlay's key column to helpColWidth cells.
func (m *listModel) helpCol(left string) string {
	if w := displayWidth(left); w >= helpColWidth {
		return left + " "
	} else {
		return left + strings.Repeat(" ", helpColWidth-w)
	}
}

// menuTitleColWidth returns the adaptive title column width for a menu: the
// widest left cell among rows with a value column, clamped to
// [titleColMin, titleColMax]. Status/toggle/action rows do not participate.
func (m *listModel) menuTitleColWidth(rows []row) int {
	width := titleColMin
	for _, r := range rows {
		n := -1
		switch r.kind {
		case rowField, rowDrill:
			if r.fn == nil {
				continue
			}
			n = displayWidth(m.rowTitleText(r))
			if m.fieldRequired(r) {
				n++ // required marker
			}
		case rowUnknown:
			n = displayWidth(r.label)
		case rowVariant:
			n = displayWidth("○ ") + displayWidth(r.variant.Title)
		}
		if n > width {
			width = n
		}
	}
	if width > titleColMax {
		width = titleColMax
	}
	return width
}

// titleCol pads the left column to width cells and appends the separator
// before the value. Titles wider than width are shown in full (no ellipsis);
// their value starts further right, which is accepted for very long keys.
func (m *listModel) titleCol(left string, width int) string {
	if w := displayWidth(left); w < width {
		return left + strings.Repeat(" ", width-w+1)
	}
	return left + " "
}

// inlineEditRow reports whether the active in-place editor renders on r: the
// edited field's own row, or the enclosing oneOf field row when a leaf
// variant is being edited. Rows compare by path, so shared value nodes (map
// members, array items) match by position; a new item spliced by a/A already
// has its placeholder row, and edits inline there.
func (m *listModel) inlineEditRow(r row) bool {
	if m.edit == nil || !pathEq(r.path, m.edit.path) {
		return false
	}
	if r.kind == rowUnknown {
		return true // schema-less scalar edited against its value type
	}
	if r.kind == rowField && r.fn == m.edit.fn {
		return true
	}
	return (r.kind == rowField || r.kind == rowDrill) && r.fn != nil &&
		r.fn.Kind == smodel.KOneOf && variantContains(r.fn, m.edit.fn)
}

// hasInlineEditRow reports whether any row renders the active editor
// (writeRows then skips the separate new-item line).
func hasInlineEditRow(m *listModel, rows []row) bool {
	for _, r := range rows {
		if m.inlineEditRow(r) {
			return true
		}
	}
	return false
}

// newItemTitle labels the standalone input of the append-only < Add item >
// flow: the element address it will take once committed (key[append index]),
// mirroring itemRow's labels. The document key is the last path segment (the
// array's key in its parent), falling back to the node's key.
func (m *listModel) newItemTitle() string {
	st := m.top()
	key := st.node.Key
	if n := len(st.path); n > 0 {
		if s, ok := st.path[n-1].(string); ok {
			key = s
		}
	}
	return fmt.Sprintf("%s[%d]", key, docArrayLen(m.doc, st.path))
}

// rowTitleText is the left-column label of a field or drill row: the document
// key for map rows, otherwise the schema title.
func (m *listModel) rowTitleText(r row) string {
	if r.label != "" {
		return r.label
	}
	if r.fn != nil {
		return r.fn.Title
	}
	return ""
}

func (m *listModel) rowLine(r row, cursor bool, titleWidth int) string {
	// in-place input replaces the edited row: the field's own row, or the
	// oneOf field row when a leaf variant is being edited (the picker pops
	// back to the parent menu for leaf variants). A spliced (a/A) new item
	// has its placeholder row already in the document and edits there too.
	if m.inlineEditRow(r) {
		return m.editLines(m.titleCol(m.rowTitleText(r), titleWidth))
	}
	// selected rows carry the red > marker; the cursor row's > follows the
	// visual select switch (red while it is on, white when off) and its
	// key is bold bright red
	sel := m.rowSelected(r)
	visual := m.visualOn()
	var left, right string
	switch r.kind {
	case rowVariant:
		v, _ := m.doc.Get(r.path)
		cur := currentVariant(m.top().node, v)
		mark, markStyle := "○", mDim
		if cur >= 0 && m.top().node.Variants[cur] == r.variant {
			mark, markStyle = "●", mAccent
		}
		left = markStyle.Render(mark)
		if r.variant.Title != "" {
			left += " " + r.variant.Title
		}
		right = mTail.Render(VariantSignature(r.variant))
		if mark == "●" {
			if v, ok := m.doc.Get(r.path); ok && v != nil && !v.Null {
				if s := m.variantValueSummary(r.variant, v); s != "" {
					right += " " + mValEnum.Render(s)
				}
			}
		}
	case rowToggle:
		box, boxStyle := "[ ]", mDim
		if m.enumMemberOn(r) {
			box, boxStyle = "[x]", mAccent
		}
		left = boxStyle.Render(box) + " " + r.fn.Items.Enum[r.idx].Display
	case rowAdd, rowSave, rowRaw, rowRemove, rowExpand:
		left = mAccent.Render(r.label)
	case rowNoItems:
		left = mDim.Render(r.label)
	case rowUnknown:
		// document key the schema does not define: edit as raw JSON or delete
		switch {
		case sel && cursor:
			left = mBold.Render(mMark.Render(r.label)) // cursor on selected: bold red key
		case sel:
			left = mMark.Render(r.label)
		default:
			left = mWarn.Render(r.label)
		}
		v, _ := m.doc.Get(r.path)
		right = mergeBadge(r) + m.anchorBadge(r.path) + m.rawValueStyle(v).Render(m.rawValueSummary(r.path))
	case rowIssues:
		left = m.issuesSeverity().Render(r.label)
	default: // rowField / rowDrill
		title := m.rowTitleText(r)
		if m.fieldRequired(r) {
			title += mStar.Render("*")
		}
		left = title
		switch {
		case sel && cursor:
			left = mBold.Render(mMark.Render(title)) // cursor on selected: bold red key
		case sel:
			left = mMark.Render(title)
		}
		right = mergeBadge(r) + m.anchorBadge(r.path) + m.valueStyle(r.fn, r.path).Render(summary(m, r.fn, r.path))
	}
	var content string
	switch r.kind {
	case rowField, rowDrill, rowUnknown, rowVariant:
		content = m.titleCol(left, titleWidth) + right
	default:
		// Status, toggle and action rows have no value column: keep their
		// label whole instead of padding it to the title column.
		content = left + right
	}
	prefix := cursorPrefix(cursor)
	if sel && (!cursor || visual) {
		prefix = mMark.Render("> ") // selected rows carry a red > marker
	}
	line := prefix + content
	if r.kind == rowUnknown {
		line += " " + mDim.Render("(not in schema)")
	}
	if r.kind == rowDrill || r.kind == rowIssues {
		line += " " + mAccent.Render("--->")
	}
	if r.kind == rowField || r.kind == rowDrill || r.kind == rowUnknown {
		switch {
		case m.hasErr(r.path):
			line += " " + mErr.Render(icons.Err)
		case r.kind == rowUnknown || m.hasWarn(r.path):
			// Every row labelled "(not in schema)" is a warning by definition;
			// the warning set is variant-aware and can miss rows the current
			// (pinned) menu shows as unknown. Errors still outrank warnings.
			line += " " + mWarn.Render(icons.Warn)
		}
		line += m.changeMark(r.path)
	}
	if cursor {
		line = mSel.Render(line)
	}
	return line
}

// rawValueSummary renders a document value with no schema behind it (unknown
// keys): scalars as text, containers compactly. Reference/anchor identity is
// shown on the row title (anchorBadge).
func (m *listModel) rawValueSummary(path []any) string {
	v, ok := m.doc.Get(path)
	if !ok || v == nil {
		return ""
	}
	switch {
	case v.Null:
		return "—"
	case v.Str != nil:
		return valueClip(*v.Str)
	case v.Num != nil:
		return v.Num.String()
	case v.Bool != nil:
		if *v.Bool {
			return "yes"
		}
		return "no"
	case v.Object != nil:
		if s := objectSummary(nil, v); s != "" {
			return s
		}
		return "{}"
	case v.Array != nil:
		return fmt.Sprintf("%d items", len(*v.Array))
	}
	return ""
}

// rawValueStyle colors a schema-less value by its JSON type.
func (m *listModel) rawValueStyle(v *jsondoc.Node) lipgloss.Style {
	switch {
	case v == nil || v.Null:
		return mDim
	case v.Str != nil:
		return mValStr
	case v.Num != nil:
		return mValNum
	case v.Bool != nil:
		return mValBool
	}
	return mValObj
}

// hasErr reports whether path is a validation-error location or one of its
// ancestors (so containers light up too).
func (m *listModel) hasErr(path []any) bool {
	for _, p := range m.errPaths {
		if isPathPrefix(path, p) {
			return true
		}
	}
	return false
}

// hasWarn reports whether path is a warning-only issue location or one of
// its ancestors. Errors take precedence over warnings at the call site.
func (m *listModel) hasWarn(path []any) bool {
	for _, p := range m.warnPaths {
		if isPathPrefix(path, p) {
			return true
		}
	}
	return false
}

// worstIssueIcon picks the severity glyph for a set of issues: any real
// validation error outranks the warnings.
func worstIssueIcon(items []issue) string {
	for _, it := range items {
		if !it.warn {
			return icons.Err
		}
	}
	return icons.Warn
}

// issuesSeverity returns the style for the aggregate status row: red while
// any real validation error remains, yellow when only warnings are left.
func (m *listModel) issuesSeverity() lipgloss.Style {
	for _, it := range m.issuesNow {
		if !it.warn {
			return mErr
		}
	}
	return mWarn
}

// severityStyle picks the text color for one issue line: yellow for
// unknown-key warnings, red for validator errors.
func (m *listModel) severityStyle(it issue) lipgloss.Style {
	if it.warn {
		return mWarn
	}
	return mErr
}

// variantContains reports whether target is one of the oneOf node's variants
// (nested groups included).
func variantContains(node, target *smodel.FormNode) bool {
	if node == nil || node.Kind != smodel.KOneOf {
		return false
	}
	for _, v := range node.Variants {
		if v == target || variantContains(v, target) {
			return true
		}
	}
	return false
}

// isPathPrefix reports whether prefix is a prefix of path.
func isPathPrefix(prefix, path []any) bool {
	if len(prefix) > len(path) {
		return false
	}
	for i := range prefix {
		if prefix[i] != path[i] {
			return false
		}
	}
	return true
}

// jumpToRow rebuilds the navigation stack down to the menu that contains
// path and puts the cursor on the row itself — a container field is
// selected, not entered (ga on a merged key points at the field that
// defines the value).
func (m *listModel) jumpToRow(path []any) {
	if len(path) == 0 {
		m.jumpToPath(path)
		return
	}
	m.revealHiddenEmpty(path)
	m.stack = rebuildStack(m.model, m.doc, path[:len(path)-1])
	rows := menuRows(m)
	m.clampCursor(rows)
	m.top().cursor = cursorByPath(rows, path)
	m.top().offset = 0
}

// jumpToPath rebuilds the navigation stack down to path and puts the cursor on
// the row representing it (search-jump semantics).
func (m *listModel) jumpToPath(path []any) {
	m.revealHiddenEmpty(path)
	m.stack = rebuildStack(m.model, m.doc, path)
	rows := menuRows(m)
	m.clampCursor(rows)
	m.top().cursor = cursorByPath(rows, path)
	m.top().offset = 0
}

// revealHiddenEmpty turns the z filter off when path names an object key with
// no document value, so a search or issue jump always lands on a visible row
// instead of its parent.
func (m *listModel) revealHiddenEmpty(path []any) {
	if !m.hideEmpty || m.doc == nil || len(path) == 0 {
		return
	}
	if _, ok := path[len(path)-1].(string); !ok {
		return
	}
	v, ok := m.doc.Get(path)
	if !ok || v == nil || v.Null {
		m.hideEmpty = false
		m.notice = "empty fields shown"
	}
}

// cursorByPath finds the row for path: an exact match first, else the deepest
// row whose path is a prefix of it.
func cursorByPath(rows []row, path []any) int {
	for i, r := range rows {
		if r.path != nil && pathEq(r.path, path) {
			return i
		}
	}
	best, bi := -1, 0
	for i, r := range rows {
		if r.path != nil && isPathPrefix(r.path, path) && len(r.path) > best {
			best, bi = len(r.path), i
		}
	}
	return bi
}

// openIssues shows the load-time issues overlay, recomputed against the
// current document when save-time validation is available.
func (m *listModel) openIssues() {
	items := m.issuesNow
	if m.opts.Validate != nil {
		items = m.liveIssues(m.opts.Validate(m.doc))
		m.setIssues(items)
	}
	m.issuesBack = nil // reopening from the status row resets the parked list
	m.issues = &issuesState{items: items}
}

// issuesReturnTo reopens the parked issue list at the jump target, refreshing
// its content against the current document (fixed entries disappear) while
// keeping the cursor position. The stack goes back to where the list was
// opened from, so closing it never leaves the user at the jump target.
func (m *listModel) issuesReturnTo() {
	leaving := m.navSnapshot()
	cur := m.issuesBack.cursor
	items := m.issuesBack.items
	if m.opts.Validate != nil {
		items = m.liveIssues(m.opts.Validate(m.doc))
		m.setIssues(items)
	}
	m.issuesBack = nil
	if origin := m.issuesOrigin; origin != nil {
		m.stack = origin
		m.issuesOrigin = nil
	}
	m.navBackStep(leaving)
	if cur > len(items)-1 {
		cur = len(items) - 1
	}
	if cur < 0 {
		cur = 0
	}
	m.issues = &issuesState{items: items, cursor: cur}
}

// fieldRequired reports whether the row's field is required in the current
// context: the variant's own definition when pinned; in the merged
// (ambiguous) view a key counts only when every defining variant requires it.
func (m *listModel) fieldRequired(r row) bool {
	if r.fn == nil {
		return false
	}
	required := r.fn.Required
	st := m.top()
	if st == nil || st.variantOf == nil {
		return required
	}
	variants := dynamicVariants(st.variantOf)
	if len(variants) == 0 {
		return required
	}
	_, defs := variantFieldDefs(variants)
	ds := defs[r.fn.Key]
	if len(ds) == 0 {
		return required
	}
	val, _ := m.doc.Get(st.path)
	if len(matchVariantList(variants, val)) != 1 { // merged view
		required = true
		for _, d := range ds {
			if !d.Required {
				required = false
				break
			}
		}
	}
	return required
}

// showsSchemaDefault reports whether the row previews fn.Default because the
// document has no value at path (missing or explicit null). An explicit value
// that happens to equal the default is user data, so it keeps its type color.
func (m *listModel) showsSchemaDefault(fn *smodel.FormNode, path []any) bool {
	if fn == nil || fn.Default == nil || fn.Default.Null {
		return false
	}
	v, ok := m.doc.Get(path)
	return !ok || v == nil || v.Null
}

// valueStyle picks the value-summary color: schema-default previews use a
// slightly brighter gray than the hint bar, unset faint, enums amber,
// booleans magenta, numbers green, strings cyan, containers bold.
func (m *listModel) valueStyle(fn *smodel.FormNode, path []any) lipgloss.Style {
	if m.showsSchemaDefault(fn, path) {
		return mValDefault
	}
	v, ok := m.doc.Get(path)
	if !ok || v == nil || v.Null {
		return mDim // "—" or the default preview
	}
	eff := fn
	if fn.Kind == smodel.KOneOf {
		if leaf := matchedLeafVariant(fn, v); leaf != nil {
			eff = leaf
		}
	}
	switch eff.Kind {
	case smodel.KEnum:
		return mValEnum
	case smodel.KBoolean:
		return mValBool
	case smodel.KObject, smodel.KArray, smodel.KMap, smodel.KOneOf:
		return mValObj
	default:
		switch {
		case v.Num != nil:
			return mValNum
		case v.Bool != nil:
			return mValBool
		default:
			return mValStr
		}
	}
}

// variantValueSummary is the short value hint shown next to the signature
// of the current variant.
func (m *listModel) variantValueSummary(v *smodel.FormNode, val *jsondoc.Node) string {
	if v.Kind == smodel.KOneOf {
		// nested variant group (classic picker): show the matched leaf's value
		if leaf := matchedLeafVariant(v, val); leaf != nil {
			return m.variantValueSummary(leaf, val)
		}
		return ""
	}
	switch v.Kind {
	case smodel.KObject, smodel.KMap:
		if val.Object != nil {
			return objectSummary(v, val)
		}
	case smodel.KArray:
		return arraySummary(val)
	case smodel.KEnum:
		enc := smodel.EncodeValue(val)
		for _, o := range v.Enum {
			if o.Encoded == enc {
				return o.Display
			}
		}
	default:
		if s := scalarText(val); s != "" {
			return valueClip(s)
		}
	}
	return ""
}

// enumMemberOn reports whether toggle row r's option is in the doc array.
func (m *listModel) enumMemberOn(r row) bool {
	cur, ok := m.doc.Get(r.path)
	if !ok || cur == nil || cur.Array == nil {
		return false
	}
	enc := r.fn.Items.Enum[r.idx].Encoded
	for _, it := range *cur.Array {
		if smodel.EncodeValue(it) == enc {
			return true
		}
	}
	return false
}

// clampStateCursor keeps st.cursor inside rows.
func clampStateCursor(st *menuState, rows []row) {
	if st == nil {
		return
	}
	max := len(rows) - 1
	if max < 0 {
		max = 0
	}
	if st.cursor > max {
		st.cursor = max
	}
	if st.cursor < 0 {
		st.cursor = 0
	}
}

func (m *listModel) clampCursor(rows []row) {
	clampStateCursor(m.top(), rows)
}

// --- overlay views -------------------------------------------------------

func (m *listModel) confirmView() string {
	hint := "y/enter/space yes · n/esc no"
	if m.confirm.hint != "" {
		hint = m.confirm.hint
	}
	return clipDisplay(mTitle.Render(m.confirm.title), m.width-1) + "\n\n" +
		clipDisplay(mHelp.Render(hint), m.width-1) + "\n"
}

// issuesView lists the load-time validation issues; enter jumps to the field.
func (m *listModel) issuesView() string {
	var b strings.Builder
	b.WriteString(clipDisplay(mTitle.Render(fmt.Sprintf("%s Validation issues (%d)", worstIssueIcon(m.issues.items), len(m.issues.items))), m.width-1))
	b.WriteByte('\n')
	if len(m.issues.items) == 0 {
		b.WriteString(mDim.Render("( no issues )"))
		b.WriteByte('\n')
		b.WriteString(m.recHint("esc close"))
		b.WriteByte('\n')
		return b.String()
	}
	height := m.height - 3
	if m.notice != "" {
		height-- // the notice renders as an extra row above the hints
	}
	if height < 1 {
		height = 1
	}
	if m.issues.cursor >= len(m.issues.items) {
		m.issues.cursor = len(m.issues.items) - 1
	}
	if m.issues.cursor < 0 {
		m.issues.cursor = 0
	}
	off := m.issues.cursor - height + 1
	if off < 0 {
		off = 0
	}
	for i := off; i < len(m.issues.items) && i < off+height; i++ {
		it := m.issues.items[i]
		line := "  " + m.severityStyle(it).Render(it.msg)
		if i == m.issues.cursor {
			line = mSel.Render("> " + it.msg)
		}
		b.WriteString(clipDisplay(line, m.width-1))
		b.WriteByte('\n')
	}
	if m.notice != "" {
		b.WriteString(clipDisplay(mErr.Render(clipRunes(m.notice, m.width)), m.width-1))
		b.WriteByte('\n')
	}
	b.WriteString(m.recHint("↑/↓ or ctrl+j/k move · enter jump to the field · esc close"))
	b.WriteByte('\n')
	return b.String()
}

// refPickView lists the references to one anchor (gr with several targets);
// enter jumps to the highlighted one.
func (m *listModel) refPickView() string {
	var b strings.Builder
	b.WriteString(clipDisplay(mTitle.Render(fmt.Sprintf("References to &%s (%d)", m.refPick.name, len(m.refPick.paths))), m.width-1))
	b.WriteByte('\n')
	height := m.height - 2
	if height < 1 {
		height = 1
	}
	if m.refPick.cursor >= len(m.refPick.paths) {
		m.refPick.cursor = len(m.refPick.paths) - 1
	}
	if m.refPick.cursor < 0 {
		m.refPick.cursor = 0
	}
	off := m.refPick.cursor - height + 1
	if off < 0 {
		off = 0
	}
	for i := off; i < len(m.refPick.paths) && i < off+height; i++ {
		p := m.refPick.paths[i]
		label := jsondoc.PathString(p) + "  " + m.rawValueSummary(p)
		line := "  " + label
		if i == m.refPick.cursor {
			line = mSel.Render("> " + label)
		}
		b.WriteString(clipDisplay(line, m.width-1))
		b.WriteByte('\n')
	}
	b.WriteString(m.recHint("↑/↓ or ctrl+j/k move · enter jump · esc close"))
	b.WriteByte('\n')
	return b.String()
}

func (m *listModel) errsView() string {
	var b strings.Builder
	b.WriteString(clipDisplay(mTitle.Render(icons.Err+" Cannot save: validation errors"), m.width-1))
	b.WriteByte('\n')
	height := m.height - 3
	if height < 1 {
		height = 1
	}
	if m.errs.cursor >= len(m.errs.lines) {
		m.errs.cursor = len(m.errs.lines) - 1
	}
	if m.errs.cursor < 0 {
		m.errs.cursor = 0
	}
	off := m.errs.cursor - height + 1
	if off < 0 {
		off = 0
	}
	for i := off; i < len(m.errs.lines) && i < off+height; i++ {
		line := "  " + m.errs.lines[i]
		if i == m.errs.cursor {
			line = mSel.Render("> " + m.errs.lines[i])
		}
		b.WriteString(clipDisplay(line, m.width-1))
		b.WriteByte('\n')
	}
	if m.opts.Force {
		b.WriteString(clipDisplay(mHelp.Render("enter jump to field · y save anyway · n back"), m.width-1))
	} else {
		b.WriteString(clipDisplay(mHelp.Render("enter jump to field · fix fields and save again, or rerun with --force"), m.width-1))
	}
	b.WriteByte('\n')
	return b.String()
}

func (m *listModel) textView() string {
	title := "Edit raw " + m.opts.rawLabel()
	if m.text.fn != nil && m.text.fn.Title != "" {
		title = m.text.fn.Title
	}
	var b strings.Builder
	b.WriteString(mTitle.Render(title))
	b.WriteByte('\n')
	b.WriteString(m.text.ta.View())
	b.WriteByte('\n')
	if m.text.err != "" {
		b.WriteString(mErr.Render(m.text.err))
		b.WriteByte('\n')
	}
	b.WriteString(mHelp.Render("ctrl+s apply · esc cancel"))
	b.WriteByte('\n')
	return b.String()
}

func (m *listModel) selView() string {
	var b strings.Builder
	curEnc := ""
	if v, ok := m.doc.Get(m.sel.path); ok && v != nil && !v.Null {
		curEnc = smodel.EncodeValue(v)
	}
	b.WriteString(clipDisplay(mTitle.Render(displayTitle(m.sel.fn)), m.width-1))
	b.WriteByte('\n')
	for i, o := range m.sel.opts {
		mark, markStyle := "○", mDim
		if o.Encoded == curEnc {
			mark, markStyle = "●", mAccent
		}
		line := cursorPrefix(i == m.sel.cursor) + markStyle.Render(mark) + " " + o.Display
		if i == m.sel.cursor {
			line = mSel.Render(line)
		}
		b.WriteString(clipDisplay(line, m.width-1))
		b.WriteByte('\n')
	}
	b.WriteString(clipDisplay(mHelp.Render(overlayHelp), m.width-1))
	b.WriteByte('\n')
	return b.String()
}

func pathEq(a, b []any) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// menuTitle is the title-line label of the current menu and, when the
// schema node has no title, the structural signature to show in the bottom
// metadata block instead. A menu without a schema title is labelled with the
// document key it edits — a map member, an array item, or the oneOf field a
// variant editor belongs to — rather than with a variant signature.
func (m *listModel) menuTitle(st *menuState) (title, summary string) {
	if st == nil {
		return "", ""
	}
	if st.node != nil && st.node.Title != "" {
		return st.node.Title, ""
	}
	// classic variant editors carry the oneOf field in oneOf, dynamic
	// all-object menus in variantOf: the field's label names the menu
	field := st.oneOf
	if field == nil {
		field = st.variantOf
	}
	switch {
	case field != nil && field.Title != "":
		title = field.Title
	case field != nil && field.Key != "":
		title = field.Key
	default:
		title = docKeyAt(st.path)
	}
	if title == "" {
		return displayTitle(st.node), "" // nothing to name it after
	}
	return title, VariantSignature(st.node)
}

// docKeyAt renders the document position of a menu path as a key label: the
// map/object key, or "key[i]" for an array item ("" for the root).
func docKeyAt(path []any) string {
	if len(path) == 0 {
		return ""
	}
	switch seg := path[len(path)-1].(type) {
	case string:
		return seg
	case int:
		if len(path) >= 2 {
			if key, ok := path[len(path)-2].(string); ok {
				return fmt.Sprintf("%s[%d]", key, seg)
			}
		}
		return fmt.Sprintf("[%d]", seg)
	}
	return ""
}
