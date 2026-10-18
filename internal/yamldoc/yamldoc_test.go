package yamldoc

import (
	"bytes"
	"strings"
	"testing"

	"json-schema-editor/internal/jsondoc"
)

// mustJSON parses expected JSON into the reference tree.
func mustJSON(t *testing.T, s string) *jsondoc.Node {
	t.Helper()
	n, err := jsondoc.Parse(strings.NewReader(s))
	if err != nil {
		t.Fatalf("parse reference JSON: %v", err)
	}
	return n
}

func writeString(t *testing.T, n *jsondoc.Node, meta *Meta) string {
	t.Helper()
	var buf bytes.Buffer
	if err := Write(&buf, n, meta); err != nil {
		t.Fatalf("Write: %v", err)
	}
	return buf.String()
}

func TestParseBasic(t *testing.T) {
	const src = `name: demo
port: 8080
ratio: 1.0
items:
  - a
  - b
nested:
  k: v
`
	doc, meta, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	want := mustJSON(t, `{"name":"demo","port":8080,"ratio":1.0,"items":["a","b"],"nested":{"k":"v"}}`)
	if got, want := doc.String(), want.String(); got != want {
		t.Fatalf("doc = %s\nwant %s", got, want)
	}
	if meta.Aliases != 0 {
		t.Fatalf("aliases = %d", meta.Aliases)
	}
}

func TestParseNumberNormalization(t *testing.T) {
	const src = `hex: 0x1F
oct: 0o17
under: 1_000
f: 1.0
exp: 1e3
`
	doc, _, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	want := mustJSON(t, `{"hex":31,"oct":15,"under":1000,"f":1.0,"exp":1e3}`)
	if got := doc.String(); got != want.String() {
		t.Fatalf("doc = %s\nwant %s", got, want.String())
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"multiple documents", "a: 1\n---\nb: 2\n", "multiple YAML documents"},
		{"non-string key", "1: x\n", "only string keys"},
		{"merge key", "base: &b {p: 1}\nchild:\n  <<: *b\n", "merge keys"},
		{"infinity", "v: .inf\n", "unsupported YAML float"},
		{"nan", "v: .nan\n", "unsupported YAML float"},
		{"empty", "", "empty YAML document"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := Parse([]byte(tc.src))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestParseAliasExpands(t *testing.T) {
	doc, meta, err := Parse([]byte("a: &x {p: 1}\nb: *x\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := mustJSON(t, `{"a":{"p":1},"b":{"p":1}}`)
	if got := doc.String(); got != want.String() {
		t.Fatalf("doc = %s\nwant %s", got, want.String())
	}
	if meta.Aliases != 1 {
		t.Fatalf("aliases = %d, want 1", meta.Aliases)
	}
}

func TestParseTimestampAsString(t *testing.T) {
	doc, _, err := Parse([]byte("t: 2026-10-18\n"))
	if err != nil {
		t.Fatal(err)
	}
	n, ok := doc.Get([]any{"t"})
	if !ok || n.Str == nil || *n.Str != "2026-10-18" {
		t.Fatalf("timestamp = %#v", n)
	}
}

func TestWriteRoundTrip(t *testing.T) {
	const src = `# config header
name: demo # keep me
port: 8080
items:
  - a # item note
  - b
`
	doc, meta, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	out := writeString(t, doc, meta)
	doc2, _, err := Parse([]byte(out))
	if err != nil {
		t.Fatalf("reparse: %v\n%s", err, out)
	}
	if got, want := doc2.String(), doc.String(); got != want {
		t.Fatalf("round-trip doc = %s\nwant %s", got, want)
	}
}

func TestCommentsPreservedVerbatim(t *testing.T) {
	const src = `# config header
name: demo # keep me
# port note
port: 8080
items:
  - a # item note
  - b
`
	doc, meta, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if got := writeString(t, doc, meta); got != src {
		t.Fatalf("output:\n%s\nwant:\n%s", got, src)
	}
}

func TestBlankLineSeparatedCommentPreserved(t *testing.T) {
	// yaml.v3 does not record the blank line itself, so the comment is
	// preserved but the separation collapses. The comment must stay with
	// the member it precedes.
	const src = "name: demo\n\n# separated note\nport: 8080\n"
	doc, meta, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	out := writeString(t, doc, meta)
	if !strings.Contains(out, "# separated note\nport: 8080") {
		t.Fatalf("comment lost or moved:\n%s", out)
	}
	if !strings.Contains(out, "name: demo") {
		t.Fatalf("data lost:\n%s", out)
	}
}

func TestCommentDroppedWithDeletedNode(t *testing.T) {
	const src = `name: demo
# port note
port: 8080
`
	doc, meta, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if !doc.Remove([]any{"port"}) {
		t.Fatal("remove failed")
	}
	out := writeString(t, doc, meta)
	if strings.Contains(out, "# port note") || strings.Contains(out, "port:") {
		t.Fatalf("deleted member leaked:\n%s", out)
	}
}

func TestWriteQuotesAmbiguousStrings(t *testing.T) {
	cases := []struct{ in, want string }{
		{`v: "123"` + "\n", `v: "123"` + "\n"},
		{`v: "true"` + "\n", `v: "true"` + "\n"},
		{`v: ""` + "\n", `v: ""` + "\n"},
	}
	for _, tc := range cases {
		doc, meta, err := Parse([]byte(tc.in))
		if err != nil {
			t.Fatal(err)
		}
		if got := writeString(t, doc, meta); got != tc.want {
			t.Fatalf("in %q: got %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestWriteIndent(t *testing.T) {
	doc := mustJSON(t, `{"a":{"b":1},"c":[2,3]}`)
	const want = "a:\n  b: 1\nc:\n  - 2\n  - 3\n"
	if got := writeString(t, doc, nil); got != want {
		t.Fatalf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestWriteNilMeta(t *testing.T) {
	doc := mustJSON(t, `{"a":1}`)
	if got := writeString(t, doc, nil); got != "a: 1\n" {
		t.Fatalf("got %q", got)
	}
}

func TestWriteEditedValueKeepsComments(t *testing.T) {
	const src = `# header
name: demo # keep me
`
	doc, meta, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	doc.Set([]any{"name"}, &jsondoc.Node{Str: new("newname")})
	const want = `# header
name: newname # keep me
`
	if got := writeString(t, doc, meta); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestWriteDroppedTrailingRootComment(t *testing.T) {
	const src = "a: 1\n# tail\n"
	doc, meta, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if got := writeString(t, doc, meta); got != src {
		t.Fatalf("got %q want %q", got, src)
	}
}
