package docfmt

import (
	"bytes"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"json-schema-editor/internal/jsondoc"
)

func writeSession(t *testing.T, s Session) string {
	t.Helper()
	var buf bytes.Buffer
	if err := s.Write(&buf); err != nil {
		t.Fatalf("Write: %v", err)
	}
	return buf.String()
}

func TestForFormat(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"a.yaml", ".yaml"},
		{"a.YML", ".yaml"},
		{"x/a.yml", ".yaml"},
		{"a.json", ".json"},
		{"a.jsonc", ".json"},
		{"a.conf", ".json"},
		{"a", ".json"},
		{"a.JSONC", ".json"},
	}
	for _, tc := range cases {
		f := ForFormat(tc.path)
		if f == nil {
			t.Fatalf("%s: nil format", tc.path)
		}
		if got := f.Extensions()[0]; got != tc.want {
			t.Fatalf("%s: format %q, want %q", tc.path, got, tc.want)
		}
	}
	if _, ok := ForFormat("a.yaml").(yamlFormat); !ok {
		t.Fatalf("a.yaml is %T", ForFormat("a.yaml"))
	}
	if _, ok := ForFormat("a.unknown").(jsoncFormat); !ok {
		t.Fatalf("fallback is %T", ForFormat("a.unknown"))
	}
}

func TestJSONCNoCommentDelegates(t *testing.T) {
	s, err := (jsoncFormat{}).Load([]byte(`{"a":1,"b":[2,3]}`))
	if err != nil {
		t.Fatal(err)
	}
	want := s.Doc().String()
	if got := writeSession(t, s); got != want {
		t.Fatalf("session output %q, want doc output %q", got, want)
	}
}

func TestJSONCCommentSmoke(t *testing.T) {
	const src = "// hdr\n{\n  \"a\": 1 // note\n}\n"
	s, err := (jsoncFormat{}).Load([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if got := writeSession(t, s); got != src {
		t.Fatalf("got:\n%s\nwant:\n%s", got, src)
	}
}

func TestYAMLFormatNotices(t *testing.T) {
	s, err := (yamlFormat{}).Load([]byte("a: &x {p: 1}\nb: *x\n"))
	if err != nil {
		t.Fatal(err)
	}
	notes := s.Notices()
	if len(notes) != 1 || notes[0] != "yaml: 1 alias expanded" {
		t.Fatalf("notices = %#v", notes)
	}
	plain, err := (yamlFormat{}).Load([]byte("a: 1\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := plain.Notices(); got != nil {
		t.Fatalf("plain notices = %#v", got)
	}
	if got := (yamlFormat{}).NewEmpty().Notices(); got != nil {
		t.Fatalf("empty notices = %#v", got)
	}
	jsonc, err := (jsoncFormat{}).Load([]byte(`{"a":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := jsonc.Notices(); got != nil {
		t.Fatalf("jsonc notices = %#v", got)
	}
}

func TestJSONSessionSchemaLocation(t *testing.T) {
	str := func(s string) *jsondoc.Node { return &jsondoc.Node{Str: &s} }
	doc := &jsondoc.Node{Object: &[]jsondoc.Member{{Key: "name", Val: str("x")}}}
	s := jsoncSession{doc: doc}

	if _, ok := s.SchemaLocation(); ok {
		t.Fatal("absent location reported")
	}
	s.SetSchemaLocation("sch.json")
	if loc, ok := s.SchemaLocation(); !ok || loc != "sch.json" {
		t.Fatalf("location = %q, %v", loc, ok)
	}
	if got := (*doc.Object)[0].Key; got != "$schema" {
		t.Fatalf("new member not first: %v", got)
	}
	s.SetSchemaLocation("other.json")
	if loc, _ := s.SchemaLocation(); loc != "sch.json" {
		t.Fatalf("existing location overwritten: %q", loc)
	}

	// Empty and null members are filled in place.
	empty := &jsondoc.Node{Object: &[]jsondoc.Member{
		{Key: "a", Val: str("1")},
		{Key: "$schema", Val: str("")},
		{Key: "b", Val: str("2")},
	}}
	es := jsoncSession{doc: empty}
	es.SetSchemaLocation("fill.json")
	if got := (*empty.Object)[1]; got.Key != "$schema" || *got.Val.Str != "fill.json" {
		t.Fatalf("empty case: %#v", *empty.Object)
	}
	nullv := &jsondoc.Node{Object: &[]jsondoc.Member{{Key: "$schema", Val: &jsondoc.Node{Null: true}}}}
	ns := jsoncSession{doc: nullv}
	ns.SetSchemaLocation("n.json")
	if *(*nullv.Object)[0].Val.Str != "n.json" {
		t.Fatalf("null case: %#v", *nullv.Object)
	}

	s.ClearSchemaLocation()
	if _, ok := s.SchemaLocation(); ok {
		t.Fatal("clear left a location")
	}
	if len(*doc.Object) != 1 || (*doc.Object)[0].Key != "name" {
		t.Fatalf("clear changed other members: %#v", *doc.Object)
	}
}

func TestYAMLSchemaDirective(t *testing.T) {
	const src = "# config header\nname: demo # keep me\n"
	s, err := (yamlFormat{}).Load([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.SchemaLocation(); ok {
		t.Fatal("absent location reported")
	}
	s.SetSchemaLocation("sch.json")
	if loc, ok := s.SchemaLocation(); !ok || loc != "sch.json" {
		t.Fatalf("location = %q, %v", loc, ok)
	}
	const want = "# yaml-language-server: $schema=sch.json\n# config header\nname: demo # keep me\n"
	if got := writeSession(t, s); got != want {
		t.Fatalf("got:\n%q\nwant:\n%q", got, want)
	}
	s.SetSchemaLocation("other.json") // keeps the existing value
	if loc, _ := s.SchemaLocation(); loc != "sch.json" {
		t.Fatalf("existing location overwritten: %q", loc)
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(writeSession(t, s)), &doc); err != nil {
		t.Fatalf("output is not valid YAML: %v", err)
	}
	if len(doc) != 1 || doc["name"] != "demo" {
		t.Fatalf("schema leaked into the document: %#v", doc)
	}

	s.ClearSchemaLocation()
	if _, ok := s.SchemaLocation(); ok {
		t.Fatal("clear left a location")
	}
	if got := writeSession(t, s); got != src {
		t.Fatalf("after clear:\n%q\nwant:\n%q", got, src)
	}
}

func TestYAMLDirectiveParsed(t *testing.T) {
	const src = "# yaml-language-server: $schema=https://example.com/s.json\nname: demo\n"
	s, err := (yamlFormat{}).Load([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if loc, ok := s.SchemaLocation(); !ok || loc != "https://example.com/s.json" {
		t.Fatalf("location = %q, %v", loc, ok)
	}
	if got := writeSession(t, s); got != src {
		t.Fatalf("got:\n%q\nwant:\n%q", got, src)
	}
	s.SetSchemaLocation("other.json") // keeps the existing directive
	if loc, _ := s.SchemaLocation(); loc != "https://example.com/s.json" {
		t.Fatalf("existing directive overwritten: %q", loc)
	}
	// --pick clears first, then records the chosen schema.
	s.ClearSchemaLocation()
	s.SetSchemaLocation("picked.json")
	if loc, ok := s.SchemaLocation(); !ok || loc != "picked.json" {
		t.Fatalf("picked location = %q, %v", loc, ok)
	}
	out := writeSession(t, s)
	if strings.Contains(out, "example.com") || !strings.Contains(out, "yaml-language-server: $schema=picked.json") {
		t.Fatalf("old directive not replaced:\n%s", out)
	}
}

func TestYAMLExplicitSchemaMember(t *testing.T) {
	const src = "$schema: old.json\nname: demo\n"
	s, err := (yamlFormat{}).Load([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if loc, ok := s.SchemaLocation(); !ok || loc != "old.json" {
		t.Fatalf("location = %q, %v", loc, ok)
	}
	s.SetSchemaLocation("new.json")
	if loc, _ := s.SchemaLocation(); loc != "old.json" {
		t.Fatalf("explicit $schema overwritten: %q", loc)
	}
	if strings.Contains(writeSession(t, s), "yaml-language-server") {
		t.Fatal("directive added despite $schema member")
	}
	s.ClearSchemaLocation()
	if _, ok := s.SchemaLocation(); ok {
		t.Fatal("clear left a location")
	}
	if got := writeSession(t, s); got != "name: demo\n" {
		t.Fatalf("after clear: %q", got)
	}
}

func TestYAMLEmptySchemaMemberNotFilled(t *testing.T) {
	cases := []struct {
		name string
		src  string
		keep string
	}{
		{"empty string", "$schema: \"\"\nname: demo\n", "$schema: \"\""},
		{"explicit null", "$schema: null\nname: demo\n", "$schema: null"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := (yamlFormat{}).Load([]byte(tc.src))
			if err != nil {
				t.Fatal(err)
			}
			s.SetSchemaLocation("sch.json")
			out := writeSession(t, s)
			if !strings.Contains(out, tc.keep) {
				t.Fatalf("empty $schema member was filled or removed:\n%s", out)
			}
			if strings.Contains(out, "$schema: sch.json") {
				t.Fatalf("schema written into a $schema field:\n%s", out)
			}
			if !strings.Contains(out, "# yaml-language-server: $schema=sch.json") {
				t.Fatalf("directive missing:\n%s", out)
			}
			if loc, ok := s.SchemaLocation(); !ok || loc != "sch.json" {
				t.Fatalf("location = %q, %v", loc, ok)
			}
		})
	}
}

func TestYAMLNewEmptySchemaDirective(t *testing.T) {
	s := yamlFormat{}.NewEmpty()
	s.SetSchemaLocation("sch.json")
	const want = "# yaml-language-server: $schema=sch.json\n{}\n"
	if got := writeSession(t, s); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestNewEmptyWrites(t *testing.T) {
	for _, f := range []Format{jsoncFormat{}, yamlFormat{}} {
		if got := writeSession(t, f.NewEmpty()); got != "{}\n" {
			t.Fatalf("%T empty output %q", f, got)
		}
	}
}

func TestYAMLSessionRoundTrip(t *testing.T) {
	const src = `# config header
name: demo # keep me
items:
  - a # item note
`
	s, err := (yamlFormat{}).Load([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if got := writeSession(t, s); got != src {
		t.Fatalf("got:\n%s\nwant:\n%s", got, src)
	}
}
