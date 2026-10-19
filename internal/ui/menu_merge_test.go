package ui

// YAML merge keys in the menu: a mapping's menu lists the merged keys with
// the anchor they came from, edits write through to the anchor's object, E
// materializes one merged key into the mapping, `<` edits the merge list,
// and the breadcrumb follows the cursor onto the merged key's anchor.

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/wertasy/json-schema-editor/internal/jsondoc"
	"github.com/wertasy/json-schema-editor/internal/smodel"
	"github.com/wertasy/json-schema-editor/internal/yamldoc"
)

const mergeSrc = `jobs:
  build_base1: &base1
    env:
      env1: val1
  build_base2: &base2
    outputs:
      version: v1
  build2:
    <<: [*base1, *base2]
    name: Build Apple
`

// mergeTestModel builds the menu for a YAML document over a schema that
// models a jobs map of job objects with name/env/outputs.
func mergeTestModel(t *testing.T, src string) *listModel {
	t.Helper()
	doc, _, err := yamldoc.Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	str := func(key, title string) *smodel.FormNode {
		return &smodel.FormNode{Key: key, Title: title, Kind: smodel.KScalar, JSONType: "string"}
	}
	obj := func(key, title string, kids ...*smodel.FormNode) *smodel.FormNode {
		return &smodel.FormNode{Key: key, Title: title, Kind: smodel.KObject, Children: kids}
	}
	job := obj("job", "Job", str("name", "Name"),
		obj("env", "Env", str("env1", "Env1")),
		obj("outputs", "Outputs", str("version", "Version")))
	jobs := &smodel.FormNode{Key: "jobs", Title: "Jobs", Kind: smodel.KMap, Items: job}
	m := newListModel(obj("$", "Jobs", jobs), doc, Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return m
}

// drillBuild2 lands in the job menu of $.jobs.build2.
func drillBuild2(t *testing.T, m *listModel) {
	t.Helper()
	press(m, "enter")                 // jobs
	press(m, "down", "down", "enter") // build_base1, build_base2, build2
	if !pathEq(m.top().path, []any{"jobs", "build2"}) {
		t.Fatalf("drill landed on %v", m.top().path)
	}
}

func TestMergeRowsShowProvenance(t *testing.T) {
	m := mergeTestModel(t, mergeSrc)
	drillBuild2(t, m)

	var names []string
	rows := menuRows(m)
	for _, r := range rows {
		if r.kind != rowField && r.kind != rowDrill && r.kind != rowUnknown {
			continue
		}
		id := r.label
		if r.fn != nil {
			id = r.fn.Key
		}
		names = append(names, id)
		switch id {
		case "name":
			if r.merged != "" {
				t.Fatalf("own key name must not be merged: %+v", r)
			}
			if !pathEq(r.path, []any{"jobs", "build2", "name"}) {
				t.Fatalf("name row path = %v", r.path)
			}
		case "env":
			if r.merged != "base1" {
				t.Fatalf("env row merged = %q, want base1", r.merged)
			}
			if !pathEq(r.path, []any{"jobs", "build_base1", "env"}) {
				t.Fatalf("env row must point into the anchor object, got %v", r.path)
			}
		case "outputs":
			if r.merged != "base2" {
				t.Fatalf("outputs row merged = %q, want base2", r.merged)
			}
		}
	}
	if strings.Join(names, ",") != "name,env,outputs" {
		t.Fatalf("row order = %v, want schema-declared order", names)
	}
	v := m.View()
	if !contains(v, "*base1") || !contains(v, "*base2") {
		t.Fatalf("merged rows must carry the anchor badges:\n%s", v)
	}
	if !contains(v, "<<: *base1 *base2") {
		t.Fatalf("title must list the merge keys:\n%s", v)
	}

	// the layout of the document does not matter: merged keys still take
	// their schema-declared slots (name, env, outputs)
	const tailMerge = `jobs:
  build_base1: &base1
    env:
      env1: val1
  build_base2: &base2
    outputs:
      version: v1
  build2:
    name: Build Apple
    <<: [*base1, *base2]
`
	m2 := mergeTestModel(t, tailMerge)
	drillBuild2(t, m2)
	var got []string
	for _, r := range menuRows(m2) {
		if r.kind != rowField && r.kind != rowDrill && r.kind != rowUnknown {
			continue
		}
		id := r.label
		if r.fn != nil {
			id = r.fn.Key
		}
		got = append(got, id)
	}
	if strings.Join(got, ",") != "name,env,outputs" {
		t.Fatalf("row order = %v, want schema-declared order regardless of the << position", got)
	}
}

func TestMergeOwnKeyShadowsMerged(t *testing.T) {
	const src = `jobs:
  b1: &base1
    env:
      env1: from-anchor
  b2: &base2
    out: o
  child:
    <<: [*base1, *base2]
    env:
      env1: own-value
`
	m := mergeTestModel(t, src)
	press(m, "enter", "down", "down", "enter") // jobs → child
	for _, r := range menuRows(m) {
		if r.label == "env" {
			if r.merged != "" || !pathEq(r.path, []any{"jobs", "child", "env"}) {
				t.Fatalf("own env must shadow the merged one: %+v", r)
			}
		}
	}
}

func TestMergeEarlierAnchorWins(t *testing.T) {
	const src = `jobs:
  b1: &base1
    env:
      env1: first
  b2: &base2
    env:
      env1: second
  child:
    <<: [*base1, *base2]
`
	m := mergeTestModel(t, src)
	press(m, "enter", "down", "down", "enter") // jobs → child
	for _, r := range menuRows(m) {
		if r.label == "env" && r.merged != "base1" {
			t.Fatalf("the earlier anchor must win, got %q", r.merged)
		}
	}
}

func TestMergeBreadcrumbFollowsCursor(t *testing.T) {
	m := mergeTestModel(t, mergeSrc)
	drillBuild2(t, m)

	// schema order: Name (own), env (base1), outputs (base2)
	if got := m.breadcrumb(); got != "$.jobs.build2" {
		t.Fatalf("breadcrumb on own key = %q, want no arrow", got)
	}
	press(m, "down") // env (from base1)
	if got := m.breadcrumb(); got != "$.jobs.build2 → $.jobs.build_base1" {
		t.Fatalf("breadcrumb on merged env = %q", got)
	}
	press(m, "down") // outputs (from base2): the anchor path updates
	if got := m.breadcrumb(); got != "$.jobs.build2 → $.jobs.build_base2" {
		t.Fatalf("breadcrumb on merged outputs = %q", got)
	}
	press(m, "up", "up") // back onto the own key: the arrow clears
	if got := m.breadcrumb(); got != "$.jobs.build2" {
		t.Fatalf("breadcrumb back on own key = %q, want the arrow cleared", got)
	}
}

func TestMergeEditWritesThroughToAnchor(t *testing.T) {
	m := mergeTestModel(t, mergeSrc)
	drillBuild2(t, m)

	press(m, "down")  // env (merged, schema slot 1)
	press(m, "enter") // drill into the anchor's own object
	if !pathEq(m.top().path, []any{"jobs", "build_base1", "env"}) {
		t.Fatalf("drill must land in the anchor's mapping, got %v", m.top().path)
	}
	if _, ok := m.doc.Get([]any{"jobs", "build2", "env"}); ok {
		t.Fatal("editing must not materialize a local copy")
	}
	if contains(m.View(), "merged key expanded") {
		t.Fatalf("no implicit expansion may happen:\n%s", m.View())
	}
	// edits act on the anchor mapping's field, like an alias reference
	press(m, "enter") // in-place edit of env1
	if m.edit == nil {
		t.Fatal("in-place edit did not open")
	}
	m.edit.ti.SetValue("val2")
	press(m, "enter")
	if got := strAt2(t, m.doc, "jobs", "build_base1", "env", "env1"); got != "val2" {
		t.Fatalf("edit must reach the anchor field, env1 = %q", got)
	}
	if _, ok := m.doc.Get([]any{"jobs", "build2", "env"}); ok {
		t.Fatal("the edit must not spread into the mapping as an own key")
	}
}

// boolMergeModel builds a menu over a document whose child merges an
// anchored mapping that provides a boolean flag.
func boolMergeModel(t *testing.T) *listModel {
	t.Helper()
	doc, _, err := yamldoc.Parse([]byte("base: &b\n  flag: true\nchild:\n  <<: *b\n"))
	if err != nil {
		t.Fatal(err)
	}
	obj := func(key, title string, kids ...*smodel.FormNode) *smodel.FormNode {
		return &smodel.FormNode{Key: key, Title: title, Kind: smodel.KObject, Children: kids}
	}
	flag := &smodel.FormNode{Key: "flag", Title: "Flag", Kind: smodel.KBoolean}
	model := obj("$", "Root", obj("base", "Base", flag), obj("child", "Child", flag))
	m := newListModel(model, doc, Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return m
}

func TestMergeToggleWritesThroughToAnchor(t *testing.T) {
	m := boolMergeModel(t)
	press(m, "down", "enter") // child's menu: the merged flag row
	press(m, " ")
	if src, _ := m.doc.Get([]any{"base", "flag"}); src == nil || src.Bool == nil || *src.Bool {
		t.Fatalf("space must toggle the anchor field: %#v", src)
	}
	if _, ok := m.doc.Get([]any{"child", "flag"}); ok {
		t.Fatal("space must not materialize a local copy")
	}
}

func TestMergeDeleteWritesThroughToAnchor(t *testing.T) {
	m := mergeTestModel(t, mergeSrc)
	drillBuild2(t, m)
	press(m, "down", "d") // env (merged, optional): deletes the anchor field
	if _, ok := m.doc.Get([]any{"jobs", "build_base1", "env"}); ok {
		t.Fatal("d must delete the anchor mapping's field")
	}
	if _, ok := m.doc.Get([]any{"jobs", "build2", "env"}); ok {
		t.Fatal("d must not materialize a local copy")
	}
	// the merged row is gone; the schema's own (unset) env row remains
	for _, r := range menuRows(m) {
		if r.fn != nil && r.fn.Key == "env" && r.merged != "" {
			t.Fatalf("merged env row must disappear: %+v", r)
		}
	}

	// a required merged key refuses: deleting the anchor field would remove
	// it for every mapping merging the anchor
	m2 := requiredMergeModel(t)
	press(m2, "down", "enter") // child menu, merged name (required)
	press(m2, "d")
	if _, ok := m2.doc.Get([]any{"base", "name"}); !ok {
		t.Fatal("required merged key must survive d")
	}
	if !contains(m2.View(), "Name is required") {
		t.Fatalf("required refusal missing:\n%s", m2.View())
	}
}

func strAt2(t *testing.T, doc *jsondoc.Node, path ...any) string {
	t.Helper()
	n, ok := doc.Get(path)
	if !ok || n == nil || n.Str == nil {
		return ""
	}
	return *n.Str
}

func TestMergeExpandKey(t *testing.T) {
	m := mergeTestModel(t, mergeSrc)
	drillBuild2(t, m)

	press(m, "down") // env, at its schema slot
	press(m, "E")
	own, ok := m.doc.Get([]any{"jobs", "build2", "env"})
	if !ok || own == nil {
		t.Fatal("E must materialize an own env member")
	}
	src, _ := m.doc.Get([]any{"jobs", "build_base1", "env"})
	if own == src {
		t.Fatal("the expanded value must be an independent copy")
	}
	if !contains(m.View(), "merged key expanded") {
		t.Fatalf("notice missing:\n%s", m.View())
	}
	// the merged row is gone; the own row replaces it
	for _, r := range menuRows(m) {
		if r.label == "env" && r.merged != "" {
			t.Fatalf("expanded key must not be merged anymore: %+v", r)
		}
	}
	// the anchor keeps its value
	if s := strAt2(t, m.doc, "jobs", "build_base1", "env", "env1"); s != "val1" {
		t.Fatalf("anchor value changed: %q", s)
	}
}

func TestMergeInputEditsMergeList(t *testing.T) {
	m := mergeTestModel(t, mergeSrc)
	drillBuild2(t, m)

	press(m, "<")
	if m.mrg == nil {
		t.Fatal("< must open the merge-keys input")
	}
	if got := m.mrg.ti.Value(); got != "base1 base2" {
		t.Fatalf("input prefill = %q, want the current anchors", got)
	}
	// tab completes the token being typed and leaves a separator, so the
	// next anchor follows immediately; already-listed anchors are skipped
	m.mrg.ti.SetValue("base")
	m.cycleMergeSug(1)
	if got := m.mrg.ti.Value(); got != "base1 " && got != "base2 " {
		t.Fatalf("tab completion = %q", got)
	}
	m.cycleMergeSug(1)
	if got := m.mrg.ti.Value(); got != "base1 base2 " && got != "base2 base1 " {
		t.Fatalf("second tab completion = %q", got)
	}
	// one input line merges several anchors
	press(m, "enter")
	child, _ := m.doc.Get([]any{"jobs", "build2"})
	if refs := child.MergeRefs(); len(refs) != 2 {
		t.Fatalf("multi-anchor merge refs = %v", refs)
	}
	// "*" prefixes and commas are accepted too, as in the title display
	press(m, "<")
	if got := m.mrg.ti.Value(); got != "base1 base2" {
		t.Fatalf("input prefill = %q, want all current anchors", got)
	}
	m.mrg.ti.SetValue("*base2, *base1")
	press(m, "enter")
	child, _ = m.doc.Get([]any{"jobs", "build2"})
	if refs := child.MergeRefs(); len(refs) != 2 || refs[0].Anchor != "base2" {
		t.Fatalf("merge refs after * / comma input = %v", refs)
	}
	// apply a single-anchor merge
	press(m, "<")
	m.mrg.ti.SetValue("base2")
	press(m, "enter")
	if m.mrg != nil {
		t.Fatal("enter must close the input")
	}
	child, _ = m.doc.Get([]any{"jobs", "build2"})
	if refs := child.MergeRefs(); len(refs) != 1 || refs[0].Anchor != "base2" {
		t.Fatalf("merge refs after apply = %v", refs)
	}
	for _, r := range menuRows(m) {
		if r.label == "env" && r.merged != "" {
			t.Fatalf("env comes from base1 and must be gone: %+v", r)
		}
		if r.label == "outputs" && r.merged != "base2" {
			t.Fatalf("outputs must stay merged from base2: %+v", r)
		}
	}

	// empty enter clears the merges
	press(m, "<")
	m.mrg.ti.SetValue("")
	press(m, "enter")
	child, _ = m.doc.Get([]any{"jobs", "build2"})
	if len(child.MergeRefs()) != 0 {
		t.Fatalf("merge refs must be cleared: %v", child.MergeRefs())
	}
	for _, r := range menuRows(m) {
		if r.merged != "" {
			t.Fatalf("no merged rows may remain: %+v", r)
		}
	}

	// adding merges back from scratch
	press(m, "<")
	if got := m.mrg.ti.Value(); got != "" {
		t.Fatalf("input after clear = %q", got)
	}
	m.mrg.ti.SetValue("base1")
	press(m, "enter")
	child, _ = m.doc.Get([]any{"jobs", "build2"})
	if refs := child.MergeRefs(); len(refs) != 1 || refs[0].Anchor != "base1" {
		t.Fatalf("merge refs after re-add = %v", refs)
	}
	found := false
	for _, r := range menuRows(m) {
		if r.label == "env" && r.merged == "base1" {
			found = true
		}
	}
	if !found {
		t.Fatal("merged env row must be back")
	}
	if !contains(m.View(), "<<: *base1") {
		t.Fatalf("title must list the new merge:\n%s", m.View())
	}
}

func TestMergeInputErrors(t *testing.T) {
	m := mergeTestModel(t, mergeSrc)
	drillBuild2(t, m)

	press(m, "<")
	m.mrg.ti.SetValue("nope")
	press(m, "enter")
	if m.mrg == nil || !contains(m.mrg.err, "unknown anchor") {
		t.Fatalf("unknown anchor must be refused, mrg=%v err=%q", m.mrg, m.mrg.err)
	}
	m.mrg.ti.SetValue("base1 base2") // restore for the cycle test below
	press(m, "esc")
	if m.mrg != nil {
		t.Fatal("esc must close the input")
	}

	// a merge cycle: b merges a's anchor, so a's mapping may not merge b back
	const cycSrc = `jobs:
  a: &b1
    name: from-a
  b: &b2
    <<: *b1
    name: from-b
`
	m2 := mergeTestModel(t, cycSrc)
	press(m2, "enter", "enter") // jobs → a's job menu ($.jobs.a)
	if !pathEq(m2.top().path, []any{"jobs", "a"}) {
		t.Fatalf("drill landed on %v", m2.top().path)
	}
	press(m2, "<")
	m2.mrg.ti.SetValue("b2") // b2's merge graph reaches b1, this mapping
	press(m2, "enter")
	if m2.mrg == nil || !contains(m2.mrg.err, "circular") {
		t.Fatalf("a merge cycle must be refused, err=%q", m2.mrg.err)
	}
	press(m2, "esc")
}

func TestMergeSelfReferenceRefused(t *testing.T) {
	const src = `jobs:
  a: &b1
    name: x
`
	m := mergeTestModel(t, src)
	press(m, "enter", "enter") // jobs → a's job menu: the anchored mapping itself
	press(m, "<")
	m.mrg.ti.SetValue("b1")
	press(m, "enter")
	if m.mrg == nil || !contains(m.mrg.err, "itself") {
		t.Fatalf("self-merge must be refused, err=%q", m.mrg.err)
	}
}

func TestMergeAnchorDeletionGuards(t *testing.T) {
	m := mergeTestModel(t, mergeSrc)
	press(m, "enter") // jobs map menu; rows build_base1, build_base2, build2

	// d on the anchor carrier is refused while a merge references it
	press(m, "d")
	if _, ok := m.doc.Get([]any{"jobs", "build_base1"}); !ok {
		t.Fatal("the anchored mapping must survive d")
	}
	if !contains(m.View(), "merge key referencing") {
		t.Fatalf("refusal notice missing:\n%s", m.View())
	}

	// clearing the anchor via & is refused for the same reason
	press(m, "&")
	if m.anc == nil {
		t.Fatal("& must open the anchor input on the carrier row")
	}
	m.anc.ti.SetValue("")
	press(m, "enter")
	if m.anc == nil || !contains(m.anc.err, "merge key") {
		t.Fatalf("anchor removal must be refused while merged, err=%q", m.anc.err)
	}
	press(m, "esc")

	// removing the merge first frees the anchor
	press(m, "down", "down", "enter") // build2's menu
	press(m, "<")
	m.mrg.ti.SetValue("")
	press(m, "enter")
	press(m, "esc")      // back to the jobs menu (cursor on build2)
	press(m, "up", "up") // onto build_base1, the anchor carrier
	press(m, "&")
	if m.anc == nil {
		t.Fatal("& must open on the carrier row again")
	}
	m.anc.ti.SetValue("")
	press(m, "enter")
	if m.anc != nil {
		t.Fatalf("anchor removal must succeed after the merge is gone: %q", m.anc.err)
	}
	if n, _ := m.doc.Get([]any{"jobs", "build_base1"}); n == nil || n.Anchor != "" {
		t.Fatalf("anchor must be cleared: %#v", n)
	}
}

func TestMergeUndoRestoresMergeList(t *testing.T) {
	m := mergeTestModel(t, mergeSrc)
	drillBuild2(t, m)

	press(m, "<")
	m.mrg.ti.SetValue("")
	press(m, "enter") // clears the merges: one undo step
	child, _ := m.doc.Get([]any{"jobs", "build2"})
	if len(child.MergeRefs()) != 0 {
		t.Fatal("merges must be cleared before undo")
	}
	press(m, "u")
	child, _ = m.doc.Get([]any{"jobs", "build2"})
	if refs := child.MergeRefs(); len(refs) != 2 {
		t.Fatalf("undo must restore the merge list, got %v", refs)
	}
}

func TestMergeSummaryShowsEffectiveContent(t *testing.T) {
	m := mergeTestModel(t, mergeSrc)
	v := m.View() // root menu; the jobs row summarizes its members
	if !contains(v, "build_base1={…}") {
		t.Fatalf("jobs summary should reflect the mapping: %s", firstLine(v))
	}
	// build2's own row in the jobs menu summarizes merged members too
	press(m, "enter")
	if v := m.View(); !contains(v, "name=Build Apple") || !contains(v, "env={…}") {
		t.Fatalf("build2 summary must include merged members:\n%s", v)
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// cursorOnMerged moves the cursor onto the menu's merged row of key.
func cursorOnMerged(t *testing.T, m *listModel, key string) {
	t.Helper()
	for i, r := range menuRows(m) {
		if r.label == key && r.merged != "" {
			m.top().cursor = i
			return
		}
	}
	t.Fatalf("no merged row %q in:\n%s", key, m.View())
}

func TestMergeGaJumpsToAnchorCarrier(t *testing.T) {
	m := mergeTestModel(t, mergeSrc)
	drillBuild2(t, m)
	cursorOnMerged(t, m, "env")

	press(m, "g", "a")
	if len(m.stack) < 2 || !pathEq(m.top().path, []any{"jobs", "build_base1"}) {
		t.Fatalf("ga must jump to the anchor's mapping, landed on %v", m.top().path)
	}
	// ... onto the field row that defines the merged value, not its first row
	if r := menuRows(m)[m.top().cursor]; !pathEq(r.path, []any{"jobs", "build_base1", "env"}) {
		t.Fatalf("ga must land on the defining field row, cursor on %+v", r)
	}
	press(m, "esc") // esc returns to the jumped-from menu
	if !pathEq(m.top().path, []any{"jobs", "build2"}) {
		t.Fatalf("esc must return to build2, landed on %v", m.top().path)
	}
}

func TestMergeGrJumpsToOtherMergeUsers(t *testing.T) {
	const src = `jobs:
  build_base1: &base1
    env:
      env1: val1
  build3:
    <<: *base1
    name: other
  build2:
    <<: *base1
    name: Build Apple
`
	m := mergeTestModel(t, src)
	drillBuild2(t, m)
	cursorOnMerged(t, m, "env")

	press(m, "g", "r") // build3 is the only other user of &base1
	if !pathEq(m.top().path, []any{"jobs", "build3"}) {
		t.Fatalf("gr must jump to the other mapping merging &base1, landed on %v (notice %q)",
			m.top().path, m.notice)
	}
}

func TestMergeGrPicksAmongSeveralUsers(t *testing.T) {
	const src = `jobs:
  build_base1: &base1
    env:
      env1: val1
  build3:
    <<: *base1
  build4:
    <<: *base1
  direct: *base1
  build2:
    <<: *base1
`
	m := mergeTestModel(t, src)
	press(m, "enter") // jobs map menu
	for i, r := range menuRows(m) {
		if r.label == "build2" {
			m.top().cursor = i
			break
		}
	}
	press(m, "enter") // build2's job menu
	if !pathEq(m.top().path, []any{"jobs", "build2"}) {
		t.Fatalf("drill landed on %v", m.top().path)
	}
	cursorOnMerged(t, m, "env")

	press(m, "g", "r")
	if m.refPick == nil {
		t.Fatalf("several users must open the picker, notice %q", m.notice)
	}
	var got []string
	for _, p := range m.refPick.paths {
		got = append(got, jsondoc.PathString(p))
	}
	want := []string{"$.jobs.build3", "$.jobs.build4", "$.jobs.direct"}
	if len(got) != len(want) {
		t.Fatalf("picker targets = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("target[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	// enter on the second entry jumps there
	m.refPick.cursor = 1
	press(m, "enter")
	if !pathEq(m.top().path, []any{"jobs", "build4"}) {
		t.Fatalf("picker enter must jump, landed on %v", m.top().path)
	}
}

func TestMergeGrWithoutOtherUsers(t *testing.T) {
	m := mergeTestModel(t, mergeSrc) // base1 is merged by build2 only
	drillBuild2(t, m)
	cursorOnMerged(t, m, "env")

	press(m, "g", "r")
	if m.refPick != nil || !pathEq(m.top().path, []any{"jobs", "build2"}) {
		t.Fatalf("gr must stay put without other users, landed on %v", m.top().path)
	}
	if !contains(m.View(), "no other references to &base1") {
		t.Fatalf("notice missing:\n%s", m.View())
	}
}

func TestGrOnReferenceIncludesMergeUsers(t *testing.T) {
	const src = `base: &x
  p: 1
copy: *x
merger:
  <<: *x
`
	// the plain-alias model knows base/copy as objects with field p
	doc, _, err := yamldoc.Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	field := func(key, title string) *smodel.FormNode {
		return &smodel.FormNode{Key: key, Title: title, Kind: smodel.KScalar, JSONType: "integer"}
	}
	obj := func(key, title string) *smodel.FormNode {
		return &smodel.FormNode{Key: key, Title: title, Kind: smodel.KObject,
			Children: []*smodel.FormNode{field("p", "P")}}
	}
	model := &smodel.FormNode{Key: "$", Title: "X", Kind: smodel.KObject,
		Children: []*smodel.FormNode{obj("base", "Base"), obj("copy", "Copy"),
			obj("merger", "Merger")}}
	m := newListModel(model, doc, Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	press(m, "down", "g", "r") // cursor on copy (a *x reference)
	if !pathEq(m.top().path, []any{"merger"}) {
		t.Fatalf("gr from a reference must reach the mapping merging the anchor, landed on %v (notice %q)",
			m.top().path, m.notice)
	}
}

func TestMergeInputLivesUnderTheTitle(t *testing.T) {
	m := mergeTestModel(t, mergeSrc)
	drillBuild2(t, m)
	press(m, "<")
	lines := strings.Split(m.View(), "\n")
	if len(lines) < 3 || !contains(lines[2], "merge keys:") {
		t.Fatalf("merge input must sit right under the breadcrumb:\n%s", m.View())
	}
	// the cursor row stays a normal row, with no input below it
	for i, line := range lines {
		if strings.Contains(line, ">") && strings.Contains(line, "merge keys:") {
			t.Fatalf("input must not render on a row line (%d): %q", i, line)
		}
	}
}

// freshMappingModel: an unset schema object (settings) beside an anchored
// mapping (base) whose env key can be merged in.
func freshMappingModel(t *testing.T) *listModel {
	t.Helper()
	doc, _, err := yamldoc.Parse([]byte("base: &b\n  env:\n    env1: v1\nsettings:\n"))
	if err != nil {
		t.Fatal(err)
	}
	str := func(k, title string) *smodel.FormNode {
		return &smodel.FormNode{Key: k, Title: title, Kind: smodel.KScalar, JSONType: "string"}
	}
	env := func() *smodel.FormNode {
		return &smodel.FormNode{Key: "env", Title: "Env", Kind: smodel.KObject,
			Children: []*smodel.FormNode{str("env1", "Env1")}}
	}
	model := &smodel.FormNode{Key: "$", Title: "Root", Kind: smodel.KObject,
		Children: []*smodel.FormNode{
			{Key: "base", Title: "Base", Kind: smodel.KObject, Children: []*smodel.FormNode{env()}},
			{Key: "settings", Title: "Settings", Kind: smodel.KObject, Children: []*smodel.FormNode{env()}},
		}}
	m := newListModel(model, doc, Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return m
}

func TestMergeCreatesMissingMapping(t *testing.T) {
	m := freshMappingModel(t)
	press(m, "down", "enter") // settings: unset (null) in the document
	press(m, "<")
	if m.mrg == nil {
		t.Fatalf("merge input must open on an unset mapping, notice %q", m.notice)
	}
	// opening (and cancelling) the input must not create the object yet
	press(m, "esc")
	if n, _ := m.doc.Get([]any{"settings"}); n != nil && n.Object != nil {
		t.Fatal("cancelling the merge input must not create the mapping")
	}
	press(m, "<")
	m.mrg.ti.SetValue("b")
	press(m, "enter")
	n, ok := m.doc.Get([]any{"settings"})
	if !ok || n == nil || n.Object == nil {
		t.Fatalf("the mapping must be created: %#v", n)
	}
	if refs := n.MergeRefs(); len(refs) != 1 || refs[0].Anchor != "b" {
		t.Fatalf("merge refs = %v", refs)
	}
	found := false
	for _, r := range menuRows(m) {
		if r.fn != nil && r.fn.Key == "env" && r.merged == "b" {
			found = true
		}
	}
	if !found {
		t.Fatalf("merged env row missing after creating the mapping:\n%s", m.View())
	}
}

// requiredMergeModel marks name required in both base and child; child's
// name comes from the merge.
func requiredMergeModel(t *testing.T) *listModel {
	t.Helper()
	doc, _, err := yamldoc.Parse([]byte("base: &b\n  name: from-base\nchild:\n  <<: *b\n"))
	if err != nil {
		t.Fatal(err)
	}
	obj := func(k, title string) *smodel.FormNode {
		return &smodel.FormNode{Key: k, Title: title, Kind: smodel.KObject,
			Children: []*smodel.FormNode{{Key: "name", Title: "Name",
				Kind: smodel.KScalar, JSONType: "string", Required: true}}}
	}
	model := &smodel.FormNode{Key: "$", Title: "Root", Kind: smodel.KObject,
		Children: []*smodel.FormNode{obj("base", "Base"), obj("child", "Child")}}
	m := newListModel(model, doc, Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return m
}

func TestDeleteExpandedMergedKeyAllowedWhenRequired(t *testing.T) {
	m := requiredMergeModel(t)
	press(m, "down", "enter") // child's menu: name merged (required)
	press(m, "E")             // materialize the required merged key
	if _, ok := m.doc.Get([]any{"child", "name"}); !ok {
		t.Fatal("E must materialize the merged name")
	}
	press(m, "d")
	if _, ok := m.doc.Get([]any{"child", "name"}); ok {
		t.Fatalf("the expanded copy must be deletable even though required: %s", m.View())
	}
	// the merged value takes over again
	found := false
	for _, r := range menuRows(m) {
		if r.fn != nil && r.fn.Key == "name" && r.merged == "b" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the merged row must return after the deletion:\n%s", m.View())
	}
	if contains(m.View(), "Name is required") {
		t.Fatalf("required must not refuse an expanded merged key:\n%s", m.View())
	}
}

func TestMenuTitleShowsKeyAndPutsSignatureBelow(t *testing.T) {
	// a map item menu whose item schema has no title: the title is the key
	doc, _, err := yamldoc.Parse([]byte("jobs:\n  build2:\n    name: Build\n"))
	if err != nil {
		t.Fatal(err)
	}
	items := &smodel.FormNode{Key: "job", Kind: smodel.KObject,
		Children: []*smodel.FormNode{{Key: "name", Title: "Name", Kind: smodel.KScalar, JSONType: "string"}}}
	model := &smodel.FormNode{Key: "$", Title: "Root", Kind: smodel.KObject,
		Children: []*smodel.FormNode{{Key: "jobs", Title: "Jobs", Kind: smodel.KMap, Items: items}}}
	m := newListModel(model, doc, Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	press(m, "enter", "enter") // jobs → build2
	lines := strings.Split(m.View(), "\n")
	if !contains(lines[0], "build2") || contains(lines[0], "{name}") {
		t.Fatalf("title must show the key, got %q", lines[0])
	}
	if !contains(lines[len(lines)-2], "variant: {name}") {
		t.Fatalf("the variant signature must move to the bottom block:\n%s", m.View())
	}

	// a classic oneOf variant editor: the field's title, signature below
	doc2, _, err := yamldoc.Parse([]byte("auth:\n  token: x\n"))
	if err != nil {
		t.Fatal(err)
	}
	auth := &smodel.FormNode{Key: "auth", Title: "Auth", Kind: smodel.KOneOf,
		Variants: []*smodel.FormNode{{Kind: smodel.KObject,
			Children: []*smodel.FormNode{{Key: "token", Title: "Token", Kind: smodel.KScalar, JSONType: "string"}}}}}
	model2 := &smodel.FormNode{Key: "$", Title: "Root", Kind: smodel.KObject,
		Children: []*smodel.FormNode{auth}}
	m2 := newListModel(model2, doc2, Options{})
	m2.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	press(m2, "enter") // auth has a value: straight into the variant editor
	lines = strings.Split(m2.View(), "\n")
	if !contains(lines[0], "Auth") || contains(lines[0], "{token}") {
		t.Fatalf("variant editor title must show the field key, got %q", lines[0])
	}
	if !contains(lines[len(lines)-2], "variant: {token}") {
		t.Fatalf("the variant signature must move to the bottom block:\n%s", m2.View())
	}
}

func TestMergeChainRowsFollowTheDefiningAnchor(t *testing.T) {
	const src = `jobs:
  b1: &b1
    name: from-b1
    env:
      env1: v1
  b2: &b2
    <<: *b1
    name: from-b2
  job1:
    <<: *b2
`
	m := mergeTestModel(t, src)
	press(m, "enter") // jobs map
	for i, r := range menuRows(m) {
		if r.label == "job1" {
			m.top().cursor = i
		}
	}
	press(m, "enter")
	if !pathEq(m.top().path, []any{"jobs", "job1"}) {
		t.Fatalf("drill landed on %v", m.top().path)
	}
	var nameRow, envRow row
	for _, r := range menuRows(m) {
		switch {
		case r.fn != nil && r.fn.Key == "name":
			nameRow = r
		case r.fn != nil && r.fn.Key == "env":
			envRow = r
		}
	}
	// b2's own name beats the one it inherits from b1
	if nameRow.merged != "b2" || !pathEq(nameRow.path, []any{"jobs", "b2", "name"}) {
		t.Fatalf("name row = %+v, want the b2 definition", nameRow)
	}
	if got := summary(m, nameRow.fn, nameRow.path); got != "from-b2" {
		t.Fatalf("name summary = %q, want b2's value", got)
	}
	// env is inherited through b2 and lives in b1: badge and path follow it
	if envRow.merged != "b1" || !pathEq(envRow.path, []any{"jobs", "b1", "env"}) {
		t.Fatalf("env row = %+v, want the b1 definition", envRow)
	}
	v := m.View()
	if !contains(v, "*b2") || !contains(v, "from-b2") || contains(v, "from-b1") {
		t.Fatalf("merged view must show b2's override:\n%s", v)
	}
	// the chain-provided key is expandable and deletable even if required
	press(m, "E") // cursor is on name (schema slot 0)
	press(m, "d")
	if _, ok := m.doc.Get([]any{"jobs", "job1", "name"}); ok {
		t.Fatal("the expanded copy must be deletable")
	}
	found := false
	for _, r := range menuRows(m) {
		if r.fn != nil && r.fn.Key == "name" && r.merged == "b2" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the chain-provided merged row must return:\n%s", m.View())
	}
}

// mergeVariantModel mirrors the GitHub workflow job union: normalJob requires
// runs-on, the reusable variant requires uses, and both define name.
func mergeVariantModel(t *testing.T, src string) *listModel {
	t.Helper()
	doc, _, err := yamldoc.Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	str := func(k, title string) *smodel.FormNode {
		return &smodel.FormNode{Key: k, Title: title, Kind: smodel.KScalar, JSONType: "string"}
	}
	normal := &smodel.FormNode{Key: "job", Title: "Normal", Kind: smodel.KObject,
		Children: []*smodel.FormNode{
			str("name", "Name"),
			{Key: "runs-on", Title: "Runs on", Kind: smodel.KScalar, JSONType: "string", Required: true},
			{Key: "steps", Title: "Steps", Kind: smodel.KArray, Items: &smodel.FormNode{
				Kind: smodel.KObject, Children: []*smodel.FormNode{str("run", "Run")}}},
		}}
	reusable := &smodel.FormNode{Key: "job", Title: "Reusable", Kind: smodel.KObject,
		Children: []*smodel.FormNode{
			str("name", "Name"),
			{Key: "uses", Title: "Uses", Kind: smodel.KScalar, JSONType: "string", Required: true},
		}}
	jobs := &smodel.FormNode{Key: "jobs", Title: "Jobs", Kind: smodel.KMap,
		Items: &smodel.FormNode{Kind: smodel.KOneOf, Variants: []*smodel.FormNode{normal, reusable}}}
	model := &smodel.FormNode{Key: "$", Title: "Root", Kind: smodel.KObject,
		Children: []*smodel.FormNode{jobs}}
	m := newListModel(model, doc, Options{})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	return m
}

func TestMergeDrivesVariantInference(t *testing.T) {
	m := mergeVariantModel(t, `jobs:
  base: &b
    runs-on: ubuntu-latest
    steps:
      - run: echo hi
  build:
    <<: *b
`)
	press(m, "enter")         // jobs map
	press(m, "down", "enter") // build
	rows := menuRows(m)
	var hasRunsOn, hasUses bool
	for _, r := range rows {
		if r.fn != nil && r.fn.Key == "runs-on" {
			hasRunsOn = true
			if r.merged != "b" {
				t.Fatalf("runs-on row must come from the merge: %+v", r)
			}
		}
		if r.fn != nil && r.fn.Key == "uses" {
			hasUses = true
		}
	}
	if !hasRunsOn {
		t.Fatalf("merged runs-on must identify the normal variant:\n%s", m.View())
	}
	if hasUses {
		t.Fatalf("the other variant's required field must not leak into the pinned menu:\n%s", m.View())
	}
	// the view must not mark a variant-irrelevant required key
	if contains(m.View(), "Uses*") {
		t.Fatalf("stale union required marker:\n%s", m.View())
	}

	// a genuinely required key missing from the merge keeps its marker and
	// the variant stays pinned: saving is up to the validator
	m2 := mergeVariantModel(t, `jobs:
  base: &b
    name: base
    steps:
      - run: echo hi
  build:
    <<: *b
`)
	press(m2, "enter")
	press(m2, "down", "enter")
	var nullRequired bool
	for _, r := range menuRows(m2) {
		if r.fn != nil && r.fn.Key == "runs-on" && r.fn.Required {
			if v, ok := m2.doc.Get(r.path); !ok || v == nil || v.Null {
				nullRequired = true
			}
		}
	}
	if !nullRequired {
		t.Fatalf("missing required runs-on must stay visible:\n%s", m2.View())
	}
}

func TestMergeGaLandsOnDefiningField(t *testing.T) {
	// a merged scalar lands on its own row in the defining mapping
	m := mergeTestModel(t, `jobs:
  b1: &b1
    name: from-b1
  build2:
    <<: *b1
`)
	press(m, "enter") // jobs map
	press(m, "down", "enter")
	if !pathEq(m.top().path, []any{"jobs", "build2"}) {
		t.Fatalf("drill landed on %v", m.top().path)
	}
	cursorOnMerged(t, m, "name")
	press(m, "g", "a")
	if !pathEq(m.top().path, []any{"jobs", "b1"}) {
		t.Fatalf("ga landed on %v", m.top().path)
	}
	if r := menuRows(m)[m.top().cursor]; r.fn == nil || r.fn.Key != "name" ||
		!pathEq(r.path, []any{"jobs", "b1", "name"}) {
		t.Fatalf("ga must select the defining name row, got %+v", r)
	}
	if r := menuRows(m)[m.top().cursor]; r.merged != "" {
		t.Fatalf("the defining row is the anchor's own key: %+v", r)
	}
	press(m, "esc")
	if !pathEq(m.top().path, []any{"jobs", "build2"}) {
		t.Fatalf("esc must return to build2, landed on %v", m.top().path)
	}
}

func TestMergeGaFollowsTheChainToTheDefiningField(t *testing.T) {
	// env is inherited through b2 from b1: ga must land on b1's env row,
	// the mapping the badge and breadcrumb name
	m := mergeTestModel(t, `jobs:
  b1: &b1
    env:
      env1: v1
  b2: &b2
    <<: *b1
    name: from-b2
  job1:
    <<: *b2
`)
	press(m, "enter") // jobs map
	for i, r := range menuRows(m) {
		if r.label == "job1" {
			m.top().cursor = i
		}
	}
	press(m, "enter")
	cursorOnMerged(t, m, "env")
	press(m, "g", "a")
	if !pathEq(m.top().path, []any{"jobs", "b1"}) {
		t.Fatalf("ga landed on %v, want the defining mapping b1", m.top().path)
	}
	if r := menuRows(m)[m.top().cursor]; !pathEq(r.path, []any{"jobs", "b1", "env"}) {
		t.Fatalf("ga must select b1's env row, got %+v", r)
	}
}

func TestMergeCompletionFiltersCandidates(t *testing.T) {
	doc, _, err := yamldoc.Parse([]byte(`base: &b
  x: 1
scalar: &s hello
arr: &a
  - 1
  - 2
job: &j
  <<: *b
  name: j
wrap: &w
  <<: *j
  k: v
`))
	if err != nil {
		t.Fatal(err)
	}
	str := func(k, title string) *smodel.FormNode {
		return &smodel.FormNode{Key: k, Title: title, Kind: smodel.KScalar, JSONType: "string"}
	}
	obj := func(k, title string, kids ...*smodel.FormNode) *smodel.FormNode {
		return &smodel.FormNode{Key: k, Title: title, Kind: smodel.KObject, Children: kids}
	}
	model := obj("$", "Root",
		obj("base", "Base",
			&smodel.FormNode{Key: "x", Title: "X", Kind: smodel.KScalar, JSONType: "integer"}),
		str("scalar", "Scalar"),
		&smodel.FormNode{Key: "arr", Title: "Arr", Kind: smodel.KArray,
			Items: &smodel.FormNode{Key: "arr", Kind: smodel.KScalar, JSONType: "integer"}},
		obj("job", "Job", str("name", "Name")),
		obj("wrap", "Wrap", str("k", "K")),
	)
	m := newListModel(model, doc, Options{})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})

	for i, r := range menuRows(m) {
		if r.fn != nil && r.fn.Key == "job" {
			m.top().cursor = i
		}
	}
	press(m, "enter") // job's mapping menu
	if !pathEq(m.top().path, []any{"job"}) {
		t.Fatalf("drill landed on %v", m.top().path)
	}
	press(m, "<")
	if m.mrg == nil {
		t.Fatal("merge input did not open")
	}
	// only mapping anchors that can legally merge here: &b survives;
	// &s and &a are not mappings, &j is the mapping itself and &w's merge
	// chain reaches it (cycle)
	if got := strings.Join(m.mrg.cands, ","); got != "b" {
		t.Fatalf("merge candidates = %q, want b", got)
	}
	// the completion follows the typed text (case-insensitive)
	m.mrg.ti.SetValue("B")
	if got := strings.Join(m.mrg.mergeFiltered(), ","); got != "b" {
		t.Fatalf("filtered merge candidates = %q", got)
	}
	if hint := m.mergeSugView(); !strings.Contains(hint, "b") {
		t.Fatalf("hint must follow the typed prefix: %q", hint)
	}
	press(m, "tab")
	if got := m.mrg.ti.Value(); got != "b " {
		t.Fatalf("tab must complete the mapping anchor, got %q", got)
	}
}
