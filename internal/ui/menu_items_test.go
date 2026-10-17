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
	if v := m.View(); !contains(v, "$.mounts[0]") {
		t.Fatalf("breadcrumb should carry the item index:\n%s", v)
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

func TestMenuMoveItem(t *testing.T) {
	doc := tobj(tmem("logging", tobj(tmem("tags", tarr(tstr("prod"), tstr("dev"), tstr("stage"))))))
	m := newListModel(testModel(), doc, Options{})
	press(m, "down", "down", "down", "enter", "down", "enter", "down") // Logging → Tags → item 0
	if got := strArrAt(t, doc, "logging", "tags"); !eqStrs(got, "prod", "dev", "stage") {
		t.Fatalf("setup tags = %v", got)
	}

	press(m, "J") // move prod down, cursor follows the row
	if got := strArrAt(t, doc, "logging", "tags"); !eqStrs(got, "dev", "prod", "stage") {
		t.Fatalf("after J tags = %v, want [dev prod stage]", got)
	}
	if m.top().cursor != 2 {
		t.Fatalf("cursor = %d, want 2 (on the moved row)", m.top().cursor)
	}
	press(m, "K") // and back up
	if got := strArrAt(t, doc, "logging", "tags"); !eqStrs(got, "prod", "dev", "stage") {
		t.Fatalf("after K tags = %v, want [prod dev stage]", got)
	}
	if m.top().cursor != 1 {
		t.Fatalf("cursor = %d, want 1", m.top().cursor)
	}
	press(m, "K") // already first: notice, doc untouched
	if m.notice != "already first item" || !eqStrs(strArrAt(t, doc, "logging", "tags"), "prod", "dev", "stage") {
		t.Fatalf("first-item K: notice=%q tags=%v", m.notice, strArrAt(t, doc, "logging", "tags"))
	}
	press(m, "down", "down") // stage (last)
	press(m, "J")
	if m.notice != "already last item" || !eqStrs(strArrAt(t, doc, "logging", "tags"), "prod", "dev", "stage") {
		t.Fatalf("last-item J: notice=%q tags=%v", m.notice, strArrAt(t, doc, "logging", "tags"))
	}

	// one key = one undo step
	press(m, "up", "up") // back to prod
	press(m, "J")
	if got := strArrAt(t, doc, "logging", "tags"); !eqStrs(got, "dev", "prod", "stage") {
		t.Fatalf("after J tags = %v", got)
	}
	press(m, "u")
	if got := strArrAt(t, doc, "logging", "tags"); !eqStrs(got, "prod", "dev", "stage") {
		t.Fatalf("after u tags = %v, want [prod dev stage]", got)
	}

	// single-item array: both directions refuse
	doc2 := tobj(tmem("logging", tobj(tmem("tags", tarr(tstr("solo"))))))
	m2 := newListModel(testModel(), doc2, Options{})
	press(m2, "down", "down", "down", "enter", "down", "enter", "down")
	press(m2, "J")
	if m2.notice != "already last item" || !eqStrs(strArrAt(t, doc2, "logging", "tags"), "solo") {
		t.Fatalf("solo J: notice=%q tags=%v", m2.notice, strArrAt(t, doc2, "logging", "tags"))
	}
	press(m2, "K")
	if m2.notice != "already first item" || !eqStrs(strArrAt(t, doc2, "logging", "tags"), "solo") {
		t.Fatalf("solo K: notice=%q tags=%v", m2.notice, strArrAt(t, doc2, "logging", "tags"))
	}

	// object item rows (drill rows) move the same way; undo restores
	doc3 := tobj(tmem("mounts", tarr(tobj(tmem("src", tstr("/a"))), tobj(tmem("src", tstr("/b"))))))
	m3 := newListModel(testModel(), doc3, Options{})
	press(m3, "down", "down", "down", "down", "enter", "down") // Mounts → item 0
	press(m3, "J")
	if s := strAt(t, doc3, "mounts", 0, "src"); s != "/b" {
		t.Fatalf("mounts[0].src = %q, want /b", s)
	}
	if m3.top().cursor != 2 {
		t.Fatalf("cursor = %d, want 2 (on the moved mount row)", m3.top().cursor)
	}
	press(m3, "u")
	if s := strAt(t, doc3, "mounts", 0, "src"); s != "/a" {
		t.Fatalf("after undo mounts[0].src = %q, want /a", s)
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
	// a value exists: enter skips the variant picker and edits the current
	// variant (Token object) directly
	if m.top().kind != menuObject || m.top().node == nil || m.top().node.Title != "Token" {
		t.Fatalf("enter with a value must edit the current variant, got kind=%v", m.top().kind)
	}
	if m.top().oneOf == nil {
		t.Fatal("the variant editor must carry its oneOf for the type switch")
	}

	// ctrl+t opens the type picker on the current variant
	press(m, "ctrl+t")
	if m.top().kind != menuOneOf {
		t.Fatalf("ctrl+t must open the variant picker, got %d", m.top().kind)
	}
	v := m.View()
	if !contains(v, "● Token") || !contains(v, "○ None") {
		t.Fatalf("variant markers wrong:\n%s", v)
	}
	if !contains(v, "token=secret123") {
		t.Fatalf("current variant should show its value summary:\n%s", v)
	}
	if m.top().cursor != 1 {
		t.Fatalf("picker should open on the current variant, cursor = %d", m.top().cursor)
	}
	press(m, "enter")
	// the chosen editor replaces the picker in the stack: esc lands on the
	// parent menu, and ctrl+t offers the type switch from the editor
	if len(m.stack) != 2 || m.top().kind != menuObject || m.top().node == nil || m.top().node.Title != "Token" {
		t.Fatalf("enter on current variant should re-enter its editor:\n%s", m.View())
	}
	press(m, "esc") // out of the token editor, back to the root list

	// re-enter, then offer the switch via ctrl+t: picker, cursor onto None
	press(m, "enter", "ctrl+t", "up")

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

	// n on the confirm keeps the value: enter edits the current None variant
	// (enum overlay) directly, ctrl+t offers the switch to Token
	doc2 := tobj(tmem("auth", tstr("none")))
	m2 := newListModel(testModel(), doc2, Options{})
	press(m2, "down", "down", "down", "down", "down", "enter", "ctrl+t", "down", "enter")
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
		"at '$.server.port': minimum value is 1024",
		"at '$.name': got number, want string",
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
	if len(m.search.results) != 1 { // $.server.port only: $.server just rolls the value up
		t.Fatalf("results = %d, want 1", len(m.search.results))
	}
	press(m, "enter")
	if m.search.active {
		t.Fatal("enter should park the search and render the jump target")
	}
	if len(m.stack) != 2 || !contains(m.View(), "$.server") {
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

	// search is scoped to the path the breadcrumb shows
	m3 := newListModel(testModel(), testDoc(), Options{})
	m3.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	press(m3, "down", "down", "enter") // root → Server menu ($.server)
	press(m3, "/", "n", "a", "m", "e")
	if len(m3.search.results) != 0 {
		t.Fatalf("scoped search must exclude rows outside $.server, got %d", len(m3.search.results))
	}
	press(m3, "esc")
	press(m3, "/", "0", ".", "0")
	if len(m3.search.results) != 2 { // regex default: "." matches any char, so 8080 counts too
		t.Fatalf("scoped regex search = %d results, want 2", len(m3.search.results))
	}
	if v := m3.View(); !contains(v, "$.server.host") {
		t.Fatalf("scoped results should show breadcrumb-style paths:\n%s", v)
	}
	press(m3, "esc")
	press(m3, "/", "0", "\\", ".", "0")
	if len(m3.search.results) != 1 { // escaped dot: only $.server.host = 0.0.0.0
		t.Fatalf("scoped escaped search = %d results, want 1", len(m3.search.results))
	}
	press(m3, "esc")
}

func TestMenuSearchJumpReturn(t *testing.T) {
	// a jump out of the results is reversible: esc at the landing reopens the
	// search, and closing the search returns to the menu it was opened from
	m := newListModel(testModel(), testDoc(), Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	press(m, "/", "8", "0", "8", "0")
	press(m, "enter") // land on $.server.port
	if m.search.active || len(m.stack) != 2 {
		t.Fatalf("jump should land inside Server: stack %d, active %v", len(m.stack), m.search.active)
	}
	if !contains(m.View(), "> Port") {
		t.Fatalf("cursor should sit on Port:\n%s", m.View())
	}

	press(m, "esc") // back to the list: query, results and cursor intact
	if !m.search.active {
		t.Fatal("esc at the landing should reopen the search")
	}
	if len(m.search.results) != 1 || m.search.cursor != 0 {
		t.Fatalf("parked search = %d results, cursor %d", len(m.search.results), m.search.cursor)
	}
	if v := m.View(); !contains(v, "search: 8080") {
		t.Fatalf("the query should survive the jump:\n%s", v)
	}

	press(m, "esc") // closing returns to the pre-search menu, not to $.server
	if m.search.active || len(m.stack) != 1 || !contains(m.View(), "> Name*") {
		t.Fatalf("close should restore the root menu:\n%s", m.View())
	}

	// edits made at the landing show up in the refreshed list on return
	m2 := newListModel(testModel(), testDoc(), Options{})
	m2.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	press(m2, "/", "8", "0", "8", "0", "enter")
	press(m2, "enter", "ctrl+u", "9", "9", "9", "9", "enter") // edit the landed value
	if got := numAt(t, m2.doc, "server", "port"); got != "9999" {
		t.Fatalf("port = %s, want 9999", got)
	}
	press(m2, "esc")
	if !m2.search.active || len(m2.search.results) != 0 {
		t.Fatalf("the refreshed list should drop the edited value: active %v, %d results",
			m2.search.active, len(m2.search.results))
	}

	// landing on a container: drilly deeper first, the overlay comes last
	m3 := newListModel(testModel(), testDoc(), Options{})
	m3.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	press(m3, "/", "m", "o", "u", "n", "t", "s")
	press(m3, "enter") // land on the Mounts menu
	if len(m3.stack) != 2 {
		t.Fatalf("mounts landing stack = %d, want 2", len(m3.stack))
	}
	press(m3, "enter") // drill into the first item
	if len(m3.stack) != 3 {
		t.Fatalf("drill stack = %d, want 3", len(m3.stack))
	}
	press(m3, "esc") // one level up, the search stays parked
	if m3.search.active || len(m3.stack) != 2 {
		t.Fatalf("esc below the landing should pop a level: stack %d, active %v",
			len(m3.stack), m3.search.active)
	}
	press(m3, "esc") // back at the landing: the list reopens
	if !m3.search.active {
		t.Fatal("esc at the landing should reopen the search")
	}

	// replace mode parks and returns the same way
	m4 := newListModel(testModel(), testDoc(), Options{})
	m4.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	press(m4, "r", "w", "e", "b", "a", "p", "p", "enter")
	press(m4, "esc")
	if !m4.search.active || !m4.search.replace {
		t.Fatal("the replace overlay should come back")
	}
	if v := m4.View(); !contains(v, "replace:") {
		t.Fatalf("replace input missing after the return:\n%s", v)
	}
}

func TestMenuSearchSecondJumpReturn(t *testing.T) {
	// a jump made after a full park→return cycle must still close back at the
	// entry menu: every park re-captures the return point
	m := newListModel(testModel(), testDoc(), Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	press(m, "/", "8", "0", "8", "0", "enter") // jump to $.server.port
	if len(m.stack) != 2 {
		t.Fatalf("first jump stack = %d, want 2", len(m.stack))
	}
	press(m, "esc") // back at the root, the search list reopens
	if !m.search.active || len(m.stack) != 1 {
		t.Fatalf("first return: active %v, stack %d", m.search.active, len(m.stack))
	}
	press(m, "ctrl+u", "0", ".", "0", ".", "0", ".", "0", "enter") // jump to $.server.host
	if len(m.stack) != 2 {
		t.Fatalf("second jump stack = %d, want 2", len(m.stack))
	}
	press(m, "esc") // the list reopens, the stack is back at the entry menu
	if !m.search.active || len(m.stack) != 1 {
		t.Fatalf("second return: active %v, stack %d", m.search.active, len(m.stack))
	}
	press(m, "esc") // close the list
	if m.search.active || len(m.stack) != 1 || !contains(m.View(), "> Name*") {
		t.Fatalf("close should restore the entry menu:\n%s", m.View())
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

func TestMenuSearchReplace(t *testing.T) {
	doc := testDoc()
	m := newListModel(testModel(), doc, Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	// r opens the overlay in replace mode, search input focused
	press(m, "r")
	if !m.search.active || !m.search.replace {
		t.Fatal("r should open search in replace mode")
	}
	if !m.search.ti.Focused() || m.search.rti.Focused() {
		t.Fatal("the search input must be focused first")
	}
	if v := m.View(); !contains(v, "replace:") || !contains(v, "case-insensitive") {
		t.Fatalf("replace overlay should show the input and case mode:\n%s", m.View())
	}

	// the query previews the projected replacement on string hits only
	press(m, "/data")
	if len(m.search.results) != 1 {
		t.Fatalf("results = %d, want the $.mounts[0].src hit", len(m.search.results))
	}
	press(m, "tab")
	if !m.search.rti.Focused() {
		t.Fatal("tab should focus the replace input")
	}
	press(m, "/opt")
	if v := m.View(); !contains(v, "$.mounts[0].src  /data → /opt") {
		t.Fatalf("preview label missing:\n%s", v)
	}

	// enter replaces the highlighted value and keeps the overlay open
	press(m, "enter")
	if s := strAt(t, doc, "mounts", 0, "src"); s != "/opt" {
		t.Fatalf("src = %q, want /opt", s)
	}
	if !m.search.active {
		t.Fatal("enter on the replace input must keep the overlay open")
	}
	if len(m.search.results) != 0 { // "/opt" no longer contains "/data"
		t.Fatalf("replaced value should drop from the results, got %d", len(m.search.results))
	}
	press(m, "esc")
	if m.search.active || m.search.replace {
		t.Fatal("esc should close the overlay")
	}
	if s := strAt(t, doc, "mounts", 0, "dst"); s != "/mnt" {
		t.Fatalf("esc must not touch other values, dst = %q", s)
	}

	// ctrl+r replaces every hit in scope; numbers stay untouched
	doc2 := tobj(tmem("name", tstr("alpha")),
		tmem("server", tobj(tmem("host", tstr("alpha.example.com")), tmem("port", tnum("8080")))))
	m2 := newListModel(testModel(), doc2, Options{})
	m2.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	press(m2, "r", "alpha", "tab", "beta", "ctrl+r")
	if s := strAt(t, doc2, "name"); s != "beta" {
		t.Fatalf("name = %q, want beta", s)
	}
	if s := strAt(t, doc2, "server", "host"); s != "beta.example.com" {
		t.Fatalf("host = %q, want beta.example.com", s)
	}
	if got := numAt(t, doc2, "server", "port"); got != "8080" {
		t.Fatalf("port = %s, want 8080 untouched", got)
	}
	if m2.search.active {
		t.Fatal("replace-all should close the overlay")
	}
	if m2.notice != "replaced 2 occurrence(s) in 2 value(s)" {
		t.Fatalf("notice = %q", m2.notice)
	}

	// alt+i toggles case sensitivity for the search and replace alike
	doc3 := tobj(tmem("name", tstr("alpha")),
		tmem("server", tobj(tmem("host", tstr("alpha.example.com")))))
	m3 := newListModel(testModel(), doc3, Options{})
	m3.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	press(m3, "r", "ALPHA")
	if len(m3.search.results) != 2 {
		t.Fatalf("folded ALPHA = %d hits, want 2", len(m3.search.results))
	}
	press(m3, "alt+i")
	if len(m3.search.results) != 0 {
		t.Fatalf("exact-case ALPHA = %d hits, want 0", len(m3.search.results))
	}
	if v := m3.View(); !contains(v, "case-sensitive") || contains(v, "case-insensitive") {
		t.Fatalf("help line should show case-sensitive:\n%s", v)
	}
	press(m3, "enter", "ctrl+r") // both must no-op on zero hits
	if s := strAt(t, doc3, "name"); s != "alpha" {
		t.Fatalf("enter/ctrl+r with 0 hits must not write, name = %q", s)
	}
	press(m3, "alt+i")
	if len(m3.search.results) != 2 {
		t.Fatalf("toggling back should restore the folded hits, got %d", len(m3.search.results))
	}

	// the replace scope follows the breadcrumb: only the subtree is rewritten
	doc6 := tobj(tmem("name", tstr("same")),
		tmem("server", tobj(tmem("host", tstr("same")))))
	m6 := newListModel(testModel(), doc6, Options{})
	m6.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	press(m6, "down", "down", "enter") // root → $.server
	press(m6, "r", "same", "tab", "other", "ctrl+r")
	if s := strAt(t, doc6, "server", "host"); s != "other" {
		t.Fatalf("host = %q, want other", s)
	}
	if s := strAt(t, doc6, "name"); s != "same" {
		t.Fatalf("scoped replace leaked to $.name: %q", s)
	}

	// / opens jump search; tab arms replace mid-flight
	m4 := newListModel(testModel(), testDoc(), Options{})
	m4.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	press(m4, "/", "t", "a", "b")
	if m4.search.replace {
		t.Fatal("typing in the search input must not arm replace")
	}
	press(m4, "tab")
	if !m4.search.replace || !m4.search.rti.Focused() {
		t.Fatal("tab should arm replace and focus its input")
	}

	// esc aborts without writing
	doc5 := testDoc()
	m5 := newListModel(testModel(), doc5, Options{})
	m5.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	press(m5, "r", "/data", "tab", "zzz", "esc")
	if s := strAt(t, doc5, "mounts", 0, "src"); s != "/data" {
		t.Fatalf("esc must discard the replacement, src = %q", s)
	}

	// with replace armed, enter on the search input still jumps
	press(m4, "esc", "/", "webapp")
	if len(m4.search.results) != 1 {
		t.Fatalf("webapp hits = %d, want 1", len(m4.search.results))
	}
	press(m4, "tab", "tab") // arm replace, focus back on the search input
	if !m4.search.replace || !m4.search.ti.Focused() {
		t.Fatal("the second tab should return focus to the search input")
	}
	press(m4, "enter")
	if m4.search.active {
		t.Fatal("enter on the search input still jumps")
	}
	if v := m4.View(); !contains(v, "Name*") || !contains(v, "webapp") {
		t.Fatalf("jump target wrong:\n%s", v)
	}
	if c := m4.top().cursor; c != 0 {
		t.Fatalf("jump cursor = %d, want the Name row (0)", c)
	}
}

func TestMenuSearchRegex(t *testing.T) {
	// regex is the default query mode: metacharacters are live as soon as the
	// overlay opens, alt+x falls back to literal substring matching and back
	m := newListModel(testModel(), testDoc(), Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	press(m, "/")
	if v := m.View(); !contains(v, "alt+x regex") {
		t.Fatalf("the overlay should open in regex mode:\n%s", v)
	}
	press(m, "0", "\\", ".", "0")
	if len(m.search.results) != 1 { // $.server.host = 0.0.0.0
		t.Fatalf("regex 0\\.0 = %d hits, want 1", len(m.search.results))
	}
	press(m, "alt+x")
	if len(m.search.results) != 0 { // literal `0\.0` matches no value
		t.Fatalf("literal 0\\.0 = %d hits, want 0", len(m.search.results))
	}
	if v := m.View(); !contains(v, "alt+x literal") {
		t.Fatalf("footer should show literal mode:\n%s", v)
	}
	press(m, "alt+x")
	if len(m.search.results) != 1 {
		t.Fatalf("toggling back to regex = %d hits, want 1", len(m.search.results))
	}
	press(m, "esc")

	// the path syntax the breadcrumbs print keeps working in regex mode: "$."
	// anchors the match at the document root instead of reading as an "end of
	// text" assertion
	m5 := newListModel(testModel(), testDoc(), Options{})
	m5.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	press(m5, "/")
	for _, k := range []string{"$", ".", "s", "e", "r", "v", "e", "r"} {
		press(m5, k)
	}
	if len(m5.search.results) == 0 {
		t.Fatal("anchored path query found nothing")
	}
	for _, r := range m5.search.results { // prefix match: $.server and its children
		if p := jsondoc.PathString(r.path); !strings.HasPrefix(p, "$.server") {
			t.Fatalf("anchored hit %q lacks the $.server prefix", p)
		}
	}
	if got := m5.search.results[0].path; !pathEq(got, []any{"server"}) {
		t.Fatalf("exact path should rank first, got %v", got)
	}
	press(m5, "esc")

	// deeper than the root: an anchored query is not a substring search
	m6 := newListModel(testModel(), testDoc(), Options{})
	m6.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	press(m6, "/")
	for _, k := range []string{"$", ".", "p", "o", "r", "t"} {
		press(m6, k)
	}
	if len(m6.search.results) != 0 { // nothing starts with $.port …
		t.Fatalf("anchored $.port = %d hits, want 0", len(m6.search.results))
	}
	press(m6, "esc")

	// an [N] index pasted from a breadcrumb stays literal: the "[" typed on
	// its own must not momentarily break the regex
	m7 := newListModel(testModel(), testDoc(), Options{})
	m7.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	press(m7, "/")
	for _, k := range []string{"$", ".", "m", "o", "u", "n", "t", "s", "["} {
		press(m7, k)
	}
	if m7.search.reErr != "" {
		t.Fatalf("a path index still being typed must keep the regex valid: %s", m7.search.reErr)
	}
	press(m7, "0", "]")
	// prefix semantics: the item row and its fields, exact path first
	if len(m7.search.results) != 3 {
		t.Fatalf("pasted $.mounts[0] = %d hits, want the item row and its fields", len(m7.search.results))
	}
	if got := jsondoc.PathString(m7.search.results[0].path); got != "$.mounts[0]" {
		t.Fatalf("hit = %q, want $.mounts[0]", got)
	}
	press(m7, "esc")

	// an uncompilable pattern yields no results plus a footer error
	m2 := newListModel(testModel(), testDoc(), Options{})
	m2.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	press(m2, "/", "(")
	if len(m2.search.results) != 0 {
		t.Fatalf("invalid regex = %d hits, want 0", len(m2.search.results))
	}
	if v := m2.View(); !contains(v, "invalid regex") {
		t.Fatalf("footer should report the regex error:\n%s", v)
	}
	press(m2, "esc")

	// replace mode: regex by default, $1 groups expand in the replacement
	m3 := newListModel(testModel(), testDoc(), Options{})
	m3.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	press(m3, "r", "(", "\\", "d", "+", ")")
	if len(m3.search.results) != 1 { // only $.server.host holds digits
		t.Fatalf("replace regex hits = %d, want 1", len(m3.search.results))
	}
	press(m3, "tab", "[", "$", "1", "]")
	if v := m3.View(); !contains(v, "[0].[0].[0].[0]") {
		t.Fatalf("preview should expand $1:\n%s", v)
	}
	press(m3, "enter")
	if s := strAt(t, m3.doc, "server", "host"); s != "[0].[0].[0].[0]" {
		t.Fatalf("host = %q, want [0].[0].[0].[0]", s)
	}
}

func TestSearchReplaceUnknownKey(t *testing.T) {
	// strings under schema-undefined keys replace like any other value
	doc := testDoc()
	doc.Set([]any{"comment"}, tstr("hello world"))
	m := newListModel(testModel(), doc, Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	press(m, "r")
	if !m.search.active || !m.search.replace {
		t.Fatal("r should open search in replace mode")
	}
	for _, k := range "hello" {
		press(m, string(k))
	}
	found := false
	for _, r := range m.search.results {
		if r.kind == rowUnknown && pathEq(r.path, []any{"comment"}) {
			found = true
		}
	}
	if !found {
		t.Fatalf("unknown key missing from replace results: %+v", m.search.results)
	}
	press(m, "tab")
	for _, k := range "goodbye" {
		press(m, string(k))
	}
	press(m, "ctrl+r")
	if s := strAt(t, doc, "comment"); s != "goodbye world" {
		t.Fatalf("comment = %q, want goodbye world", s)
	}
	if !contains(m.notice, "replaced 1 occurrence") {
		t.Fatalf("notice = %q", m.notice)
	}
}

// TestInsertItemAroundCursor: a inserts before, A after the cursor item;
// < Add item > keeps appending without touching the doc until a field of
// the fresh item is set.
func TestInsertItemAroundCursor(t *testing.T) {
	mk := func() (*listModel, *jsondoc.Node) {
		doc := tobj(tmem("logging", tobj(tmem("tags", tarr(tstr("a"), tstr("b"), tstr("c"))))))
		tags := childOf(t, childOf(t, testModel(), "logging"), "tags")
		return lmAt(doc, &menuState{kind: menuItems, node: tags,
			path: []any{"logging", "tags"}}), doc
	}

	// rows: < Add item >(0), a(1), b(2), c(3)
	m, doc := mk()
	press(m, "down", "A") // cursor on item 0 → insert after, editor opens
	if m.edit == nil {
		t.Fatal("A on a scalar item must open the editor")
	}
	press(m, "x", "enter")
	if got := strArrAt(t, doc, "logging", "tags"); !eqStrs(got, "a", "x", "b", "c") {
		t.Fatalf("A after item 0 = %v, want a x b c", got)
	}

	m, doc = mk()
	press(m, "down", "down", "a") // cursor on item 1 (b) → insert before
	press(m, "x", "enter")
	if got := strArrAt(t, doc, "logging", "tags"); !eqStrs(got, "a", "x", "b", "c") {
		t.Fatalf("a before item 1 = %v, want a x b c", got)
	}

	// cursor on < Add item >: A clamps to append
	m, doc = mk()
	press(m, "A")
	press(m, "x", "enter")
	if got := strArrAt(t, doc, "logging", "tags"); !eqStrs(got, "a", "b", "c", "x") {
		t.Fatalf("A on the add row = %v, want a b c x", got)
	}

	// object items: A splices an empty container at idx+1 and drills in;
	// the existing item shifts right
	doc2 := tobj(tmem("mounts", tarr(
		tobj(tmem("src", tstr("/a"))),
		tobj(tmem("src", tstr("/b"))),
	)))
	mounts := childOf(t, testModel(), "mounts")
	m2 := lmAt(doc2, &menuState{kind: menuItems, node: mounts, path: []any{"mounts"}})
	press(m2, "down", "A")
	if m2.top().kind != menuObject || !pathEq(m2.top().path, []any{"mounts", 1}) {
		t.Fatalf("A must drill into the spliced item, got kind=%v path=%v",
			m2.top().kind, m2.top().path)
	}
	arr, _ := m2.doc.Get([]any{"mounts"})
	if len(*arr.Array) != 3 || (*arr.Array)[1].Object == nil || len(*(*arr.Array)[1].Object) != 0 {
		t.Fatalf("spliced item 1 = %+v, want an empty object", (*arr.Array)[1])
	}
	shifted := *(*arr.Array)[2].Object
	if len(shifted) != 1 || *shifted[0].Val.Str != "/b" {
		t.Fatalf("item 1 must shift to index 2: %+v", (*arr.Array)[2])
	}

	// < Add item > stays traceless until a field is written
	press(m2, "esc") // back to the items list
	press(m2, "up", "enter")
	if m2.top().kind != menuObject || !pathEq(m2.top().path, []any{"mounts", 3}) {
		t.Fatalf("add row must append at len, path=%v", m2.top().path)
	}
	if arr, _ = m2.doc.Get([]any{"mounts"}); len(*arr.Array) != 3 {
		t.Fatalf("append must not write the doc: len=%d", len(*arr.Array))
	}
}
