package jsoncdoc

import (
	"bytes"
	"strings"
	"testing"

	"github.com/wertasy/json-schema-editor/internal/jsondoc"
)

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

func TestParseMatchesJSON(t *testing.T) {
	cases := []string{
		`{"a":1}`,
		`{"nested":{"k":"v"},"arr":[1,2,3]}`,
		`{"s":"line\nquote\"tab\t"}`,
		`{"f":1.0,"e":1e3,"neg":-0.5}`,
		`{"b":true,"z":false,"n":null}`,
		`{"dup":1,"dup":2}`,
		`[1,{"a":[]},[[]]]`,
		`"scalar"`,
		`42`,
		`null`,
	}
	for _, src := range cases {
		doc, _, err := Parse([]byte(src))
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		want, err := jsondoc.Parse(strings.NewReader(src))
		if err != nil {
			t.Fatalf("%s: reference: %v", src, err)
		}
		if got, want := doc.String(), want.String(); got != want {
			t.Fatalf("%s: got %s, want %s", src, got, want)
		}
	}
}

func TestParseComments(t *testing.T) {
	const src = `// config header
{
  "name": "demo", // keep me
  // port note
  "port": 8080,
  "items": [
    // item note
    "a"
    // tail note
  ]
}
`
	doc, meta, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	want := mustJSON(t, `{"name":"demo","port":8080,"items":["a"]}`)
	if got := doc.String(); got != want.String() {
		t.Fatalf("doc = %s", got)
	}
	cases := []struct {
		path []any
		get  func(commentSet) string
		want string
	}{
		{nil, func(c commentSet) string { return c.Head }, "// config header"},
		{[]any{"name"}, func(c commentSet) string { return c.Line }, "// keep me"},
		{[]any{"port"}, func(c commentSet) string { return c.KeyHead }, "// port note"},
		{[]any{"items", 0}, func(c commentSet) string { return c.Head }, "// item note"},
		{[]any{"items"}, func(c commentSet) string { return c.Foot }, "// tail note"},
	}
	for _, tc := range cases {
		cs, ok := meta.ByPath[pathKey(tc.path)]
		if !ok {
			t.Fatalf("no comments at %v", tc.path)
		}
		if got := tc.get(cs); got != tc.want {
			t.Fatalf("%v: got %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"missing value", `{"a": }`, "invalid character"},
		{"unclosed string", `{"a": "x}`, "unexpected end of JSON input"},
		{"unclosed block comment", `{"a": 1 /* x}`, "unexpected end of JSON input"},
		{"trailing data", `{"a":1} garbage`, "trailing data after JSON document"},
		{"bare control char", "{\"a\":\"x\ny\"}", "invalid character"},
		{"unquoted key", `{a: 1}`, "object key string"},
		{"empty", "", "unexpected end of JSON input"},
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

func TestWriteDelegatesWithoutComments(t *testing.T) {
	doc := mustJSON(t, `{"a":1,"b":[2,3]}`)
	want := doc.String()
	var wantBuf bytes.Buffer
	if err := doc.Write(&wantBuf); err != nil {
		t.Fatal(err)
	}
	if got := writeString(t, doc, nil); got != wantBuf.String() {
		t.Fatalf("nil meta: got %q, want %q", got, wantBuf.String())
	}
	if got := writeString(t, doc, &Meta{ByPath: map[string]commentSet{}}); got != wantBuf.String() {
		t.Fatalf("empty meta: got %q, want %q", got, wantBuf.String())
	}
	if want != wantBuf.String() {
		t.Fatalf("String and Write disagree")
	}
}

func TestWriteCommentsVerbatim(t *testing.T) {
	const src = `// config header
{
  "name": "demo", // keep me
  // port note
  "port": 8080,
  "items": [
    // item note
    "a"
  ]
}
`
	doc, meta, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if got := writeString(t, doc, meta); got != src {
		t.Fatalf("output:\n%s\nwant:\n%s", got, src)
	}
}

func TestWriteNormalizesTrailingCommas(t *testing.T) {
	doc, meta, err := Parse([]byte(`{"a":1,"b":[2,3,],}`))
	if err != nil {
		t.Fatal(err)
	}
	out := writeString(t, doc, meta)
	if strings.Contains(out, ",]") || strings.Contains(out, ",}") || !strings.Contains(out, `"b": [`) {
		t.Fatalf("trailing commas not normalized:\n%s", out)
	}
	want := mustJSON(t, `{"a":1,"b":[2,3]}`)
	if got := doc.String(); got != want.String() {
		t.Fatalf("data = %s, want %s", got, want.String())
	}
}

func TestCommentDroppedWithDeletedNode(t *testing.T) {
	const src = `{
  "name": "demo",
  // port note
  "port": 8080
}
`
	doc, meta, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if !doc.Remove([]any{"port"}) {
		t.Fatal("remove failed")
	}
	out := writeString(t, doc, meta)
	if strings.Contains(out, "// port note") || strings.Contains(out, `"port"`) {
		t.Fatalf("deleted member leaked:\n%s", out)
	}
}

func TestWriteEditedValueKeepsComments(t *testing.T) {
	const src = `{
  "name": "demo" // keep me
}
`
	doc, meta, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	doc.Set([]any{"name"}, &jsondoc.Node{Str: new("newname")})
	const want = `{
  "name": "newname" // keep me
}
`
	if got := writeString(t, doc, meta); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestWriteEmptyContainerComments(t *testing.T) {
	const src = `{
  "a": {
    // empty note
  },
  "b": [
    // empty array
  ]
}
`
	doc, meta, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if got := writeString(t, doc, meta); got != src {
		t.Fatalf("output:\n%s\nwant:\n%s", got, src)
	}
}

func TestWriteRootContainerTrailingComment(t *testing.T) {
	const src = `// hdr
{
  "a": 1
} // keep
`
	doc, meta, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if got := writeString(t, doc, meta); got != src {
		t.Fatalf("output:\n%s\nwant:\n%s", got, src)
	}
}

func TestWriteTailOnlyComment(t *testing.T) {
	const src = "{\"a\": 1}\n// tail\n"
	doc, meta, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if meta.Tail != "// tail" {
		t.Fatalf("tail = %q", meta.Tail)
	}
	if got := writeString(t, doc, meta); got != "{\n  \"a\": 1\n}\n// tail\n" {
		t.Fatalf("got %q", got)
	}
}

func TestWriteRootScalarComment(t *testing.T) {
	const src = "// header\n\"x\"\n// tail\n"
	doc, meta, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if got := writeString(t, doc, meta); got != src {
		t.Fatalf("got %q, want %q", got, src)
	}
}
