package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/wertasy/json-schema-editor/internal/docfmt"
)

func TestResolveSchemaLocRelative(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	workDir := filepath.Join(root, "work")
	for _, dir := range []string{configDir, workDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := filepath.Join(configDir, "cfg.json")
	writeSchema := func(path string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(`{"type":"object"}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	session := func(loc string) docfmt.Session {
		s := docfmt.ForFormat(cfg).NewEmpty()
		s.SetSchemaLocation(loc)
		return s
	}

	t.Run("config directory wins", func(t *testing.T) {
		t.Chdir(workDir)
		writeSchema(filepath.Join(configDir, "schema.json"))
		writeSchema(filepath.Join(workDir, "schema.json"))
		got, err := resolveSchemaLoc("", cfg, session("schema.json"))
		if err != nil {
			t.Fatal(err)
		}
		if want := filepath.Join(configDir, "schema.json"); got != want {
			t.Fatalf("resolved %q, want %q", got, want)
		}
	})

	t.Run("falls back to cwd", func(t *testing.T) {
		t.Chdir(workDir)
		writeSchema(filepath.Join(workDir, "fallback.json"))
		got, err := resolveSchemaLoc("", cfg, session("fallback.json"))
		if err != nil {
			t.Fatal(err)
		}
		if got != "fallback.json" {
			t.Fatalf("resolved %q, want the cwd-relative path", got)
		}
		abs, err := filepath.Abs(got)
		if err != nil {
			t.Fatal(err)
		}
		if want := filepath.Join(workDir, "fallback.json"); abs != want {
			t.Fatalf("resolved to %q, want %q", abs, want)
		}
	})
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
