package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocDefault(t *testing.T) {
	t.Setenv("JSE_CATALOG_URL", "")
	if got := Loc(); got != DefaultURL {
		t.Fatalf("Loc() = %s, want %s", got, DefaultURL)
	}
}

func TestLocEnvOverride(t *testing.T) {
	t.Setenv("JSE_CATALOG_URL", "http://127.0.0.1:1/catalog.json")
	if got := Loc(); got != "http://127.0.0.1:1/catalog.json" {
		t.Fatalf("Loc() = %s, want the env override", got)
	}
}

func TestLoad(t *testing.T) {
	entries, err := Load("testdata/catalog.json")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(entries) != 9 {
		t.Fatalf("loaded %d entries, want 9 (NoURL skipped): %v", len(entries), names(entries))
	}
	byName := map[string]Entry{}
	for _, e := range entries {
		byName[e.Name] = e
	}
	e := byName["Versioned"]
	if e.URL != "https://example.org/versioned.json" {
		t.Fatalf("versions field not ignored, url = %s", e.URL)
	}
	if e.FileMatch[0] != "versioned.json" {
		t.Fatalf("fileMatch lost: %v", e.FileMatch)
	}
	if byName["Tsconfig"].Description != "TypeScript compiler configuration" {
		t.Fatalf("description lost: %#v", byName["Tsconfig"])
	}
	if byName["AliasB"].URL != byName["AliasA"].URL {
		t.Fatalf("aliased entries diverged: %#v vs %#v", byName["AliasA"], byName["AliasB"])
	}
}

func TestLoadLocalFileURLAndPath(t *testing.T) {
	abs, err := filepath.Abs("testdata/catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, loc := range []string{abs, "file://" + abs} {
		entries, err := Load(loc)
		if err != nil {
			t.Fatalf("load %s: %v", loc, err)
		}
		if len(entries) != 9 {
			t.Fatalf("load %s: %d entries", loc, len(entries))
		}
	}
}

func TestLoadErrors(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(bad); err == nil || !strings.Contains(err.Error(), "parse catalog") {
		t.Fatalf("malformed catalog: err = %v", err)
	}
	if _, err := Load(filepath.Join(dir, "missing.json")); err == nil {
		t.Fatal("missing catalog loaded without error")
	}
}
