package main

import (
	"reflect"
	"strings"
	"testing"

	"json-schema-editor/internal/jsondoc"
)

func TestFillSchemaField(t *testing.T) {
	str := func(s string) *jsondoc.Node { return &jsondoc.Node{Str: &s} }
	members := func(ms ...jsondoc.Member) *jsondoc.Node {
		o := []jsondoc.Member(ms)
		return &jsondoc.Node{Object: &o}
	}

	// absent → prepended first
	doc := members(jsondoc.Member{Key: "name", Val: str("x")})
	fillSchemaField(doc, "sch.json")
	if len(*doc.Object) != 2 || (*doc.Object)[0].Key != "$schema" || *(*doc.Object)[0].Val.Str != "sch.json" {
		t.Fatalf("absent case: %#v", *doc.Object)
	}

	// existing non-empty → untouched
	fillSchemaField(doc, "other.json")
	if *(*doc.Object)[0].Val.Str != "sch.json" {
		t.Fatalf("existing value overwritten: %#v", *doc.Object)
	}

	// existing empty → filled in place, position kept
	empty := members(
		jsondoc.Member{Key: "a", Val: str("1")},
		jsondoc.Member{Key: "$schema", Val: str("")},
		jsondoc.Member{Key: "b", Val: str("2")})
	fillSchemaField(empty, "fill.json")
	if len(*empty.Object) != 3 || (*empty.Object)[1].Key != "$schema" || *(*empty.Object)[1].Val.Str != "fill.json" {
		t.Fatalf("empty case: %#v", *empty.Object)
	}

	// null value → filled too
	nullv := members(jsondoc.Member{Key: "$schema", Val: &jsondoc.Node{Null: true}})
	fillSchemaField(nullv, "n.json")
	if *(*nullv.Object)[0].Val.Str != "n.json" {
		t.Fatalf("null case: %#v", *nullv.Object)
	}
}

func TestClearSchemaField(t *testing.T) {
	str := func(s string) *jsondoc.Node { return &jsondoc.Node{Str: &s} }
	members := func(ms ...jsondoc.Member) *jsondoc.Node {
		o := []jsondoc.Member(ms)
		return &jsondoc.Node{Object: &o}
	}

	// removed, order of the rest kept
	doc := members(
		jsondoc.Member{Key: "$schema", Val: str("old.json")},
		jsondoc.Member{Key: "name", Val: str("x")},
		jsondoc.Member{Key: "mode", Val: str("http")})
	clearSchemaField(doc)
	if keys := func() []string {
		ks := []string{}
		for _, m := range *doc.Object {
			ks = append(ks, m.Key)
		}
		return ks
	}(); strings.Join(keys, ",") != "name,mode" {
		t.Fatalf("keys after clear = %v", keys)
	}

	// absent $schema is a no-op
	plain := members(jsondoc.Member{Key: "name", Val: str("x")})
	clearSchemaField(plain)
	if len(*plain.Object) != 1 {
		t.Fatalf("plain doc changed: %#v", *plain.Object)
	}

	// nil document is tolerated
	clearSchemaField(nil)
}

func TestSearchStem(t *testing.T) {
	cases := map[string]string{
		"server.json": "server",
		"a.b.json":    "a.b",
		".cshrc":      ".cshrc", // Ext(".cshrc") is the whole name
		"noext":       "noext",
	}
	for in, want := range cases {
		if got := searchStem(in); got != want {
			t.Errorf("searchStem(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSplitArgs(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		fl   []string
		pos  []string
	}{
		{"flags before path", []string{"-s", "sch", "cfg.json", "--accessible"},
			[]string{"-s", "sch", "--accessible"}, []string{"cfg.json"}},
		{"flags after path", []string{"cfg.json", "--force", "-s", "sch"},
			[]string{"--force", "-s", "sch"}, []string{"cfg.json"}},
		{"equals form", []string{"cfg.json", "-s=sch"},
			[]string{"-s=sch"}, []string{"cfg.json"}},
		{"bare -s keeps value slot open", []string{"cfg.json", "-s"},
			[]string{"-s"}, []string{"cfg.json"}},
		{"no flags", []string{"cfg.json"}, nil, []string{"cfg.json"}},
		{"path-like value stays with -s", []string{"-s", "a b", "c"},
			[]string{"-s", "a b"}, []string{"c"}},
	}
	for _, tc := range cases {
		fl, pos := splitArgs(tc.in)
		if !reflect.DeepEqual(fl, tc.fl) || !reflect.DeepEqual(pos, tc.pos) {
			t.Errorf("%s: splitArgs(%q) = (%q, %q), want (%q, %q)",
				tc.name, tc.in, fl, pos, tc.fl, tc.pos)
		}
	}
}
