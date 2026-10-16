package ui

// The menuconfig-style default TUI: one persistent bubbletea program
// (breadcrumb + scrolling list + help bar) over the smodel form tree.
// Editing writes through to the document immediately; there is no
// per-screen form submit. huh remains only in the --accessible path.

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"json-schema-editor/internal/jsondoc"
	"json-schema-editor/internal/smodel"
)

const menuHelp = "↑/↓ · gg/G · enter edit/open · space · del delete · esc · ctrl+s save · / search · r replace · ctrl+c quit"
const overlayHelp = "enter select · esc cancel"

// maxMacroKeys caps a single recorded macro; reaching it stops recording and
// keeps the keys captured so far.
const maxMacroKeys = 500

// ggTimeout is how long a lone g stays armed waiting for its second press
// of the gg jump-to-top combo.
const ggTimeout = 750 * time.Millisecond

var (
	mTitle  = lipgloss.NewStyle().Bold(true)
	mCrumb  = lipgloss.NewStyle().Faint(true)
	mStatus = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	mStar   = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	mErr    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	mTail   = lipgloss.NewStyle().Faint(true)
	mHelp   = lipgloss.NewStyle().Faint(true).Foreground(lipgloss.AdaptiveColor{Light: "245", Dark: "240"})
	mDim    = lipgloss.NewStyle().Faint(true)

	// panel chrome and value colors (adaptive: legible on light and dark)
	mAccent  = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "27", Dark: "39"})
	mSel     = lipgloss.NewStyle().Bold(true).Background(lipgloss.AdaptiveColor{Light: "250", Dark: "236"})
	mValStr  = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "25", Dark: "51"})
	mValNum  = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "28", Dark: "114"})
	mValBool = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "127", Dark: "212"})
	mValEnum = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "166", Dark: "215"})
	mValObj  = lipgloss.NewStyle().Bold(true)
	mDiff    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "161", Dark: "204"}) // changed values
)

// editState is an in-place scalar input on one field row.
type editState struct {
	fn     *smodel.FormNode
	path   []any
	ti     textinput.Model
	err    string
	isNew  bool     // editing a not-yet-appended array item; esc writes nothing
	cands  []string // existing same-key values in the document (fixed at open)
	sugs   []string // cands filtered by the current input prefix
	sugIdx int      // tab cycle position (-1 = none)
}

// selState is the full-screen enum option picker.
type selState struct {
	fn     *smodel.FormNode
	path   []any
	opts   []smodel.EnumOpt
	cursor int
}

// textState is the raw-JSON textarea overlay (nil path = whole document).
type textState struct {
	path []any
	fn   *smodel.FormNode // nil at the root editor
	ta   textarea.Model
	err  string
}

// issuesState is the load-time validation issues overlay: each entry can jump
// to the offending field.
type issuesState struct {
	items  []issue
	cursor int
}

// issue is one validation message with its parsed instance location (nil when
// the message carries none).
type issue struct {
	msg  string
	path []any
}

// errsState is the save-time validation error overlay.
type errsState struct {
	lines  []string
	paths  [][]any
	cursor int
	exit   bool // end the session after the --force save (vs plain ctrl+s)
}

// confirmState is a y/n question overlay.
type confirmState struct {
	title string
	onYes func(m *listModel) tea.Cmd
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
	exiting       bool // a save requested the session to end (Save and exit row)
	edit          *editState
	sel           *selState
	text          *textState
	errs          *errsState
	confirm       *confirmState
	search        searchState
	menuCache     map[*smodel.FormNode]*smodel.FormNode
	notice        string        // transient list-mode message, cleared on the next key
	gArm          time.Time     // nonzero while a lone g is waiting for its gg partner
	recording     bool          // macro capture armed
	recBuf        []tea.KeyMsg  // keys captured so far
	macro         []tea.KeyMsg  // last completed macro
	baseline      *jsondoc.Node // loaded/last-saved snapshot for change marks
	issuesNow     []issue       // live validation problems (refreshed after writes)
	errPaths      [][]any       // issue locations, marked on their rows
	issues        *issuesState  // open issues overlay
	issuesBack    *issuesState  // parked overlay: esc at the jump target returns here
	issuesReturn  int           // stack depth of the jump target
}

// newMenuTextarea builds the raw-JSON editor sized for the current screen.
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
	m := &listModel{
		model:  model,
		doc:    doc,
		opts:   opts,
		width:  80,
		height: 24,
		stack:  []*menuState{{kind: menuKindFor(model), node: model}},
	}
	if doc != nil {
		m.baseline = jsondoc.FromAny(doc.ToAny())
	}
	m.setIssues(m.liveIssues(opts.LoadErrors))
	return m
}

// setIssues replaces the live validation problem list and its row marks.
func (m *listModel) setIssues(items []issue) {
	m.issuesNow = items
	m.errPaths = m.errPaths[:0]
	for _, it := range items {
		if it.path != nil {
			m.errPaths = append(m.errPaths, it.path)
		}
	}
}

// refreshIssues re-validates the document after a write so fixed problems
// leave the list, the counts and the row marks. Gated on a validator being
// available and problems being known — a clean document is not re-validated
// on every keypress (save-time validation remains the backstop; without a
// validator the load-time list stays as loaded).
func (m *listModel) refreshIssues() {
	if m.opts.Validate == nil || len(m.issuesNow) == 0 {
		return
	}
	m.setIssues(m.liveIssues(m.opts.Validate(m.doc)))
}

// liveIssues combines validator messages with the editor's unknown-key
// warnings for the live issue list. A key the validator already flags
// (e.g. additionalProperties) is not warned about twice.
func (m *listModel) liveIssues(msgs []string) []issue {
	items := issuesFrom(msgs)
	flagged := map[string]bool{}
	for _, it := range items {
		if it.path != nil {
			flagged[docPath(it.path)] = true
		}
	}
	for _, it := range unknownKeyIssues(m.model, m.doc) {
		if !flagged[docPath(it.path)] {
			items = append(items, it)
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
		out = append(out, issue{msg: s, path: path})
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
// refreshes (variant inference, live validation issues).
func (m *listModel) dispatchKey(msg tea.KeyMsg) tea.Cmd {
	var cmd tea.Cmd
	switch {
	case m.confirm != nil:
		cmd = m.keyConfirm(msg)
	case m.issues != nil:
		cmd = m.keyIssues(msg)
	case m.errs != nil:
		cmd = m.keyErrs(msg)
	case m.text != nil:
		cmd = m.keyText(msg)
	case m.sel != nil:
		cmd = m.keySel(msg)
	case m.search.active:
		cmd = m.keySearch(msg)
	case m.edit != nil:
		cmd = m.keyEdit(msg)
	default:
		cmd = m.keyList(msg)
	}
	m.refreshVariantMenu()
	m.refreshIssues()
	return cmd
}

func (m *listModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tea.KeyMsg:
		// ctrl+c always quits immediately without writing (contract).
		if msg.Type == tea.KeyCtrlC {
			m.aborted = true
			return m, tea.Quit
		}
		m.notice = ""
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
	case m.confirm != nil:
		return m.confirmView()
	case m.issues != nil:
		return m.issuesView()
	case m.errs != nil:
		return m.errsView()
	case m.text != nil:
		return m.textView()
	case m.sel != nil:
		return m.selView()
	}
	return m.listView()
}

// --- list rendering ------------------------------------------------------

// breadcrumb renders the navigation context as a jq-like document path
// ("$/a/b[0]"). Levels that do not extend the path — oneOf pickers and
// variant editors — add no segment (the title line names them). Overlong
// paths keep their tail, which is the informative end.
func (m *listModel) breadcrumb() string {
	st := m.top()
	if st == nil {
		return "$"
	}
	return clipLeft(docPath(st.path), m.width)
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
	b.WriteString(mTitle.Render(displayTitle(st.node)))
	if m.dirty() {
		b.WriteString(" " + mDiff.Render("●"))
	}
	b.WriteByte('\n')
	b.WriteString(mCrumb.Render(clipRunes(m.breadcrumb(), m.width)))
	b.WriteByte('\n')
	header := 2
	if len(m.stack) == 1 && m.opts.Status != "" {
		b.WriteString(mStatus.Render(clipRunes(m.opts.Status, m.width)))
		b.WriteByte('\n')
		header++
	}

	if m.search.active {
		h := m.height - header - 2 // search line + help line
		if m.search.replace {
			h--
		}
		m.writeSearchRows(&b, h)
		b.WriteString(mHelp.Render("search: ") + m.search.ti.View())
		b.WriteByte('\n')
		if m.search.replace {
			b.WriteString(mHelp.Render("replace: ") + m.search.rti.View())
			b.WriteByte('\n')
		}
		caseTxt := "case-insensitive"
		if !m.search.foldCase {
			caseTxt = "case-sensitive"
		}
		b.WriteString(mHelp.Render(clipRunes("enter jump/replace · tab replace · alt+i "+caseTxt+" · ctrl+r replace all · esc close", m.width)))
		b.WriteByte('\n')
		return b.String()
	}

	rows := menuRows(m)
	m.clampCursor(rows)
	h := m.height - header - 1
	if m.notice != "" {
		h--
	}
	m.writeRows(&b, rows, h)
	if m.notice != "" {
		b.WriteString(mErr.Render(clipRunes(m.notice, m.width)))
		b.WriteByte('\n')
	}
	b.WriteString(m.helpLine())
	b.WriteByte('\n')
	return b.String()
}

// helpLine renders the bottom hint row via recHint.
func (m *listModel) helpLine() string {
	return m.recHint(menuHelp)
}

// recHint renders a hint row; while a macro capture is running it keeps a
// right-aligned "● REC n" indicator instead of the hint tail, so arming q
// stays visible on every surface that renders hints.
func (m *listModel) recHint(hints string) string {
	rec := fmt.Sprintf("● REC %d", len(m.recBuf))
	w := lipgloss.Width(rec)
	if !m.recording || m.width < w+8 {
		return mHelp.Render(clipRunes(hints, m.width))
	}
	left := clipRunes(hints, m.width-w-1)
	pad := m.width - lipgloss.Width(left) - w
	if pad < 1 {
		pad = 1
	}
	return mHelp.Render(left+strings.Repeat(" ", pad)) + mErr.Render(rec)
}

// writeCut clips s to the terminal width line by line (multiline row content:
// in-place input + completion hint + error) and writes it out.
func (m *listModel) writeCut(b *strings.Builder, s string) {
	cut := lipgloss.NewStyle().MaxWidth(m.width)
	for _, line := range strings.Split(s, "\n") {
		b.WriteString(cut.Render(line))
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
	for i := st.offset; i < len(rows) && i < st.offset+height; i++ {
		m.writeCut(b, m.rowLine(rows[i], i == st.cursor))
	}
	// in-place input for a brand-new array item (row not in the list yet)
	if m.edit != nil && m.edit.isNew {
		m.writeCut(b, m.editLines(m.titleCol("New item")))
	}
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
// order ignored), arrays order-sensitively, scalars by exact text.
func nodesEqual(a, b *jsondoc.Node) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
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

// titleCol pads the left column to 24 cells.
func (m *listModel) titleCol(left string) string {
	if w := lipgloss.Width(left); w >= 24 {
		return left + " "
	} else {
		return left + strings.Repeat(" ", 24-w)
	}
}

func (m *listModel) rowLine(r row, cursor bool) string {
	// in-place input replaces the edited row: the field's own row, or the
	// oneOf field row when a leaf variant is being edited (the picker pops
	// back to the parent menu for leaf variants)
	if m.edit != nil && !m.edit.isNew && pathEq(r.path, m.edit.path) {
		switch {
		case r.kind == rowField && r.fn == m.edit.fn:
			return m.editLines(m.titleCol(r.fn.Title))
		case (r.kind == rowField || r.kind == rowDrill) && r.fn != nil &&
			r.fn.Kind == smodel.KOneOf && variantContains(r.fn, m.edit.fn):
			return m.editLines(m.titleCol(r.fn.Title))
		}
	}
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
	case rowAdd, rowSave, rowRaw, rowRemove:
		left = mAccent.Render(r.label)
	case rowNoItems:
		left = mDim.Render(r.label)
	case rowUnknown:
		// document key the schema does not define: edit as raw JSON or delete
		left = mErr.Render(r.label)
		v, _ := m.doc.Get(r.path)
		right = m.rawValueStyle(v).Render(m.rawValueSummary(r.path))
	case rowIssues:
		left = mErr.Render(r.label)
	default: // rowField / rowDrill
		title := r.fn.Title
		if m.fieldRequired(r) {
			title += mStar.Render("*")
		}
		left = title
		right = m.valueStyle(r.fn, r.path).Render(summary(m, r.fn, r.path))
	}
	content := m.titleCol(left) + right
	line := cursorPrefix(cursor) + content
	if r.kind == rowUnknown {
		line += " " + mDim.Render("(not in schema)")
	}
	if r.kind == rowDrill || r.kind == rowIssues {
		line += " " + mAccent.Render("--->")
	}
	if r.kind == rowField || r.kind == rowDrill || r.kind == rowUnknown {
		if m.hasIssue(r.path) {
			line += " " + mErr.Render("⚠")
		}
		line += m.changeMark(r.path)
	}
	if cursor {
		line = mSel.Render(line)
	}
	return line
}

// rawValueSummary renders a document value with no schema behind it (unknown
// keys): scalars as text, containers compactly.
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
		if s := objectSummary(v); s != "" {
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

// hasIssue reports whether path is a validation issue location or one of its
// ancestors (so containers light up too).
func (m *listModel) hasIssue(path []any) bool {
	for _, p := range m.errPaths {
		if isPathPrefix(path, p) {
			return true
		}
	}
	return false
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

// jumpToPath rebuilds the navigation stack down to path and puts the cursor on
// the row representing it (search-jump semantics).
func (m *listModel) jumpToPath(path []any) {
	m.stack = rebuildStack(m.model, m.doc, path)
	rows := menuRows(m)
	m.clampCursor(rows)
	m.top().cursor = cursorByPath(rows, path)
	m.top().offset = 0
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
// keeping the cursor position.
func (m *listModel) issuesReturnTo() {
	cur := m.issuesBack.cursor
	items := m.issuesBack.items
	if m.opts.Validate != nil {
		items = m.liveIssues(m.opts.Validate(m.doc))
		m.setIssues(items)
	}
	m.issuesBack = nil
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

// valueStyle picks the value-summary color: unset faint, enums amber,
// booleans magenta, numbers green, strings cyan, containers bold.
func (m *listModel) valueStyle(fn *smodel.FormNode, path []any) lipgloss.Style {
	v, ok := m.doc.Get(path)
	if !ok || v == nil || v.Null {
		return mDim // "—" or the default preview
	}
	eff := fn
	if fn.Kind == smodel.KOneOf {
		if idx := currentVariant(fn, v); idx >= 0 {
			eff = fn.Variants[idx]
		}
	}
	switch eff.Kind {
	case smodel.KEnum:
		return mValEnum
	case smodel.KBoolean:
		return mValBool
	case smodel.KObject, smodel.KArray, smodel.KOneOf:
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
	switch v.Kind {
	case smodel.KObject:
		if val.Object != nil {
			return objectSummary(val)
		}
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

func (m *listModel) clampCursor(rows []row) {
	st := m.top()
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

// --- overlay views -------------------------------------------------------

func (m *listModel) confirmView() string {
	return mTitle.Render(m.confirm.title) + "\n\n" +
		mHelp.Render("y yes · n no · esc no") + "\n"
}

// issuesView lists the load-time validation issues; enter jumps to the field.
func (m *listModel) issuesView() string {
	var b strings.Builder
	b.WriteString(mTitle.Render(fmt.Sprintf("⚠ Validation issues (%d)", len(m.issues.items))))
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
	cut := lipgloss.NewStyle().MaxWidth(m.width)
	for i := off; i < len(m.issues.items) && i < off+height; i++ {
		line := "  " + m.issues.items[i].msg
		if i == m.issues.cursor {
			line = mSel.Render("> " + m.issues.items[i].msg)
		}
		b.WriteString(cut.Render(line))
		b.WriteByte('\n')
	}
	if m.notice != "" {
		b.WriteString(mErr.Render(clipRunes(m.notice, m.width)))
		b.WriteByte('\n')
	}
	b.WriteString(m.recHint("↑/↓ move · enter jump to the field · esc close"))
	b.WriteByte('\n')
	return b.String()
}

func (m *listModel) errsView() string {
	var b strings.Builder
	b.WriteString(mTitle.Render("Cannot save: validation errors"))
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
	cut := lipgloss.NewStyle().MaxWidth(m.width)
	for i := off; i < len(m.errs.lines) && i < off+height; i++ {
		line := "  " + m.errs.lines[i]
		if i == m.errs.cursor {
			line = mSel.Render("> " + m.errs.lines[i])
		}
		b.WriteString(cut.Render(line))
		b.WriteByte('\n')
	}
	if m.opts.Force {
		b.WriteString(mHelp.Render("enter jump to field · y save anyway · n back"))
	} else {
		b.WriteString(mHelp.Render("enter jump to field · fix fields and save again, or rerun with --force"))
	}
	b.WriteByte('\n')
	return b.String()
}

func (m *listModel) textView() string {
	title := "Edit raw JSON"
	if m.text.fn != nil && m.text.fn.Title != "" {
		title = m.text.fn.Title
	}
	var b strings.Builder
	b.WriteString(mTitle.Render(title))
	b.WriteByte('\n')
	if m.text.fn == nil && len(m.text.path) > 0 {
		b.WriteString(mErr.Render("not in schema — edit the JSON, or esc and press delete to remove the field"))
		b.WriteByte('\n')
	}
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
	b.WriteString(mTitle.Render(displayTitle(m.sel.fn)))
	b.WriteByte('\n')
	cut := lipgloss.NewStyle().MaxWidth(m.width)
	for i, o := range m.sel.opts {
		mark, markStyle := "○", mDim
		if o.Encoded == curEnc {
			mark, markStyle = "●", mAccent
		}
		line := cursorPrefix(i == m.sel.cursor) + markStyle.Render(mark) + " " + o.Display
		if i == m.sel.cursor {
			line = mSel.Render(line)
		}
		b.WriteString(cut.Render(line))
		b.WriteByte('\n')
	}
	b.WriteString(mHelp.Render(overlayHelp))
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
