package ui

// Tests for the dynamic oneOf item menu: add without picking a variant,
// value-aware variant inference and in-place variant switching.

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"json-schema-editor/internal/icons"
	"json-schema-editor/internal/jsondoc"
	"json-schema-editor/internal/smodel"
)

// inferModel mirrors a sing-box-style schema: an array whose items are a
// oneOf of object variants discriminated by a required const `type`, plus a
// mixed oneOf array (enum + object variants) that keeps the classic picker.
func inferModel() (root, inbounds, alerts *smodel.FormNode) {
	inbounds = &smodel.FormNode{Key: "inbounds", Title: "Inbound", Kind: smodel.KOneOf, Variants: []*smodel.FormNode{
		{Key: "inbounds", Title: "variant 1", Kind: smodel.KObject, Children: []*smodel.FormNode{
			{Key: "type", Title: "Type", Kind: smodel.KEnum, Enum: tenum("direct"), Required: true},
			{Key: "listen", Title: "Listen", Kind: smodel.KScalar, JSONType: "string"},
		}},
		{Key: "inbounds", Title: "variant 2", Kind: smodel.KObject, Children: []*smodel.FormNode{
			{Key: "type", Title: "Type", Kind: smodel.KEnum, Enum: tenum("trojan"), Required: true},
			{Key: "listen", Title: "Listen", Kind: smodel.KScalar, JSONType: "string"},
			{Key: "password", Title: "Password", Kind: smodel.KScalar, JSONType: "string", Required: true},
		}},
	}}
	alerts = &smodel.FormNode{Key: "alerts", Title: "Alert", Kind: smodel.KOneOf, Variants: []*smodel.FormNode{
		{Key: "alerts", Title: "None", Kind: smodel.KEnum, Enum: tenum("off")},
		{Key: "alerts", Title: "Webhook", Kind: smodel.KObject, Children: []*smodel.FormNode{
			{Key: "url", Title: "Url", Kind: smodel.KScalar, JSONType: "string", Required: true},
		}},
	}}
	root = &smodel.FormNode{Key: "$", Title: "Config", Kind: smodel.KObject, Children: []*smodel.FormNode{
		{Key: "inbounds", Title: "Inbounds", Kind: smodel.KArray, Items: inbounds},
		{Key: "alerts", Title: "Alerts", Kind: smodel.KArray, Items: alerts},
	}}
	return root, inbounds, alerts
}

func variantTitles(cands []*smodel.FormNode) string {
	s := ""
	for i, c := range cands {
		if i > 0 {
			s += ","
		}
		s += c.Title
	}
	return s
}

func TestMatchingVariants(t *testing.T) {
	_, oneOf, _ := inferModel()
	cases := []struct {
		name string
		val  *jsondoc.Node
		want string
	}{
		{"nil value", nil, "variant 1,variant 2"},
		{"discriminator direct", tobj(tmem("type", tstr("direct"))), "variant 1"},
		{"discriminator trojan plus field", tobj(tmem("type", tstr("trojan")), tmem("password", tstr("x"))), "variant 2"},
		{"shared field only is ambiguous", tobj(tmem("listen", tstr("1.2.3.4"))), "variant 1,variant 2"},
		{"undefined key ignored", tobj(tmem("type", tstr("direct")), tmem("password", tstr("x"))), "variant 1"},
	}
	for _, tc := range cases {
		if got := variantTitles(matchingVariants(oneOf, tc.val)); got != tc.want {
			t.Errorf("%s: candidates = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestCurrentVariantValueAware(t *testing.T) {
	_, oneOf, _ := inferModel()
	if got := currentVariant(oneOf, tobj(tmem("type", tstr("direct")), tmem("listen", tstr("::")))); got != 0 {
		t.Errorf("direct value: currentVariant = %d, want 0", got)
	}
	if got := currentVariant(oneOf, tobj(tmem("type", tstr("trojan")))); got != 1 {
		t.Errorf("trojan value: currentVariant = %d, want 1", got)
	}

	// examples-style mixed oneOf keeps its semantics
	auth := childOf(t, testModel(), "auth")
	if got := currentVariant(auth, tstr("none")); got != 0 {
		t.Errorf("auth none: currentVariant = %d, want 0", got)
	}
	if got := currentVariant(auth, tobj(tmem("token", tstr("t")))); got != 1 {
		t.Errorf("auth token: currentVariant = %d, want 1", got)
	}
	if got := currentVariant(auth, tobj()); got != -1 {
		t.Errorf("auth empty: currentVariant = %d, want -1", got)
	}
	if got := currentVariant(auth, nil); got != -1 {
		t.Errorf("auth nil: currentVariant = %d, want -1", got)
	}
}

func TestOneOfItemAddInfer(t *testing.T) {
	root, oneOf, _ := inferModel()
	doc := tobj()
	m := newListModel(root, doc, Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	press(m, "enter") // Inbounds → items menu
	press(m, "enter") // < Add item > → dynamic merged menu, no variant picker

	top := m.top()
	if top.variantOf != oneOf {
		t.Fatalf("add should open the dynamic menu of the oneOf, got variantOf=%v", top.variantOf)
	}
	// the breadcrumb carries the item index
	if v := m.View(); !contains(v, "$/inbounds/0") {
		t.Fatalf("breadcrumb should carry the item index:\n%s", v)
	}
	if v := m.View(); !contains(v, "Type") || !contains(v, "Listen") || !contains(v, "Password") {
		t.Fatalf("merged menu should show the union of fields:\n%s", v)
	}
	if contains(m.View(), "variant 1") {
		t.Fatalf("merged menu must not be the variant picker:\n%s", m.View())
	}

	// row 0 is < Remove this item >: cursor 1 is the union Type row
	press(m, "down", "enter") // Type → enum overlay with the union options
	if m.sel == nil || !contains(m.View(), "trojan") {
		t.Fatalf("type overlay should offer the union enum:\n%s", m.View())
	}
	press(m, "enter") // choose direct
	if n, _ := doc.Get([]any{"inbounds", 0, "type"}); n == nil || n.Str == nil || *n.Str != "direct" {
		t.Fatalf("inbounds/0.type = %v, want direct", n)
	}
	if v := m.View(); !contains(v, "Listen") || contains(v, "Password") {
		t.Fatalf("direct should narrow the menu to its own fields:\n%s", v)
	}
	// the flip must keep the cursor on the discriminator row
	if r := rowAtCursor(m); r.fn == nil || r.fn.Key != "type" {
		t.Fatalf("cursor should stay on the type row, got %+v", r)
	}

	// switch variant in place: the pinned menu keeps the union discriminator
	press(m, "enter") // still on Type → enum overlay
	if m.sel == nil || !contains(m.View(), "trojan") {
		t.Fatalf("pinned menu must keep the union discriminator:\n%s", m.View())
	}
	press(m, "down", "enter") // choose trojan
	if n, _ := doc.Get([]any{"inbounds", 0, "type"}); n == nil || n.Str == nil || *n.Str != "trojan" {
		t.Fatalf("inbounds/0.type = %v, want trojan", n)
	}
	if v := m.View(); !contains(v, "Password") {
		t.Fatalf("trojan should expose its own fields:\n%s", v)
	}
	if r := rowAtCursor(m); r.fn == nil || r.fn.Key != "type" {
		t.Fatalf("cursor should stay on the type row after switching, got %+v", r)
	}
}

// rowAtCursor returns the row under the top menu's cursor.
func rowAtCursor(m *listModel) row {
	rows := menuRows(m)
	if m.top().cursor < len(rows) {
		return rows[m.top().cursor]
	}
	return row{}
}

// TestVariantFlipKeepsCursor: writes that flip the menu (here: filling a
// variant-specific field) keep the cursor on the edited field.
func TestVariantFlipKeepsCursor(t *testing.T) {
	root, _, _ := inferModel()
	doc := tobj()
	m := newListModel(root, doc, Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	press(m, "enter", "enter") // Inbounds → items → Add: merged menu

	// move onto the Password row (only trojan defines it)
	for i, r := range menuRows(m) {
		if r.fn != nil && r.fn.Key == "password" {
			m.top().cursor = i
			break
		}
	}
	press(m, "enter") // open the inline input
	if m.edit == nil {
		t.Fatal("password input did not open")
	}
	press(m, "x", "y", "enter") // write the value → trojan is the only candidate
	if n, _ := doc.Get([]any{"inbounds", 0, "password"}); n == nil || n.Str == nil || *n.Str != "xy" {
		t.Fatalf("password = %v, want xy", n)
	}
	if top := m.top(); len(top.node.Children) != 3 {
		t.Fatalf("menu should pin the trojan variant (3 fields), got %d", len(top.node.Children))
	}
	if r := rowAtCursor(m); r.fn == nil || r.fn.Key != "password" {
		t.Fatalf("cursor should stay on the password row after the flip, got %+v", r)
	}
}

// testScalarOneOf mirrors sing-box's http_client: a oneOf between a plain
// string variant and an object variant (the classic picker path).
func testScalarOneOf() (root, client *smodel.FormNode) {
	client = &smodel.FormNode{Key: "http_client", Title: "HTTP Client", Kind: smodel.KOneOf, Variants: []*smodel.FormNode{
		{Key: "http_client", Title: "variant 1", Kind: smodel.KScalar, JSONType: "string"},
		{Key: "http_client", Title: "variant 2", Kind: smodel.KObject, Children: []*smodel.FormNode{
			{Key: "engine", Title: "Engine", Kind: smodel.KScalar, JSONType: "string"},
		}},
	}}
	root = &smodel.FormNode{Key: "$", Title: "Root", Kind: smodel.KObject,
		Children: []*smodel.FormNode{client}}
	return root, client
}

func TestScalarVariantSelection(t *testing.T) {
	root, client := testScalarOneOf()
	doc := tobj()
	m := newListModel(root, doc, Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	press(m, "enter") // HTTP Client → variant picker
	if m.top().kind != menuOneOf {
		t.Fatalf("mixed oneOf should keep the picker, got kind=%v", m.top().kind)
	}

	// selecting the string variant leaves the picker and edits on the parent
	// menu's field row
	press(m, "enter")
	if m.top().kind != menuObject {
		t.Fatalf("scalar variant must return to the parent menu, got kind=%v", m.top().kind)
	}
	if m.edit == nil {
		t.Fatal("scalar variant should open the inline editor on the field row")
	}
	press(m, "a", "b", "c")
	if v := m.View(); !contains(v, "abc") {
		t.Fatalf("typed text must be visible on the field row:\n%s", v)
	}
	press(m, "enter")
	if n, _ := doc.Get([]any{"http_client"}); n == nil || n.Str == nil || *n.Str != "abc" {
		t.Fatalf("http_client = %v, want \"abc\"", n)
	}

	// the string value resolves to the first variant (● mark, no confirm)
	if got := currentVariant(client, tstr("abc")); got != 0 {
		t.Fatalf("currentVariant(string) = %d, want 0", got)
	}
	// re-entering with a value skips the picker: the inline editor opens
	// prefilled straight away
	press(m, "enter")
	if m.top().kind != menuObject {
		t.Fatalf("re-entering must stay in the list, got kind=%v", m.top().kind)
	}
	if m.edit == nil || m.edit.ti.Value() != "abc" {
		t.Fatalf("editor should open prefilled directly, got %+v", m.edit)
	}
	// ctrl+t offers the type switch: the picker opens on the current variant
	press(m, "ctrl+t")
	if m.top().kind != menuOneOf {
		t.Fatalf("ctrl+t must open the variant picker, got %d", m.top().kind)
	}
	if m.top().cursor != 0 {
		t.Fatalf("picker must open on the current (string) variant, cursor = %d", m.top().cursor)
	}
	press(m, "enter")
	if m.confirm != nil {
		t.Fatal("re-entering the current variant must not confirm")
	}
	if m.edit == nil || m.edit.ti.Value() != "abc" {
		t.Fatalf("editor should reopen prefilled, got %+v", m.edit)
	}
	press(m, "esc")

	// switching to the object variant still asks before replacing: re-enter
	// (direct inline editor), then offer the type switch with ctrl+t
	press(m, "enter", "ctrl+t") // enter → direct editor; ctrl+t → picker
	press(m, "down")            // cursor on the object variant
	press(m, "enter")
	if m.confirm == nil || !contains(m.View(), "Replace current value?") {
		t.Fatal("switching variants should confirm")
	}
	press(m, "y")
	if _, ok := doc.Get([]any{"http_client"}); ok {
		t.Fatal("default-less object variant should clear the value first")
	}
	if m.top().kind != menuObject || m.top().node.Key != "http_client" {
		t.Fatalf("object variant editor should open, got kind=%v", m.top().kind)
	}
}

func TestVariantPickerCursorOnCurrentValue(t *testing.T) {
	// sing-box ip_cidr style: scalar vs list-of-string variant; the current
	// value is a list, so enter edits the list variant directly and ctrl+t
	// offers the type picker, which opens on the ● row (index 1)
	client := &smodel.FormNode{Key: "ip_cidr", Title: "IP CIDR", Kind: smodel.KOneOf, Variants: []*smodel.FormNode{
		{Key: "ip_cidr", Kind: smodel.KScalar, JSONType: "string"},
		{Key: "ip_cidr", Kind: smodel.KArray,
			Items: &smodel.FormNode{Key: "ip_cidr", Kind: smodel.KScalar, JSONType: "string"}},
	}}
	root := &smodel.FormNode{Key: "$", Title: "Root", Kind: smodel.KObject,
		Children: []*smodel.FormNode{client}}
	m := newListModel(root, tobj(tmem("ip_cidr", tarr(tstr("1.2.3.4")))), Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	press(m, "enter") // IP CIDR row → straight into the list variant editor
	if m.top().kind != menuItems {
		t.Fatalf("enter with a list value must edit the list variant, got kind=%v", m.top().kind)
	}
	if m.top().oneOf == nil {
		t.Fatal("the variant editor must carry its oneOf for the type switch")
	}
	press(m, "ctrl+t")
	if m.top().kind != menuOneOf {
		t.Fatalf("ctrl+t must open the variant picker, got %d", m.top().kind)
	}
	if m.top().cursor != 1 {
		t.Fatalf("picker cursor = %d, want 1 (current value is a list)", m.top().cursor)
	}
	if v := m.View(); !contains(v, "●") || !contains(v, "list of string") {
		t.Fatalf("current list variant should be marked:\n%s", v)
	}

	// no value: the pick-a-type-first flow keeps the picker on row 0
	m2 := newListModel(root, tobj(), Options{})
	m2.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	press(m2, "enter")
	if m2.top().kind != menuOneOf || m2.top().cursor != 0 {
		t.Fatalf("unset picker: kind=%v cursor=%d, want menuOneOf/0", m2.top().kind, m2.top().cursor)
	}

	// search jump reaches the variant editor through rebuildStack with the
	// same rule: an existing value edits directly, oneOf carried for ctrl+t
	m3 := newListModel(root, tobj(tmem("ip_cidr", tarr(tstr("1.2.3.4")))), Options{})
	m3.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	press(m3, "/", "c", "i", "d", "r", "enter")
	if m3.top().kind != menuItems || m3.top().oneOf == nil {
		t.Fatalf("search jump: kind=%v oneOf=%v, want the list variant editor",
			m3.top().kind, m3.top().oneOf != nil)
	}
	press(m3, "ctrl+t")
	if m3.top().kind != menuOneOf || m3.top().cursor != 1 {
		t.Fatalf("search-jump ctrl+t: kind=%v cursor=%d, want menuOneOf/1",
			m3.top().kind, m3.top().cursor)
	}
}

func TestTagReferenceCompletion(t *testing.T) {
	// http_client-like: the string variant references http_clients[].tag
	client := &smodel.FormNode{Key: "http_client", Title: "HTTP Client", Kind: smodel.KOneOf, Variants: []*smodel.FormNode{
		{Key: "http_client", Title: "variant 1", Kind: smodel.KScalar, JSONType: "string", TagRef: "http_client"},
		{Key: "http_client", Title: "variant 2", Kind: smodel.KObject, Children: []*smodel.FormNode{
			{Key: "engine", Title: "Engine", Kind: smodel.KScalar, JSONType: "string"},
		}},
	}}
	clients := &smodel.FormNode{Key: "http_clients", Title: "HTTP Clients", Kind: smodel.KArray,
		Items: &smodel.FormNode{Key: "http_clients", Title: "Client", Kind: smodel.KObject,
			Children: []*smodel.FormNode{{Key: "tag", Title: "Tag", Kind: smodel.KScalar, JSONType: "string"}}}}
	root := &smodel.FormNode{Key: "$", Title: "Root", Kind: smodel.KObject,
		Children: []*smodel.FormNode{client, clients}}
	doc := tobj(tmem("http_clients", tarr(
		tobj(tmem("tag", tstr("c1"))),
		tobj(tmem("tag", tstr("c2"))))))
	m := newListModel(root, doc, Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	press(m, "enter", "enter") // picker → string variant editor
	if m.edit == nil {
		t.Fatal("scalar variant editor did not open")
	}
	if len(m.edit.cands) != 2 || m.edit.cands[0] != "c1" || m.edit.cands[1] != "c2" {
		t.Fatalf("tag-reference candidates = %v, want [c1 c2]", m.edit.cands)
	}
	press(m, "c", "2")
	if len(m.edit.sugs) != 1 || m.edit.sugs[0] != "c2" {
		t.Fatalf("filtered = %v", m.edit.sugs)
	}
	if v := m.View(); !contains(v, "tab: ") || !contains(v, "c2") {
		t.Fatalf("completion hint missing:\n%s", v)
	}
	press(m, "tab", "enter")
	if n, _ := doc.Get([]any{"http_client"}); n == nil || n.Str == nil || *n.Str != "c2" {
		t.Fatalf("http_client = %v, want c2", n)
	}
}

// hintLine extracts the completion hint line from a rendered view.
func hintLine(view string) string {
	for _, l := range strings.Split(view, "\n") {
		if strings.Contains(l, "tab:") {
			return l
		}
	}
	return ""
}

func TestCompletionScrolling(t *testing.T) {
	// five existing same-key values: the hint window must follow the tab
	// selection instead of hiding later candidates behind "…"
	doc := tobj(tmem("mounts", tarr(
		tobj(tmem("src", tstr("v1"))),
		tobj(tmem("src", tstr("v2"))),
		tobj(tmem("src", tstr("v3"))),
		tobj(tmem("src", tstr("v4"))),
		tobj(tmem("src", tstr("v5"))))))
	m := newListModel(testModel(), doc, Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	press(m, "down", "down", "down", "down", "enter") // root → Mounts items
	press(m, "enter")                                 // < Add item > → new item menu
	press(m, "down", "enter")                         // Source input (5 candidates)
	if m.edit == nil || len(m.edit.cands) != 5 {
		t.Fatalf("candidates = %v, want 5", m.edit.sugs)
	}

	// before tabbing: the head of the list
	if h := hintLine(m.View()); !strings.Contains(h, "v1 · v2 · v3") || !strings.HasSuffix(strings.TrimSpace(h), "…") {
		t.Fatalf("initial hint = %q", h)
	}

	// fourth tab selects v4 → the window scrolls past v1
	press(m, "tab", "tab", "tab", "tab")
	h := hintLine(m.View())
	if !strings.Contains(h, "v2 · v3 · v4") || !strings.Contains(h, "…") {
		t.Fatalf("scrolled hint after 4 tabs = %q", h)
	}
	if strings.Contains(h, "v1") {
		t.Fatalf("the scrolled-out candidate must leave the window: %q", h)
	}
	if got := m.edit.ti.Value(); got != "v4" {
		t.Fatalf("tab fill = %q, want v4", got)
	}

	// fifth tab selects v5 → window tail
	press(m, "tab")
	h = hintLine(m.View())
	if !strings.Contains(h, "v3 · v4 · v5") || strings.Contains(h, "v1") || strings.Contains(h, "v2") {
		t.Fatalf("tail hint = %q", h)
	}
	press(m, "enter")
	if s := strAt(t, doc, "mounts", 5, "src"); s != "v5" {
		t.Fatalf("committed src = %q, want v5", s)
	}
}

func TestCompletionDedup(t *testing.T) {
	// same-key path: exact duplicates collapse, empties are skipped
	doc := tobj(tmem("mounts", tarr(
		tobj(tmem("src", tstr("/data"))),
		tobj(tmem("src", tstr("/data"))),
		tobj(tmem("src", tstr("/data2"))),
		tobj(tmem("src", tstr(""))))))
	m := newListModel(testModel(), doc, Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	press(m, "down", "down", "down", "down", "enter") // Mounts items
	press(m, "enter")                                 // Add item
	press(m, "down", "enter")                         // Source input
	if got := m.edit.cands; len(got) != 2 || got[0] != "/data" || got[1] != "/data2" {
		t.Fatalf("same-key candidates = %v, want [/data /data2]", got)
	}
	press(m, "esc", "esc", "esc")

	// tag-reference path: exact duplicate tags collapse too
	client := &smodel.FormNode{Key: "http_client", Title: "HTTP Client", Kind: smodel.KOneOf, Variants: []*smodel.FormNode{
		{Key: "http_client", Title: "variant 1", Kind: smodel.KScalar, JSONType: "string", TagRef: "http_client"},
		{Key: "http_client", Title: "variant 2", Kind: smodel.KObject, Children: []*smodel.FormNode{
			{Key: "engine", Title: "Engine", Kind: smodel.KScalar, JSONType: "string"},
		}},
	}}
	root := &smodel.FormNode{Key: "$", Title: "Root", Kind: smodel.KObject,
		Children: []*smodel.FormNode{client}}
	doc2 := tobj(tmem("http_clients", tarr(
		tobj(tmem("tag", tstr("c1"))),
		tobj(tmem("tag", tstr("c1"))),
		tobj(tmem("tag", tstr("c2"))))))
	m2 := newListModel(root, doc2, Options{})
	m2.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	press(m2, "down", "enter", "enter") // skip the unknown-key status row → picker → string variant
	if got := m2.edit.cands; len(got) != 2 || got[0] != "c1" || got[1] != "c2" {
		t.Fatalf("tag candidates = %v, want [c1 c2]", got)
	}
}

func TestOneOfLeafSummaries(t *testing.T) {
	// tag-like: oneOf between a string and a string[]
	tag := &smodel.FormNode{Key: "tag", Title: "Tag", Kind: smodel.KOneOf, Variants: []*smodel.FormNode{
		{Key: "tag", Title: "variant 1", Kind: smodel.KScalar, JSONType: "string"},
		{Key: "tag", Title: "variant 2", Kind: smodel.KArray,
			Items: &smodel.FormNode{Key: "tag", Kind: smodel.KScalar, JSONType: "string"}},
	}}
	root := &smodel.FormNode{Key: "$", Title: "Root", Kind: smodel.KObject, Children: []*smodel.FormNode{tag}}

	lm := lmAt(tobj(tmem("tag", tstr("geosite-github"))))
	if got := summary(lm, tag, []any{"tag"}); got != "geosite-github" {
		t.Errorf("string variant summary = %q, want the value", got)
	}
	lm2 := lmAt(tobj(tmem("tag", tarr(tstr("a"), tstr("b")))))
	if got := summary(lm2, tag, []any{"tag"}); got != "[2] a b" {
		t.Errorf("array variant summary = %q, want [2] a b", got)
	}

	// the rendered row shows the value too (no "variant 1" echo)
	m := newListModel(root, tobj(tmem("tag", tstr("geosite-github"))), Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	if v := m.View(); !contains(v, "geosite-github") || contains(v, "variant 1") {
		t.Fatalf("row should show the value:\n%s", v)
	}
}

func TestBreadcrumbPath(t *testing.T) {
	root, _, _ := inferModel()
	lm := lmAt(tobj())
	lm.model = root

	// leaf field: keys as path segments
	lm.stack = []*menuState{
		{kind: menuObject, node: root},
		{kind: menuItems, node: childOf(t, root, "inbounds"), path: []any{"inbounds"}},
		{kind: menuObject, node: oneOfOf(t, root, "inbounds"), path: []any{"inbounds", 0}},
	}
	if got := lm.breadcrumb(); got != "$/inbounds/0" {
		t.Fatalf("breadcrumb = %q, want $/inbounds/0", got)
	}

	// oneOf picker level shares the path: no extra segment
	lm.stack = append(lm.stack, &menuState{kind: menuOneOf, node: oneOfOf(t, root, "inbounds"), path: []any{"inbounds", 0}})
	if got := lm.breadcrumb(); got != "$/inbounds/0" {
		t.Fatalf("picker level must not extend the path, got %q", got)
	}

	// depth: keys and indices compose
	lm.stack = append(lm.stack, &menuState{kind: menuObject,
		node: &smodel.FormNode{Key: "listen", Title: "Listen"}, path: []any{"inbounds", 3, "listen"}})
	if got := lm.breadcrumb(); got != "$/inbounds/3/listen" {
		t.Fatalf("deep path = %q", got)
	}

	// narrow terminal: the tail survives within the width
	lm.width = 12
	if got := lm.breadcrumb(); got != "…ds/3/listen" {
		t.Fatalf("clipped path = %q, want the tail", got)
	}

	// root
	lm.stack = lm.stack[:1]
	if got := lm.breadcrumb(); got != "$" {
		t.Fatalf("root = %q", got)
	}
}

func oneOfOf(t *testing.T, n *smodel.FormNode, key string) *smodel.FormNode {
	t.Helper()
	c := childOf(t, n, key)
	if c.Items == nil {
		t.Fatalf("%s has no items", key)
	}
	return c.Items
}

func TestIssuesJumpReturn(t *testing.T) {
	doc := testDoc()
	msgs := []string{
		"at '/server/port': minimum value is 1024",
		"at '/name': got number, want string",
	}
	m := newListModel(testModel(), doc, Options{LoadErrors: msgs})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	// jump to the second issue (/name at the root), then esc returns to the
	// list with the cursor preserved
	press(m, "enter") // status row → issues
	press(m, "down")  // cursor on the second entry
	press(m, "enter") // jump
	if m.issues != nil {
		t.Fatal("jump should close the overlay")
	}
	if r := rowAtCursor(m); r.fn == nil || r.fn.Key != "name" {
		t.Fatalf("jump should land on the name row, got %+v", r)
	}
	press(m, "esc")
	if m.issues == nil || m.issues.cursor != 1 {
		t.Fatalf("esc at the jump target should return to the issues list, got %+v", m.issues)
	}
	if m.confirm != nil {
		t.Fatal("esc must not fall through to the quit confirm")
	}
	// closing the list resumes normal navigation
	press(m, "esc")
	if m.issues != nil {
		t.Fatal("esc on the issues list should close it")
	}

	// deeper drill from the jump target pops first, then returns to the list
	m.top().cursor = 0
	press(m, "enter", "enter") // issues → jump to /server/port
	if m.top().node.Key != "server" || len(m.stack) != 2 {
		t.Fatalf("jump should land in the Server menu, stack = %d", len(m.stack))
	}
	press(m, "enter") // drill deeper: Port input
	if m.edit == nil {
		t.Fatal("port input should open")
	}
	press(m, "esc") // close the input, still in the Server menu
	if m.issues != nil {
		t.Fatal("closing the input must not return to the issues list")
	}
	press(m, "esc") // now return to the list
	if m.issues == nil || m.issues.cursor != 0 {
		t.Fatalf("esc at the jump target should return to the issues list, got %+v", m.issues)
	}
	// a search jump replaces the parked list
	press(m, "esc") // close the list
	m.top().cursor = 0
	press(m, "enter", "enter") // jump again (parked)
	press(m, "/", "p", "o", "r", "t", "enter")
	press(m, "esc")
	if m.issues != nil {
		t.Fatal("a search jump must clear the parked issues list")
	}
}

func TestIssuesLiveRefresh(t *testing.T) {
	// a stub validator: port must be 1024+ and the name must not be "webapp"
	check := func(doc *jsondoc.Node) []string {
		var out []string
		if n, _ := doc.Get([]any{"server", "port"}); n != nil && n.Num != nil && n.Num.String() == "8080" {
			out = append(out, "at '/server/port': minimum value is 1024")
		}
		if n, _ := doc.Get([]any{"name"}); n != nil && n.Str != nil && *n.Str == "webapp" {
			out = append(out, "at '/name': invalid name")
		}
		return out
	}
	doc := testDoc() // port=8080 (bad), name=webapp (bad)
	m := newListModel(testModel(), doc, Options{LoadErrors: check(doc), Validate: check})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	// two problems marked, status row counts them
	if !contains(m.View(), icons.Err+" 2 validation errors") {
		t.Fatalf("status row should count 2:\n%s", m.View())
	}
	if !m.hasIssue([]any{"server", "port"}) || !m.hasIssue([]any{"name"}) {
		t.Fatal("both fields should be marked")
	}

	// fix the port → the list, the count and the marks update immediately
	for i, r := range menuRows(m) {
		if r.fn != nil && r.fn.Key == "server" {
			m.top().cursor = i
		}
	}
	press(m, "enter") // Server menu
	for i, r := range menuRows(m) {
		if r.fn != nil && r.fn.Key == "port" {
			m.top().cursor = i
		}
	}
	press(m, "enter", "ctrl+u", "9", "0", "5", "0", "enter")
	if len(m.issuesNow) != 1 {
		t.Fatalf("issues after fix = %d, want 1", len(m.issuesNow))
	}
	if m.hasIssue([]any{"server", "port"}) {
		t.Fatal("the fixed row must lose its mark")
	}
	press(m, "esc") // back to the root
	if v := m.View(); !contains(v, icons.Err+" 1 validation error") {
		t.Fatalf("count should drop to 1 (singular):\n%s", v)
	}

	// fix the name too → status row disappears entirely
	for i, r := range menuRows(m) {
		if r.fn != nil && r.fn.Key == "name" {
			m.top().cursor = i
		}
	}
	press(m, "enter", "ctrl+u", "fixed", "enter")
	if len(m.issuesNow) != 0 {
		t.Fatalf("issues after both fixes = %d, want 0", len(m.issuesNow))
	}
	if v := m.View(); contains(v, "validation errors") || contains(v, icons.Err) || contains(v, icons.Warn) {
		t.Fatalf("clean document must show no status row or marks:\n%s", v)
	}

	// jumping out and returning refreshes the parked list too
	doc2 := testDoc()
	m2 := newListModel(testModel(), doc2, Options{LoadErrors: check(doc2), Validate: check})
	m2.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	press(m2, "enter") // issues list
	press(m2, "enter") // jump to the first entry (/server/port)
	fixPort := func() {
		for i, r := range menuRows(m2) {
			if r.fn != nil && r.fn.Key == "port" {
				m2.top().cursor = i
			}
		}
		press(m2, "enter", "ctrl+u", "9", "0", "5", "0", "enter")
	}
	fixPort()
	press(m2, "esc") // returns to the list, refreshed
	if m2.issues == nil || len(m2.issues.items) != 1 {
		t.Fatalf("returned list should hold the remaining issue only, got %+v", m2.issues)
	}
	if contains(m2.issues.items[0].msg, "/server/port") {
		t.Fatalf("fixed entry still listed: %q", m2.issues.items[0].msg)
	}
}

func TestOneOfItemDrillExisting(t *testing.T) {
	root, oneOf, _ := inferModel()
	doc := tobj(tmem("inbounds", tarr(
		tobj(tmem("type", tstr("trojan")), tmem("password", tstr("x"))))))
	m := newListModel(root, doc, Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	press(m, "enter") // Inbounds → items menu
	press(m, "enter") // item row → pinned variant menu, no picker

	top := m.top()
	if top.variantOf != oneOf || top.kind != menuObject {
		t.Fatalf("drill should open the dynamic pinned menu, got kind=%v variantOf=%v", top.kind, top.variantOf)
	}
	if len(m.stack) != 3 {
		t.Fatalf("stack depth = %d, want 3", len(m.stack))
	}
	if v := m.View(); !contains(v, "Password") || contains(v, "variant 1") {
		t.Fatalf("existing item should land on the trojan variant fields:\n%s", v)
	}
}

func TestMixedOneOfStillPicker(t *testing.T) {
	root, _, _ := inferModel()
	doc := tobj()
	m := newListModel(root, doc, Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	press(m, "down", "enter") // Alerts → items menu
	press(m, "enter")         // < Add item > → classic variant picker
	if m.top().kind != menuOneOf {
		t.Fatalf("mixed oneOf add should keep the picker, got kind=%v", m.top().kind)
	}
	if v := m.View(); !contains(v, "None") || !contains(v, "Webhook") {
		t.Fatalf("picker should list the variants:\n%s", v)
	}
}

func TestOneOfSearchJump(t *testing.T) {
	root, _, _ := inferModel()
	doc := tobj(tmem("inbounds", tarr(
		tobj(tmem("type", tstr("trojan")), tmem("password", tstr("x"))))))
	m := newListModel(root, doc, Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	press(m, "/", "p", "a", "s", "s", "w", "o", "r", "d", "enter")
	if m.search.active {
		t.Fatal("enter should close search")
	}
	if v := m.View(); !contains(v, "Password") {
		t.Fatalf("jump should land inside the item's variant menu:\n%s", v)
	}
	if m.top().node == nil || m.top().variantOf == nil && !contains(m.View(), "Password") {
		t.Fatal("jump should land on a menu containing the password row")
	}
}

func TestFieldRequired(t *testing.T) {
	root, _, _ := inferModel()
	doc := tobj()
	m := newListModel(root, doc, Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	press(m, "enter", "enter") // Inbounds → items → Add: merged menu

	rowOf := func(k string) row {
		t.Helper()
		for _, r := range menuRows(m) {
			if r.fn != nil && r.fn.Key == k {
				return r
			}
		}
		t.Fatalf("row %q not found", k)
		return row{}
	}

	// merged: type required by every variant; listen by none
	if !m.fieldRequired(rowOf("type")) {
		t.Fatal("merged type should be required")
	}
	if m.fieldRequired(rowOf("listen")) {
		t.Fatal("merged listen should not be required")
	}
	// password: required by the only variant defining it
	if !m.fieldRequired(rowOf("password")) {
		t.Fatal("merged password should count as required")
	}
	if v := m.View(); !contains(v, "Password*") {
		t.Fatalf("merged view should mark the required key:\n%s", v)
	}

	// pinned trojan: the variant's own requirement wins
	doc.Set([]any{"inbounds", 0, "type"}, tstr("trojan"))
	m.refreshVariantMenu()
	if !m.fieldRequired(rowOf("password")) {
		t.Fatal("pinned trojan password should be required")
	}

	// pinned direct: password row is gone, listen is not required
	doc.Set([]any{"inbounds", 0, "type"}, tstr("direct"))
	m.refreshVariantMenu()
	if m.fieldRequired(rowOf("listen")) {
		t.Fatal("pinned direct listen should not be required")
	}
}

func TestNestedOneOfGroupFlatten(t *testing.T) {
	// variant 2 is itself a oneOf group (sing-box inbound schema shape)
	oneOf := &smodel.FormNode{Key: "inbounds", Title: "Inbound", Kind: smodel.KOneOf, Variants: []*smodel.FormNode{
		{Key: "inbounds", Title: "variant 1", Kind: smodel.KObject, Children: []*smodel.FormNode{
			{Key: "type", Title: "Type", Kind: smodel.KEnum, Enum: tenum("direct"), Required: true},
		}},
		{Key: "inbounds", Title: "group", Kind: smodel.KOneOf, Variants: []*smodel.FormNode{
			{Key: "inbounds", Title: "variant 2", Kind: smodel.KObject, Children: []*smodel.FormNode{
				{Key: "type", Title: "Type", Kind: smodel.KEnum, Enum: tenum("vless"), Required: true},
				{Key: "flow", Title: "Flow", Kind: smodel.KScalar, JSONType: "string"},
			}},
			{Key: "inbounds", Title: "variant 3", Kind: smodel.KObject, Children: []*smodel.FormNode{
				{Key: "type", Title: "Type", Kind: smodel.KEnum, Enum: tenum("vmess"), Required: true},
			}},
		}},
	}}
	root := &smodel.FormNode{Key: "$", Title: "Config", Kind: smodel.KObject, Children: []*smodel.FormNode{
		{Key: "inbounds", Title: "Inbounds", Kind: smodel.KArray, Items: oneOf},
	}}

	if leaves := dynamicVariants(oneOf); len(leaves) != 3 {
		t.Fatalf("dynamicVariants flattened %d leaves, want 3", len(leaves))
	}
	// group values resolve through currentVariant to the group's index
	if got := currentVariant(oneOf, tobj(tmem("type", tstr("vless")), tmem("flow", tstr("xtls-rprx-vision")))); got != 1 {
		t.Fatalf("vless value: currentVariant = %d, want 1 (the group)", got)
	}

	doc := tobj()
	m := newListModel(root, doc, Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	press(m, "enter", "enter") // Inbounds → items → Add → merged menu
	if m.top().variantOf != oneOf {
		t.Fatal("add should open the dynamic menu despite the group variant")
	}
	merged := m.top().node
	if len(merged.Children) != 2 { // type (union of 3 consts) + flow
		t.Fatalf("merged children = %d, want 2", len(merged.Children))
	}
	if u := merged.Children[0]; u.Kind != smodel.KEnum || len(u.Enum) != 3 ||
		u.Enum[0].Display != "direct" || u.Enum[1].Display != "vless" || u.Enum[2].Display != "vmess" {
		t.Fatalf("union discriminator = %v", u.Enum)
	}

	// pick type=vless → pinned to the group's vless leaf
	m.doc.Set([]any{"inbounds", 0, "type"}, tstr("vless"))
	m.refreshVariantMenu()
	if len(m.top().node.Children) != 2 || m.top().node.Children[1].Key != "flow" {
		t.Fatalf("vless should pin with its flow field, got %d children", len(m.top().node.Children))
	}
}
