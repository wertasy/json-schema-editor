package ui

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"json-schema-editor/internal/jsondoc"
	"json-schema-editor/internal/smodel"
)

// --- fixtures -----------------------------------------------------------

func tstr(s string) *jsondoc.Node { return &jsondoc.Node{Str: &s} }
func tnum(s string) *jsondoc.Node { n := json.Number(s); return &jsondoc.Node{Num: &n} }
func tbool(b bool) *jsondoc.Node  { return &jsondoc.Node{Bool: &b} }
func tnull() *jsondoc.Node        { return &jsondoc.Node{Null: true} }
func tobj(ms ...jsondoc.Member) *jsondoc.Node {
	o := []jsondoc.Member(ms)
	return &jsondoc.Node{Object: &o}
}
func tarr(ns ...*jsondoc.Node) *jsondoc.Node {
	a := []*jsondoc.Node(ns)
	return &jsondoc.Node{Array: &a}
}
func tmem(k string, v *jsondoc.Node) jsondoc.Member { return jsondoc.Member{Key: k, Val: v} }

func tenum(vals ...string) []smodel.EnumOpt {
	opts := make([]smodel.EnumOpt, 0, len(vals))
	for _, v := range vals {
		n := tstr(v)
		opts = append(opts, smodel.EnumOpt{Display: v, Encoded: smodel.EncodeValue(n), Val: n})
	}
	return opts
}

// testModel mirrors examples/server.schema.json.
func testModel() *smodel.FormNode {
	min, max := 1024.0, 65535.0
	return &smodel.FormNode{Key: "$", Title: "Server Configuration", Kind: smodel.KObject, Children: []*smodel.FormNode{
		{Key: "name", Title: "Name", Kind: smodel.KScalar, JSONType: "string", Required: true},
		{Key: "mode", Title: "Mode", Kind: smodel.KEnum, Enum: tenum("http", "grpc", "tcp"), Default: tstr("http")},
		{Key: "server", Title: "Server", Kind: smodel.KObject, Required: true, Children: []*smodel.FormNode{
			{Key: "port", Title: "Port", Kind: smodel.KScalar, JSONType: "integer", Required: true, Min: &min, Max: &max, Default: tnum("8080")},
			{Key: "host", Title: "Host", Kind: smodel.KScalar, JSONType: "string", Default: tstr("0.0.0.0")},
			{Key: "debug", Title: "Debug", Kind: smodel.KBoolean, Default: tbool(false)},
			{Key: "ratio", Title: "Ratio", Kind: smodel.KScalar, JSONType: "number"},
		}},
		{Key: "logging", Title: "Logging", Kind: smodel.KObject, Children: []*smodel.FormNode{
			{Key: "levels", Title: "Levels", Kind: smodel.KArray, MinItems: 1,
				Items: &smodel.FormNode{Key: "levels", Kind: smodel.KEnum, Enum: tenum("debug", "info", "warn", "error")}},
			{Key: "tags", Title: "Tags", Kind: smodel.KArray,
				Items: &smodel.FormNode{Key: "tags", Kind: smodel.KScalar, JSONType: "string"}},
		}},
		{Key: "mounts", Title: "Mounts", Kind: smodel.KArray, Items: &smodel.FormNode{
			Key: "mounts", Title: "Mount", Kind: smodel.KObject, Children: []*smodel.FormNode{
				{Key: "src", Title: "Source", Kind: smodel.KScalar, JSONType: "string", Required: true},
				{Key: "dst", Title: "Target", Kind: smodel.KScalar, JSONType: "string"},
			}}},
		{Key: "auth", Title: "Auth", Kind: smodel.KOneOf, Variants: []*smodel.FormNode{
			{Key: "auth", Title: "None", Kind: smodel.KEnum, Enum: tenum("none")},
			{Key: "auth", Title: "Token", Kind: smodel.KObject, Children: []*smodel.FormNode{
				{Key: "token", Title: "Token", Kind: smodel.KScalar, JSONType: "string", Required: true},
			}},
		}},
	}}
}

func testDoc() *jsondoc.Node {
	return tobj(
		tmem("name", tstr("webapp")),
		tmem("server", tobj(
			tmem("port", tnum("8080")),
			tmem("host", tstr("0.0.0.0")),
			tmem("debug", tbool(true)),
		)),
		tmem("mounts", tarr(tobj(tmem("src", tstr("/data")), tmem("dst", tstr("/mnt"))))),
	)
}

// lmAt builds a listModel over the test tree with the given states stacked
// on top of the root menu.
func lmAt(doc *jsondoc.Node, states ...*menuState) *listModel {
	root := testModel()
	stack := []*menuState{{kind: menuObject, node: root}}
	stack = append(stack, states...)
	return &listModel{model: root, doc: doc, stack: stack, width: 80, height: 24}
}

func childOf(t *testing.T, n *smodel.FormNode, key string) *smodel.FormNode {
	t.Helper()
	for _, c := range n.Children {
		if c.Key == key {
			return c
		}
	}
	t.Fatalf("child %q not found", key)
	return nil
}

// --- summary ------------------------------------------------------------

func TestSummaryRules(t *testing.T) {
	root := testModel()
	doc := testDoc()
	lm := lmAt(doc)
	name := childOf(t, root, "name")
	mode := childOf(t, root, "mode")
	server := childOf(t, root, "server")
	port := childOf(t, server, "port")
	host := childOf(t, server, "host")
	debug := childOf(t, server, "debug")
	ratio := childOf(t, server, "ratio")
	mounts := childOf(t, root, "mounts")
	auth := childOf(t, root, "auth")

	cases := []struct {
		fn   *smodel.FormNode
		path []any
		want string
	}{
		{name, []any{"name"}, "webapp"},
		{mode, []any{"mode"}, "http"},                          // default displayed
		{port, []any{"server", "port"}, "8080"},                // set value
		{host, []any{"server", "host"}, "0.0.0.0"},             // default
		{debug, []any{"server", "debug"}, "[y]"},               // set true
		{ratio, []any{"server", "ratio"}, "—"},                 // unset, no default
		{server, []any{"server"}, "port=8080 host=0.0.0.0 +1"}, // first 2 members + rest
		{mounts, []any{"mounts"}, "1 item"},
		{auth, []any{"auth"}, "—"}, // unset oneOf
	}
	for _, c := range cases {
		if got := summary(lm, c.fn, c.path); got != c.want {
			t.Errorf("summary(%s) = %q, want %q", c.fn.Key, got, c.want)
		}
	}

	// oneOf value summaries: enum/leaf variants show the value itself,
	// object variants keep the title for context
	lm2 := lmAt(tobj(tmem("auth", tstr("none"))))
	if got := summary(lm2, auth, []any{"auth"}); got != "none" {
		t.Errorf("oneOf enum value = %q, want none", got)
	}
	lm3 := lmAt(tobj(tmem("auth", tobj(tmem("token", tstr("secret123"))))))
	if got := summary(lm3, auth, []any{"auth"}); got != "Token token=secret123" {
		t.Errorf("oneOf object value = %q, want Token token=secret123", got)
	}
	// untitled object variant: members only, no synthesized fallback label
	untitled := &smodel.FormNode{Key: "rules", Kind: smodel.KOneOf, Variants: []*smodel.FormNode{
		{Kind: smodel.KObject, Children: []*smodel.FormNode{{Key: "action"}}},
	}}
	lm3b := lmAt(tobj(tmem("rules", tobj(tmem("action", tstr("sniff"))))))
	if got := summary(lm3b, untitled, []any{"rules"}); got != "action=sniff" {
		t.Errorf("untitled oneOf object value = %q, want action=sniff", got)
	}
	// empty and long arrays
	lm4 := lmAt(tobj(tmem("mounts", tarr())))
	if got := summary(lm4, mounts, []any{"mounts"}); got != "empty" {
		t.Errorf("empty array = %q, want empty", got)
	}
	lm5 := lmAt(tobj(tmem("mounts", tarr(tobj(), tobj()))))
	if got := summary(lm5, mounts, []any{"mounts"}); got != "2 items" {
		t.Errorf("two items = %q, want 2 items", got)
	}
	// long string clipped to 24 runes including ellipsis
	long := make([]byte, 40)
	for i := range long {
		long[i] = 'a'
	}
	lm6 := lmAt(tobj(tmem("name", tstr(string(long)))))
	if got := summary(lm6, name, []any{"name"}); got != string(long[:23])+"…" {
		t.Errorf("long string = %q", got)
	}
	// string lists preview their members: [N] v0 v1 v2 +rest
	tags := &smodel.FormNode{Key: "tags", Kind: smodel.KArray}
	lm8 := lmAt(tobj(tmem("tags", tarr(tstr("a"), tstr("b"), tstr("c"), tstr("d")))))
	if got := summary(lm8, tags, []any{"tags"}); got != "[4] a b c +1" {
		t.Errorf("string list = %q, want [4] a b c +1", got)
	}
	if got := summary(lmAt(tobj(tmem("tags", tarr(tstr("a"), tstr("b"))))), tags, []any{"tags"}); got != "[2] a b" {
		t.Errorf("short string list = %q, want [2] a b", got)
	}
	if got := summary(lmAt(tobj(tmem("tags", tarr(tstr("legacy"))))), tags, []any{"tags"}); got != "[1] legacy" {
		t.Errorf("single string list = %q, want [1] legacy", got)
	}
	if got := summary(lmAt(tobj(tmem("tags", tarr(tstr("a"), tnum("1"))))), tags, []any{"tags"}); got != "[2] a 1" {
		t.Errorf("mixed list = %q, want [2] a 1", got)
	}
	// numbers preview like strings; objects in a mixed list render as {…}
	if got := summary(lmAt(tobj(tmem("tags", tarr(tnum("1"), tnum("22"), tnum("3.5"))))), tags, []any{"tags"}); got != "[3] 1 22 3.5" {
		t.Errorf("number list = %q, want [3] 1 22 3.5", got)
	}
	if got := summary(lmAt(tobj(tmem("tags", tarr(tstr("a"), tnum("1"), tobj(tmem("k", tstr("v"))), tbool(true))))), tags, []any{"tags"}); got != "[4] a 1 {…} +1" {
		t.Errorf("mixed with object = %q, want [4] a 1 {…} +1", got)
	}
	// pure object arrays keep the plain count
	if got := summary(lmAt(tobj(tmem("tags", tarr(tobj(), tobj())))), tags, []any{"tags"}); got != "2 items" {
		t.Errorf("pure object list = %q, want 2 items", got)
	}
	lm9 := lmAt(tobj(tmem("tags", tarr(tstr(string(long))))))
	if got := summary(lm9, tags, []any{"tags"}); got != "[1] "+string(long[:15])+"…" {
		t.Errorf("long string list = %q", got)
	}
	// raw nodes
	raw := &smodel.FormNode{Key: "extra", Title: "Extra", Kind: smodel.KRaw}
	lm7 := lmAt(tobj(tmem("extra", tobj(tmem("a", tnum("1"))))))
	if got := summary(lm7, raw, []any{"extra"}); got != `json {"a":1}` {
		t.Errorf("raw summary = %q", got)
	}
}

// TestSummaryNestedOneOfGroups covers the sing-box DNSRule shape: the item
// schema is a oneOf over nested oneOf groups, so the summary must descend to
// the matched leaf instead of falling through to "—".
func TestSummaryNestedOneOfGroups(t *testing.T) {
	rules := &smodel.FormNode{Key: "rules", Kind: smodel.KOneOf, Variants: []*smodel.FormNode{
		{Key: "rules", Kind: smodel.KOneOf, Variants: []*smodel.FormNode{
			{Key: "rules", Kind: smodel.KObject, Children: []*smodel.FormNode{
				{Key: "type", Kind: smodel.KEnum, Enum: tenum("default"), Required: true},
				{Key: "domain_suffix", Kind: smodel.KArray,
					Items: &smodel.FormNode{Key: "domain_suffix", Kind: smodel.KScalar, JSONType: "string"}},
				{Key: "server", Kind: smodel.KScalar, JSONType: "string"},
			}},
		}},
		{Key: "rules", Kind: smodel.KOneOf, Variants: []*smodel.FormNode{
			{Key: "rules", Kind: smodel.KObject, Children: []*smodel.FormNode{
				{Key: "type", Kind: smodel.KEnum, Enum: tenum("logical"), Required: true},
				{Key: "mode", Kind: smodel.KEnum, Enum: tenum("and", "or"), Required: true},
				{Key: "rules", Kind: smodel.KArray, Required: true},
			}},
		}},
	}}
	doc := tobj(tmem("rules", tarr(
		tobj(tmem("type", tstr("default")),
			tmem("domain_suffix", tarr(tstr("a.com"), tstr("b.com"))),
			tmem("server", tstr("dns-local"))),
		tobj(tmem("type", tstr("logical")), tmem("mode", tstr("or")), tmem("rules", tarr())),
	)))
	lm := lmAt(doc)
	if got := summary(lm, rules, []any{"rules", 0}); got != "type=default domain_suffix=[2] +1" {
		t.Errorf("matcher group summary = %q", got)
	}
	if got := summary(lm, rules, []any{"rules", 1}); got != "type=logical mode=or +1" {
		t.Errorf("logical group summary = %q", got)
	}
	// classic picker rows: a nested group variant shows its matched leaf's value
	logical, _ := doc.Get([]any{"rules", 1})
	if got := lm.variantValueSummary(rules.Variants[1], logical); got != "type=logical mode=or +1" {
		t.Errorf("group variant value summary = %q", got)
	}

	// the array screen renders the summaries on the element rows
	root := &smodel.FormNode{Key: "$", Kind: smodel.KObject, Children: []*smodel.FormNode{
		{Key: "rules", Title: "Rules", Kind: smodel.KArray, Items: rules},
	}}
	m := newListModel(root, doc, Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	press(m, "enter")
	v := m.View()
	if !contains(v, "rules[0]") || !contains(v, "type=default domain_suffix=[2]") {
		t.Fatalf("element row summary missing:\n%s", v)
	}
	if !contains(v, "type=logical mode=or") {
		t.Fatalf("logical element row summary missing:\n%s", v)
	}
}

func TestObjectSummary(t *testing.T) {
	cases := []struct {
		fn   *smodel.FormNode
		n    *jsondoc.Node
		want string
	}{
		{nil, tobj(), "{}"},
		{nil, nil, "{}"},
		{nil, tobj(tmem("a", tnum("1"))), "a=1"},
		{nil, tobj(tmem("a", tnum("1")), tmem("b", tbool(true))), "a=1 b=true"},
		{nil, tobj(tmem("a", tnum("1")), tmem("b", tbool(true)), tmem("c", tstr("x"))), "a=1 b=true +1"},
		{nil, tobj(tmem("a", tobj()), tmem("b", tarr(tnum("1")))), "a={…} b=[1]"},
		{nil, tobj(tmem("a", tnull())), "a=null"},
	}
	for _, c := range cases {
		if got := objectSummary(c.fn, c.n); got != c.want {
			t.Errorf("objectSummary = %q, want %q", got, c.want)
		}
	}

	// members render in schema order even when the document lists them
	// differently: two list rows summarize alike (type before tag)
	schema := &smodel.FormNode{Key: "inbound", Kind: smodel.KObject, Children: []*smodel.FormNode{
		{Key: "type"}, {Key: "tag"}, {Key: "listen"}, {Key: "port"},
	}}
	doc := tobj(tmem("tag", tstr("in-proxy")), tmem("type", tstr("mixed")),
		tmem("listen", tstr("::")), tmem("port", tnum("1080")))
	if got := objectSummary(schema, doc); got != "type=mixed tag=in-proxy +2" {
		t.Errorf("schema order = %q, want type=mixed tag=in-proxy +2", got)
	}
	docReordered := tobj(tmem("port", tnum("1080")), tmem("listen", tstr("::")),
		tmem("type", tstr("mixed")), tmem("tag", tstr("in-proxy")))
	if got := objectSummary(schema, docReordered); got != "type=mixed tag=in-proxy +2" {
		t.Errorf("reordered doc = %q, want type=mixed tag=in-proxy +2", got)
	}
	// keys the schema does not know keep document order after the known ones
	docUnknown := tobj(tmem("zz", tnum("1")), tmem("tag", tstr("t")), tmem("type", tstr("x")))
	if got := objectSummary(schema, docUnknown); got != "type=x tag=t +1" {
		t.Errorf("unknown key = %q, want type=x tag=t +1", got)
	}
}

func TestVariantSignature(t *testing.T) {
	cases := []struct {
		fn   *smodel.FormNode
		want string
	}{
		{&smodel.FormNode{Kind: smodel.KScalar, JSONType: "integer"}, "integer"},
		{&smodel.FormNode{Kind: smodel.KScalar, JSONType: "string"}, "string"},
		{&smodel.FormNode{Kind: smodel.KScalar}, "string"},
		{&smodel.FormNode{Kind: smodel.KEnum, Enum: tenum("http", "grpc", "tcp")}, "http |grpc |tcp"},
		{&smodel.FormNode{Kind: smodel.KEnum, Enum: tenum("a", "b", "c", "d")}, "a |b |c …"},
		{&smodel.FormNode{Kind: smodel.KBoolean}, "yes/no"},
		{&smodel.FormNode{Kind: smodel.KRaw}, "json"},
		{&smodel.FormNode{Kind: smodel.KArray, Items: &smodel.FormNode{Kind: smodel.KScalar, JSONType: "string"}}, "list of string"},
		{&smodel.FormNode{Kind: smodel.KArray}, "list of json"},
		{&smodel.FormNode{Kind: smodel.KObject}, "{}"},
		{&smodel.FormNode{Kind: smodel.KObject, Children: []*smodel.FormNode{
			{Key: "src", Required: true}, {Key: "dst"}}}, "{src*, dst}"},
		{&smodel.FormNode{Kind: smodel.KObject, Children: []*smodel.FormNode{
			{Key: "a"}, {Key: "b", Required: true}, {Key: "c"}, {Key: "d"}, {Key: "e"}, {Key: "f"}}},
			"{a, b*, c, d, +2}"},
		// required const child shows its fixed value (sing-box style
		// discriminated variants: {type: {const: "direct"}})
		{&smodel.FormNode{Kind: smodel.KObject, Children: []*smodel.FormNode{
			{Key: "type", Required: true, Kind: smodel.KEnum, Enum: tenum("direct")},
			{Key: "tag"}, {Key: "listen"}}}, "{type=direct, tag, listen}"},
		// optional const and multi-option required keep the plain/star forms
		{&smodel.FormNode{Kind: smodel.KObject, Children: []*smodel.FormNode{
			{Key: "type", Kind: smodel.KEnum, Enum: tenum("direct")},
			{Key: "mode", Required: true, Kind: smodel.KEnum, Enum: tenum("a", "b")}}},
			"{type, mode*}"},
	}
	for _, c := range cases {
		if got := VariantSignature(c.fn); got != c.want {
			t.Errorf("VariantSignature = %q, want %q", got, c.want)
		}
	}
}

func TestCurrentVariant(t *testing.T) {
	auth := childOf(t, testModel(), "auth")
	cases := []struct {
		val  *jsondoc.Node
		want int
	}{
		{nil, -1},
		{tnull(), -1},
		{tstr("none"), 0},                   // enum variant match
		{tobj(tmem("token", tstr("x"))), 1}, // key intersection
		{tobj(), -1},                        // token is required → no key hit
		{tstr("other"), -1},                 // no variant matches
	}
	for _, c := range cases {
		if got := currentVariant(auth, c.val); got != c.want {
			t.Errorf("currentVariant(%s) = %d, want %d", smodel.EncodeValue(c.val), got, c.want)
		}
	}
	// object variant without required children matches any object
	free := &smodel.FormNode{Kind: smodel.KOneOf, Variants: []*smodel.FormNode{
		{Kind: smodel.KObject, Children: []*smodel.FormNode{{Key: "a"}}},
	}}
	if got := currentVariant(free, tobj()); got != 0 {
		t.Errorf("required-free object variant should match, got %d", got)
	}
}

// --- rows & stack --------------------------------------------------------

func TestMenuRowsRootOrder(t *testing.T) {
	lm := lmAt(testDoc())
	rows := menuRows(lm)
	wantKinds := []rowKind{rowField, rowField, rowDrill, rowDrill, rowDrill, rowDrill, rowSave, rowRaw}
	if len(rows) != len(wantKinds) {
		t.Fatalf("root rows = %d, want %d: %+v", len(rows), len(wantKinds), rows)
	}
	for i, k := range wantKinds {
		if rows[i].kind != k {
			t.Errorf("row %d kind = %d, want %d", i, rows[i].kind, k)
		}
	}
	if rows[2].fn.Key != "server" || rows[5].fn.Key != "auth" {
		t.Errorf("drill rows out of order: %s, %s", rows[2].fn.Key, rows[5].fn.Key)
	}
	if rows[6].label != "< Save and exit >" || rows[7].label != "< Edit raw JSON >" {
		t.Errorf("root action labels = %q / %q", rows[6].label, rows[7].label)
	}
}

func TestMenuRowsItemMenus(t *testing.T) {
	root := testModel()
	doc := testDoc()
	mounts := childOf(t, root, "mounts")
	levels := childOf(t, childOf(t, root, "logging"), "levels")

	// array manager: add + one drill row per item, no save rows (not root)
	lm := lmAt(doc, &menuState{kind: menuItems, node: mounts, path: []any{"mounts"}})
	rows := menuRows(lm)
	if len(rows) != 2 || rows[0].kind != rowAdd || rows[1].kind != rowDrill || rows[1].idx != 0 {
		t.Fatalf("mounts rows = %+v", rows)
	}

	// object item menu: remove row on top, then fields
	lm2 := lmAt(doc, &menuState{kind: menuObject, node: mounts.Items, path: []any{"mounts", 0}})
	rows2 := menuRows(lm2)
	if len(rows2) != 3 || rows2[0].kind != rowRemove || rows2[1].fn.Key != "src" || rows2[2].fn.Key != "dst" {
		t.Fatalf("mount item rows = %+v", rows2)
	}

	// scalar item menu: add + field rows
	tags := childOf(t, childOf(t, root, "logging"), "tags")
	lm3 := lmAt(tobj(tmem("logging", tobj(tmem("tags", tarr(tstr("prod"), tstr("dev")))))),
		&menuState{kind: menuItems, node: tags, path: []any{"logging", "tags"}})
	rows3 := menuRows(lm3)
	if len(rows3) != 3 || rows3[0].kind != rowAdd || rows3[1].kind != rowField || rows3[1].idx != 0 || rows3[2].idx != 1 {
		t.Fatalf("tags rows = %+v", rows3)
	}

	// enum array: one toggle row per option
	lm4 := lmAt(doc, &menuState{kind: menuEnumArray, node: levels, path: []any{"logging", "levels"}})
	rows4 := menuRows(lm4)
	if len(rows4) != 4 {
		t.Fatalf("levels rows = %d, want 4", len(rows4))
	}
	for i, r := range rows4 {
		if r.kind != rowToggle || r.idx != i {
			t.Fatalf("toggle row %d wrong: %+v", i, r)
		}
	}
}

func TestMenuRowsOneOf(t *testing.T) {
	root := testModel()
	auth := childOf(t, root, "auth")
	lm := lmAt(testDoc(), &menuState{kind: menuOneOf, node: auth, path: []any{"auth"}})
	rows := menuRows(lm)
	if len(rows) != 2 || rows[0].kind != rowVariant || rows[1].kind != rowVariant {
		t.Fatalf("auth rows = %+v", rows)
	}
	if rows[0].variant.Title != "None" || rows[1].variant.Title != "Token" {
		t.Errorf("variant rows = %s / %s", rows[0].variant.Title, rows[1].variant.Title)
	}
}

func TestRebuildStack(t *testing.T) {
	root, doc := testModel(), testDoc()

	st := rebuildStack(root, doc, []any{"server"})
	if len(st) != 2 || st[1].kind != menuObject || st[1].node.Key != "server" {
		t.Fatalf("server stack = %+v", st)
	}
	// leaf field stops at the containing menu
	st = rebuildStack(root, doc, []any{"server", "port"})
	if len(st) != 2 {
		t.Fatalf("port stack depth = %d, want 2", len(st))
	}
	// array item menu
	st = rebuildStack(root, doc, []any{"mounts", 0, "src"})
	if len(st) != 3 || st[1].kind != menuItems || st[2].kind != menuObject || st[2].node.Key != "mounts" {
		t.Fatalf("mount src stack = %+v", st)
	}
	if p, _ := st[2].path[1].(int); p != 0 {
		t.Errorf("item menu path = %#v", st[2].path)
	}
	// scalar array item: row lives in the array menu
	tagsDoc := tobj(tmem("logging", tobj(tmem("tags", tarr(tstr("x"))))))
	st = rebuildStack(root, tagsDoc, []any{"logging", "tags", 0})
	if len(st) != 3 || st[2].kind != menuItems {
		t.Fatalf("tags item stack = %+v", st)
	}
	// oneOf descent follows the current variant; an existing value edits it
	// directly (oneOf carried for the ctrl+t type switch)
	docAuth := tobj(tmem("auth", tobj(tmem("token", tstr("x")))))
	st = rebuildStack(root, docAuth, []any{"auth", "token"})
	if len(st) != 2 || st[1].node == nil || st[1].node.Title != "Token" || st[1].oneOf == nil {
		t.Fatalf("auth token stack = %+v", st)
	}
	// unset oneOf cannot descend past the picker
	st = rebuildStack(root, testDoc(), []any{"auth", "token"})
	if len(st) != 2 || st[1].kind != menuOneOf {
		t.Fatalf("unset auth stack = %+v", st)
	}
	// unknown keys are skipped
	st = rebuildStack(root, doc, []any{"nope"})
	if len(st) != 1 {
		t.Fatalf("unknown key stack = %+v", st)
	}
}

func TestSearchIndexAndFilter(t *testing.T) {
	lm := lmAt(testDoc())
	all := searchIndex(lm, lm.top())
	find := func(want string) string {
		for _, r := range all {
			if r.fn != nil && r.fn.Title == want {
				return r.label
			}
		}
		return ""
	}
	// labels are breadcrumb-style paths plus the menu value overview
	if l := find("Source"); l != "$.mounts[0].src  /data" {
		t.Errorf("Source label = %q", l)
	}
	if l := find("Server"); l != "$.server  port=8080 host=0.0.0.0 +1" {
		t.Errorf("Server label = %q", l)
	}
	// variants show their structural overview, never fallback titles
	if l := find("Token"); l != "$.auth  {token*}" {
		t.Errorf("Token label = %q", l)
	}
	for _, r := range all {
		if strings.Contains(r.label, "variant ") {
			t.Errorf("label leaks a fallback variant title: %q", r.label)
		}
	}
	// filter by title and key, case-insensitive
	if got := filterSearch(lm, all, "token", true, nil); len(got) != 2 { // variant + field
		t.Errorf("filter token = %d rows, want 2", len(got))
	}
	if got := filterSearch(lm, all, "SRC", true, nil); len(got) != 1 { // Source row only
		t.Errorf("filter SRC by key = %d rows, want 1", len(got))
	}
	// fold=false demands the exact case
	if got := filterSearch(lm, all, "SRC", false, nil); len(got) != 0 {
		t.Errorf("exact-case SRC = %d rows, want 0", len(got))
	}
	if got := filterSearch(lm, all, "src", false, nil); len(got) != 1 { // $.mounts[0].src
		t.Errorf("exact-case src = %d rows, want 1", len(got))
	}
	if got := filterSearch(lm, all, "", true, nil); len(got) != len(all) {
		t.Errorf("empty query must keep all rows")
	}
	// a row matches with its own value: the container above it only rolls its
	// child values up, so the roll-up is no match target and the hit is not
	// listed twice
	if got := filterSearch(lm, all, "8080", true, nil); len(got) != 1 { // $.server.port
		t.Errorf("filter 8080 by value = %d rows, want 1", len(got))
	}
	if got := filterSearch(lm, all, "port=8080", true, nil); len(got) != 0 {
		t.Errorf("container roll-up query = %d rows, want 0", len(got))
	}
	// values with a slash stay ordinary text: only a "$" prefix switches to
	// path mode
	if got := filterSearch(lm, all, "mnt", true, nil); len(got) != 1 { // $.mounts[0].dst
		t.Errorf("filter mnt by value = %d rows, want 1", len(got))
	}
	if got := filterSearch(lm, all, "/mnt", true, nil); len(got) != 1 {
		t.Errorf("slashed value query = %d rows, want 1", len(got))
	}

	// scope: searching from inside Server stays below the breadcrumb
	inner := lmAt(testDoc(), &menuState{kind: menuObject,
		node: childOf(t, testModel(), "server"), path: []any{"server"}})
	innerAll := searchIndex(inner, inner.top())
	if len(innerAll) != 4 { // port, host, debug, ratio
		t.Fatalf("scoped index = %d rows, want 4", len(innerAll))
	}
	if got := filterSearch(inner, innerAll, "name", true, nil); len(got) != 0 {
		t.Errorf("scoped search must not leave the current menu, got %d", len(got))
	}
}

func TestSearchIndexVariantRowsOnlyWithoutValue(t *testing.T) {
	count := func(rows []row) int {
		n := 0
		for _, r := range rows {
			if r.kind == rowVariant && pathEq(r.path, []any{"auth"}) {
				n++
			}
		}
		return n
	}

	// Unset oneOf: the variant rows are useful searchable type targets.
	lm := lmAt(testDoc())
	if n := count(searchIndex(lm, lm.top())); n != 2 {
		t.Fatalf("unset auth variants = %d, want 2", n)
	}

	// Set oneOf: the field row already carries the value; the signature rows
	// would repeat the same path with "string"/"list of string".
	doc := testDoc()
	doc.Set([]any{"auth"}, tobj(tmem("token", tstr("secret"))))
	lm2 := lmAt(doc)
	all := searchIndex(lm2, lm2.top())
	if n := count(all); n != 0 {
		t.Fatalf("set auth variants = %d, want none", n)
	}
	found := false
	for _, r := range all {
		if r.fn != nil && r.fn.Key == "auth" && pathEq(r.path, []any{"auth"}) {
			found = true
		}
	}
	if !found {
		t.Fatal("the value-bearing auth field row must stay searchable")
	}
}

func TestSearchRankAndPathQueries(t *testing.T) {
	lm := lmAt(testDoc())
	all := searchIndex(lm, lm.top())
	paths := func(rows []row) (out []string) {
		for _, r := range rows {
			out = append(out, jsondoc.PathString(r.path))
		}
		return out
	}
	eq := func(got []string, want ...string) {
		t.Helper()
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("paths = %v, want %v", got, want)
		}
	}

	// exact-key priority: the row whose key IS the query outranks rows that
	// merely mention it (parent overviews, values containing the word)
	if got := filterSearch(lm, all, "port", true, nil); len(got) == 0 || paths(got)[0] != "$.server.port" {
		t.Errorf("port first hit = %v, want $.server.port first", paths(got))
	}
	if got := filterSearch(lm, all, "src", true, nil); len(got) == 0 || paths(got)[0] != "$.mounts[0].src" {
		t.Errorf("src first hit = %v, want $.mounts[0].src first", paths(got))
	}
	// a value only its own row carries matches that row alone — the container
	// above it rolls the value up, which is no longer a match target
	if got := filterSearch(lm, all, "8080", true, nil); len(got) != 1 || paths(got)[0] != "$.server.port" {
		t.Errorf("8080 hits = %v, want exactly $.server.port", paths(got))
	}
	// the tier is stable: without an exact key the order is unchanged
	eq(paths(filterSearch(lm, all, "0", true, nil)), "$.server.port", "$.server.host")

	// path queries: "$"-prefixed, literal prefix match from the document root
	eq(paths(filterSearch(lm, all, "$.server.port", true, nil)), "$.server.port")
	anch := filterSearch(lm, all, "$.server", true, nil)
	for _, p := range paths(anch) {
		if !strings.HasPrefix(p, "$.server") {
			t.Errorf("path hit %q lacks the $.server prefix", p)
		}
	}
	eq(paths(anch)[:1], "$.server")                                       // exact full path ranks first
	eq(paths(filterSearch(lm, all, "$.serv", true, nil))[:1], "$.server") // partial segment
	// array indexes use the rendered [N] syntax
	eq(paths(filterSearch(lm, all, "$.mounts[0]", true, nil)),
		"$.mounts[0]", "$.mounts[0].src", "$.mounts[0].dst")
	eq(paths(filterSearch(lm, all, "$.mounts[0].src", true, nil)), "$.mounts[0].src")

	// dotted and slashed values remain ordinary text queries
	if got := filterSearch(lm, all, "0.0.0.0", true, nil); len(got) != 1 {
		t.Errorf("dotted value query = %d rows, want 1", len(got))
	}
	if got := filterSearch(lm, all, "/mnt", true, nil); len(got) != 1 {
		t.Errorf("slashed value query = %d rows, want 1", len(got))
	}
}

// --- search & replace helpers ----------------------------------------------

func TestFilterSearchRegex(t *testing.T) {
	lm := lmAt(testDoc())
	all := searchIndex(lm, lm.top())
	paths := func(rows []row) (out []string) {
		for _, r := range rows {
			out = append(out, jsondoc.PathString(r.path))
		}
		return out
	}
	eq := func(got []string, want ...string) {
		t.Helper()
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("paths = %v, want %v", got, want)
		}
	}
	match := func(q string) []string {
		pat := q
		if isPathQuery(q) {
			pat = pathPattern(q) // as refilter compiles it
		}
		return paths(filterSearch(lm, all, q, false, regexp.MustCompile(pat)))
	}

	// the regex targets the same text literal mode matches: title/key plus the
	// row's own scalar value; a full leaf-key match still ranks first
	eq(match(`(?i)token`), "$.auth.token", "$.auth") // the field outranks its variant row
	eq(match(`\bport\b`), "$.server.port")           // $.server's roll-up is no target
	eq(match(`0\.0\.0\.0`), "$.server.host")
	// a "$."-prefixed query targets the document path, regex included
	eq(match(`$.server.port`), "$.server.port")
	eq(match(`$.server`), "$.server", "$.server.port", "$.server.host", "$.server.debug", "$.server.ratio")
	// [N] index brackets in a pasted path stay literal in regex mode
	eq(match(`$.mounts[0].src`), "$.mounts[0].src")
	eq(match(`$.mounts\[0\].src`), "$.mounts[0].src") // an explicit escape still works
	// the pattern matches original-case text: folding is the caller's (?i)
	eq(paths(filterSearch(lm, all, `(?i)SRC`, false, regexp.MustCompile(`(?i)SRC`))), "$.mounts[0].src")
	if got := match(`SRC`); len(got) != 0 {
		t.Errorf("exact-case SRC = %v, want no regex hit", got)
	}
	// a pattern matching only a container roll-up finds nothing
	if got := match(`port=8080`); len(got) != 0 {
		t.Errorf("container roll-up matched by regex: %v", got)
	}
}

func TestReplaceAll(t *testing.T) {
	cases := []struct {
		s, old, rep string
		fold        bool
		want        string
		wantN       int
	}{
		{"Hello world HELLO", "hello", "hi", true, "hi world hi", 2},
		{"Hello", "hello", "x", true, "x", 1},
		{"Hello", "hello", "x", false, "Hello", 0},
		{"Grösse GRÖSSE", "grösse", "S", true, "S S", 2},
		{"Größe", "GRÖSSE", "X", true, "Größe", 0},   // simple fold: ß is not ss
		{"aaa", "aa", "b", true, "ba", 1},            // non-overlapping scan
		{"ab", "a", "aa", true, "aab", 1},            // rep containing old: no rescan
		{"abc", "", "x", true, "abc", 0},             // empty old
		{"abc", "z", "x", false, "abc", 0},           // no match
		{"a§b A§B", "§", "§§", true, "a§§b A§§B", 2}, // multibyte-safe
	}
	for _, c := range cases {
		got, n := replaceAll(c.s, c.old, c.rep, c.fold, nil)
		if got != c.want || n != c.wantN {
			t.Errorf("replaceAll(%q, %q, %q, %v) = (%q, %d), want (%q, %d)",
				c.s, c.old, c.rep, c.fold, got, n, c.want, c.wantN)
		}
	}
}

func TestReplaceAllRegex(t *testing.T) {
	cases := []struct {
		s, pat, rep string
		want        string
		wantN       int
	}{
		{"hello", "l+", "x", "hexo", 1},
		{"foo1bar2", "[0-9]+", "#", "foo#bar#", 2},
		{"abc123", "(b)(c)", "[$2$1]", "a[cb]123", 1}, // $N groups expand
		{"HELLO hello", "(?i)hello", "x", "x x", 2},   // folding is the caller's (?i)
		{"bbb", "a*", "x", "xbxbxbx", 4},              // empty match at each boundary
	}
	for _, c := range cases {
		got, n := replaceAll(c.s, c.pat, c.rep, false, regexp.MustCompile(c.pat))
		if got != c.want || n != c.wantN {
			t.Errorf("replaceAll(%q, %q, %q, regexp) = (%q, %d), want (%q, %d)",
				c.s, c.pat, c.rep, got, n, c.want, c.wantN)
		}
	}
}

func TestReplaceHits(t *testing.T) {
	lm := lmAt(testDoc())
	all := searchIndex(lm, lm.top())

	// non-empty query: only string values containing q, relabeled as previews
	hits := replaceHits(lm, all, "/data", "/opt", true, nil)
	if len(hits) != 1 {
		t.Fatalf("hits = %d, want 1 ($.mounts[0].src)", len(hits))
	}
	if hits[0].path == nil || len(hits[0].path) != 3 {
		t.Fatalf("hit path = %v, want $.mounts[0].src", hits[0].path)
	}
	if hits[0].label != "$.mounts[0].src  /data → /opt" {
		t.Errorf("preview label = %q", hits[0].label)
	}

	// fold=false demands the exact case
	if got := replaceHits(lm, all, "/DATA", "/opt", false, nil); len(got) != 0 {
		t.Errorf("exact-case /DATA = %d hits, want 0", len(got))
	}
	if got := replaceHits(lm, all, "/DATA", "/opt", true, nil); len(got) != 1 {
		t.Errorf("folded /DATA = %d hits, want 1", len(got))
	}

	// empty query: every string value, original labels, numbers excluded
	strs := replaceHits(lm, all, "", "", true, nil)
	if len(strs) != 4 { // name, server/host, mounts/0/src, mounts/0/dst
		t.Fatalf("empty-query hits = %d, want 4", len(strs))
	}
	for _, r := range strs {
		if r.fn != nil && r.fn.Key == "port" {
			t.Error("the number port must not be a replace hit")
		}
		if r.label == "" {
			t.Error("empty-query hits keep their original labels")
		}
	}

	// duplicate paths (rowField + two variant rows share the oneOf field's
	// path) are kept once
	dup := tobj(tmem("auth", tstr("secret")))
	dupModel := &smodel.FormNode{Key: "$", Title: "$", Kind: smodel.KObject, Children: []*smodel.FormNode{
		{Key: "auth", Title: "Auth", Kind: smodel.KOneOf, Variants: []*smodel.FormNode{
			{Key: "auth", Title: "A", Kind: smodel.KScalar, JSONType: "string"},
			{Key: "auth", Title: "B", Kind: smodel.KScalar, JSONType: "string"},
		}},
	}}
	lm2 := &listModel{model: dupModel, doc: dup, stack: []*menuState{{kind: menuObject,
		node: dupModel, path: nil}}, width: 80, height: 24}
	got := replaceHits(lm2, searchIndex(lm2, lm2.top()), "secret", "x", true, nil)
	if len(got) != 1 {
		t.Errorf("duplicate variant paths = %d hits, want 1", len(got))
	}
}

func TestSearchIndexUnknownKeys(t *testing.T) {
	// schema-less document keys (with nested values) are indexed, matched
	// and offered to replace like any other row
	doc := testDoc()
	doc.Set([]any{"extra"}, tobj(tmem("note", tstr("alpha")), tmem("tags", tarr(tstr("beta")))))
	doc.Set([]any{"server", "legacy"}, tstr("gamma"))
	lm := lmAt(doc)
	all := searchIndex(lm, lm.top())

	labels := map[string]bool{}
	for _, r := range all {
		if r.kind == rowUnknown {
			labels[r.label] = true
		}
	}
	for _, want := range []string{
		"$.extra.note  alpha",
		"$.extra.tags[0]  beta",
		"$.server.legacy  gamma",
	} {
		if !labels[want] {
			t.Errorf("unknown row %q missing from index: %v", want, labels)
		}
	}

	// search matches the unknown key name and its value overview
	for _, q := range []string{"legacy", "gamma", "extra"} {
		if got := filterSearch(lm, all, q, true, nil); len(got) == 0 {
			t.Errorf("filter %q found no unknown row", q)
		}
	}

	// replace previews target unknown string values, nested included
	hits := replaceHits(lm, all, "a", "A", true, nil)
	paths := map[string]bool{}
	for _, h := range hits {
		paths[jsondoc.PathString(h.path)] = true
	}
	for _, want := range []string{"$.extra.note", "$.extra.tags[0]", "$.server.legacy"} {
		if !paths[want] {
			t.Errorf("replace hit %q missing: %v", want, paths)
		}
	}
}
