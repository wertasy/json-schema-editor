package ui

// Tests for validator-issue surfacing: message parsing, the jumpable status
// row, field marks, and schema-unknown document keys shown as repairable rows.

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/wertasy/json-schema-editor/internal/icons"
	"github.com/wertasy/json-schema-editor/internal/jsondoc"
	"github.com/wertasy/json-schema-editor/internal/smodel"
)

func TestParseIssue(t *testing.T) {
	cases := []struct {
		msg  string
		want string // path rendered as a compact string
		ok   bool
	}{
		{"at '$.server.port': minimum value is 1024", "server/port", true},
		{"at '$': missing property 'port'", "port", true},
		{"at '$.inner': additional properties 'extra_key' not allowed", "inner/extra_key", true},
		{"at '$': additional properties 'a', 'b' not allowed", "a", true},
		{"at '$.a[0].b': got number, want string", "a/0/b", true},
		{"at '$.a/b': unexpected", "a/b", true},
		{"at '$': got string, want object", "", true},
		{"jsonschema validation failed", "", false},
	}
	for _, tc := range cases {
		path, ok := parseIssue(tc.msg)
		if ok != tc.ok {
			t.Errorf("%q: ok = %v, want %v", tc.msg, ok, tc.ok)
			continue
		}
		if got := renderPath(path); got != tc.want {
			t.Errorf("%q: path = %q, want %q", tc.msg, got, tc.want)
		}
	}
}

func renderPath(path []any) string {
	s := ""
	for i, seg := range path {
		if i > 0 {
			s += "/"
		}
		switch v := seg.(type) {
		case string:
			s += v
		case int:
			s += string(rune('0' + v))
		}
	}
	return s
}

func TestWorstIssueIcon(t *testing.T) {
	cases := []struct {
		items []issue
		want  string
	}{
		{nil, icons.Warn},
		{[]issue{{msg: "unknown key", warn: true}}, icons.Warn},
		{[]issue{{msg: "unknown key", warn: true}, {msg: "bad value"}}, icons.Err}, // errors outrank warnings
		{[]issue{{msg: "bad value"}}, icons.Err},
	}
	for _, c := range cases {
		if got := worstIssueIcon(c.items); got != c.want {
			t.Errorf("worstIssueIcon(%+v) = %q, want %q", c.items, got, c.want)
		}
	}
}

func TestUnknownKeyRows(t *testing.T) {
	doc := testDoc()
	doc.Set([]any{"extra_key"}, tstr("x"))
	doc.Set([]any{"server", "legacy"}, tnum("1"))
	m := newListModel(testModel(), doc, Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	// root: the schema-unknown key is visible and flagged
	rows := menuRows(m)
	idx := -1
	for i, r := range rows {
		if r.kind == rowUnknown && r.label == "extra_key" {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatalf("unknown key row missing: %+v", rows)
	}
	if v := m.View(); !contains(v, "(not in schema)") || !contains(v, "extra_key") {
		t.Fatalf("unknown row should be marked:\n%s", v)
	}

	// enter opens the raw JSON editor with the repair hint
	m.top().cursor = idx
	press(m, "enter")
	if m.text == nil {
		t.Fatal("unknown key should open the raw editor")
	}
	if v := m.View(); !contains(v, "not in schema") {
		t.Fatalf("raw editor should explain the repair:\n%s", v)
	}
	press(m, "esc")

	// nested unknown key shows in the sub menu
	for i, r := range menuRows(m) {
		if r.fn != nil && r.fn.Key == "server" {
			m.top().cursor = i
		}
	}
	press(m, "enter") // Server menu
	found := false
	for _, r := range menuRows(m) {
		if r.kind == rowUnknown && r.label == "legacy" {
			found = true
		}
	}
	if !found {
		t.Fatalf("nested unknown key missing:\n%s", m.View())
	}
	press(m, "esc")

	// delete removes the root unknown key
	for i, r := range menuRows(m) {
		if r.kind == rowUnknown {
			m.top().cursor = i
		}
	}
	press(m, "delete")
	if _, ok := doc.Get([]any{"extra_key"}); ok {
		t.Fatal("delete must remove the unknown key")
	}
}

func TestLoadIssuesRowAndJump(t *testing.T) {
	doc := testDoc()
	doc.Set([]any{"extra_key"}, tstr("x"))
	msgs := []string{
		"at '$.server.port': minimum value is 1024",
		"at '$': additional properties 'extra_key' not allowed",
	}
	m := newListModel(testModel(), doc, Options{LoadErrors: msgs})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	// jumpable status row first, field marks on the affected rows
	rows := menuRows(m)
	if len(rows) == 0 || rows[0].kind != rowIssues {
		t.Fatalf("status row should come first: %+v", rows)
	}
	if !contains(rows[0].label, "2 validation errors") {
		t.Fatalf("status label = %q", rows[0].label)
	}
	v := m.View()
	if !contains(v, icons.Err+" 2 validation errors") {
		t.Fatalf("status row not rendered:\n%s", v)
	}
	if !strings.Contains(issueLine(v), "--->") {
		t.Fatalf("status row should offer the drill arrow:\n%s", v)
	}
	if !contains(v, icons.Err) {
		t.Fatalf("affected rows should carry the error mark:\n%s", v)
	}

	// open the list and jump to the offending field
	press(m, "enter")
	if m.issues == nil || len(m.issues.items) != 2 {
		t.Fatalf("issues overlay should list both messages: %+v", m.issues)
	}
	if v := m.View(); !contains(v, "minimum value is 1024") || !contains(v, "additional properties") {
		t.Fatalf("issues view:\n%s", v)
	}
	press(m, "enter") // jump to /server/port
	if m.issues != nil {
		t.Fatal("jump should close the overlay")
	}
	top := m.top()
	if top.node.Key != "server" {
		t.Fatalf("jump should land in the Server menu, got %q", top.node.Key)
	}
	if r := rowAtCursor(m); !pathEq(r.path, []any{"server", "port"}) {
		t.Fatalf("cursor should sit on the port row, got %v", r.path)
	}

	// the first esc returns to the issue list, the second closes it back at
	// the menu the list was opened from — the jump never becomes the context
	press(m, "esc")
	if m.issues == nil {
		t.Fatal("esc at the jump target should return to the issues list")
	}
	press(m, "esc") // close the list
	if m.issues != nil || m.confirm != nil || len(m.stack) != 1 {
		t.Fatalf("close should restore the entry menu: issues %v, confirm %v, stack %d",
			m.issues != nil, m.confirm != nil, len(m.stack))
	}
	m.top().cursor = 0
	press(m, "enter", "down", "enter") // status row → issues → second entry → jump
	if m.issues != nil {
		t.Fatal("jump should close the overlay")
	}
	if r := rowAtCursor(m); r.kind != rowUnknown || r.label != "extra_key" {
		t.Fatalf("cursor should sit on the unknown-key row, got %+v", r)
	}

	// the overlay recomputes against the current doc when a validator exists
	m.opts.Validate = func(*jsondoc.Node) []string {
		return []string{"at '$.name': got number, want string"}
	}
	m.top().cursor = 0
	press(m, "enter")
	if m.issues == nil || len(m.issues.items) != 2 ||
		!contains(m.issues.items[0].msg, "$.name") ||
		!contains(m.issues.items[1].msg, `unknown key "extra_key"`) {
		t.Fatalf("overlay should recompute from the validator: %+v", m.issues)
	}
	press(m, "esc")
	if m.issues != nil {
		t.Fatal("esc should close the overlay")
	}
}

func TestAdditionalPropertiesDedupesAllNames(t *testing.T) {
	doc := testDoc()
	doc.Set([]any{"server", "legacy"}, tnum("1"))
	doc.Set([]any{"server", "other"}, tnum("2"))
	m := newListModel(testModel(), doc, Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	// The validator lists additional properties in map order. Every name in
	// the message must suppress its unknown-key warning, whatever the order.
	for _, msg := range []string{
		"at '$.server': additional properties 'legacy', 'other' not allowed",
		"at '$.server': additional properties 'other', 'legacy' not allowed",
		"at '$.server': additional properties 'other', 'legacy', 'x' not allowed",
	} {
		for _, it := range m.liveIssues([]string{msg}) {
			if it.warn {
				t.Fatalf("%q: warning %q was not deduped", msg, it.msg)
			}
		}
	}

	// A key the message does not list still warns.
	warned := false
	for _, it := range m.liveIssues([]string{
		"at '$.server': additional properties 'legacy' not allowed",
	}) {
		if it.warn && contains(it.msg, `unknown key "other"`) {
			warned = true
		}
	}
	if !warned {
		t.Fatal("unlisted unknown key should keep its warning")
	}
}

func TestAdditionalPropertiesMarksRealUnknownsOnly(t *testing.T) {
	doc := testDoc()
	doc.Set([]any{"server", "legacy"}, tnum("1"))
	doc.Set([]any{"server", "other"}, tnum("2"))
	m := newListModel(testModel(), doc, Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	// A failed oneOf branch lists valid fields (port/host) next to the real
	// unknown key. Only the unknown key (and its ancestors) may be marked.
	m.setIssues(m.liveIssues([]string{
		"at '$.server': additional properties 'port', 'host', 'legacy' not allowed",
	}))
	if m.hasErr([]any{"server", "port"}) || m.hasErr([]any{"server", "host"}) {
		t.Fatalf("valid fields must not carry the error mark: err=%v", m.errPaths)
	}
	if !m.hasErr([]any{"server", "legacy"}) || !m.hasErr([]any{"server"}) {
		t.Fatalf("the real unknown key and its object must be marked: err=%v", m.errPaths)
	}

	// Reordered names produce the same marks.
	m.setIssues(m.liveIssues([]string{
		"at '$.server': additional properties 'legacy', 'host', 'port' not allowed",
	}))
	if m.hasErr([]any{"server", "port"}) || !m.hasErr([]any{"server", "legacy"}) {
		t.Fatalf("marks depend on message order: err=%v", m.errPaths)
	}

	// Only valid names (a non-matching branch): mark the object itself, never
	// the valid siblings.
	m.setIssues(m.liveIssues([]string{
		"at '$.server': additional properties 'port', 'host' not allowed",
	}))
	if m.hasErr([]any{"server", "port"}) || m.hasErr([]any{"server", "host"}) {
		t.Fatalf("valid fields marked: err=%v", m.errPaths)
	}
	if !m.hasErr([]any{"server"}) {
		t.Fatalf("the object itself should be marked: err=%v", m.errPaths)
	}

	// A root-level message with no actual unknown key must not mark every row
	// through a nil prefix.
	m.setIssues(m.liveIssues([]string{
		"at '$': additional properties 'name' not allowed",
	}))
	if len(m.errPaths) != 0 {
		t.Fatalf("root-level mark leaked to rows: %v", m.errPaths)
	}
}

func TestUnknownRowsAlwaysCarryWarningMark(t *testing.T) {
	doc := testDoc()
	doc.Set([]any{"extra_key"}, tnum("1"))
	m := newListModel(testModel(), doc, Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	unknownLine := func() string {
		t.Helper()
		for _, r := range menuRows(m) {
			if r.kind == rowUnknown {
				return m.rowLine(r, false)
			}
		}
		t.Fatal("unknown row missing")
		return ""
	}

	// The variant-aware warning set can miss a row the current menu shows as
	// unknown; the "(not in schema)" label must still carry the yellow mark.
	m.warnPaths = nil
	m.errPaths = nil
	if line := unknownLine(); !contains(line, icons.Warn) || contains(line, icons.Err) {
		t.Fatalf("unknown row mark = %q, want the warning glyph", line)
	}

	// A validator error outranks the fallback warning.
	m.errPaths = [][]any{{"extra_key"}}
	if line := unknownLine(); !contains(line, icons.Err) || contains(line, icons.Warn) {
		t.Fatalf("error-marked unknown row = %q, want the error glyph", line)
	}
}

func TestUnknownKeyIssuesListed(t *testing.T) {
	doc := testDoc()
	doc.Set([]any{"extra_key"}, tstr("x"))
	doc.Set([]any{"server", "legacy"}, tnum("1"))
	m := newListModel(testModel(), doc, Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	// unknown keys warn in the live issue list, paths jump to their rows
	if len(m.issuesNow) != 2 {
		t.Fatalf("issues = %+v, want two unknown-key warnings", m.issuesNow)
	}
	if !m.issuesNow[0].warn || !m.issuesNow[1].warn {
		t.Fatalf("unknown keys must be warnings: %+v", m.issuesNow)
	}
	if !pathEq(m.issuesNow[0].path, []any{"server", "legacy"}) ||
		!pathEq(m.issuesNow[1].path, []any{"extra_key"}) {
		t.Fatalf("warning paths = %v %v", m.issuesNow[0].path, m.issuesNow[1].path)
	}
	if v := m.View(); !contains(v, icons.Warn+" 2 warnings") {
		t.Fatalf("status row missing:\n%s", v)
	}
	if !m.hasWarn([]any{"server", "legacy"}) || !m.hasWarn([]any{"extra_key"}) {
		t.Fatalf("warning marks missing: err=%v warn=%v", m.errPaths, m.warnPaths)
	}

	// validator output combines with the warnings without double-flagging
	m.opts.Validate = func(d *jsondoc.Node) []string {
		if _, ok := d.Get([]any{"extra_key"}); ok {
			return []string{"at '$': additional properties 'extra_key' not allowed"}
		}
		return nil
	}
	m.refreshIssues()
	if len(m.issuesNow) != 2 || !contains(m.issuesNow[0].msg, "additional properties") ||
		!contains(m.issuesNow[1].msg, `unknown key "legacy"`) {
		t.Fatalf("combined issues = %+v", m.issuesNow)
	}
	if m.issuesNow[0].warn || !m.issuesNow[1].warn {
		t.Fatalf("severity mixed up: %+v", m.issuesNow)
	}
	if v := m.View(); !contains(v, icons.Err+" 1 validation error · "+icons.Warn+" 1 warning") {
		t.Fatalf("mixed status label missing:\n%s", v)
	}
	if !m.hasErr([]any{"extra_key"}) || !m.hasWarn([]any{"server", "legacy"}) {
		t.Fatalf("severity routing broken: err=%v warn=%v", m.errPaths, m.warnPaths)
	}

	// removing the keys clears their warnings
	doc.Remove([]any{"extra_key"})
	doc.Remove([]any{"server", "legacy"})
	m.refreshIssues()
	if len(m.issuesNow) != 0 {
		t.Fatalf("warnings must clear after removal: %+v", m.issuesNow)
	}
}

func TestUnknownKeyIssuesSchemaPointerExempt(t *testing.T) {
	// the editor's own "$schema" write-back is bookkeeping, not a mistake
	doc := tobj(tmem("$schema", tstr("mini.schema.json")), tmem("name", tstr("z")))
	root := &smodel.FormNode{Key: "$", Title: "$", Kind: smodel.KObject, Children: []*smodel.FormNode{
		{Key: "name", Title: "Name", Kind: smodel.KScalar, JSONType: "string"},
	}}
	if got := unknownKeyIssues(root, doc); len(got) != 0 {
		t.Fatalf("$schema must not warn: %+v", got)
	}
	// a "$schema" key below the root is ordinary data and still warns
	root2 := &smodel.FormNode{Key: "$", Title: "$", Kind: smodel.KObject, Children: []*smodel.FormNode{
		{Key: "server", Title: "Server", Kind: smodel.KObject},
	}}
	doc2 := tobj(tmem("server", tobj(tmem("$schema", tstr("x")))))
	if got := unknownKeyIssues(root2, doc2); len(got) != 1 || !pathEq(got[0].path, []any{"server", "$schema"}) {
		t.Fatalf("nested $schema should warn: %+v", got)
	}
}

// issueLine extracts the rendered validation-status row.
func issueLine(view string) string {
	for _, l := range strings.Split(view, "\n") {
		if strings.Contains(l, "validation errors") {
			return l
		}
	}
	return ""
}

func TestUnknownKeyWarningColor(t *testing.T) {
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI)
	defer lipgloss.SetColorProfile(old)

	doc := testDoc()
	doc.Set([]any{"extra_key"}, tstr("x"))
	m := newListModel(testModel(), doc, Options{LoadErrors: []string{
		"at '$.server.port': minimum value is 1024",
	}})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	var unknownLine, portLine bool
	for _, l := range strings.Split(m.View(), "\n") {
		switch {
		case strings.Contains(l, "extra_key"):
			unknownLine = true
			// the unknown-key row is warning-only: yellow, never red
			if !strings.Contains(l, "\x1b[33m") || strings.Contains(l, "\x1b[31m") {
				t.Fatalf("unknown-key row must be yellow only, got %q", l)
			}
		case strings.Contains(l, "port=8080"): // the Server row: error ancestor
			portLine = true
			// the error and its ancestors keep the red mark
			if !strings.Contains(l, "\x1b[31m") {
				t.Fatalf("error-marked row must be red, got %q", l)
			}
		}
	}
	if !unknownLine || !portLine {
		t.Fatalf("rows missing from view (unknown=%v port=%v)", unknownLine, portLine)
	}
}
