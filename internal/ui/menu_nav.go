package ui

// Navigation history for the menu TUI: every relocation — drill-ins into
// submenus, level pops, and jumps (gg/G, ga/gr, search/issue/error jumps) —
// records the menu stack it leaves behind. ctrl+o walks back through that
// trail, ctrl+i forward (bubbletea reports ctrl+i as the tab key: same
// byte). Fresh forward navigation drops the not-yet-visited forward branch,
// like a browser.

import "github.com/wertasy/json-schema-editor/internal/jsondoc"

// maxNavHistory caps each direction of the navigation trail; the oldest
// entry is dropped first.
const maxNavHistory = 100

// navMark is one frozen menu stack: value copies, so cursors and offsets
// are snapshots, not live state.
type navMark struct {
	stack []menuState
}

// navSnapshot freezes the current menu stack.
func (m *listModel) navSnapshot() navMark {
	mk := navMark{stack: make([]menuState, len(m.stack))}
	for i, st := range m.stack {
		mk.stack[i] = *st
	}
	return mk
}

// sameMark reports whether two marks describe the same location: node
// identity per level plus the frozen cursor. Rebuilt dynamic menus may
// compare unequal for the same path, which only leaves a stale entry.
func sameMark(a, b navMark) bool {
	if len(a.stack) != len(b.stack) {
		return false
	}
	for i := range a.stack {
		x, y := a.stack[i], b.stack[i]
		if x.node != y.node || x.kind != y.kind || x.cursor != y.cursor ||
			x.variantOf != y.variantOf || x.oneOf != y.oneOf ||
			jsondoc.PathString(x.path) != jsondoc.PathString(y.path) {
			return false
		}
	}
	return true
}

// navTrailPush appends a mark, capping the trail.
func navTrailPush(trail *[]navMark, mk navMark) {
	*trail = append(*trail, mk)
	if len(*trail) > maxNavHistory {
		*trail = (*trail)[1:]
	}
}

// navForward records the location left behind by a forward relocation
// (drill-in, jump): it becomes a ctrl+o back step, and the ctrl+i forward
// branch dies like a browser's.
func (m *listModel) navForward() {
	navTrailPush(&m.navBack, m.navSnapshot())
	m.navFwd = nil
}

// navBackStep records the location a back motion (esc level pop, one-level
// return to a jump or search/issue origin) leaves behind as a ctrl+i
// forward step. When the landing is exactly the trail's back top — the
// common drill/esc cycle — that entry is consumed, so the cycle does not
// pile up dead locations. Call after the stack changed.
func (m *listModel) navBackStep(leaving navMark) {
	if n := len(m.navBack); n > 0 && sameMark(m.navBack[n-1], m.navSnapshot()) {
		m.navBack = m.navBack[:n-1]
	}
	navTrailPush(&m.navFwd, leaving)
}

// navGo moves one step along the navigation trail: back=true is ctrl+o
// (older), back=false is ctrl+i (newer). List mode only — overlays and
// inputs route keys elsewhere.
func (m *listModel) navGo(back bool) {
	leaving := m.navSnapshot()
	if back {
		if len(m.navBack) == 0 {
			m.notice = "no earlier location"
			return
		}
		mk := m.navBack[len(m.navBack)-1]
		m.navBack = m.navBack[:len(m.navBack)-1]
		navTrailPush(&m.navFwd, leaving)
		m.navRestore(mk)
		return
	}
	if len(m.navFwd) == 0 {
		m.notice = "no later location"
		return
	}
	mk := m.navFwd[len(m.navFwd)-1]
	m.navFwd = m.navFwd[:len(m.navFwd)-1]
	navTrailPush(&m.navBack, leaving)
	m.navRestore(mk)
}

// navRestore swaps in a frozen stack. One-level return parks are dropped:
// the restored location has no relation to the current landing, and levels
// the document no longer resolves (undo/redo shrank it meanwhile) are
// pruned like after an undo.
func (m *listModel) navRestore(mk navMark) {
	stack := make([]*menuState, len(mk.stack))
	for i := range mk.stack {
		st := mk.stack[i]
		stack[i] = &st
	}
	m.stack = stack
	m.jumpOrigin, m.jumpReturn = nil, 0
	m.search.parked = false
	m.issuesBack = nil
	m.repairStack()
}
