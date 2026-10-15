package ui

// Array, enum-array, oneOf and search behavior for the menuconfig TUI.

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"json-schema-editor/internal/jsondoc"
)

func strArrAt(t *testing.T, doc *jsondoc.Node, path ...any) []string {
	t.Helper()
	n, ok := doc.Get(path)
	if !ok || n == nil || n.Array == nil {
		return nil
	}
	out := make([]string, 0, len(*n.Array))
	for _, it := range *n.Array {
		if it.Str != nil {
			out = append(out, *it.Str)
		}
	}
	return out
}

func eqStrs(a []string, b ...string) bool {
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

func TestMenuMountsAddFillBack(t *testing.T) {
	doc := tobj() // fresh config: mounts empty
	m := newListModel(testModel(), doc, Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	press(m, "down", "down", "down", "down", "enter") // root → Mounts items
	if m.top().kind != menuItems {
		t.Fatalf("expected items menu, got %d", m.top().kind)
	}
	press(m, "enter") // < Add item > → object item menu
	if m.top().kind != menuObject || !contains(m.View(), "< Remove this item >") {
		t.Fatalf("item menu should offer removal:\n%s", m.View())
	}
	if v := m.View(); !contains(v, "$/mounts[0]") {
		t.Fatalf("breadcrumb should read like a jq path:\n%s", v)
	}
	press(m, "down", "enter") // Source field
	if m.edit == nil {
		t.Fatal("source edit should open inline")
	}
	press(m, "/data", "enter")
	if m.edit != nil {
		t.Fatalf("edit should close:\n%s", m.View())
	}
	if s := strAt(t, doc, "mounts", 0, "src"); s != "/data" {
		t.Fatalf("doc src = %q, want /data", s)
	}
	press(m, "esc") // back to the items menu
	v := m.View()
	if !contains(v, "src=/data") {
		t.Fatalf("items menu should show the member summary:\n%s", v)
	}
	// drill into the item again: fields survive
	press(m, "down", "enter")
	if !contains(m.View(), "Source") || !contains(m.View(), "/data") {
		t.Fatalf("item menu should show Source:\n%s", m.View())
	}
}

func TestMenuDeleteItem(t *testing.T) {
	doc := testDoc() // one mount
	m := newListModel(testModel(), doc, Options{})
	press(m, "down", "down", "down", "down", "enter", "down") // Mounts → item 0
	press(m, "d")
	if n := docArrayLen(doc, []any{"mounts"}); n != 0 {
		t.Fatalf("mounts len = %d, want 0", n)
	}
	if v := m.View(); !contains(v, "< Add item >") || contains(v, "src=/data") {
		t.Fatalf("deleted item should disappear:\n%s", v)
	}
	press(m, "esc") // back to root
	if v := m.View(); !contains(v, "Mounts") || !contains(v, "empty") {
		t.Fatalf("root summary should show empty:\n%s", v)
	}

	// scalar array items delete the same way
	doc2 := tobj(tmem("logging", tobj(tmem("tags", tarr(tstr("prod"), tstr("dev"))))))
	m2 := newListModel(testModel(), doc2, Options{})
	press(m2, "down", "down", "down", "enter", "down", "enter", "down") // Logging → Tags → item 0
	press(m2, "d")
	if got := strArrAt(t, doc2, "logging", "tags"); !eqStrs(got, "dev") {
		t.Fatalf("tags = %v, want [dev]", got)
	}
}

func TestMenuScalarArrayAdd(t *testing.T) {
	doc := tobj()
	m := newListModel(testModel(), doc, Options{})
	press(m, "down", "down", "down", "enter", "down", "enter", "enter") // Logging → Tags → Add
	if m.edit == nil || !m.edit.isNew {
		t.Fatal("scalar add should open the new-item inline editor")
	}
	press(m, "esc") // cancel writes nothing
	if _, ok := doc.Get([]any{"logging", "tags"}); ok {
		t.Fatal("cancelled add must not create the array")
	}
	press(m, "enter", "prod", "enter")
	if got := strArrAt(t, doc, "logging", "tags"); !eqStrs(got, "prod") {
		t.Fatalf("tags = %v, want [prod]", got)
	}
	if v := m.View(); !contains(v, "prod") {
		t.Fatalf("items menu should show the value:\n%s", v)
	}
}

func TestMenuEnumArrayToggle(t *testing.T) {
	doc := tobj()
	m := newListModel(testModel(), doc, Options{})
	press(m, "down", "down", "down", "enter", "enter") // Logging → Levels toggles
	if m.top().kind != menuEnumArray {
		t.Fatalf("expected enum-array menu, got %d", m.top().kind)
	}
	v := m.View()
	for _, want := range []string{"[ ] debug", "[ ] info", "[ ] warn", "[ ] error"} {
		if !contains(v, want) {
			t.Fatalf("toggle rows missing %q:\n%s", want, v)
		}
	}
	press(m, "space") // select debug
	if got := strArrAt(t, doc, "logging", "levels"); !eqStrs(got, "debug") {
		t.Fatalf("levels = %v, want [debug]", got)
	}
	if !contains(m.View(), "[x] debug") {
		t.Fatalf("debug should be checked:\n%s", m.View())
	}
	press(m, "down", "space") // + info → [debug info]
	if got := strArrAt(t, doc, "logging", "levels"); !eqStrs(got, "debug", "info") {
		t.Fatalf("levels = %v, want [debug info]", got)
	}
	press(m, "space") // deselect info → [debug]
	if got := strArrAt(t, doc, "logging", "levels"); !eqStrs(got, "debug") {
		t.Fatalf("levels = %v, want [debug]", got)
	}
	// last toggle off removes the optional key entirely
	press(m, "up", "space")
	if _, ok := doc.Get([]any{"logging", "levels"}); ok {
		t.Fatal("empty optional enum array should be removed")
	}
}

func TestMenuOneOfVariants(t *testing.T) {
	doc := tobj(tmem("auth", tobj(tmem("token", tstr("secret123")))))
	m := newListModel(testModel(), doc, Options{})
	press(m, "down", "down", "down", "down", "down", "enter") // root → Auth
	if m.top().kind != menuOneOf {
		t.Fatalf("expected oneOf menu, got %d", m.top().kind)
	}
	v := m.View()
	if !contains(v, "● Token") || !contains(v, "○ None") {
		t.Fatalf("variant markers wrong:\n%s", v)
	}
	if !contains(v, "token=secret123") {
		t.Fatalf("current variant should show its value summary:\n%s", v)
	}

	// enter on the current variant drills straight in
	press(m, "down", "enter")
	if len(m.stack) != 3 || !contains(m.View(), "$/auth") {
		t.Fatalf("enter on current variant should drill:\n%s", m.View())
	}
	press(m, "esc", "up") // back to the variant list, cursor on None

	// switching an existing value asks first
	press(m, "enter")
	if m.confirm == nil || !contains(m.View(), "Replace current value?") {
		t.Fatalf("switching should confirm:\n%s", m.View())
	}
	press(m, "y")
	// Token variant had no default → value removed, then the enum picker opens
	if _, ok := doc.Get([]any{"auth"}); ok {
		t.Fatal("replacing with the default-less None variant should clear the key")
	}
	if m.sel == nil {
		t.Fatal("enum variant editor should open after replacing")
	}
	press(m, "enter") // pick "none"
	if s := strAt(t, doc, "auth"); s != "none" {
		t.Fatalf("auth = %q, want none", s)
	}
	if v := m.View(); !contains(v, "● None") {
		t.Fatalf("None should now be current:\n%s", v)
	}

	// n on the confirm keeps the value
	doc2 := tobj(tmem("auth", tstr("none")))
	m2 := newListModel(testModel(), doc2, Options{})
	press(m2, "down", "down", "down", "down", "down", "enter", "down", "enter")
	if m2.confirm == nil {
		t.Fatal("switching to Token should confirm")
	}
	press(m2, "n")
	if m2.confirm != nil {
		t.Fatal("n must dismiss the confirm")
	}
	if s := strAt(t, doc2, "auth"); s != "none" {
		t.Fatalf("n must keep the value, auth = %q", s)
	}
}

func TestCursorWrap(t *testing.T) {
	// main list: up from the first row lands on the last, down wraps back
	m := newListModel(testModel(), testDoc(), Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	rows := menuRows(m)
	if len(rows) < 3 {
		t.Fatalf("need a few rows, got %d", len(rows))
	}
	press(m, "up")
	if m.top().cursor != len(rows)-1 {
		t.Fatalf("up on first row: cursor = %d, want %d", m.top().cursor, len(rows)-1)
	}
	if r := rowAtCursor(m); r.kind != rowRaw {
		t.Fatalf("wrapped cursor should sit on the last row, got %+v", r)
	}
	press(m, "down")
	if m.top().cursor != 0 {
		t.Fatalf("down on last row: cursor = %d, want 0", m.top().cursor)
	}

	// enum picker overlay
	press(m, "down", "enter") // Mode row → option picker
	if m.sel == nil {
		t.Fatal("picker did not open")
	}
	press(m, "up")
	if m.sel.cursor != len(m.sel.opts)-1 {
		t.Fatalf("picker up wrap: cursor = %d, want %d", m.sel.cursor, len(m.sel.opts)-1)
	}
	press(m, "down")
	if m.sel.cursor != 0 {
		t.Fatalf("picker down wrap: cursor = %d, want 0", m.sel.cursor)
	}
	press(m, "esc")

	// search overlay: results wrap too
	m2 := newListModel(testModel(), testDoc(), Options{})
	m2.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	press(m2, "/", "e") // matches several rows
	if len(m2.search.results) < 2 {
		t.Fatalf("need several search results, got %d", len(m2.search.results))
	}
	press(m2, "up")
	if m2.search.cursor != len(m2.search.results)-1 {
		t.Fatalf("search up wrap: cursor = %d, want %d", m2.search.cursor, len(m2.search.results)-1)
	}
	press(m2, "down")
	if m2.search.cursor != 0 {
		t.Fatalf("search down wrap: cursor = %d, want 0", m2.search.cursor)
	}

	// issues overlay: two entries wrap as well
	doc := testDoc()
	m3 := newListModel(testModel(), doc, Options{LoadErrors: []string{
		"at '/server/port': minimum value is 1024",
		"at '/name': got number, want string",
	}})
	m3.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	press(m3, "enter") // status row → issues
	if m3.issues == nil || len(m3.issues.items) != 2 {
		t.Fatalf("issues overlay = %+v", m3.issues)
	}
	press(m3, "up")
	if m3.issues.cursor != 1 {
		t.Fatalf("issues up wrap: cursor = %d, want 1", m3.issues.cursor)
	}
	press(m3, "down")
	if m3.issues.cursor != 0 {
		t.Fatalf("issues down wrap: cursor = %d, want 0", m3.issues.cursor)
	}
}

func TestVimKeys(t *testing.T) {
	doc := testDoc()
	m := newListModel(testModel(), doc, Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	// j/k move the cursor, h/l mirror the arrow actions
	press(m, "j", "j") // Name → Season? no: rows Name,Mode,Server…
	if m.top().cursor != 2 {
		t.Fatalf("j j should move to row 2, got %d", m.top().cursor)
	}
	press(m, "k")
	if m.top().cursor != 1 {
		t.Fatalf("k should move back to row 1, got %d", m.top().cursor)
	}

	// l cycles an enum row forward, h cycles back
	if s := strAt(t, doc, "mode"); s != "<missing>" {
		t.Fatalf("precondition: mode unset, got %q", s)
	}
	press(m, "l")
	if s := strAt(t, doc, "mode"); s != "grpc" {
		t.Fatalf("l should cycle the enum forward, mode = %q", s)
	}
	press(m, "h")
	if s := strAt(t, doc, "mode"); s != "http" {
		t.Fatalf("h should cycle the enum back, mode = %q", s)
	}

	// l drills into a container row, h goes back
	press(m, "j", "j") // back onto Server? cursor 1 → j j = 3? recompute below
	for i, r := range menuRows(m) {
		if r.fn != nil && r.fn.Key == "server" {
			m.top().cursor = i
		}
	}
	press(m, "l")
	if len(m.stack) != 2 {
		t.Fatalf("l should drill into Server, stack = %d", len(m.stack))
	}
	press(m, "h")
	if len(m.stack) != 1 {
		t.Fatalf("h should pop back, stack = %d", len(m.stack))
	}

	// inside an input, h and l are plain text
	press(m, "up", "up", "enter") // Name row → inline input
	if m.edit == nil {
		t.Fatal("edit should be open")
	}
	press(m, "h", "l")
	if got := m.edit.ti.Value(); !strings.Contains(got, "hl") {
		t.Fatalf("h/l must type inside an input, value = %q", got)
	}
	press(m, "esc")

	// enum picker: j/k move like ↓/↑
	m2 := newListModel(testModel(), testDoc(), Options{})
	m2.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	for i, r := range menuRows(m2) {
		if r.fn != nil && r.fn.Key == "mode" {
			m2.top().cursor = i
		}
	}
	press(m2, "enter")
	if m2.sel == nil {
		t.Fatal("picker should open")
	}
	start := m2.sel.cursor
	press(m2, "j")
	if m2.sel.cursor == start {
		t.Fatal("j should move the picker cursor")
	}
	press(m2, "k")
	if m2.sel.cursor != start {
		t.Fatalf("k should move it back, got %d", m2.sel.cursor)
	}
}

func TestMenuSearchJump(t *testing.T) {
	m := newListModel(testModel(), testDoc(), Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	press(m, "/")
	if !m.search.active || !contains(m.View(), "search:") {
		t.Fatalf("search mode should open:\n%s", m.View())
	}
	press(m, "p", "o", "r", "t")
	if len(m.search.results) != 1 {
		t.Fatalf("results = %d, want 1 (Port)", len(m.search.results))
	}
	press(m, "enter")
	if m.search.active {
		t.Fatal("enter should close search")
	}
	if len(m.stack) != 2 || !contains(m.View(), "$/server") {
		t.Fatalf("jump should land inside Server:\n%s", m.View())
	}
	if !contains(m.View(), "> Port") {
		t.Fatalf("cursor should sit on Port:\n%s", m.View())
	}

	// esc clears search without moving
	m2 := newListModel(testModel(), testDoc(), Options{})
	press(m2, "/", "t", "o", "k", "esc")
	if m2.search.active || len(m2.stack) != 1 {
		t.Fatal("esc should clear search and stay put")
	}
}

func TestEditCompletion(t *testing.T) {
	// two mounts already carry src values; a third item should suggest them
	doc := tobj(tmem("mounts", tarr(
		tobj(tmem("src", tstr("/data")), tmem("dst", tstr("/mnt"))),
		tobj(tmem("src", tstr("/data2"))))))
	m := newListModel(testModel(), doc, Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	press(m, "down", "down", "down", "down", "enter") // root → Mounts items
	press(m, "enter")                                 // < Add item > → new item menu
	press(m, "down", "enter")                         // past < Remove this item >, Source input (isNew)

	if m.edit == nil || len(m.edit.cands) != 2 {
		t.Fatalf("candidates = %v, want the two existing src values", m.edit.cands)
	}
	if v := m.View(); !contains(v, "tab: ") || !contains(v, "/data2") {
		t.Fatalf("completion hints should be listed:\n%s", v)
	}

	// prefix narrows the list
	press(m, "/", "d", "a", "t", "a", "2")
	if len(m.edit.sugs) != 1 || m.edit.sugs[0] != "/data2" {
		t.Fatalf("filtered suggestions = %v, want [/data2]", m.edit.sugs)
	}
	press(m, "ctrl+u") // clear input
	if len(m.edit.sugs) != 2 {
		t.Fatalf("cleared input should restore both candidates: %v", m.edit.sugs)
	}

	// tab cycles and fills the input
	press(m, "tab")
	if got := m.edit.ti.Value(); got != "/data" {
		t.Fatalf("first tab = %q, want /data", got)
	}
	press(m, "tab")
	if got := m.edit.ti.Value(); got != "/data2" {
		t.Fatalf("second tab = %q, want /data2", got)
	}
	press(m, "enter") // accept
	if s := strAt(t, doc, "mounts", 2, "src"); s != "/data2" {
		t.Fatalf("doc src = %q, want /data2", s)
	}

	// no prefix match → no hint line, plain input still commits
	press(m, "down", "enter") // cursor 1 (Source) → 2 (Target) input
	press(m, "z", "z")        // no "dst" value starts with zz
	if len(m.edit.sugs) != 0 {
		t.Fatalf("suggestions = %v, want none for an unmatched prefix", m.edit.sugs)
	}
	if v := m.View(); contains(v, "tab: ") {
		t.Fatalf("hint line must be hidden without matches:\n%s", v)
	}
	press(m, "enter")
	if s := strAt(t, doc, "mounts", 2, "dst"); s != "zz" {
		t.Fatalf("doc dst = %q, want the typed value", s)
	}
}

func TestMenuRemoveItemConfirm(t *testing.T) {
	doc := tobj(tmem("mounts", tarr(tobj(tmem("src", tstr("/data"))))))
	m := newListModel(testModel(), doc, Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	press(m, "down", "down", "down", "down", "enter") // root → Mounts items
	press(m, "down", "enter")                         // item row → item menu

	press(m, "enter") // < Remove this item > → confirm
	if m.confirm == nil || !contains(m.View(), "Remove this item? (y/n)") {
		t.Fatalf("removal should ask first:\n%s", m.View())
	}
	press(m, "n") // keep
	if m.confirm != nil || m.top().kind != menuObject {
		t.Fatal("n must dismiss the confirm and stay in the item menu")
	}
	if n, ok := doc.Get([]any{"mounts", 0}); !ok || n == nil {
		t.Fatal("n must keep the item")
	}

	press(m, "enter", "y") // remove for real
	if m.confirm != nil {
		t.Fatal("confirm should close after y")
	}
	if m.top().kind != menuItems {
		t.Fatalf("y should return to the items menu, got %v", m.top().kind)
	}
	if _, ok := doc.Get([]any{"mounts", 0}); ok {
		t.Fatal("y must remove the item")
	}

	// esc also cancels
	doc2 := tobj(tmem("mounts", tarr(tobj(tmem("src", tstr("/a"))))))
	m2 := newListModel(testModel(), doc2, Options{})
	m2.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	press(m2, "down", "down", "down", "down", "enter", "down", "enter", "enter", "esc")
	if m2.confirm != nil {
		t.Fatal("esc must cancel the confirm")
	}
	if _, ok := doc2.Get([]any{"mounts", 0}); !ok {
		t.Fatal("esc must keep the item")
	}
}

func TestMenuDeleteKey(t *testing.T) {
	// optional scalar field: host inside Server
	doc := testDoc()
	m := newListModel(testModel(), doc, Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	press(m, "down", "down", "enter") // root → Server
	press(m, "down", "delete")        // cursor on Host
	if _, ok := doc.Get([]any{"server", "host"}); ok {
		t.Fatal("delete must remove the optional host pair")
	}
	if m.notice != "" {
		t.Fatalf("optional delete must not raise a notice, got %q", m.notice)
	}

	// required scalar field: port refuses with a notice
	press(m, "up", "delete") // cursor on Port
	if _, ok := doc.Get([]any{"server", "port"}); !ok {
		t.Fatal("required port must be kept")
	}
	if m.notice != "Port is required" {
		t.Fatalf("notice = %q, want 'Port is required'", m.notice)
	}
	press(m, "down") // any key clears the notice
	if m.notice != "" {
		t.Fatalf("notice must clear on the next key, got %q", m.notice)
	}

	// optional object field: logging on the root menu
	doc2 := tobj(tmem("logging", tobj(tmem("levels", tarr(tstr("info"))))))
	m2 := newListModel(testModel(), doc2, Options{})
	m2.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	press(m2, "down", "down", "down", "delete") // cursor on Logging
	if _, ok := doc2.Get([]any{"logging"}); ok {
		t.Fatal("delete must remove the optional object pair")
	}

	// array item rows delete like d
	doc3 := testDoc()
	m3 := newListModel(testModel(), doc3, Options{})
	m3.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	press(m3, "down", "down", "down", "down", "enter") // root → Mounts items
	press(m3, "down", "delete")                        // cursor on the item row
	if _, ok := doc3.Get([]any{"mounts", 0}); ok {
		t.Fatal("delete on an item row must remove the item")
	}
}
