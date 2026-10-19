package ui

// YAML anchors, references and expansion in the menu: an aliased value is
// not expanded into a copy — its menu shows the anchored original with
// < Expand > (shortcut e) on top; edits write through to the original until
// the user expands. & sets/renames/removes the anchor on a value, * points a
// value at an anchor (tab completes) or removes the reference.

import (
	"bytes"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/wertasy/json-schema-editor/internal/jsondoc"
	"github.com/wertasy/json-schema-editor/internal/smodel"
	"github.com/wertasy/json-schema-editor/internal/yamldoc"
)

// aliasTestModel builds the menu for a YAML document with anchors/aliases
// over a schema that knows base and copy as objects with one integer field.
func aliasTestModel(t *testing.T, src string) (*listModel, *jsondoc.Node, *yamldoc.Meta) {
	t.Helper()
	doc, meta, err := yamldoc.Parse([]byte(src))
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
	model := &smodel.FormNode{Key: "$", Title: "Aliases", Kind: smodel.KObject,
		Children: []*smodel.FormNode{obj("base", "Base"), obj("copy", "Copy")}}
	m := newListModel(model, doc, Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return m, doc, meta
}

// drillCopy pushes the menu for the copy key (rows: base, copy).
func drillCopy(t *testing.T, m *listModel) {
	t.Helper()
	press(m, "down", "enter")
	if !pathEq(m.top().path, []any{"copy"}) {
		t.Fatalf("drill landed on %v", m.top().path)
	}
}

func TestAliasMenuOffersExpandOnTop(t *testing.T) {
	m, _, _ := aliasTestModel(t, "base: &x\n  p: 1\ncopy: *x\n")
	drillCopy(t, m)

	rows := menuRows(m)
	if len(rows) == 0 || rows[0].kind != rowExpand || rows[0].label != "< Expand >" {
		t.Fatalf("first row = %+v, want < Expand >", rows[0])
	}
	if len(rows) != 2 || rows[1].kind != rowField || rows[1].fn.Key != "p" {
		t.Fatalf("alias menu rows = %+v, want Expand + p", rows)
	}
	if v := m.View(); !contains(v, "< Expand >") {
		t.Fatalf("view lost the Expand row:\n%s", v)
	}

	// The anchored original is not a reference: no Expand there.
	press(m, "esc")
	press(m, "up", "enter")
	if !pathEq(m.top().path, []any{"base"}) {
		t.Fatalf("drill landed on %v", m.top().path)
	}
	for _, r := range menuRows(m) {
		if r.kind == rowExpand {
			t.Fatalf("anchored original must not offer Expand:\n%s", m.View())
		}
	}
}

func TestAliasReferenceShownOnTitleAndCrumb(t *testing.T) {
	m, _, _ := aliasTestModel(t, "base: &x\n  p: 1\ncopy: *x\n")
	if v := m.View(); !contains(v, "*x") {
		t.Fatalf("copy row summary should carry the *x reference:\n%s", v)
	}
	drillCopy(t, m)
	v := m.View()
	// title carries the reference, the breadcrumb merges both paths
	if !contains(v, ": *x") || !contains(v, "$.copy → $.base") {
		t.Fatalf("reference menu must show `: *x` and the merged path line:\n%s", v)
	}
	// The anchored original's menu names the anchor on the title instead.
	press(m, "esc")
	press(m, "up", "enter")
	if v := m.View(); !contains(v, ": &x") || contains(v, "→ $.base") {
		t.Fatalf("anchor menu should show `: &x` and a plain path:\n%s", v)
	}
}

func TestAliasEditWritesAnchoredOriginal(t *testing.T) {
	m, doc, _ := aliasTestModel(t, "base: &x\n  p: 1\ncopy: *x\n")
	drillCopy(t, m)
	press(m, "down", "enter") // p field editor on the alias menu
	m.edit.ti.SetValue("2")
	press(m, "enter")

	for _, key := range []string{"base", "copy"} {
		n, ok := doc.Get([]any{key, "p"})
		if !ok || n.Num == nil || n.Num.String() != "2" {
			t.Fatalf("%s.p = %v, want 2 (edits reach the original)", key, n)
		}
	}
	// The reference is intact: still one shared node.
	base, _ := doc.Get([]any{"base"})
	if cp, _ := doc.Get([]any{"copy"}); cp != base {
		t.Fatal("edit broke the alias sharing")
	}
}

func TestAliasExpandDetachesCopy(t *testing.T) {
	m, doc, _ := aliasTestModel(t, "base: &x\n  p: 1\ncopy: *x\n")
	drillCopy(t, m)
	press(m, "enter") // < Expand > is the first row

	if cp, _ := doc.Get([]any{"copy"}); cp.Anchor != "" {
		t.Fatalf("expanded copy should be plain data, anchor %q", cp.Anchor)
	}
	for _, r := range menuRows(m) {
		if r.kind == rowExpand {
			t.Fatalf("Expand row must disappear after expanding:\n%s", m.View())
		}
	}
	// The expanded copy is independent: edits stay local.
	press(m, "down", "enter") // p field editor
	m.edit.ti.SetValue("3")
	press(m, "enter")
	if n, _ := doc.Get([]any{"copy", "p"}); n.Num == nil || n.Num.String() != "3" {
		t.Fatalf("copy.p = %v, want 3", n)
	}
	if n, _ := doc.Get([]any{"base", "p"}); n.Num == nil || n.Num.String() != "1" {
		t.Fatalf("base.p = %v, want 1 (expand must detach)", n)
	}
	// Writing the document keeps the anchor on base and plain content on copy.
	var buf bytes.Buffer
	if err := yamldoc.Write(&buf, doc, &yamldoc.Meta{}); err != nil {
		t.Fatal(err)
	}
	const want = "base: &x\n  p: 1\ncopy:\n  p: 3\n"
	if buf.String() != want {
		t.Fatalf("output:\n%s\nwant:\n%s", buf.String(), want)
	}
}

func TestAliasExpandShortcutE(t *testing.T) {
	m, doc, _ := aliasTestModel(t, "base: &x\n  p: 1\ncopy: *x\n")
	drillCopy(t, m)
	press(m, "e") // the menu itself is a reference
	if cp, _ := doc.Get([]any{"copy"}); cp.Anchor != "" {
		t.Fatal("e must expand the reference menu")
	}
	if _, ok := m.aliasAt([]any{"copy"}); ok {
		t.Fatal("e must leave an independent copy behind")
	}

	// e on a reference row in the parent menu expands that row.
	m2, doc2, _ := aliasTestModel(t, "base: &x\n  p: 1\ncopy: *x\n")
	press(m2, "down") // cursor on the copy row
	press(m2, "e")
	if cp, _ := doc2.Get([]any{"copy"}); cp.Anchor != "" {
		t.Fatal("e on a reference row must expand it")
	}
	if b, _ := doc2.Get([]any{"base"}); b.Anchor != "x" {
		t.Fatalf("base anchor must survive: %#v", b)
	}

	// Without a reference under the cursor e does nothing.
	m3, doc3, _ := aliasTestModel(t, "base: &x\n  p: 1\n")
	press(m3, "e")
	if n, _ := doc3.Get([]any{"base"}); n.Anchor != "x" {
		t.Fatal("e without a reference must not touch the document")
	}
}

func TestAnchorSetAndRename(t *testing.T) {
	m, doc, _ := aliasTestModel(t, "base:\n  p: 1\n")
	press(m, "&") // base row: open the anchor input
	if m.anc == nil {
		t.Fatalf("& must open the anchor input:\n%s", m.View())
	}
	m.anc.ti.SetValue("x")
	press(m, "enter")
	if m.anc != nil {
		t.Fatal("enter must commit the anchor input")
	}
	base, _ := doc.Get([]any{"base"})
	if base.Anchor != "x" {
		t.Fatalf("anchor = %q, want x", base.Anchor)
	}

	// A reference sharing the node, then renaming through it.
	doc.Set([]any{"copy"}, base)
	press(m, "down", "&") // cursor on the copy row
	m.anc.ti.SetValue("y")
	press(m, "enter")
	if base.Anchor != "y" {
		t.Fatalf("rename must update the shared anchor, got %q", base.Anchor)
	}
	if cp, _ := doc.Get([]any{"copy"}); cp != base {
		t.Fatal("rename broke the sharing")
	}
	if v := m.View(); !contains(v, "*y") {
		t.Fatalf("renamed reference must show *y:\n%s", v)
	}
}

func TestAnchorDeleteRules(t *testing.T) {
	m, doc, _ := aliasTestModel(t, "base: &x\n  p: 1\ncopy: *x\n")
	press(m, "&") // base row: anchor is referenced → refused
	m.anc.ti.SetValue("")
	press(m, "enter")
	if m.anc == nil || !strings.Contains(m.anc.err, "referenced") {
		t.Fatalf("deleting a referenced anchor must be refused, anc=%v err=%q",
			m.anc != nil, mAncErr(m))
	}
	press(m, "esc")

	press(m, "down", "*") // copy row: the reference input prefills the name
	if m.anc == nil || m.anc.ti.Value() != "x" {
		t.Fatalf("reference input must prefill the anchor name, anc=%v val=%q",
			m.anc != nil, m.anc.ti.Value())
	}
	m.anc.ti.SetValue("")
	press(m, "enter")
	if _, ok := doc.Get([]any{"copy"}); ok {
		t.Fatal("empty reference input must remove the reference")
	}

	// With the reference gone, the anchor is deletable.
	press(m, "up", "&")
	m.anc.ti.SetValue("")
	press(m, "enter")
	if n, _ := doc.Get([]any{"base"}); n.Anchor != "" {
		t.Fatalf("unreferenced anchor must be deletable, got %q", n.Anchor)
	}
}

func TestReferenceSetWithCompletion(t *testing.T) {
	m, doc, _ := aliasTestModel(t, "base: &x\n  p: 1\n")
	press(m, "down") // root rows: base(0), copy(1) — cursor on the unset copy
	press(m, "*")
	if m.anc == nil || len(m.anc.cands) != 1 || m.anc.cands[0] != "x" {
		t.Fatalf("* must offer the existing anchors, cands=%v", m.anc.cands)
	}
	press(m, "tab") // complete "x"
	if m.anc.ti.Value() != "x" {
		t.Fatalf("tab must complete the anchor name, got %q", m.anc.ti.Value())
	}
	press(m, "enter")
	cp, _ := doc.Get([]any{"copy"})
	base, _ := doc.Get([]any{"base"})
	if cp != base {
		t.Fatal("committing the reference must share the anchor node")
	}
	if v := m.View(); !contains(v, "*x") {
		t.Fatalf("reference row must show *x:\n%s", v)
	}

	// Editing through the new reference writes the anchored original.
	press(m, "enter")         // drill into the referencing copy menu
	press(m, "down", "enter") // past < Expand >, p field editor
	m.edit.ti.SetValue("9")
	press(m, "enter")
	for _, key := range []string{"base", "copy"} {
		if n, _ := doc.Get([]any{key, "p"}); n.Num == nil || n.Num.String() != "9" {
			t.Fatalf("%s.p = %v, want 9", key, n)
		}
	}
}

func TestReferenceSetRefusals(t *testing.T) {
	m, doc, _ := aliasTestModel(t, "base: &x\n  p: 1\ncopy:\n  p: 2\n")
	// root rows: base(0), copy(1), save, raw — no unknown keys, no issue row

	// A reference that would nest the anchor inside itself is refused:
	// drill into base and point its own field at &x.
	press(m, "enter") // base menu: p(0)
	press(m, "*")
	if m.anc == nil {
		t.Fatal("* must open the reference input")
	}
	m.anc.ti.SetValue("x")
	press(m, "enter")
	if !strings.Contains(mAncErr(m), "circular") {
		t.Fatalf("self-containing reference must be refused, err=%q", mAncErr(m))
	}
	press(m, "esc") // close the input
	press(m, "esc") // back to the root, cursor on base

	// Renaming an anchor to a name another node already holds is refused.
	press(m, "down", "&") // copy row: give it anchor y
	m.anc.ti.SetValue("y")
	press(m, "enter")
	if n, _ := doc.Get([]any{"copy"}); n.Anchor != "y" {
		t.Fatalf("fixture: copy anchor = %q", n.Anchor)
	}
	press(m, "up", "&") // base row: rename x → y
	m.anc.ti.SetValue("y")
	press(m, "enter")
	if !strings.Contains(mAncErr(m), "already in use") {
		t.Fatalf("duplicate anchor name must be refused, err=%q", mAncErr(m))
	}
	press(m, "esc")
	if n, _ := doc.Get([]any{"base"}); n.Anchor != "x" {
		t.Fatalf("refused commit must not touch the doc, anchor=%q", n.Anchor)
	}
}

func mAncErr(m *listModel) string {
	if m.anc == nil {
		return ""
	}
	return m.anc.err
}

// TestAnchorInputUnderCursor pins the input placement: the & / * input
// renders on the line directly below the cursor row, with the remaining rows
// continuing beneath it (not bunched at the menu's end).
func TestAnchorInputUnderCursor(t *testing.T) {
	m, _, _ := aliasTestModel(t, "base: &x\n  p: 1\ncopy: *x\n")
	press(m, "down") // cursor on the copy row
	press(m, "*")
	lines := strings.Split(m.View(), "\n")
	inputIdx, copyIdx := -1, -1
	for i, l := range lines {
		switch {
		case strings.Contains(l, "reference:"):
			inputIdx = i
		case strings.Contains(l, "Copy"):
			copyIdx = i
		}
	}
	if copyIdx == -1 || inputIdx == -1 || inputIdx != copyIdx+1 {
		t.Fatalf("input must render directly under the cursor row:\n%s", m.View())
	}
	// the input block is input + completion hint; rows continue beneath it
	if !strings.Contains(strings.Join(lines[inputIdx:inputIdx+3], "\n"), "Save and exit") {
		t.Fatalf("rows must continue below the input:\n%s", m.View())
	}
}

// TestAnchorBadgeUsesAnchorColor pins the shared marker color: the &anchor
// and *reference badges and the title suffix all render in mAnchor violet.
func TestAnchorBadgeUsesAnchorColor(t *testing.T) {
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(old) })

	m := newListModel(testModel(), testDoc(), Options{})
	doc := m.doc
	base, _ := doc.Get([]any{"name"})
	base.Anchor = "x"

	want := mAnchor.Render("&x")
	if badge := m.anchorBadge([]any{"name"}); !strings.Contains(badge, want) {
		t.Fatalf("anchor badge must render in the anchor color, got %q", badge)
	}
	doc.Set([]any{"copy"}, base) // an occurrence: same node, another path
	if badge := m.anchorBadge([]any{"copy"}); !strings.Contains(badge, mAnchor.Render("*x")) {
		t.Fatalf("reference badge must render in the anchor color, got %q", badge)
	}
}

// aliasJumpModel builds the menu for an anchor/alias document whose schema
// knows the given keys as objects with one integer field each (the gr picker
// needs more keys than aliasTestModel's two).
func aliasJumpModel(t *testing.T, src string, keys ...string) *listModel {
	t.Helper()
	doc, _, err := yamldoc.Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	obj := func(key string) *smodel.FormNode {
		return &smodel.FormNode{Key: key, Title: strings.ToUpper(key[:1]) + key[1:],
			Kind:     smodel.KObject,
			Children: []*smodel.FormNode{{Key: "p", Title: "P", Kind: smodel.KScalar, JSONType: "integer"}}}
	}
	var children []*smodel.FormNode
	for _, k := range keys {
		children = append(children, obj(k))
	}
	model := &smodel.FormNode{Key: "$", Title: "Aliases", Kind: smodel.KObject, Children: children}
	m := newListModel(model, doc, Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return m
}

// jumpCursorTo moves the root cursor onto the row for path.
func jumpCursorTo(t *testing.T, m *listModel, path []any) {
	t.Helper()
	rows := menuRows(m)
	idx := cursorByPath(rows, path)
	if idx < 0 {
		t.Fatalf("no row for %v in %+v", path, rows)
	}
	for m.top().cursor < idx {
		press(m, "down")
		rows = menuRows(m)
	}
	if !pathEq(rows[m.top().cursor].path, path) {
		t.Fatalf("cursor on %v, want %v", rows[m.top().cursor].path, path)
	}
}

func TestAnchorJumpGaGr(t *testing.T) {
	four := "base: &x\n  p: 1\ncopy: *x\nagain: *x\nthird: *x\n"
	fourKeys := []string{"base", "copy", "again", "third"}

	t.Run("ga jumps to the carrier and esc returns", func(t *testing.T) {
		m := aliasJumpModel(t, four, fourKeys...)
		jumpCursorTo(t, m, []any{"copy"})
		press(m, "g", "a")
		if len(m.stack) != 2 || !pathEq(m.top().path, []any{"base"}) {
			t.Fatalf("ga landed on %v (stack %d)", m.top().path, len(m.stack))
		}
		if v := m.View(); !contains(v, ": &x") || !contains(v, "esc returns") {
			t.Fatalf("anchor menu must show `: &x` and the return notice:\n%s", v)
		}
		press(m, "esc")
		if len(m.stack) != 1 || m.jumpOrigin != nil {
			t.Fatalf("esc must restore the root (stack %d, origin %v)", len(m.stack), m.jumpOrigin != nil)
		}
		if v := m.View(); !contains(v, "> Copy") {
			t.Fatalf("cursor must be back on copy:\n%s", v)
		}
	})

	t.Run("ga return works with q too", func(t *testing.T) {
		m := aliasJumpModel(t, four, fourKeys...)
		jumpCursorTo(t, m, []any{"copy"})
		press(m, "g", "a", "q")
		if len(m.stack) != 1 || !pathEq(menuRows(m)[m.top().cursor].path, []any{"copy"}) {
			t.Fatalf("q must return to copy (stack %d)", len(m.stack))
		}
	})

	t.Run("gr with one other reference jumps directly", func(t *testing.T) {
		m := aliasJumpModel(t, "base: &x\n  p: 1\ncopy: *x\nagain: *x\n", "base", "copy", "again")
		jumpCursorTo(t, m, []any{"again"})
		press(m, "g", "r")
		if m.refPick != nil {
			t.Fatal("a single target must jump without the picker")
		}
		if len(m.stack) != 2 || !pathEq(m.top().path, []any{"copy"}) {
			t.Fatalf("gr landed on %v (stack %d)", m.top().path, len(m.stack))
		}
	})

	t.Run("gr with several references opens the picker", func(t *testing.T) {
		m := aliasJumpModel(t, four, fourKeys...)
		jumpCursorTo(t, m, []any{"copy"})
		press(m, "g", "r")
		if m.refPick == nil {
			t.Fatal("two targets must open the picker")
		}
		v := m.View()
		if !contains(v, "References to &x (2)") || !contains(v, "$.again") || !contains(v, "$.third") {
			t.Fatalf("picker must list the other references:\n%s", v)
		}
		press(m, "down", "enter")
		if m.refPick != nil || len(m.stack) != 2 || !pathEq(m.top().path, []any{"third"}) {
			t.Fatalf("enter must jump to third (top %v, stack %d)", m.top().path, len(m.stack))
		}
		press(m, "esc")
		if len(m.stack) != 1 || !pathEq(menuRows(m)[m.top().cursor].path, []any{"copy"}) {
			t.Fatalf("esc must return to copy (stack %d)", len(m.stack))
		}
	})

	t.Run("gr from the carrier lists all references and esc just closes", func(t *testing.T) {
		m := aliasJumpModel(t, four, fourKeys...)
		jumpCursorTo(t, m, []any{"base"})
		press(m, "g", "r")
		if m.refPick == nil || len(m.refPick.paths) != 3 {
			t.Fatalf("carrier gr must pick among 3 references, got %+v", m.refPick)
		}
		if v := m.View(); !contains(v, "References to &x (3)") || !contains(v, "$.copy") {
			t.Fatalf("picker must list every reference:\n%s", v)
		}
		press(m, "esc")
		if m.refPick != nil || len(m.stack) != 1 {
			t.Fatalf("esc must close without moving (stack %d)", len(m.stack))
		}
	})

	t.Run("notices", func(t *testing.T) {
		m := aliasJumpModel(t, four, fourKeys...)
		jumpCursorTo(t, m, []any{"base"})
		press(m, "g", "a")
		if v := m.View(); !contains(v, "already at the anchor &x") {
			t.Fatalf("ga on the carrier must explain:\n%s", v)
		}
		press(m, "enter") // drill into base: the p row carries no anchor
		press(m, "g", "a")
		if v := m.View(); !contains(v, "not on an anchor or reference") {
			t.Fatalf("ga off an anchor must explain:\n%s", v)
		}
		press(m, "g", "r")
		if v := m.View(); !contains(v, "not on an anchor or reference") {
			t.Fatalf("gr off an anchor must explain:\n%s", v)
		}
		press(m, "esc")
	})

	t.Run("gr with no other references", func(t *testing.T) {
		m := aliasJumpModel(t, "base: &x\n  p: 1\ncopy: *x\n", "base", "copy")
		jumpCursorTo(t, m, []any{"copy"})
		press(m, "g", "r")
		if m.refPick != nil || len(m.stack) != 1 {
			t.Fatalf("no other references must stay put (stack %d)", len(m.stack))
		}
		if v := m.View(); !contains(v, "no other references to &x") {
			t.Fatalf("gr must explain:\n%s", v)
		}
	})

	t.Run("plain a and r keys are unaffected", func(t *testing.T) {
		m := aliasJumpModel(t, four, fourKeys...)
		jumpCursorTo(t, m, []any{"again"})
		press(m, "enter") // drill into the schema-less again menu
		press(m, "a")
		if m.add == nil {
			t.Fatal("plain a must still open the add-key input")
		}
		press(m, "esc", "esc")
		press(m, "r")
		if !m.search.active || !m.search.replace {
			t.Fatal("plain r must still open the replace search")
		}
		press(m, "esc")
	})
}
