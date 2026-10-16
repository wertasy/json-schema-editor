package catalog

import (
	"testing"
)

func testEntries(t *testing.T) []Entry {
	t.Helper()
	entries, err := Load("testdata/catalog.json")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	return entries
}

func names(entries []Entry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Name
	}
	return out
}

func TestMatchExactName(t *testing.T) {
	entries := testEntries(t)
	got := Match(entries, "/home/u/app/package.json")
	if len(got) != 1 || got[0].Name != "Package" {
		t.Fatalf("exact name: %v", names(got))
	}
}

func TestMatchCaseInsensitive(t *testing.T) {
	entries := testEntries(t)
	got := Match(entries, "/home/u/app/PACKAGE.JSON")
	if len(got) != 1 || got[0].Name != "Package" {
		t.Fatalf("case-insensitive: %v", names(got))
	}
}

func TestMatchStarWithinSegment(t *testing.T) {
	entries := testEntries(t)
	cases := []struct {
		path string
		want string
	}{
		{"/x/tsconfig.json", "Tsconfig"},
		{"/x/tsconfig.build.json", "Tsconfig"},
	}
	for _, tc := range cases {
		got := Match(entries, tc.path)
		if len(got) != 1 || got[0].Name != tc.want {
			t.Fatalf("%s: %v, want %s", tc.path, names(got), tc.want)
		}
	}
	// "*" stays within one segment: a leading character is not spanned.
	if got := Match(entries, "/x/mytsconfig.json"); len(got) != 0 {
		t.Fatalf("mytsconfig.json matched: %v", names(got))
	}
}

func TestMatchSuffixGlob(t *testing.T) {
	entries := testEntries(t)
	got := Match(entries, "/srv/app/odd.srv.json")
	if len(got) != 1 || got[0].Name != "Srv" {
		t.Fatalf("*.srv.json: %v", names(got))
	}
	// "*.srv.json" does not match plain srv.json.
	if got := Match(entries, "/srv/app/srv.json"); len(got) != 0 {
		t.Fatalf("srv.json matched by *.srv.json: %v", names(got))
	}
}

func TestMatchDoubleStarSpansDirectories(t *testing.T) {
	entries := testEntries(t)
	for _, p := range []string{
		"/.cdf/config.yaml",
		"/a/.cdf/config.yaml",
		"/a/b/c/.cdf/config.yaml",
	} {
		got := Match(entries, p)
		if len(got) != 1 || got[0].Name != "CDF" {
			t.Fatalf("%s: %v, want CDF", p, names(got))
		}
	}
	if got := Match(entries, "/a/config.yaml"); len(got) != 0 {
		t.Fatalf("/a/config.yaml matched: %v", names(got))
	}
	if got := Match(entries, "/a/config.yaml/.cdf/config.yaml"); len(got) != 1 {
		t.Fatalf("segment boundary not respected: %v", names(got))
	}
}

func TestMatchSlashPatternAnchorsFullPath(t *testing.T) {
	entries := testEntries(t)
	got := Match(entries, "/srv/app/.platform/applications.yml")
	if len(got) != 1 || got[0].Name != "Platform" {
		t.Fatalf(".platform pattern: %v", names(got))
	}
	// Never matched by basename alone, and the directory name is exact.
	for _, p := range []string{
		"/srv/app/applications.yml",
		"/srv/app/platform/applications.yml",
		"/srv/app/my.platform/applications.yml",
	} {
		if m := Match(entries, p); len(m) != 0 {
			t.Fatalf("%s matched: %v", p, names(m))
		}
	}
}

func TestMatchNamePatternIgnoresPath(t *testing.T) {
	entries := testEntries(t)
	// A name-only pattern matches whatever directory the file lives in...
	got := Match(entries, "/deep/nested/dir/package.json")
	if len(got) != 1 || got[0].Name != "Package" {
		t.Fatalf("name pattern vs path: %v", names(got))
	}
	// ...but never matches when the name itself is just part of the path.
	if m := Match(entries, "/x/package.json/README"); len(m) != 0 {
		t.Fatalf("path containing the name matched: %v", names(m))
	}
}

func TestMatchDeduplicatesByURL(t *testing.T) {
	entries := testEntries(t)
	// one.json hits AliasA and AliasB; both share the URL.
	got := Match(entries, "/x/one.json")
	if len(got) != 1 || got[0].Name != "AliasA" {
		t.Fatalf("same-URL aliases not deduplicated: %v", names(got))
	}
	if got := Match(entries, "/x/two.json"); len(got) != 1 || got[0].Name != "AliasB" {
		t.Fatalf("second alias: %v", names(got))
	}
}

func TestMatchCatalogOrder(t *testing.T) {
	// A multi-entry result keeps catalog order.
	cat := []Entry{
		{Name: "B", URL: "https://b", FileMatch: []string{"*.json"}},
		{Name: "A", URL: "https://a", FileMatch: []string{"a.json"}},
	}
	got := Match(cat, "/x/a.json")
	if len(got) != 2 || got[0].Name != "B" || got[1].Name != "A" {
		t.Fatalf("catalog order lost: %v", names(got))
	}
}

func TestMatchNoFileMatchEntries(t *testing.T) {
	entries := testEntries(t)
	if got := Match(entries, "/x/nourl.json"); len(got) != 0 {
		t.Fatalf("skipped entry still matches: %v", names(got))
	}
	if got := Match(entries, "/x/nomatch.json"); len(got) != 0 {
		t.Fatalf("fileMatch-less entry matched: %v", names(got))
	}
}
