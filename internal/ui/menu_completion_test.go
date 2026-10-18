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

	"github.com/wertasy/json-schema-editor/internal/smodel"
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

// TestStringComboEditsAsTextWithTabOptions: a shell-style anyOf (open string
// plus a set of strings) edits as free text, the listed values are the only
// tab completions while the text matches one of them, and document-derived
// candidates take over once it matches none.
func TestStringComboEditsAsTextWithTabOptions(t *testing.T) {
	root := mustSchema(t, `{
  "type": "object",
  "properties": {
    "defaults": {"type": "object", "properties": {"run": {"type": "object", "properties": {
      "shell": {"anyOf": [
        {"type": "string"},
        {"type": "string", "enum": ["bash", "pwsh", "python", "sh", "cmd", "powershell"]}
      ]}
    }}}},
    "jobs": {"type": "object", "properties": {"build": {"type": "object", "properties": {
      "shell": {"type": "string"}}}}}
  }
}`)
	// a document value under the same key elsewhere: only the fallback may use it
	doc := tobj(
		tmem("defaults", tobj(tmem("run", tobj(tmem("shell", tstr("bash")))))),
		tmem("jobs", tobj(tmem("build", tobj(tmem("shell", tstr("custom-shell")))))))
	m := newListModel(root, doc, Options{})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})

	press(m, "enter", "enter", "enter") // defaults -> run -> shell
	if m.edit == nil || m.edit.fn.Key != "shell" {
		t.Fatalf("shell must edit as text, edit = %+v\n%s", m.edit, m.View())
	}
	if got := strings.Join(m.edit.cands, " "); got != "custom-shell" {
		t.Fatalf("document candidates = %q, want only custom-shell", got)
	}

	// matching text completes from the schema options alone, never the document
	press(m, "ctrl+u")
	if got := strings.Join(m.edit.sugs, " "); got != "bash pwsh python sh cmd powershell" {
		t.Fatalf("suggestions = %q, want the enum options only", got)
	}
	press(m, "p", "w")
	if len(m.edit.sugs) != 1 || m.edit.sugs[0] != "pwsh" {
		t.Fatalf("filtered suggestions = %v, want [pwsh]", m.edit.sugs)
	}
	if v := m.View(); !contains(v, "tab: ") || contains(v, "custom-shell") {
		t.Fatalf("hint line must list the option only:\n%s", v)
	}
	press(m, "tab", "enter")
	if m.edit != nil {
		t.Fatalf("enter must commit the completion, edit = %+v", m.edit)
	}
	if got := strAt(t, doc, "defaults", "run", "shell"); got != "pwsh" {
		t.Fatalf("shell = %q, want pwsh", got)
	}

	// a prefix no option matches falls back to the document-derived candidates
	press(m, "enter", "ctrl+u", "c", "u")
	if got := strings.Join(m.edit.sugs, " "); got != "custom-shell" {
		t.Fatalf("fallback suggestions = %q, want [custom-shell]", m.edit.sugs)
	}
	press(m, "tab", "enter")
	if got := strAt(t, doc, "defaults", "run", "shell"); got != "custom-shell" {
		t.Fatalf("shell = %q, want custom-shell", got)
	}

	// an unlisted value is still accepted (the open branch allows it)
	press(m, "enter")
	press(m, "ctrl+u", "z", "s", "h", "enter")
	if m.edit != nil {
		t.Fatalf("free text must commit, edit = %+v", m.edit)
	}
	if got := strAt(t, doc, "defaults", "run", "shell"); got != "zsh" {
		t.Fatalf("shell = %q, want the typed zsh", got)
	}
}

// TestCompletionShiftTabReverses: shift+tab walks the candidates backward and
// tab forward, both wrapping; a fresh cycle starts at the last candidate when
// stepping back.
func TestCompletionShiftTabReverses(t *testing.T) {
	root := mustSchema(t, `{
  "type": "object",
  "properties": {
    "shell": {"anyOf": [
      {"type": "string"},
      {"type": "string", "enum": ["bash", "pwsh", "python", "sh", "cmd", "powershell"]}
    ]}
  }
}`)
	m := newListModel(root, tobj(tmem("shell", tstr("bash"))), Options{})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})

	press(m, "enter", "ctrl+u") // open the shell editor with an empty prefix
	if m.edit == nil || len(m.edit.sugs) != 6 {
		t.Fatalf("edit = %+v, want the six options", m.edit)
	}

	press(m, "shift+tab")
	if m.edit.sugIdx != 5 || m.edit.ti.Value() != "powershell" {
		t.Fatalf("first shift+tab: idx=%d value=%q, want the last candidate", m.edit.sugIdx, m.edit.ti.Value())
	}
	if v := m.View(); !contains(v, "powershell") {
		t.Fatalf("the hint must show the selected candidate:\n%s", v)
	}
	press(m, "shift+tab")
	if m.edit.sugIdx != 4 || m.edit.ti.Value() != "cmd" {
		t.Fatalf("second shift+tab: idx=%d value=%q, want cmd", m.edit.sugIdx, m.edit.ti.Value())
	}
	press(m, "tab")
	if m.edit.sugIdx != 5 || m.edit.ti.Value() != "powershell" {
		t.Fatalf("tab must move forward again: idx=%d value=%q", m.edit.sugIdx, m.edit.ti.Value())
	}
	press(m, "tab") // wraps to the first
	if m.edit.sugIdx != 0 || m.edit.ti.Value() != "bash" {
		t.Fatalf("tab must wrap to the first candidate: idx=%d value=%q", m.edit.sugIdx, m.edit.ti.Value())
	}
	press(m, "shift+tab") // wraps back to the last
	if m.edit.sugIdx != 5 || m.edit.ti.Value() != "powershell" {
		t.Fatalf("shift+tab must wrap to the last candidate: idx=%d value=%q", m.edit.sugIdx, m.edit.ti.Value())
	}
}
