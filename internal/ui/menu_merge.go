package ui

// YAML merge keys ("<<: *a" / "<<: [*a, *b]") in the menu TUI. A mapping's
// menu lists the merged keys after its own, each carrying the anchor it came
// from; a merged row's path points into the anchor's own object, so viewing
// and editing reach the source mapping (YAML merge semantics — every mapping
// merging the anchor sees the write). E materializes one merged key into the
// current mapping as an independent copy that shadows the merged value, and
// `<` edits the mapping's merge list itself: an input below the cursor with
// tab completion over the document's mapping anchors, space-separated, one
// enter to apply (empty clears the merges).

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/wertasy/json-schema-editor/internal/jsondoc"
	"github.com/wertasy/json-schema-editor/internal/smodel"
)

// mergeSource is one anchor a mapping merges: its name (badge), the document
// path of the anchor carrier (breadcrumb target), and the anchored mapping.
type mergeSource struct {
	name string
	path []any
	node *jsondoc.Node
}

// mergeSourceFor resolves one merge reference into its source. Unreachable
// references (their anchor was deleted) drop out; Write still refuses to
// save such a document.
func (m *listModel) mergeSourceFor(ref *jsondoc.Node) (mergeSource, bool) {
	if ref == nil || ref.Anchor == "" || ref.Object == nil {
		return mergeSource{}, false
	}
	holder := m.anchorHolderPath(ref)
	if holder == nil {
		return mergeSource{}, false
	}
	return mergeSource{name: ref.Anchor, path: holder, node: ref}, true
}

// mergedKey is one key a mapping's merge pulls in.
type mergedKey struct {
	src  mergeSource
	key  string
	path []any // src.path + key: inside the anchor's own object
}

// mergedKeys lists the merged keys of the mapping at st.path in merge order
// with YAML precedence applied: keys the mapping defines itself, and keys an
// earlier anchor already provided, shadow the later ones.
func (m *listModel) mergedKeys(st *menuState) []mergedKey {
	if m.doc == nil || st == nil {
		return nil
	}
	n, ok := m.doc.Get(st.path)
	if !ok || n == nil || n.Object == nil {
		return nil
	}
	seen := map[string]bool{}
	for _, mem := range *n.Object {
		if mem.Val != nil && mem.Val.Merge != nil {
			continue
		}
		seen[mem.Key] = true
	}
	var out []mergedKey
	for _, ref := range n.MergeRefs() {
		src, ok := m.mergeSourceFor(ref)
		if !ok {
			continue
		}
		// the anchor's resolved members: its own keys plus whatever its own
		// "<<" chain provides, with the anchor's own keys winning
		for _, mem := range src.node.ResolvedMembers() {
			if seen[mem.Key] {
				continue
			}
			seen[mem.Key] = true
			// inherited keys live in the mapping that actually defines
			// them: the badge, the breadcrumb and edits follow that source
			holder, ok := m.mergeDefiningSource(src, mem.Key, map[*jsondoc.Node]bool{})
			if !ok {
				holder = src
			}
			out = append(out, mergedKey{src: holder, key: mem.Key,
				path: pathFor(holder.path, mem.Key)})
		}
	}
	return out
}

// mergeDefiningSource finds the mapping that defines key for the merge
// source src: the mapping itself when it defines it, else the first mapping
// its own merge keys pull the key from (YAML precedence: own keys win,
// earlier anchors before later ones).
func (m *listModel) mergeDefiningSource(src mergeSource, key string, busy map[*jsondoc.Node]bool) (mergeSource, bool) {
	if src.node == nil || src.node.Object == nil || busy[src.node] {
		return mergeSource{}, false
	}
	busy[src.node] = true
	defer delete(busy, src.node)
	for _, mem := range *src.node.Object {
		if mem.Val != nil && mem.Val.Merge != nil {
			continue
		}
		if mem.Key == key {
			return src, true
		}
	}
	for _, ref := range src.node.MergeRefs() {
		sub, ok := m.mergeSourceFor(ref)
		if !ok {
			continue
		}
		if got, ok := m.mergeDefiningSource(sub, key, busy); ok {
			return got, true
		}
	}
	return mergeSource{}, false
}

// mergedByKey indexes the menu's merged keys by document key.
func mergedByKey(keys []mergedKey) map[string]mergedKey {
	if len(keys) == 0 {
		return nil
	}
	by := make(map[string]mergedKey, len(keys))
	for _, mk := range keys {
		by[mk.key] = mk
	}
	return by
}

// mergedRow builds the menu row of one merged key against the menu's schema
// node: the schema child when it defines the key, else a schema-less row.
// The path points into the anchor's object; the anchor name rides the row
// for the value badge and the breadcrumb.
func mergedRow(st *menuState, mk mergedKey) row {
	var r row
	if st.node != nil && st.node.Kind == smodel.KMap {
		r = mapItemRow(st.node, mk.src.path, mk.key)
	} else {
		fn := childSchemaByKey(st.node, mk.key)
		switch {
		case fn == nil:
			r = row{kind: rowUnknown, idx: -1, path: clonePath(mk.path), label: mk.key}
		case menuLeaf(fn):
			r = row{kind: rowField, fn: fn, path: clonePath(mk.path), idx: -1, label: mk.key}
		default:
			r = row{kind: rowDrill, fn: fn, path: clonePath(mk.path), idx: -1, label: mk.key}
		}
	}
	r.merged = mk.src.name
	return r
}

// childSchemaByKey finds the object child schema defining key, nil when the
// schema leaves the key undefined.
func childSchemaByKey(node *smodel.FormNode, key string) *smodel.FormNode {
	if node == nil {
		return nil
	}
	for _, c := range node.Children {
		if c.Key == key {
			return c
		}
	}
	return nil
}

// mergeNames renders the anchor list a mapping merges for its menu title
// ("<<: *base1 *base2"), "" when it merges nothing.
func (m *listModel) mergeNames(path []any) string {
	if m.doc == nil {
		return ""
	}
	n, ok := m.doc.Get(path)
	if !ok || n == nil {
		return ""
	}
	var parts []string
	for _, ref := range n.MergeRefs() {
		if ref != nil && ref.Anchor != "" {
			parts = append(parts, "*"+ref.Anchor)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "<<: " + strings.Join(parts, " ")
}

// mergeBadge renders the "*anchor" provenance marker in front of a merged
// row's value summary, in the anchor violet like the alias badges.
func mergeBadge(r row) string {
	if r.merged == "" {
		return ""
	}
	return mAnchor.Render("*"+r.merged) + " "
}

// mergeCrumb returns the anchor path the breadcrumb appends after its arrow
// while the cursor rests on a merged key, nil otherwise.
func (m *listModel) mergeCrumb() []any {
	st := m.top()
	if st == nil {
		return nil
	}
	rows := menuRows(m)
	if st.cursor < 0 || st.cursor >= len(rows) {
		return nil
	}
	r := rows[st.cursor]
	if r.merged == "" || len(r.path) == 0 {
		return nil
	}
	return r.path[:len(r.path)-1]
}

// --- the `<` merge-keys input ---------------------------------------------

// mergeState is the input behind `<`: anchor names, space-separated; tab
// completes the token being typed over the document's mapping anchors.
type mergeState struct {
	path  []any
	ti    textinput.Model
	cands []string // mapping anchor names in document order (fixed at open)
	sug   int      // completion cursor over the filtered candidates, -1 none
	err   string
}

// openMerge opens the merge-keys input for the current mapping menu,
// prefilled with the anchors it merges (retarget, reorder or clear them).
func (m *listModel) openMerge() {
	st := m.top()
	if st == nil || st.kind != menuObject ||
		(st.node.Kind != smodel.KObject && st.node.Kind != smodel.KMap) {
		m.notice = "< edits the merge keys of a mapping menu"
		return
	}
	// an unset or null mapping is fine: it is created on commit. Only a
	// value of another kind refuses (converting it would lose data).
	cur, ok := m.doc.Get(st.path)
	if ok && cur != nil && !cur.Null && cur.Object == nil {
		m.notice = "no mapping here to merge into"
		return
	}
	ti := textinput.New()
	ti.Prompt = ""
	ti.CharLimit = 0
	ti.Focus()
	var pre []string
	for _, ref := range cur.MergeRefs() {
		if ref != nil && ref.Anchor != "" {
			pre = append(pre, ref.Anchor)
		}
	}
	if len(pre) > 0 {
		ti.SetValue(strings.Join(pre, " "))
		ti.CursorEnd()
	}
	ms := &mergeState{path: clonePath(st.path), ti: ti, sug: -1}
	// Completion offers only mapping anchors that can legally merge here:
	// the value must be a mapping (type), and the merge must not close a
	// cycle (the mapping itself or an anchor whose chain reaches it).
	for _, d := range m.anchorDefs() {
		if d.node == nil || d.node.Object == nil {
			continue
		}
		if m.mergeWouldCycle(st.path, d.node) {
			continue
		}
		ms.cands = append(ms.cands, d.name)
	}
	m.mrg = ms
}

// keyMerge drives the merge-keys input.
func (m *listModel) keyMerge(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "enter":
		m.mergeCommit()
		return nil
	case "esc":
		m.mrg = nil
		return nil
	case "tab", "ctrl+n":
		m.cycleMergeSug(1)
		return nil
	case "shift+tab", "ctrl+p":
		m.cycleMergeSug(-1)
		return nil
	}
	m.mrg.sug = -1
	var cmd tea.Cmd
	m.mrg.ti, cmd = m.mrg.ti.Update(msg)
	return cmd
}

// mergeFiltered lists the candidates for the token being typed: anchors
// already listed before it are skipped, so tab walks the not-yet-merged
// ones — one anchor, then the next, without duplicates.
func (ms *mergeState) mergeFiltered() []string {
	rest, last := splitLastToken(ms.ti.Value())
	_ = rest
	used := map[string]bool{}
	for _, t := range mergeTokenList(rest) {
		used[t] = true
	}
	last = strings.ToLower(strings.TrimPrefix(last, "*"))
	var out []string
	for _, c := range ms.cands {
		if used[c] {
			continue
		}
		if last == "" || strings.HasPrefix(strings.ToLower(c), last) {
			out = append(out, c)
		}
	}
	return out
}

// isMergeSeparator reports whether r separates merge-key tokens: spaces and
// commas (both the "a b" and "[*a, *b]" reading work).
func isMergeSeparator(r rune) bool { return r == ' ' || r == ',' || r == '\t' }

// splitLastToken splits the input into everything up to the final token and
// that token, keeping the separator before it in rest.
func splitLastToken(v string) (string, string) {
	if i := strings.LastIndexFunc(v, isMergeSeparator); i >= 0 {
		return v[:i+1], v[i+1:]
	}
	return "", v
}

// mergeTokenList splits an input fragment into bare anchor names: tokens
// separated by spaces or commas, an optional "*" prefix stripped, empty
// tokens dropped.
func mergeTokenList(v string) []string {
	var out []string
	for _, t := range strings.FieldsFunc(v, isMergeSeparator) {
		t = strings.TrimPrefix(strings.TrimSpace(t), "*")
		if t != "" {
			out = append(out, t)
		}
	}
	return out
}

// cycleMergeSug moves the completion cursor over the filtered candidates
// and fills the token being typed with the selection.
func (m *listModel) cycleMergeSug(delta int) {
	ms := m.mrg
	cands := ms.mergeFiltered()
	if len(cands) == 0 {
		return
	}
	ms.sug += delta
	if ms.sug < 0 {
		ms.sug = len(cands) - 1
	}
	if ms.sug >= len(cands) {
		ms.sug = 0
	}
	rest, _ := splitLastToken(ms.ti.Value())
	ms.ti.SetValue(rest + cands[ms.sug] + " ")
	ms.ti.CursorEnd()
}

// mergeSugView renders the completion hint: the filtered candidates, the
// selected one accented.
func (m *listModel) mergeSugView() string {
	cands := m.mrg.mergeFiltered()
	parts := make([]string, len(cands))
	for i, c := range cands {
		if i == m.mrg.sug {
			parts[i] = mAccent.Render(c)
			continue
		}
		parts[i] = c
	}
	return strings.Join(parts, " · ")
}

// mergeInputLines renders the merge-keys input directly under the title
// and breadcrumb: it edits the mapping itself, not a row.
func (m *listModel) mergeInputLines() string {
	lines := []string{mHelp.Render("merge keys: ") + m.mrg.ti.View()}
	if len(m.mrg.mergeFiltered()) > 0 {
		lines = append(lines, mHelp.Render("tab: "+m.mergeSugView()))
	}
	if m.mrg.err != "" {
		lines = append(lines, mErr.Render(m.mrg.err))
	}
	return strings.Join(lines, "\n")
}

// mergeCommit applies the `<` input: the listed anchors become the mapping's
// merge list (own keys keep shadowing them), empty removes the merges.
func (m *listModel) mergeCommit() {
	ms := m.mrg
	names := mergeTokenList(ms.ti.Value())
	if len(names) == 0 {
		if cur, _ := m.doc.Get(ms.path); cur == nil || len(cur.MergeRefs()) == 0 {
			ms.err = "this mapping merges nothing"
			return
		}
		m.applyMerges(ms.path, nil)
		m.mrg = nil
		m.notice = "merge keys removed"
		return
	}
	dedup := names[:0]
	seen := map[string]bool{}
	for _, n := range names {
		if !seen[n] {
			seen[n] = true
			dedup = append(dedup, n)
		}
	}
	var refs []*jsondoc.Node
	self, _ := m.doc.Get(ms.path)
	for _, name := range dedup {
		target := m.anchorHolder(name)
		if target == nil {
			ms.err = "unknown anchor: &" + name
			return
		}
		if target.Object == nil {
			ms.err = "&" + name + " is not a mapping anchor"
			return
		}
		if target == self {
			ms.err = "cannot merge the mapping into itself"
			return
		}
		if m.refWouldCycle(ms.path, target) || m.mergeWouldCycle(ms.path, target) {
			ms.err = "circular reference: the anchor would merge back into this mapping"
			return
		}
		refs = append(refs, target)
	}
	// a not-yet-created mapping (new key, unset field, null value) becomes
	// an empty object first: merging into it is an ordinary editing step
	if cur, ok := m.doc.Get(ms.path); !ok || cur == nil || cur.Null {
		m.doc.Set(ms.path, &jsondoc.Node{Object: &[]jsondoc.Member{}})
	}
	m.applyMerges(ms.path, refs)
	m.mrg = nil
	m.notice = "merged keys from " + strings.Join(dedup, " ")
}

// applyMerges replaces the mapping's merge-key members with one member
// merging refs (nil clears them). An existing "<<" keeps its position; a new
// one goes first, the conventional place.
func (m *listModel) applyMerges(path []any, refs []*jsondoc.Node) {
	cur, ok := m.doc.Get(path)
	if !ok || cur == nil || cur.Object == nil {
		return
	}
	idx := -1
	for i, mem := range *cur.Object {
		if mem.Val != nil && mem.Val.Merge != nil {
			if idx < 0 {
				idx = i
			}
		}
	}
	member := pathFor(path, "<<")
	for m.doc.Remove(member) {
	}
	if refs == nil {
		return
	}
	if idx < 0 {
		idx = 0
	}
	m.doc.InsertMember(path, "<<", idx, &jsondoc.Node{Merge: refs})
}

// mergeWouldCycle reports whether merging target into the mapping at path
// would recurse: the target's own merge graph must not reach the mapping.
func (m *listModel) mergeWouldCycle(path []any, target *jsondoc.Node) bool {
	self, _ := m.doc.Get(path)
	if self == nil {
		return false
	}
	seen := map[*jsondoc.Node]bool{}
	var dfs func(n *jsondoc.Node) bool
	dfs = func(n *jsondoc.Node) bool {
		if n == nil || seen[n] {
			return false
		}
		seen[n] = true
		if n == self {
			return true
		}
		for _, ref := range n.MergeRefs() {
			if dfs(ref) {
				return true
			}
		}
		return false
	}
	return dfs(target)
}

// mergeRefUses counts the merge-key members anywhere in the document (anchor
// subtrees included) whose merge list references n. Removing n's anchor or
// deleting its carrier would leave those merges dangling, so both are
// refused while the count is positive.
func (m *listModel) mergeRefUses(n *jsondoc.Node) int {
	if n == nil {
		return 0
	}
	count := 0
	onPath := map[*jsondoc.Node]bool{}
	var walk func(cur *jsondoc.Node)
	walk = func(cur *jsondoc.Node) {
		if cur == nil || onPath[cur] {
			return
		}
		onPath[cur] = true
		defer delete(onPath, cur)
		for _, ref := range cur.MergeRefs() {
			if ref == n {
				count++
			}
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

// materializeMerged copies one merged key's value into the current mapping:
// an independent copy becomes the mapping's own member, which shadows the
// merged value for this mapping only (the anchor and every other mapping
// merging it keep the original). It returns the row redirected at the new
// own member; every edit path materializes first, so a merged key is never
// modified through the anchor implicitly.
func (m *listModel) materializeMerged(r row) (row, bool) {
	st := m.top()
	if st == nil || len(r.path) == 0 {
		return r, false
	}
	key, ok := r.path[len(r.path)-1].(string)
	if !ok {
		return r, false
	}
	val, ok := m.doc.Get(r.path)
	if !ok || val == nil {
		return r, false
	}
	p := pathFor(st.path, key)
	m.doc.Set(p, val.CloneDetached())
	m.notice = "merged key expanded: this value is now an independent copy"
	r.path = p
	r.merged = ""
	return r, true
}

// expandMerged materializes the merged key under the cursor (the explicit E
// shortcut); edits of merged keys materialize automatically.
func (m *listModel) expandMerged(r row) {
	m.materializeMerged(r)
}

// anchorMergeUsePaths lists every document location that uses the anchor
// node n, in document order: the paths holding it directly (the carrier and
// each alias occurrence) plus the paths of the mappings whose merge keys
// reference it. gr jumps among these; recursive alias structures are walked
// once per occurrence (the onPath guard keeps the descent finite).
func (m *listModel) anchorMergeUsePaths(n *jsondoc.Node) [][]any {
	var paths [][]any
	seen := map[string]bool{}
	onPath := map[*jsondoc.Node]bool{}
	add := func(p []any) {
		k := jsondoc.PathString(p)
		if !seen[k] {
			seen[k] = true
			paths = append(paths, clonePath(p))
		}
	}
	var walk func(cur *jsondoc.Node, path []any)
	walk = func(cur *jsondoc.Node, path []any) {
		if cur == nil || onPath[cur] {
			return
		}
		onPath[cur] = true
		defer delete(onPath, cur)
		if cur == n {
			// the shared node itself: its subtree belongs to the anchor's
			// own definition, not to this occurrence
			add(path)
			return
		}
		for _, ref := range cur.MergeRefs() {
			if ref == n {
				add(path)
				break
			}
		}
		switch {
		case cur.Object != nil:
			for _, mem := range *cur.Object {
				walk(mem.Val, pathFor(path, mem.Key))
			}
		case cur.Array != nil:
			for i, it := range *cur.Array {
				walk(it, pathIdx(path, i))
			}
		}
	}
	if m.doc != nil {
		walk(m.doc, nil)
	}
	return paths
}

// mergeProvides reports whether the mapping at mapPath gets key from its
// merge keys, regardless of whether the mapping also defines it. Deleting
// the mapping's own (expanded) copy of such a key is safe: the merged value
// takes over again, so required-key refusals do not apply.
func (m *listModel) mergeProvides(mapPath []any, key string) bool {
	if key == "" || m.doc == nil {
		return false
	}
	n, ok := m.doc.Get(mapPath)
	if !ok || n == nil || n.Object == nil {
		return false
	}
	for _, ref := range n.MergeRefs() {
		src, ok := m.mergeSourceFor(ref)
		if !ok {
			continue
		}
		// the anchor's own merge chain counts too: a key it inherits still
		// shadows what the mapping merges, so deleting the mapping's own
		// copy falls back to a provided value
		if _, ok := m.mergeDefiningSource(src, key, map[*jsondoc.Node]bool{}); ok {
			return true
		}
	}
	return false
}
