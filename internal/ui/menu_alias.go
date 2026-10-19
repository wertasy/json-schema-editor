package ui

// YAML anchors and alias references in the menu TUI. An aliased value is not
// expanded into an independent copy while parsing: the menu at its path shows
// and edits the anchored original the reference points at (YAML reference
// semantics — one shared object behind every occurrence), and < Expand > /
// `e` materializes an independent copy at the reference's own path. Until
// then the document keeps its "*name" reference and Write restores it.
//
// Roles are derived from the document itself: anchored nodes and their
// references share one jsondoc node, so the first occurrence in document
// order is the anchor carrier ("&name" — exactly the node Write tags) and
// every other path holding the same node is a reference ("*name"). No
// format metadata is needed, which keeps renames automatic (they change the
// shared node's name) and survives undo snapshots.

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/wertasy/json-schema-editor/internal/jsondoc"
	"github.com/wertasy/json-schema-editor/internal/smodel"
)

// anchor input modes: & defines/renames the anchor on a value, * points the
// value at an existing anchor.
const (
	anchorSet = iota
	refSet
)

// anchorState is the name input behind & and *: enter commits, esc cancels,
// tab completes existing anchor names in reference mode.
type anchorState struct {
	path  []any
	mode  int
	fn    *smodel.FormNode // the row's schema node, for the required check
	ti    textinput.Model
	cands []string // reference mode: anchor names in document order
	sug   int      // completion cursor, -1 when nothing selected
	err   string
}

// anchorRole classifies the node at path: the anchor carrier ("&name", ok
// with ref=false) or a reference to one ("*name", ok with ref=true). ok is
// false when nothing there carries anchor identity.
func (m *listModel) anchorRole(path []any) (name string, ref bool, ok bool) {
	if m.doc == nil || len(path) == 0 {
		return "", false, false
	}
	n, exists := m.doc.Get(path)
	if !exists || n == nil || n.Anchor == "" {
		return "", false, false
	}
	holder := m.anchorHolderPath(n)
	return n.Anchor, !pathEq(holder, path), true
}

// aliasAt reports the anchor name when the node at path is a reference
// (a "*name" occurrence, not the anchor carrier).
func (m *listModel) aliasAt(path []any) (string, bool) {
	name, ref, ok := m.anchorRole(path)
	return name, ok && ref
}

// anchorHolderPath returns the first path in document order whose node is n
// (pointer identity) — the location Write tags with the anchor.
func (m *listModel) anchorHolderPath(n *jsondoc.Node) []any {
	var found []any
	var walk func(cur *jsondoc.Node, path []any) bool
	walk = func(cur *jsondoc.Node, path []any) bool {
		if cur == n {
			found = clonePath(path)
			return true
		}
		switch {
		case cur.Object != nil:
			for _, mem := range *cur.Object {
				if walk(mem.Val, pathFor(path, mem.Key)) {
					return true
				}
			}
		case cur.Array != nil:
			for i, it := range *cur.Array {
				if walk(it, pathIdx(path, i)) {
					return true
				}
			}
		}
		return false
	}
	if m.doc != nil {
		walk(m.doc, nil)
	}
	return found
}

// anchorDef is one anchor in document order: its name and carrier node.
type anchorDef struct {
	name string
	node *jsondoc.Node
}

// anchorDefs lists the document's anchors: every distinct anchor name with
// the first node carrying it, in document order.
func (m *listModel) anchorDefs() []anchorDef {
	var defs []anchorDef
	seen := map[string]bool{}
	var walk func(cur *jsondoc.Node)
	walk = func(cur *jsondoc.Node) {
		if cur == nil {
			return
		}
		if cur.Anchor != "" && !seen[cur.Anchor] {
			seen[cur.Anchor] = true
			defs = append(defs, anchorDef{cur.Anchor, cur})
		}
		switch {
		case cur.Object != nil:
			for _, mem := range *cur.Object {
				walk(mem.Val)
			}
		case cur.Array != nil:
			for _, it := range *cur.Array {
				walk(it)
			}
		}
	}
	walk(m.doc)
	return defs
}

// anchorHolder returns the first node carrying the anchor name, if any.
func (m *listModel) anchorHolder(name string) *jsondoc.Node {
	for _, d := range m.anchorDefs() {
		if d.name == name {
			return d.node
		}
	}
	return nil
}

// countAnchorUses counts how many document locations hold the node: the
// carrier plus every reference to it.
func (m *listModel) countAnchorUses(n *jsondoc.Node) int {
	count := 0
	var walk func(cur *jsondoc.Node)
	walk = func(cur *jsondoc.Node) {
		if cur == nil {
			return
		}
		if cur == n { // shared node: its subtree is visited once per reference
			count++
			return
		}
		switch {
		case cur.Object != nil:
			for _, mem := range *cur.Object {
				walk(mem.Val)
			}
		case cur.Array != nil:
			for _, it := range *cur.Array {
				walk(it)
			}
		}
	}
	walk(m.doc)
	return count
}

// expandAlias materializes the reference at path: the shared node is
// replaced by an independent copy with the anchor identity dropped, so
// further edits stay local to this path. The content itself is unchanged —
// and value-unchanged edits push no undo snapshot, so undoing past it
// restores the reference together with the earlier edits.
func (m *listModel) expandAlias(path []any) {
	n, ok := m.doc.Get(path)
	if !ok || n == nil {
		return
	}
	m.doc.Detach(path, n.CloneDetached())
	m.notice = "alias expanded: this value is now an independent copy"
}

// expandTarget resolves what `e` materializes: the row under the cursor when
// it is a reference, else the current menu when it drilled into one.
func (m *listModel) expandTarget(r row) ([]any, bool) {
	if (r.kind == rowField || r.kind == rowDrill || r.kind == rowUnknown) && len(r.path) > 0 {
		if _, ref, ok := m.anchorRole(r.path); ok && ref {
			return r.path, true
		}
	}
	if _, ref, ok := m.anchorRole(m.top().path); ok && ref {
		return m.top().path, true
	}
	return nil, false
}

// anchorBadge renders the "&name" / "*name" marker shown in the value
// column, aligned with the summary it precedes. Both use the anchor violet.
// "" when the row's value carries no anchor identity.
func (m *listModel) anchorBadge(path []any) string {
	name, ref, ok := m.anchorRole(path)
	if !ok {
		return ""
	}
	if ref {
		return mAnchor.Render("*"+name) + " "
	}
	return mAnchor.Render("&"+name) + " "
}

// openAnchor opens the name input behind & (anchorSet) and * (refSet) on the
// row under the cursor. The current name prefills the input — rename for
// anchors, retarget for references.
func (m *listModel) openAnchor(r row, mode int) {
	if r.kind != rowField && r.kind != rowDrill && r.kind != rowUnknown {
		m.notice = "& and * work on value rows"
		return
	}
	if len(r.path) == 0 {
		m.notice = "no value here to anchor"
		return
	}
	ti := textinput.New()
	ti.Prompt = ""
	ti.CharLimit = 0
	ti.Focus()
	name, ref, has := m.anchorRole(r.path)
	if (mode == refSet && ref) || (mode == anchorSet && has) {
		// prefill the current name: rename for anchors, retarget for refs
		ti.SetValue(name)
		ti.CursorEnd()
	}
	anc := &anchorState{path: clonePath(r.path), mode: mode, fn: r.fn,
		ti: ti, sug: -1}
	if mode == refSet {
		for _, d := range m.anchorDefs() {
			anc.cands = append(anc.cands, d.name)
		}
	}
	m.anc = anc
}

// keyAnchor drives the & / * name input.
func (m *listModel) keyAnchor(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "enter":
		m.anchorCommit()
		return nil
	case "esc":
		m.anc = nil
		return nil
	case "tab", "ctrl+n":
		m.cycleAnchorSug(1)
		return nil
	case "shift+tab", "ctrl+p":
		m.cycleAnchorSug(-1)
		return nil
	}
	m.anc.sug = -1
	var cmd tea.Cmd
	m.anc.ti, cmd = m.anc.ti.Update(msg)
	return cmd
}

// cycleAnchorSug moves the completion cursor over the anchor names and
// fills the input with the selection.
func (m *listModel) cycleAnchorSug(delta int) {
	anc := m.anc
	if anc.mode != refSet || len(anc.cands) == 0 {
		return
	}
	anc.sug += delta
	if anc.sug < 0 {
		anc.sug = len(anc.cands) - 1
	}
	if anc.sug >= len(anc.cands) {
		anc.sug = 0
	}
	anc.ti.SetValue(anc.cands[anc.sug])
	anc.ti.CursorEnd()
}

// anchorSugView renders the completion hint: every candidate, the selected
// one accented.
func (m *listModel) anchorSugView() string {
	parts := make([]string, len(m.anc.cands))
	for i, c := range m.anc.cands {
		if i == m.anc.sug {
			parts[i] = mAccent.Render(c)
			continue
		}
		parts[i] = c
	}
	return strings.Join(parts, " · ")
}

// anchorCommit applies the & / * input: anchors are set, renamed (references
// follow automatically — they share the node) or deleted (only when
// unreferenced); references retarget the value at an anchor or remove it.
func (m *listModel) anchorCommit() {
	name := strings.TrimSpace(m.anc.ti.Value())
	path := m.anc.path
	if m.anc.mode == anchorSet {
		m.commitAnchorSet(path, name)
		return
	}
	m.commitRefSet(path, name)
}

func (m *listModel) commitAnchorSet(path []any, name string) {
	n, ok := m.doc.Get(path)
	if !ok || n == nil {
		m.anc.err = "no value here"
		return
	}
	if name == "" {
		if n.Anchor == "" {
			m.anc.err = "this value has no anchor"
			return
		}
		if uses := m.countAnchorUses(n); uses > 1 {
			m.anc.err = fmt.Sprintf("&%s is referenced by %d alias(es) — remove them first", n.Anchor, uses-1)
			return
		}
		old := n.Anchor
		n.Anchor = ""
		m.anc = nil
		m.notice = "anchor &" + old + " removed"
		return
	}
	if holder := m.anchorHolder(name); holder != nil && holder != n {
		m.anc.err = fmt.Sprintf("anchor name &%s is already in use", name)
		return
	}
	renamed := n.Anchor != "" && n.Anchor != name
	n.Anchor = name
	m.anc = nil
	if renamed {
		m.notice = fmt.Sprintf("anchor renamed to &%s — references updated", name)
	} else {
		m.notice = "anchor set: &" + name
	}
}

func (m *listModel) commitRefSet(path []any, name string) {
	if name == "" {
		_, ref, ok := m.anchorRole(path)
		if !ok || !ref {
			m.anc.err = "this value is not a reference"
			return
		}
		if m.anc.fn != nil && m.anc.fn.Required {
			m.anc.err = m.anc.fn.Title + " is required"
			return
		}
		m.anc = nil
		m.doc.Remove(path)
		m.notice = "reference removed"
		return
	}
	target := m.anchorHolder(name)
	if target == nil {
		m.anc.err = "unknown anchor: &" + name
		return
	}
	if m.refWouldCycle(path, target) {
		m.anc.err = "circular reference: the anchor contains this field"
		return
	}
	m.anc = nil
	if !m.doc.Detach(path, target) {
		// the field holds no value yet: Detach only replaces existing
		// members, Set creates the slot (the reference lands as the value)
		m.doc.Set(path, target)
	}
	m.notice = "value references *" + name
}

// refWouldCycle reports whether pointing path at target would nest the
// anchor inside itself: the target must not be one of the value's ancestors
// (the value being replaced itself may become the target — that is fine).
func (m *listModel) refWouldCycle(path []any, target *jsondoc.Node) bool {
	for k := range path {
		n, ok := m.doc.Get(path[:k])
		if !ok {
			break
		}
		if n == target {
			return true
		}
	}
	return false
}
