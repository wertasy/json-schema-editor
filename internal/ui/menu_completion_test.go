package ui

// Value-completion regressions for variant (oneOf/anyOf) fields: the reference
// domains sing-box nests (dns_server → dns.servers[].tag) and the document key
// of variants shared through one $ref'd combo definition.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"json-schema-editor/internal/smodel"
)

// mustSchema parses a schema document into the form model the menu drives.
func mustSchema(t *testing.T, src string) *smodel.FormNode {
	t.Helper()
	p := filepath.Join(t.TempDir(), "schema.json")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	fn, _, err := smodel.Load(p)
	if err != nil {
		t.Fatalf("load schema: %v", err)
	}
	return fn
}

func TestTagReferenceNestedDomainCompletion(t *testing.T) {
	// sing-box's dns_server domain names structures nested as
	// dns.servers[].tag: both the anyOf's string variant and the server field
	// inside its object variant must complete those tags.
	root := mustSchema(t, `{
	  "type": "object",
	  "properties": {
	    "dns": {"type": "object", "properties": {"servers": {"type": "array", "items": {
	      "type": "object", "properties": {"tag": {"type": "string"}, "address": {"type": "string"}}}}}},
	    "route": {"type": "object", "properties": {"default_domain_resolver": {"anyOf": [
	      {"type": "string", "x-tag-reference": "dns_server"},
	      {"type": "object", "properties": {
	        "server": {"type": "string", "x-tag-reference": "dns_server"},
	        "disable_cache": {"type": "boolean"}}}]}}}
	  }
	}`)
	dns := tobj(tmem("servers", tarr(
		tobj(tmem("tag", tstr("proxy-dns"))),
		tobj(tmem("tag", tstr("zte-dns"))))))

	// an existing string value infers the string variant: enter edits in place
	doc := tobj(tmem("dns", dns), tmem("route", tobj(tmem("default_domain_resolver", tstr("zte-dns")))))
	m := newListModel(root, doc, Options{})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	press(m, "down", "enter", "enter") // route → default_domain_resolver → string editor
	if m.edit == nil || m.edit.fn.Key != "default_domain_resolver" {
		t.Fatalf("edit = %+v, want the resolver string variant:\n%s", m.edit, m.View())
	}
	if got := strings.Join(m.edit.cands, " "); got != "proxy-dns zte-dns" {
		t.Fatalf("string variant candidates = %q, want the dns.servers tags", got)
	}
	if v := m.View(); !contains(v, "tab: ") || !contains(v, "zte-dns") {
		t.Fatalf("hint line missing:\n%s", v)
	}
	press(m, "ctrl+u", "p", "r") // narrow to the proxy tag
	if len(m.edit.sugs) != 1 || m.edit.sugs[0] != "proxy-dns" {
		t.Fatalf("filtered suggestions = %v, want [proxy-dns]", m.edit.sugs)
	}
	press(m, "esc")

	// the object variant's server field carries the same reference domain
	doc2 := tobj(tmem("dns", dns), tmem("route", tobj(tmem("default_domain_resolver",
		tobj(tmem("server", tstr("zte-dns")))))))
	m2 := newListModel(root, doc2, Options{})
	m2.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	press(m2, "down", "enter", "enter", "enter") // route → resolver object menu → server editor
	if m2.edit == nil || m2.edit.fn.Key != "server" {
		t.Fatalf("edit = %+v, want the object variant's server field:\n%s", m2.edit, m2.View())
	}
	if got := strings.Join(m2.edit.cands, " "); got != "proxy-dns zte-dns" {
		t.Fatalf("object variant server candidates = %q, want the dns.servers tags", got)
	}
}

func TestSharedComboVariantCompletion(t *testing.T) {
	// two properties reference one anyOf definition: the second property's
	// string variant must suggest that property's own values (the memoized
	// variants once kept the first property's key).
	root := mustSchema(t, `{
	  "type": "object",
	  "$defs": {"duration": {"anyOf": [{"type": "integer"}, {"type": "string"}]}},
	  "properties": {
	    "connect_timeout": {"$ref": "#/$defs/duration"},
	    "udp_timeout": {"$ref": "#/$defs/duration"},
	    "wrap": {"type": "array", "items": {"type": "object",
	      "properties": {"udp_timeout": {"$ref": "#/$defs/duration"}}}}}
	}`)
	doc := tobj(
		tmem("connect_timeout", tstr("5s")),
		tmem("udp_timeout", tstr("10s")),
		tmem("wrap", tarr(tobj(tmem("udp_timeout", tstr("keep-me"))))))
	m := newListModel(root, doc, Options{})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	press(m, "down", "enter") // udp_timeout → string variant editor
	if m.edit == nil || m.edit.fn.Key != "udp_timeout" {
		t.Fatalf("edit = %+v, want the udp_timeout string variant:\n%s", m.edit, m.View())
	}
	// own key first, then every other string value in the document
	if got := strings.Join(m.edit.cands, " "); got != "keep-me 5s" {
		t.Fatalf("candidates = %q, want [keep-me 5s]", got)
	}
}

func TestCompletionAllDocumentStrings(t *testing.T) {
	// string fields without x-tag-reference complete from every non-empty
	// string in the document, same-key values first; a duplicate reaching the
	// other group first still ranks with its key
	doc := tobj(
		tmem("$schema", tstr("/tmp/probe.schema.json")),
		tmem("note", tstr("shared")),
		tmem("mounts", tarr(
			tobj(tmem("src", tstr("/data")), tmem("dst", tstr("/mnt"))),
			tobj(tmem("src", tstr("shared"))))),
		tmem("tags", tarr(tstr("alpha"), tstr("bee"))),
		tmem("ratio", tnum("1.5")),
	)
	lm := lmAt(doc)
	mounts := childOf(t, testModel(), "mounts")
	src := childOf(t, mounts.Items, "src")
	got := strings.Join(lm.completions(src, []any{"mounts", 2, "src"}), " ")
	if want := "/data shared /mnt alpha bee"; got != want {
		t.Fatalf("candidates = %q, want %q", got, want)
	}

	// array items rank their own array's values first and skip the item
	// being edited
	logging := childOf(t, testModel(), "logging")
	tags := childOf(t, logging, "tags")
	got = strings.Join(lm.completions(tags.Items, []any{"tags", 0}), " ")
	if want := "bee shared /data /mnt"; got != want {
		t.Fatalf("array item candidates = %q, want %q", got, want)
	}
}
