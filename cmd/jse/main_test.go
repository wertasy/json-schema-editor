package main

import (
	"reflect"
	"testing"
)

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
