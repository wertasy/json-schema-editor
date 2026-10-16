package ui

// Key handling for the menuconfig TUI: list navigation, in-place editing,
// overlays (enum picker, raw JSON textarea, save errors, y/n confirm) and
// the full-tree search. Every edit writes through to the doc immediately.

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"json-schema-editor/internal/jsondoc"
	"json-schema-editor/internal/smodel"
)

// --- list mode -----------------------------------------------------------

func (m *listModel) keyList(msg tea.KeyMsg) tea.Cmd {
	st := m.top()
	rows := menuRows(m)
	m.clampCursor(rows)
	if len(rows) == 0 {
		return nil
	}
	r := rows[st.cursor]
	if m.jumpKeys(msg, &st.cursor, len(rows)) {
		return nil
	}
	switch msg.String() {
	case "up", "k":
		st.cursor = wrapCursor(st.cursor, -1, len(rows))
	case "down", "j":
		st.cursor = wrapCursor(st.cursor, 1, len(rows))
	case "enter", "right", "l":
		if (msg.String() == "right" || msg.String() == "l") && r.kind == rowField && r.fn.Kind == smodel.KEnum {
			m.cycleEnum(r, 1)
			return nil
		}
		return m.activate(r)
	case "left", "h":
		if r.kind == rowField && r.fn.Kind == smodel.KEnum {
			m.cycleEnum(r, -1)
			return nil
		}
		m.back()
	case "esc":
		m.back()
	case " ":
		return m.space(r)
	case "d":
		m.deleteItem(r)
	case "delete":
		m.deletePair(r)
	case "/":
		m.openSearch(false)
	case "r":
		m.openSearch(true)
	case "ctrl+s":
		return m.save()
	}
	return nil
}

// activate performs the enter action for a row.
func (m *listModel) activate(r row) tea.Cmd {
	switch r.kind {
	case rowField:
		m.openField(r.fn, r.path, false)
	case rowDrill:
		m.pushChildMenu(r.fn, r.path)
	case rowAdd:
		m.addItem()
	case rowRemove:
		path := clonePath(r.path)
		m.confirm = &confirmState{
			title: "Remove this item? (y/n)",
			onYes: func(m *listModel) tea.Cmd {
				m.doc.Remove(path)
				m.stack = m.stack[:len(m.stack)-1] // back to the items menu
				return nil
			},
		}
	case rowSave:
		return m.saveAndExit()
	case rowRaw:
		m.openRaw(nil)
	case rowVariant:
		return m.enterVariant(r)
	case rowToggle:
		m.toggleEnumMember(r)
	case rowUnknown:
		m.openRawAt(nil, r.path) // no schema: raw JSON editor
	case rowIssues:
		m.openIssues()
	}
	return nil
}

func (m *listModel) space(r row) tea.Cmd {
	switch {
	case r.kind == rowToggle:
		m.toggleEnumMember(r)
	case r.kind == rowField && r.fn.Kind == smodel.KBoolean:
		m.toggleBool(r.fn, r.path)
	case r.kind == rowVariant && r.variant.Kind == smodel.KBoolean:
		m.toggleBool(r.variant, r.path)
	default:
		return m.activate(r)
	}
	return nil
}

// back pops one menu level; on the root screen it asks for confirmation.
// At the depth a validation-issue jump landed on, it returns to the issue
// list instead (the overlay is parked while the user fixes the field).
func (m *listModel) back() {
	if m.issuesBack != nil && len(m.stack) == m.issuesReturn {
		m.issuesReturnTo()
		return
	}
	if len(m.stack) > 1 {
		m.stack = m.stack[:len(m.stack)-1]
		return
	}
	m.confirm = &confirmState{
		title: "Quit without saving? (y/n)",
		onYes: func(m *listModel) tea.Cmd {
			m.aborted = true
			return tea.Quit
		},
	}
}

func (m *listModel) push(fn *smodel.FormNode, path []any) {
	m.stack = append(m.stack, &menuState{kind: menuKindFor(fn), node: fn, path: path})
}

// pushChildMenu drills into a child node: all-object oneOfs open the
// dynamic merged/pinned item menu, everything else the classic menu kind.
func (m *listModel) pushChildMenu(fn *smodel.FormNode, path []any) {
	if fn.Kind == smodel.KOneOf && dynamicVariants(fn) != nil {
		m.stack = append(m.stack, m.dynamicMenu(fn, path))
		return
	}
	m.push(fn, path)
}

// wrapCursor advances cur by delta over n items, wrapping at both ends (a
// non-positive list stays at 0).
func wrapCursor(cur, delta, n int) int {
	if n <= 0 {
		return 0
	}
	c := (cur + delta) % n
	if c < 0 {
		c += n
	}
	return c
}

// jumpKeys applies the gg/G/Home/End list jumps shared by the menu list and
// the pure-list overlays (issues, save errors, enum select): G/End go to
// the last row, gg/Home to the first. A lone g arms the combo for ggTimeout
// and is otherwise inert; any other key disarms it. handled reports whether
// the key was consumed.
func (m *listModel) jumpKeys(msg tea.KeyMsg, cursor *int, n int) bool {
	armed := !m.gArm.IsZero() && time.Since(m.gArm) < ggTimeout
	m.gArm = time.Time{}
	switch msg.String() {
	case "g":
		if armed {
			*cursor = 0
			return true
		}
		m.gArm = time.Now()
		return true
	case "G", "end":
		*cursor = max(n-1, 0)
		return true
	case "home":
		*cursor = 0
		return true
	}
	return false
}

// --- macro recording / playback -------------------------------------------

// macroControl consumes the macro keys (q records, @ plays) before normal
// dispatch. Interception rules:
//   - in a text input (edit field, raw textarea, search) q/@ stay literal;
//   - while recording, q stops recording in every non-typing context
//     (overlays included) so capture can always be ended;
//   - while not recording, q/@ are intercepted in every non-typing context
//     (overlays included — none binds them), so recording can be armed from
//     anywhere, e.g. the validation-issues list (esc still closes it);
//   - @ while recording is swallowed and NOT recorded (macros therefore can
//     never contain macro keys, which makes playback recursion impossible).
func (m *listModel) macroControl(msg tea.KeyMsg) (tea.Cmd, bool) {
	if m.edit != nil || m.text != nil || m.search.active {
		return nil, false // typing: q/@ are ordinary characters
	}
	switch msg.String() {
	case "q":
		if m.recording {
			if len(m.recBuf) == 0 {
				m.recBuf, m.recording = nil, false // cancel: the previous macro survives
				m.notice = "recording cancelled (no keys captured)"
				return nil, true
			}
			m.macro, m.recBuf, m.recording = m.recBuf, nil, false
			m.notice = fmt.Sprintf("macro recorded: %d keys — @ plays it", len(m.macro))
			return nil, true
		}
		m.recBuf, m.recording = make([]tea.KeyMsg, 0, 16), true
		m.notice = "recording — q stops, @ is ignored while recording"
		return nil, true
	case "@":
		if m.recording {
			return nil, true // swallowed, not recorded
		}
		if len(m.macro) == 0 {
			m.notice = "no macro recorded (q records one)"
			return nil, true
		}
		return m.playMacro(), true
	}
	return nil, false
}

// playMacro replays the recorded keys through dispatchKey from the current
// state. It stops early when a key ends the session (abort or save-and-exit;
// plain ctrl+s only writes and playback continues) and returns the last
// command so tea.Quit still reaches bubbletea.
func (m *listModel) playMacro() tea.Cmd {
	var last tea.Cmd
	for i, k := range m.macro {
		last = m.dispatchKey(k)
		if m.aborted || m.exiting {
			m.notice = fmt.Sprintf("macro stopped at key %d/%d (session ended)", i+1, len(m.macro))
			return last
		}
	}
	m.notice = fmt.Sprintf("played %d keys", len(m.macro))
	return last
}

// --- field editing -------------------------------------------------------

// openField starts the appropriate editor for a field row (isNew marks the
// append-a-new-array-item flow where cancelling writes nothing).
func (m *listModel) openField(fn *smodel.FormNode, path []any, isNew bool) {
	switch fn.Kind {
	case smodel.KScalar:
		ti := textinput.New()
		ti.Prompt = ""
		ti.CharLimit = 0
		cur, _ := m.doc.Get(path)
		pre := scalarText(cur)
		if pre == "" && fn.Default != nil {
			pre = scalarText(fn.Default)
		}
		ti.SetValue(pre)
		ti.CursorEnd()
		ti.Focus()
		m.edit = &editState{fn: fn, path: path, ti: ti, isNew: isNew,
			cands: m.completions(fn, path)}
		m.refreshEditSuggestions()
	case smodel.KEnum:
		cur, _ := m.doc.Get(path)
		m.sel = &selState{fn: fn, path: path, opts: fn.Enum, cursor: enumIndex(fn, cur)}
	case smodel.KBoolean:
		m.toggleBool(fn, path)
	default: // KRaw / item-less arrays
		m.openRawAt(fn, path)
	}
}

func (m *listModel) openRaw(path []any) {
	m.openRawAt(nil, path)
}

func (m *listModel) openRawAt(fn *smodel.FormNode, path []any) {
	ta := newMenuTextarea(m)
	var cur *jsondoc.Node
	if path == nil {
		cur = m.doc
	} else {
		cur, _ = m.doc.Get(path)
	}
	if cur != nil && !cur.Null {
		if buf, err := json.Marshal(cur.ToAny()); err == nil {
			ta.SetValue(string(buf))
		}
	}
	ta.Focus()
	m.text = &textState{path: path, fn: fn, ta: ta}
}

// toggleBool flips a boolean field between its current/effective value and
// the opposite, writing through.
func (m *listModel) toggleBool(fn *smodel.FormNode, path []any) {
	cur, _ := m.doc.Get(path)
	b := false
	if cur != nil && cur.Bool != nil {
		b = *cur.Bool
	} else if fn.Default != nil && fn.Default.Bool != nil {
		b = *fn.Default.Bool
	}
	nb := !b
	m.doc.Set(path, &jsondoc.Node{Bool: &nb})
}

// cycleEnum steps an enum field through its options without opening the
// picker, writing the new value through.
func (m *listModel) cycleEnum(r row, delta int) {
	opts := r.fn.Enum
	if len(opts) == 0 {
		return
	}
	cur, _ := m.doc.Get(r.path)
	idx := enumIndex(r.fn, cur)
	next := (idx + delta + len(opts)) % len(opts)
	m.doc.Set(r.path, opts[next].Val)
}

func enumIndex(fn *smodel.FormNode, v *jsondoc.Node) int {
	enc := ""
	if v != nil && !v.Null {
		enc = smodel.EncodeValue(v)
	}
	if enc != "" {
		for i, o := range fn.Enum {
			if o.Encoded == enc {
				return i
			}
		}
	}
	if fn.Default != nil {
		d := smodel.EncodeValue(fn.Default)
		for i, o := range fn.Enum {
			if o.Encoded == d {
				return i
			}
		}
	}
	return 0
}

// --- arrays --------------------------------------------------------------

// addItem appends a new array item: object/oneOf/nested-array items drill
// into a fresh item menu, scalar and enum items open their editors aimed at
// the append position, booleans get their default appended.
func (m *listModel) addItem() {
	st := m.top()
	items := st.node.Items
	if items == nil {
		return
	}
	ipath := pathIdx(st.path, docArrayLen(m.doc, st.path))
	switch items.Kind {
	case smodel.KOneOf:
		if dynamicVariants(items) != nil {
			m.stack = append(m.stack, m.dynamicMenu(items, ipath))
			return
		}
		m.push(items, ipath)
	case smodel.KObject, smodel.KArray:
		m.push(items, ipath)
	case smodel.KBoolean:
		b := false
		if items.Default != nil && items.Default.Bool != nil {
			b = *items.Default.Bool
		}
		m.doc.Set(ipath, &jsondoc.Node{Bool: &b})
	default: // scalar / enum / raw item
		m.openField(items, ipath, true)
	}
}

// deletePair removes the selected key-value pair: array item rows fall back
// to the item-delete flow, optional fields remove their key, required fields
// refuse with a notice (mirroring the empty-input rule of in-place editing).
func (m *listModel) deletePair(r row) {
	st := m.top()
	if st == nil {
		return
	}
	if r.kind != rowField && r.kind != rowDrill && r.kind != rowUnknown {
		return
	}
	if len(r.path) == 0 {
		return
	}
	if _, isIdx := r.path[len(r.path)-1].(int); isIdx {
		m.deleteItem(r) // array item rows (any menu they appear in)
		return
	}
	if r.fn != nil && r.fn.Required {
		m.notice = r.fn.Title + " is required"
		return
	}
	m.doc.Remove(r.path)
}

// deleteItem removes an array entry row (no confirmation: the summary shows
// the result immediately and the item can be re-added).
func (m *listModel) deleteItem(r row) {
	st := m.top()
	if st.kind != menuItems || r.idx < 0 || (r.kind != rowField && r.kind != rowDrill) {
		return
	}
	m.doc.Remove(r.path)
}

// toggleEnumMember flips one option of an enum-valued array and rewrites
// the whole array (option order); empty + optional removes the key.
func (m *listModel) toggleEnumMember(r row) {
	fn := r.fn
	opts := fn.Items.Enum
	on := map[string]bool{}
	cur, _ := m.doc.Get(r.path)
	if cur != nil && cur.Array != nil {
		for _, it := range *cur.Array {
			on[smodel.EncodeValue(it)] = true
		}
	}
	enc := opts[r.idx].Encoded
	if on[enc] {
		delete(on, enc)
	} else {
		on[enc] = true
	}
	if len(on) == 0 && !fn.Required {
		m.doc.Remove(r.path)
		return
	}
	items := make([]*jsondoc.Node, 0, len(on))
	for _, o := range opts {
		if on[o.Encoded] {
			items = append(items, o.Val)
		}
	}
	m.doc.Set(r.path, &jsondoc.Node{Array: &items})
}

// --- oneOf ---------------------------------------------------------------

func (m *listModel) enterVariant(r row) tea.Cmd {
	st := m.top()
	v, hasV := m.doc.Get(r.path)
	cur := currentVariant(st.node, v)
	if cur >= 0 && st.node.Variants[cur] == r.variant {
		m.enterVariantEditor(r.variant, r.path)
		return nil
	}
	if hasV && v != nil && !v.Null {
		variant, path := r.variant, r.path
		m.confirm = &confirmState{
			title: "Replace current value? (y/n)",
			onYes: func(m *listModel) tea.Cmd {
				if variant.Default != nil && !variant.Default.Null {
					m.doc.Set(path, variant.Default)
				} else {
					m.doc.Remove(path)
				}
				m.enterVariantEditor(variant, path)
				return nil
			},
		}
		return nil
	}
	m.enterVariantEditor(r.variant, r.path)
	return nil
}

func (m *listModel) enterVariantEditor(variant *smodel.FormNode, path []any) {
	switch variant.Kind {
	case smodel.KObject, smodel.KArray:
		m.push(variant, path)
	case smodel.KBoolean:
		m.toggleBool(variant, path)
	case smodel.KEnum:
		cur, _ := m.doc.Get(path)
		m.sel = &selState{fn: variant, path: path, opts: variant.Enum, cursor: enumIndex(variant, cur)}
	default:
		// scalar / raw: leave the picker and edit at the field row of the
		// parent menu, like any other value
		if m.top() != nil && m.top().kind == menuOneOf && len(m.stack) > 1 {
			m.stack = m.stack[:len(m.stack)-1]
		}
		m.openField(variant, path, false)
	}
}

// --- save ----------------------------------------------------------------

// save handles ctrl+s: write the document and stay in the session. Change
// markers clear against the fresh baseline; a notice confirms the write.
func (m *listModel) save() tea.Cmd { return m.persist(false) }

// saveAndExit handles the root "Save and exit" row: write, then end the
// session.
func (m *listModel) saveAndExit() tea.Cmd { return m.persist(true) }

// persist validates first; validation errors open the overlay (with the
// --force y/n path remembering whether the session should end afterwards).
func (m *listModel) persist(exit bool) tea.Cmd {
	if m.opts.Validate != nil {
		if errs := m.opts.Validate(m.doc); len(errs) > 0 {
			const maxShown = 15
			shown, paths := []string{}, [][]any{}
			for i, e := range errs {
				if i == maxShown {
					shown = append(shown, fmt.Sprintf("… and %d more", len(errs)-maxShown))
					paths = append(paths, nil)
					break
				}
				shown = append(shown, e)
				p, ok := parseIssue(e)
				if !ok {
					p = nil
				}
				paths = append(paths, p)
			}
			m.errs = &errsState{lines: shown, paths: paths, exit: exit}
			return nil
		}
	}
	return m.write(exit, false)
}

// write persists through opts.Save. A failed write surfaces as a notice and
// the session continues; a successful one refreshes the change-mark baseline.
func (m *listModel) write(exit, forced bool) tea.Cmd {
	if m.opts.Save == nil {
		m.notice = "save failed: no save target"
		return nil
	}
	if err := m.opts.Save(m.doc); err != nil {
		m.notice = "save failed: " + err.Error()
		return nil
	}
	m.saved = true
	m.withErrors = forced
	m.baseline = jsondoc.FromAny(m.doc.ToAny()) // markers clear once saved
	if exit {
		m.exiting = true
		return tea.Quit
	}
	m.notice = "Saved"
	return nil
}

// --- overlay key handlers --------------------------------------------------

func (m *listModel) keyConfirm(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "y":
		onYes := m.confirm.onYes
		m.confirm = nil
		return onYes(m)
	case "n", "esc":
		m.confirm = nil
	}
	return nil
}

func (m *listModel) keyErrs(msg tea.KeyMsg) tea.Cmd {
	if m.jumpKeys(msg, &m.errs.cursor, len(m.errs.lines)) {
		return nil
	}
	switch msg.String() {
	case "up", "k":
		m.errs.cursor = wrapCursor(m.errs.cursor, -1, len(m.errs.lines))
	case "down", "j":
		m.errs.cursor = wrapCursor(m.errs.cursor, 1, len(m.errs.lines))
	case "enter":
		if m.errs.cursor < len(m.errs.paths) && m.errs.paths[m.errs.cursor] != nil {
			path := m.errs.paths[m.errs.cursor]
			m.errs = nil
			m.issuesBack = nil // a fresh jump replaces any parked issue list
			m.jumpToPath(path)
		}
	case "y":
		if m.opts.Force {
			exit := m.errs.exit
			m.errs = nil
			return m.write(exit, true)
		}
	case "n", "esc":
		m.errs = nil
	}
	return nil
}

// keyIssues drives the load-time issues overlay: enter jumps to the field the
// highlighted issue refers to.
func (m *listModel) keyIssues(msg tea.KeyMsg) tea.Cmd {
	if m.jumpKeys(msg, &m.issues.cursor, len(m.issues.items)) {
		return nil
	}
	switch msg.String() {
	case "up", "k":
		m.issues.cursor = wrapCursor(m.issues.cursor, -1, len(m.issues.items))
	case "down", "j":
		m.issues.cursor = wrapCursor(m.issues.cursor, 1, len(m.issues.items))
	case "enter":
		if m.issues.cursor < len(m.issues.items) {
			path := m.issues.items[m.issues.cursor].path
			if path != nil {
				parked := m.issues
				m.issues = nil
				m.jumpToPath(path)
				// esc at the landing menu returns to the list, same cursor
				m.issuesBack, m.issuesReturn = parked, len(m.stack)
			}
		}
	case "esc":
		m.issues = nil
	}
	return nil
}

func (m *listModel) keyText(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+s":
		return m.applyText()
	case "esc":
		m.text = nil
		return nil
	}
	var cmd tea.Cmd
	m.text.ta, cmd = m.text.ta.Update(msg)
	return cmd
}

func (m *listModel) applyText() tea.Cmd {
	s := strings.TrimSpace(m.text.ta.Value())
	if s == "" {
		if m.text.path == nil {
			m.text.err = "cannot be empty"
			return nil
		}
		m.doc.Remove(m.text.path)
		m.text = nil
		return nil
	}
	fn := m.text.fn
	if fn == nil {
		fn = &smodel.FormNode{Title: "Edit raw JSON"}
	}
	if err := ValidateJSON(fn, s); err != nil {
		m.text.err = err.Error()
		return nil
	}
	n, err := jsondoc.Parse(strings.NewReader(s))
	if err != nil {
		m.text.err = err.Error()
		return nil
	}
	if m.text.path == nil {
		*m.doc = *n
	} else {
		m.doc.Set(m.text.path, n)
	}
	m.text = nil
	return nil
}

func (m *listModel) keySel(msg tea.KeyMsg) tea.Cmd {
	if m.jumpKeys(msg, &m.sel.cursor, len(m.sel.opts)) {
		return nil
	}
	switch msg.String() {
	case "up", "k":
		m.sel.cursor = wrapCursor(m.sel.cursor, -1, len(m.sel.opts))
	case "down", "j":
		m.sel.cursor = wrapCursor(m.sel.cursor, 1, len(m.sel.opts))
	case "enter":
		m.doc.Set(m.sel.path, m.sel.opts[m.sel.cursor].Val)
		m.sel = nil
	case "esc":
		m.sel = nil
	}
	return nil
}

func (m *listModel) keyEdit(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "enter":
		if err := applyScalar(m.doc, m.edit.fn, m.edit.path, m.edit.ti.Value()); err != nil {
			m.edit.err = err.Error()
			return nil
		}
		m.edit = nil
		return nil
	case "esc":
		m.edit = nil
		return nil
	case "tab":
		// cycle through the suggestions computed for the last typed prefix
		e := m.edit
		if len(e.sugs) == 0 {
			return nil
		}
		e.sugIdx = (e.sugIdx + 1) % len(e.sugs)
		e.ti.SetValue(e.sugs[e.sugIdx])
		e.ti.CursorEnd()
		return nil
	}
	var cmd tea.Cmd
	m.edit.ti, cmd = m.edit.ti.Update(msg)
	m.refreshEditSuggestions()
	return cmd
}

// --- value completion ------------------------------------------------------

// completions collects the candidate values for fn at path: for
// x-tag-reference fields the tag values of the referenced structures, else the
// existing same-key strings elsewhere in the document.
func (m *listModel) completions(fn *smodel.FormNode, path []any) []string {
	if fn.Kind == smodel.KScalar && fn.TagRef != "" {
		return m.tagRefCompletions(fn.TagRef)
	}
	if fn.Kind != smodel.KScalar || (fn.JSONType != "string" && fn.JSONType != "") {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	var walk func(n *jsondoc.Node, p []any)
	walk = func(n *jsondoc.Node, p []any) {
		if n == nil {
			return
		}
		if n.Object != nil {
			for _, mem := range *n.Object {
				mp := pathFor(p, mem.Key)
				if mem.Key == fn.Key && mem.Val != nil && mem.Val.Str != nil && !pathEq(mp, path) {
					v := *mem.Val.Str
					if v != "" && !seen[v] {
						seen[v] = true
						out = append(out, v)
					}
				}
				walk(mem.Val, mp)
			}
			return
		}
		if n.Array != nil {
			for i, it := range *n.Array {
				walk(it, pathIdx(p, i))
			}
		}
	}
	walk(m.doc, nil)
	return out
}

// tagRefCompletions collects the "tag" values of the document structures the
// reference domain names (its array form, e.g. http_client → http_clients,
// and the plain form), anywhere in the document, in document order.
func (m *listModel) tagRefCompletions(ref string) []string {
	names := map[string]bool{ref: true, ref + "s": true}
	var out []string
	seen := map[string]bool{}
	var collect func(n *jsondoc.Node)
	collect = func(n *jsondoc.Node) {
		if n == nil {
			return
		}
		if n.Array != nil {
			for _, it := range *n.Array {
				collect(it)
			}
			return
		}
		if tag := tagOfNode(n); tag != "" && !seen[tag] {
			seen[tag] = true
			out = append(out, tag)
		}
	}
	var walk func(n *jsondoc.Node)
	walk = func(n *jsondoc.Node) {
		switch {
		case n == nil:
			return
		case n.Object != nil:
			for _, mem := range *n.Object {
				if names[mem.Key] {
					collect(mem.Val)
				}
				walk(mem.Val)
			}
		case n.Array != nil:
			for _, it := range *n.Array {
				walk(it)
			}
		}
	}
	walk(m.doc)
	return out
}

// tagOfNode returns an object's "tag" string value.
func tagOfNode(n *jsondoc.Node) string {
	if n == nil || n.Object == nil {
		return ""
	}
	for _, mem := range *n.Object {
		if mem.Key == "tag" && mem.Val != nil && mem.Val.Str != nil {
			return *mem.Val.Str
		}
	}
	return ""
}

// refreshEditSuggestions re-filters the candidates by the current input and
// resets the tab cycle.
func (m *listModel) refreshEditSuggestions() {
	if m.edit == nil {
		return
	}
	m.edit.sugs = filterCompletions(m.edit.cands, m.edit.ti.Value())
	m.edit.sugIdx = -1
}

// filterCompletions keeps candidates starting with prefix (case-insensitive);
// an empty prefix keeps everything.
func filterCompletions(cands []string, prefix string) []string {
	var out []string
	lp := strings.ToLower(prefix)
	for _, c := range cands {
		if lp == "" || strings.HasPrefix(strings.ToLower(c), lp) {
			out = append(out, c)
		}
	}
	return out
}

// editLines renders the in-place input line plus its completion hint and
// validation error, sharing the layout with the plain field row.
func (m *listModel) editLines(left string) string {
	line := cursorPrefix(true) + left + m.edit.ti.View()
	if hint := m.suggestionHint(); hint != "" {
		line += "\n" + hint
	}
	if m.edit.err != "" {
		line += "\n" + mErr.Render("  "+m.edit.err)
	}
	return line
}

// suggestionHint renders the completion hint line: a window of up to three
// candidates that scrolls with the tab selection (ellipses mark the hidden
// ones on either side).
func (m *listModel) suggestionHint() string {
	if len(m.edit.sugs) == 0 {
		return ""
	}
	const window = 3
	start := 0
	if m.edit.sugIdx >= 0 {
		start = m.edit.sugIdx - (window - 1)
		if max := len(m.edit.sugs) - window; start > max {
			start = max
		}
		if start < 0 {
			start = 0
		}
	}
	parts := make([]string, 0, window+2)
	if start > 0 {
		parts = append(parts, "…")
	}
	for i := start; i < len(m.edit.sugs) && i < start+window; i++ {
		if i == m.edit.sugIdx {
			parts = append(parts, mAccent.Render(m.edit.sugs[i]))
		} else {
			parts = append(parts, m.edit.sugs[i])
		}
	}
	if start+window < len(m.edit.sugs) {
		parts = append(parts, "…")
	}
	return mHelp.Render("  tab: ") + strings.Join(parts, mHelp.Render(" · "))
}

// --- search ----------------------------------------------------------------

// openSearch arms the scoped search overlay; replace=true shows the replace
// input and projects string-value replacement previews from the start.
func (m *listModel) openSearch(replace bool) {
	m.search.active = true
	m.search.replace = replace
	m.search.foldCase = true
	m.search.all = searchIndex(m, m.top())
	ti := textinput.New()
	ti.Prompt = ""
	ti.Focus()
	m.search.ti = ti
	rti := textinput.New()
	rti.Prompt = ""
	m.search.rti = rti
	if replace {
		m.refilter()
	} else {
		m.search.results = m.search.all
	}
	m.search.cursor = 0
}

// closeSearch tears the overlay down (enter-jump, esc, replace-all).
func (m *listModel) closeSearch() {
	m.search.active = false
	m.search.replace = false
	m.search.results = nil
	m.search.all = nil
}

// refilter recomputes the result list from the current inputs: replace mode
// projects string-value hits with their replacement preview, jump mode keeps
// the title/key/value filter. Both honor the case-fold toggle.
func (m *listModel) refilter() {
	q := m.search.ti.Value()
	if m.search.replace {
		m.search.results = replaceHits(m, m.search.all, q, m.search.rti.Value(), m.search.foldCase)
	} else {
		m.search.results = filterSearch(m, m.search.all, q, m.search.foldCase)
	}
	m.search.cursor = 0
}

func (m *listModel) keySearch(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "up":
		m.search.cursor = wrapCursor(m.search.cursor, -1, len(m.search.results))
		return nil
	case "down":
		m.search.cursor = wrapCursor(m.search.cursor, 1, len(m.search.results))
		return nil
	case "tab", "shift+tab":
		m.search.replace = true
		if m.search.rti.Focused() {
			m.search.rti.Blur()
			m.search.ti.Focus()
		} else {
			m.search.ti.Blur()
			m.search.rti.Focus()
		}
		m.refilter()
		return nil
	case "alt+i":
		m.search.foldCase = !m.search.foldCase
		m.refilter()
		return nil
	case "ctrl+r":
		if m.search.replace {
			return m.replaceSearchAll()
		}
	case "enter":
		if m.search.replace && m.search.rti.Focused() {
			return m.replaceSearchCurrent()
		}
		if len(m.search.results) == 0 {
			return nil
		}
		res := m.search.results[m.search.cursor]
		m.closeSearch()
		m.issuesBack = nil // a fresh jump replaces any parked issue list
		m.stack = rebuildStack(m.model, m.doc, res.path)
		rows := menuRows(m)
		m.clampCursor(rows)
		m.top().cursor = cursorForRow(rows, res)
		m.top().offset = 0
		return nil
	case "esc":
		m.closeSearch()
		return nil
	}
	var cmd tea.Cmd
	if m.search.rti.Focused() {
		m.search.rti, cmd = m.search.rti.Update(msg)
	} else {
		m.search.ti, cmd = m.search.ti.Update(msg)
	}
	m.refilter()
	return cmd
}

// replaceSearchCurrent rewrites the highlighted hit's string value and
// keeps the cursor on the next surviving hit.
func (m *listModel) replaceSearchCurrent() tea.Cmd {
	q := m.search.ti.Value()
	if q == "" || len(m.search.results) == 0 {
		return nil
	}
	res := m.search.results[m.search.cursor]
	n, _ := m.doc.Get(res.path)
	if n == nil || n.Str == nil {
		return nil
	}
	s, c := replaceAll(*n.Str, q, m.search.rti.Value(), m.search.foldCase)
	if c == 0 {
		return nil
	}
	m.doc.Set(res.path, &jsondoc.Node{Str: &s})
	cur := m.search.cursor
	m.refilter()
	if cur >= len(m.search.results) {
		cur = max(len(m.search.results)-1, 0)
	}
	m.search.cursor = cur
	return nil
}

// replaceSearchAll rewrites every occurrence of the query in every string
// value of the scoped index (case per foldCase), closes the overlay and
// leaves a count notice.
func (m *listModel) replaceSearchAll() tea.Cmd {
	q := m.search.ti.Value()
	if q == "" || len(m.search.results) == 0 {
		return nil
	}
	occ, vals := 0, 0
	for _, r := range m.search.results {
		n, _ := m.doc.Get(r.path)
		if n == nil || n.Str == nil {
			continue
		}
		s, c := replaceAll(*n.Str, q, m.search.rti.Value(), m.search.foldCase)
		if c == 0 {
			continue
		}
		m.doc.Set(r.path, &jsondoc.Node{Str: &s})
		occ += c
		vals++
	}
	m.closeSearch()
	m.notice = fmt.Sprintf("replaced %d occurrence(s) in %d value(s)", occ, vals)
	return nil
}

// cursorForRow finds the row matching a search hit (same schema node at the
// same path, or the same variant); 0 when the target is a container whose
// menu we entered.
func cursorForRow(rows []row, res row) int {
	for i, r := range rows {
		if r.kind != rowVariant && res.kind != rowVariant &&
			r.fn == res.fn && pathEq(r.path, res.path) {
			return i
		}
		if r.variant != nil && res.variant != nil && r.variant == res.variant {
			return i
		}
	}
	return 0
}
