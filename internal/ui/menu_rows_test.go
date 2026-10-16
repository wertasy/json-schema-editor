package ui

import (
	"encoding/json"
	"strings"
	"testing"

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
	// raw nodes
	raw := &smodel.FormNode{Key: "extra", Title: "Extra", Kind: smodel.KRaw}
	lm7 := lmAt(tobj(tmem("extra", tobj(tmem("a", tnum("1"))))))
	if got := summary(lm7, raw, []any{"extra"}); got != `json {"a":1}` {
		t.Errorf("raw summary = %q", got)
	}
}

func TestObjectSummary(t *testing.T) {
	cases := []struct {
		n    *jsondoc.Node
		want string
	}{
		{tobj(), "{}"},
		{nil, "{}"},
		{tobj(tmem("a", tnum("1"))), "a=1"},
		{tobj(tmem("a", tnum("1")), tmem("b", tbool(true))), "a=1 b=true"},
		{tobj(tmem("a", tnum("1")), tmem("b", tbool(true)), tmem("c", tstr("x"))), "a=1 b=true +1"},
		{tobj(tmem("a", tobj()), tmem("b", tarr(tnum("1")))), "a={…} b=[1]"},
		{tobj(tmem("a", tnull())), "a=null"},
	}
	for _, c := range cases {
		if got := objectSummary(c.n); got != c.want {
			t.Errorf("objectSummary = %q, want %q", got, c.want)
		}
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
	// oneOf descent follows the current variant
	docAuth := tobj(tmem("auth", tobj(tmem("token", tstr("x")))))
	st = rebuildStack(root, docAuth, []any{"auth", "token"})
	if len(st) != 3 || st[1].kind != menuOneOf || st[2].node.Title != "Token" {
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
	if l := find("Source"); l != "$/mounts[0]/src  /data" {
		t.Errorf("Source label = %q", l)
	}
	if l := find("Server"); l != "$/server  port=8080 host=0.0.0.0 +1" {
		t.Errorf("Server label = %q", l)
	}
	// variants show their structural overview, never fallback titles
	if l := find("Token"); l != "$/auth  {token*}" {
		t.Errorf("Token label = %q", l)
	}
	for _, r := range all {
		if strings.Contains(r.label, "variant ") {
			t.Errorf("label leaks a fallback variant title: %q", r.label)
		}
	}
	// filter by title and key, case-insensitive
	if got := filterSearch(lm, all, "token", true); len(got) != 2 { // variant + field
		t.Errorf("filter token = %d rows, want 2", len(got))
	}
	if got := filterSearch(lm, all, "SRC", true); len(got) != 2 { // Source + $/mounts[0] overview
		t.Errorf("filter SRC by key = %d rows, want 2", len(got))
	}
	// fold=false demands the exact case
	if got := filterSearch(lm, all, "SRC", false); len(got) != 0 {
		t.Errorf("exact-case SRC = %d rows, want 0", len(got))
	}
	if got := filterSearch(lm, all, "src", false); len(got) != 2 { // $/mounts[0] overview + $/mounts[0]/src
		t.Errorf("exact-case src = %d rows, want 2", len(got))
	}
	if got := filterSearch(lm, all, "", true); len(got) != len(all) {
		t.Errorf("empty query must keep all rows")
	}
	// values match: leaf values and container overviews alike
	if got := filterSearch(lm, all, "8080", true); len(got) != 2 { // $/server overview + $/server/port
		t.Errorf("filter 8080 by value = %d rows, want 2", len(got))
	}
	if got := filterSearch(lm, all, "/mnt", true); len(got) != 2 { // $/mounts[0] overview + $/mounts[0]/dst
		t.Errorf("filter /mnt by value = %d rows, want 2", len(got))
	}

	// scope: searching from inside Server stays below the breadcrumb
	inner := lmAt(testDoc(), &menuState{kind: menuObject,
		node: childOf(t, testModel(), "server"), path: []any{"server"}})
	innerAll := searchIndex(inner, inner.top())
	if len(innerAll) != 4 { // port, host, debug, ratio
		t.Fatalf("scoped index = %d rows, want 4", len(innerAll))
	}
	if got := filterSearch(inner, innerAll, "name", true); len(got) != 0 {
		t.Errorf("scoped search must not leave the current menu, got %d", len(got))
	}
}

// --- search & replace helpers ----------------------------------------------

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
		got, n := replaceAll(c.s, c.old, c.rep, c.fold)
		if got != c.want || n != c.wantN {
			t.Errorf("replaceAll(%q, %q, %q, %v) = (%q, %d), want (%q, %d)",
				c.s, c.old, c.rep, c.fold, got, n, c.want, c.wantN)
		}
	}
}

func TestReplaceHits(t *testing.T) {
	lm := lmAt(testDoc())
	all := searchIndex(lm, lm.top())

	// non-empty query: only string values containing q, relabeled as previews
	hits := replaceHits(lm, all, "/data", "/opt", true)
	if len(hits) != 1 {
		t.Fatalf("hits = %d, want 1 ($/mounts[0]/src)", len(hits))
	}
	if hits[0].path == nil || len(hits[0].path) != 3 {
		t.Fatalf("hit path = %v, want $/mounts[0]/src", hits[0].path)
	}
	if hits[0].label != "$/mounts[0]/src  /data → /opt" {
		t.Errorf("preview label = %q", hits[0].label)
	}

	// fold=false demands the exact case
	if got := replaceHits(lm, all, "/DATA", "/opt", false); len(got) != 0 {
		t.Errorf("exact-case /DATA = %d hits, want 0", len(got))
	}
	if got := replaceHits(lm, all, "/DATA", "/opt", true); len(got) != 1 {
		t.Errorf("folded /DATA = %d hits, want 1", len(got))
	}

	// empty query: every string value, original labels, numbers excluded
	strs := replaceHits(lm, all, "", "", true)
	if len(strs) != 4 { // name, server/host, mounts[0]/src, mounts[0]/dst
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
	got := replaceHits(lm2, searchIndex(lm2, lm2.top()), "secret", "x", true)
	if len(got) != 1 {
		t.Errorf("duplicate variant paths = %d hits, want 1", len(got))
	}
}
