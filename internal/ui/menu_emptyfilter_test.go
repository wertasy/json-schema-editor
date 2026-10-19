package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/wertasy/json-schema-editor/internal/jsondoc"
	"github.com/wertasy/json-schema-editor/internal/smodel"
)

// emptyFilterModel mixes set values, an explicit null, an unset field, an
// unset field with a default, empty containers, a null array element and a
// schema-less null key.
func emptyFilterModel(t *testing.T) (*listModel, *jsondoc.Node) {
	t.Helper()
	root := &smodel.FormNode{Key: "$", Title: "Config", Kind: smodel.KObject,
		Children: []*smodel.FormNode{
			{Key: "first", Title: "First", Kind: smodel.KScalar, JSONType: "string"},
			{Key: "empty", Title: "Empty", Kind: smodel.KScalar, JSONType: "string"},
			{Key: "unset", Title: "Unset", Kind: smodel.KScalar, JSONType: "string"},
			{Key: "defaulted", Title: "Defaulted", Kind: smodel.KScalar, JSONType: "string", Default: tstr("dflt")},
			{Key: "last", Title: "Last", Kind: smodel.KScalar, JSONType: "string"},
			{Key: "items", Title: "Items", Kind: smodel.KArray,
				Items: &smodel.FormNode{Key: "items", Kind: smodel.KScalar, JSONType: "string"}},
			{Key: "empty_obj", Title: "EmptyObj", Kind: smodel.KObject},
			{Key: "empty_arr", Title: "EmptyArr", Kind: smodel.KArray,
				Items: &smodel.FormNode{Key: "empty_arr", Kind: smodel.KScalar, JSONType: "string"}},
		}}
	doc := tobj(
		tmem("first", tstr("one")),
		tmem("empty", tnull()),
		tmem("last", tstr("four")),
		tmem("items", tarr(tnull(), tstr("x"))),
		tmem("empty_obj", tobj()),
		tmem("empty_arr", tarr()),
		tmem("raw_null", tnull()),
	)
	m := newListModel(root, doc, Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return m, doc
}

func rowIndexAtPath(m *listModel, path ...any) int {
	for i, r := range menuRows(m) {
		if r.path != nil && pathEq(r.path, path) {
			return i
		}
	}
	return -1
}

func TestEmptyFilterHidesKeysWithoutDocumentValue(t *testing.T) {
	m, _ := emptyFilterModel(t)
	for _, p := range [][]any{
		{"first"}, {"empty"}, {"unset"}, {"defaulted"}, {"last"}, {"items"},
		{"empty_obj"}, {"empty_arr"}, {"raw_null"},
	} {
		if rowIndexAtPath(m, p...) < 0 {
			t.Fatalf("%v missing before the filter", p)
		}
	}

	press(m, "z")
	if !m.hideEmpty {
		t.Fatal("z must turn the empty filter on")
	}
	view := m.View()
	lines := strings.Split(view, "\n")
	labelLine := -1
	for i, l := range lines {
		if strings.Contains(l, "[empty hidden]") {
			labelLine = i
			break
		}
	}
	if labelLine < 1 {
		t.Fatalf("filter state line missing:\n%s", view)
	}
	if !strings.Contains(lines[labelLine-1], "Edit raw JSON") {
		t.Fatalf("state line must sit directly below the menu rows:\n%s", view)
	}
	if strings.Contains(lines[0], "[empty hidden]") ||
		strings.Contains(lines[len(lines)-1], "[empty hidden]") {
		t.Fatalf("state line must not be in the title or help bar:\n%s", view)
	}
	// Absent, explicit null and unset-with-default are all no-value keys: a
	// default is not a document value.
	for _, p := range [][]any{{"empty"}, {"unset"}, {"defaulted"}, {"raw_null"}} {
		if i := rowIndexAtPath(m, p...); i >= 0 {
			t.Fatalf("%v must be hidden, row %+v remains", p, menuRows(m)[i])
		}
	}
	// Present values, empty containers and null array elements are visible.
	for _, p := range [][]any{{"first"}, {"last"}, {"items"}, {"empty_obj"}, {"empty_arr"}} {
		if rowIndexAtPath(m, p...) < 0 {
			t.Fatalf("%v must stay visible", p)
		}
	}

	m.top().cursor = rowIndexAtPath(m, "items")
	press(m, "enter")
	rows := menuRows(m)
	if len(rows) != 3 || rows[1].kind != rowField || rows[2].kind != rowField {
		t.Fatalf("items menu = %+v, want Add plus both elements", rows)
	}
	if !pathEq(rows[1].path, []any{"items", 0}) || !pathEq(rows[2].path, []any{"items", 1}) {
		t.Fatalf("null array element must stay visible: %+v", rows)
	}

	press(m, "z") // show again from the items menu
	press(m, "esc")
	if m.hideEmpty {
		t.Fatal("second z must turn the empty filter off")
	}
	for _, p := range [][]any{{"empty"}, {"unset"}, {"defaulted"}, {"raw_null"}} {
		if rowIndexAtPath(m, p...) < 0 {
			t.Fatalf("%v must return after toggling off:\n%s", p, m.View())
		}
	}
	if strings.Contains(m.View(), "[empty hidden]") {
		t.Fatalf("state line must disappear with the filter:\n%s", m.View())
	}
}

func TestEmptyFilterLabelBelowMenuAtNarrowWidth(t *testing.T) {
	m, _ := emptyFilterModel(t)
	m.Update(tea.WindowSizeMsg{Width: 30, Height: 12})
	press(m, "z")
	view := m.View()
	lines := strings.Split(view, "\n")
	labelLine := -1
	for i, l := range lines {
		if strings.Contains(l, "[empty hidden]") {
			labelLine = i
			break
		}
	}
	if labelLine < 1 {
		t.Fatalf("narrow view lost the filter state line:\n%s", view)
	}
	if strings.Contains(lines[len(lines)-1], "[empty hidden]") {
		t.Fatalf("state line must not be in the help bar:\n%s", view)
	}
	if !strings.Contains(lines[labelLine-1], "Edit raw JSON") &&
		!strings.Contains(lines[labelLine-1], "Save and exit") {
		t.Fatalf("state line must follow the visible menu rows:\n%s", view)
	}
}

func TestEmptyFilterKeepsCursorOnVisibleKey(t *testing.T) {
	m, _ := emptyFilterModel(t)
	m.top().cursor = rowIndexAtPath(m, "last")
	press(m, "z")
	if r := rowAtCursor(m); !pathEq(r.path, []any{"last"}) {
		t.Fatalf("cursor moved to %+v, want the Last key", r)
	}

	// Cursor on a hidden key: land on the next surviving row, not the same
	// numeric position (empty and unset are both gone).
	m2, _ := emptyFilterModel(t)
	m2.top().cursor = rowIndexAtPath(m2, "empty")
	press(m2, "z")
	if r := rowAtCursor(m2); !pathEq(r.path, []any{"last"}) {
		t.Fatalf("cursor moved to %+v, want the next visible key Last", r)
	}

	// The next survivor can be an action row: hiding the last key row must
	// land on < Save and exit >, not skip it by numeric position.
	m3, _ := emptyFilterModel(t)
	m3.top().cursor = rowIndexAtPath(m3, "raw_null")
	press(m3, "z")
	if r := rowAtCursor(m3); r.kind != rowSave {
		t.Fatalf("cursor landed on %+v, want < Save and exit >", r)
	}
}

// TestEmptyFilterRemapsParentCursorAfterChildToggle: z is global, so hiding
// empty rows from a child menu must also shift the saved cursor of every
// parent menu. Otherwise esc returns to the numeric position in the unfiltered
// list, which may be an unrelated row (here: < Save and exit >).
func TestEmptyFilterRemapsParentCursorAfterChildToggle(t *testing.T) {
	m, _ := emptyFilterModel(t)
	itemsAt := rowIndexAtPath(m, "items")
	m.top().cursor = itemsAt
	press(m, "enter")
	if len(m.stack) != 2 {
		t.Fatalf("stack depth = %d, want the items menu open", len(m.stack))
	}

	press(m, "z")
	press(m, "esc")
	if len(m.stack) != 1 {
		t.Fatalf("stack depth = %d, want back at the root", len(m.stack))
	}
	if got, want := m.top().cursor, rowIndexAtPath(m, "items"); got != want {
		t.Fatalf("parent cursor = %d, want %d (Items row)", got, want)
	}
	if r := rowAtCursor(m); !pathEq(r.path, []any{"items"}) {
		t.Fatalf("parent cursor landed on %+v, want Items", r)
	}
}

func TestEmptyFilterHidesMapMembers(t *testing.T) {
	m, doc := nullKeyModel(t)
	doc.Set([]any{"headers", "Host"}, tnull())
	doc.Set([]any{"headers", "X-Trace"}, tstr("1"))

	press(m, "z")
	if v := m.View(); contains(v, "Host") {
		t.Fatalf("null map member must be hidden:\n%s", v)
	}
	if v := m.View(); !contains(v, "X-Trace") {
		t.Fatalf("set map member must stay visible:\n%s", v)
	}

	only, onlyDoc := nullKeyModel(t)
	onlyDoc.Set([]any{"headers", "Host"}, tnull())
	press(only, "z")
	if v := only.View(); !contains(v, "( nothing here )") {
		t.Fatalf("a map with only null members must show the empty placeholder:\n%s", v)
	}
}

func TestEmptyFilterRevealsJumpTarget(t *testing.T) {
	m, _ := emptyFilterModel(t)
	for _, target := range []string{"empty", "unset", "defaulted"} {
		if !m.hideEmpty {
			press(m, "z")
		}
		if rowIndexAtPath(m, target) >= 0 {
			t.Fatalf("%s must be hidden before the jump", target)
		}

		m.jumpToPath([]any{target})
		if m.hideEmpty {
			t.Fatalf("a jump to a hidden %s key must reveal it", target)
		}
		if r := rowAtCursor(m); !pathEq(r.path, []any{target}) {
			t.Fatalf("jump landed on %+v, want %s", r, target)
		}
		if v := m.View(); !contains(v, "empty fields shown") {
			t.Fatalf("reveal must leave a notice:\n%s", v)
		}
		if v := m.View(); contains(v, "[empty hidden]") {
			t.Fatalf("filter indicator must be gone after revealing:\n%s", v)
		}
	}
}

func TestEmptyFilterKeyIsLiteralWhileEditing(t *testing.T) {
	m, _ := emptyFilterModel(t)
	m.top().cursor = rowIndexAtPath(m, "first")
	press(m, "enter")
	if m.edit == nil {
		t.Fatal("enter must open the in-place editor")
	}
	press(m, "z")
	if m.hideEmpty {
		t.Fatal("z typed into an editor must not toggle the filter")
	}
	if got := m.edit.ti.Value(); !strings.HasSuffix(got, "z") {
		t.Fatalf("editor value = %q, want the typed z", got)
	}
}

func TestEmptyFilterDocumentedInHelp(t *testing.T) {
	m, _ := emptyFilterModel(t)
	press(m, "?")
	if v := m.View(); !contains(v, "show/hide fields with no value (null/unset)") {
		t.Fatalf("help overlay must document z:\n%s", v)
	}
}
