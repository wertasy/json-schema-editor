package smodel

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func mustLoad(t *testing.T, src string) *FormNode {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "schema.json")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	fn, _, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return fn
}

func child(t *testing.T, n *FormNode, key string) *FormNode {
	t.Helper()
	for _, c := range n.Children {
		if c.Key == key {
			return c
		}
	}
	t.Fatalf("child %q not found", key)
	return nil
}

func TestNestedObjects(t *testing.T) {
	fn := mustLoad(t, `{
  "type": "object",
  "properties": {
    "server": {
      "type": "object",
      "title": "Server",
      "required": ["port"],
      "properties": {
        "port": {"type": "integer", "minimum": 1024, "maximum": 65535, "title": "Port"},
        "debug": {"type": "boolean", "default": false}
      }
    },
    "name": {"type": "string", "minLength": 1, "pattern": "^[a-z]+$", "examples": ["mysrv"]}
  }
}`)
	if fn.Kind != KObject {
		t.Fatalf("root kind = %v", fn.Kind)
	}
	if fn.Title != "$" || fn.Key != "$" {
		t.Fatalf("root title/key = %q/%q", fn.Title, fn.Key)
	}
	if len(fn.Children) != 2 || fn.Children[0].Key != "server" || fn.Children[1].Key != "name" {
		t.Fatalf("children order: %+v", fn.Children)
	}
	srv := fn.Children[0]
	if srv.Kind != KObject || srv.Title != "Server" {
		t.Fatalf("server = %+v", srv)
	}
	port := child(t, srv, "port")
	if port.Kind != KScalar || port.JSONType != "integer" || !port.Required {
		t.Fatalf("port = %+v", port)
	}
	if port.Min == nil || *port.Min != 1024 || port.Max == nil || *port.Max != 65535 {
		t.Fatalf("port bounds = %+v", port)
	}
	name := child(t, fn, "name")
	if name.MinLen == nil || *name.MinLen != 1 || name.Pattern != "^[a-z]+$" || name.Placeholder != "mysrv" {
		t.Fatalf("name constraints = %+v", name)
	}
	dbg := child(t, srv, "debug")
	if dbg.Kind != KBoolean || dbg.Default == nil || dbg.Default.Bool == nil || *dbg.Default.Bool {
		t.Fatalf("debug = %+v", dbg)
	}
}

func TestDefsRef(t *testing.T) {
	fn := mustLoad(t, `{
  "$defs": {"positive": {"type": "integer", "minimum": 1, "title": "Positive"}},
  "type": "object",
  "properties": {
    "count": {"$ref": "#/$defs/positive"},
    "named": {"$ref": "#/$defs/positive", "title": "Override"}
  }
}`)
	count := child(t, fn, "count")
	if count.Kind != KScalar || count.Min == nil || *count.Min != 1 {
		t.Fatalf("count = %+v", count)
	}
	if count.Title != "Positive" {
		t.Fatalf("ref title = %q", count.Title)
	}
	named := child(t, fn, "named")
	if named.Title != "Override" {
		t.Fatalf("sibling title override = %q", named.Title)
	}
}

func TestRecursiveRefNoHang(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "schema.json")
	src := `{
  "$defs": {"node": {"type": "object", "properties": {"next": {"$ref": "#/$defs/node"}, "label": {"type": "string"}}}},
  "type": "object",
  "properties": {"root": {"$ref": "#/$defs/node"}}
}`
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	type res struct {
		fn  *FormNode
		err error
	}
	ch := make(chan res, 1)
	go func() {
		fn, _, err := Load(p)
		ch <- res{fn, err}
	}()
	var r res
	select {
	case r = <-ch:
	case <-time.After(10 * time.Second):
		t.Fatal("recursive $ref hang")
	}
	if r.err != nil {
		t.Fatal(r.err)
	}
	n := child(t, r.fn, "root")
	for n.Kind == KObject {
		n = child(t, n, "next")
	}
	if n.Kind != KRaw {
		t.Errorf("expected eventual KRaw, got %v", n.Kind)
	}
}

func TestRemoteRef(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"t": {"type": "string", "minLength": 2}}`))
	}))
	defer remote.Close()
	dir := t.TempDir()
	root := filepath.Join(dir, "root.json")
	src := strings.Replace(`{"type":"object","properties":{"x":{"$ref":"REMOTE/types.json#/t"}}}`, "REMOTE", remote.URL, 1)
	if err := os.WriteFile(root, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	fn, _, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	x := child(t, fn, "x")
	if x.Kind != KScalar || x.MinLen == nil || *x.MinLen != 2 {
		t.Fatalf("remote ref child = %+v", x)
	}
}

func TestRemoteRefUnreachable(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root.json")
	src := `{"type":"object","properties":{"x":{"$ref":"http://127.0.0.1:1/types.json"}}}`
	if err := os.WriteFile(root, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	fn, _, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	x := child(t, fn, "x")
	if x.Kind != KRaw {
		t.Fatalf("unreachable ref kind = %v", x.Kind)
	}
	if !strings.Contains(x.Desc, "ref unreachable") {
		t.Fatalf("desc = %q", x.Desc)
	}
}

func TestRelativeRefAgainstDocBase(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "common.json"), []byte(`{"defs": {"port": {"type": "integer", "minimum": 1}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "root.json")
	if err := os.WriteFile(root, []byte(`{
  "$id": "https://example.com/schemas/root.json",
  "type": "object",
  "properties": {"p": {"$ref": "common.json#/defs/port"}}
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// $id base would point at example.com; this host is unreachable, so the
	// ref must degrade. The same test with no $id resolves against the file
	// directory and succeeds.
	fn, _, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	p := child(t, fn, "p")
	if p.Kind != KRaw || !strings.Contains(p.Desc, "ref unreachable") {
		t.Fatalf("$id-relative ref should degrade: %+v", p)
	}

	root2 := filepath.Join(dir, "root2.json")
	if err := os.WriteFile(root2, []byte(`{
  "type": "object",
  "properties": {"p": {"$ref": "common.json#/defs/port"}}
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	fn2, _, err := Load(root2)
	if err != nil {
		t.Fatal(err)
	}
	p2 := child(t, fn2, "p")
	if p2.Kind != KScalar || p2.Min == nil || *p2.Min != 1 {
		t.Fatalf("directory-relative ref = %+v", p2)
	}
}

// TestDuplicatePropertyKeys: machine-generated schemas repeat keys inside
// one properties object (e.g. the dial-fields tables of older sing-box
// schemas). jsondoc keeps every member, but the document value always
// resolves to the first member, so later duplicates are dead rows — they
// must be dropped, keeping the first occurrence.
func TestDuplicatePropertyKeys(t *testing.T) {
	fn := mustLoad(t, `{
  "type": "object",
  "properties": {
    "a": {"type": "string", "title": "First A"},
    "b": {"type": "integer"},
    "a": {"type": "boolean"},
    "c": {"type": "string"},
    "b": {"type": "number", "title": "Second B"}
  }
}`)
	var keys []string
	for _, c := range fn.Children {
		keys = append(keys, c.Key)
	}
	if strings.Join(keys, ",") != "a,b,c" {
		t.Fatalf("children keys = %v, want [a b c]", keys)
	}
	a := child(t, fn, "a")
	if a.Kind != KScalar || a.Kind == KBoolean {
		t.Fatalf("first a must win, got kind=%v", a.Kind)
	}
}

func TestAllOfMerge(t *testing.T) {
	fn := mustLoad(t, `{
  "type": "object",
  "properties": {
    "merged": {
      "allOf": [
        {"type": "object", "properties": {"a": {"type": "string"}}, "required": ["a"]},
        {"properties": {"b": {"type": "integer"}}, "required": ["b"], "minimum": 5}
      ]
    }
  }
}`)
	m := child(t, fn, "merged")
	if m.Kind != KObject {
		t.Fatalf("merged kind = %v", m.Kind)
	}
	if len(m.Children) != 2 || m.Children[0].Key != "a" || m.Children[1].Key != "b" {
		t.Fatalf("merged children = %+v", m.Children)
	}
	if !m.Children[0].Required || !m.Children[1].Required {
		t.Fatal("required union failed")
	}
}

func TestAllOfNumericStricter(t *testing.T) {
	fn := mustLoad(t, `{
  "type": "object",
  "properties": {
    "n": {"allOf": [{"type": "integer", "minimum": 1}, {"minimum": 10}, {"maximum": 99}]}
  }
}`)
	n := child(t, fn, "n")
	if n.Kind != KScalar || n.JSONType != "integer" {
		t.Fatalf("n = %+v", n)
	}
	if n.Min == nil || *n.Min != 10 {
		t.Fatalf("stricter minimum = %+v", n.Min)
	}
	if n.Max == nil || *n.Max != 99 {
		t.Fatalf("maximum = %+v", n.Max)
	}
}

func TestOneOfConstMerge(t *testing.T) {
	fn := mustLoad(t, `{
  "type": "object",
  "properties": {
    "mode": {"oneOf": [{"const": "fast"}, {"const": "slow"}, {"const": "auto"}]}
  }
}`)
	m := child(t, fn, "mode")
	if m.Kind != KEnum || len(m.Enum) != 3 {
		t.Fatalf("mode = %+v", m)
	}
	if m.Enum[0].Encoded != "\"fast\"" || m.Enum[0].Display != "fast" {
		t.Fatalf("option = %+v", m.Enum[0])
	}
}

func TestOneOfVariants(t *testing.T) {
	fn := mustLoad(t, `{
  "type": "object",
  "properties": {
    "conn": {
      "oneOf": [
        {"title": "TCP", "type": "object", "properties": {"host": {"type": "string"}, "port": {"type": "integer"}}, "required": ["host"]},
        {"title": "Unix", "type": "object", "properties": {"path": {"type": "string"}}, "required": ["path"]}
      ]
    }
  }
}`)
	c := child(t, fn, "conn")
	if c.Kind != KOneOf || len(c.Variants) != 2 {
		t.Fatalf("conn = %+v", c)
	}
	if c.Variants[0].Title != "TCP" || c.Variants[1].Title != "Unix" {
		t.Fatalf("variant titles = %q %q", c.Variants[0].Title, c.Variants[1].Title)
	}
	if c.Variants[0].Kind != KObject {
		t.Fatalf("variant kind = %v", c.Variants[0].Kind)
	}
}

func TestVariantTagReference(t *testing.T) {
	fn := mustLoad(t, `{
  "type": "object",
  "properties": {
    "http_client": {"anyOf": [
      {"type": "string", "x-tag-reference": "http_client"},
      {"type": "object", "properties": {"engine": {"type": "string"}}}
    ]}
  }
}`)
	c := child(t, fn, "http_client")
	if c.Kind != KOneOf || len(c.Variants) != 2 {
		t.Fatalf("http_client = %+v", c)
	}
	if c.Variants[0].TagRef != "http_client" {
		t.Fatalf("string variant TagRef = %q, want http_client", c.Variants[0].TagRef)
	}
	if c.Variants[1].TagRef != "" {
		t.Fatalf("object variant TagRef = %q, want empty", c.Variants[1].TagRef)
	}
}

func TestVariantUntitledCarriesNoLabel(t *testing.T) {
	fn := mustLoad(t, `{
  "type": "object",
  "properties": {
    "v": {"anyOf": [{"type": "string"}, {"type": "integer"}]}
  }
}`)
	v := child(t, fn, "v")
	if v.Kind != KOneOf || len(v.Variants) != 2 {
		t.Fatalf("v = %+v", v)
	}
	// no synthesized "variant N": the UI shows the structural signature
	if v.Variants[0].Title != "" || v.Variants[1].Title != "" {
		t.Fatalf("untitled variant titles = %q %q, want empty",
			v.Variants[0].Title, v.Variants[1].Title)
	}
}

func TestEnumEncoding(t *testing.T) {
	fn := mustLoad(t, `{
  "type": "object",
  "properties": {
    "mix": {"enum": [3, "3", true, null, 1.5]}
  }
}`)
	m := child(t, fn, "mix")
	if m.Kind != KEnum || len(m.Enum) != 5 {
		t.Fatalf("mix = %+v", m)
	}
	enc := map[string]bool{}
	for _, o := range m.Enum {
		if enc[o.Encoded] {
			t.Fatalf("duplicate encoding %q", o.Encoded)
		}
		enc[o.Encoded] = true
	}
	want := map[string]string{"3": "3", "\"3\"": "\"3\"", "true": "true", "null": "null", "1.5": "1.5"}
	for w := range want {
		if !enc[w] {
			t.Fatalf("missing encoding %q in %v", w, enc)
		}
	}
}

func TestArrays(t *testing.T) {
	fn := mustLoad(t, `{
  "type": "object",
  "properties": {
    "tags": {"type": "array", "items": {"type": "string"}, "minItems": 1, "maxItems": 5},
    "levels": {"type": "array", "items": {"enum": ["debug", "info", "warn"]}},
    "mounts": {"type": "array", "items": {"type": "object", "properties": {"src": {"type": "string"}}}},
    "matrix": {"type": "array", "items": {"type": "array", "items": {"type": "integer"}}}
  }
}`)
	tags := child(t, fn, "tags")
	if tags.Kind != KArray || tags.Items == nil || tags.Items.Kind != KScalar || tags.Items.JSONType != "string" {
		t.Fatalf("tags = %+v", tags)
	}
	if tags.MinItems != 1 || tags.MaxItems != 5 {
		t.Fatalf("tags bounds = %d/%d", tags.MinItems, tags.MaxItems)
	}
	levels := child(t, fn, "levels")
	if levels.Items.Kind != KEnum || len(levels.Items.Enum) != 3 {
		t.Fatalf("levels items = %+v", levels.Items)
	}
	mounts := child(t, fn, "mounts")
	if mounts.Items.Kind != KObject {
		t.Fatalf("mounts items = %v", mounts.Items.Kind)
	}
	matrix := child(t, fn, "matrix")
	if matrix.Items.Kind != KRaw {
		t.Fatalf("nested array items should be KRaw, got %v", matrix.Items.Kind)
	}
}

func TestPropertyOrderPreserved(t *testing.T) {
	fn := mustLoad(t, `{
  "type": "object",
  "properties": {
    "zulu": {"type": "string"},
    "alpha": {"type": "string"},
    "Mike": {"type": "string"},
    "1num": {"type": "string"}
  }
}`)
	got := []string{}
	for _, c := range fn.Children {
		got = append(got, c.Key)
	}
	want := "zulu,alpha,Mike,1num"
	if strings.Join(got, ",") != want {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestUnknownTypeDegrades(t *testing.T) {
	fn := mustLoad(t, `{"type": "object", "properties": {"weird": {}, "anyConst": {"const": {"a": 1}}}}`)
	w := child(t, fn, "weird")
	if w.Kind != KRaw {
		t.Fatalf("empty schema kind = %v", w.Kind)
	}
	ac := child(t, fn, "anyConst")
	if ac.Kind != KEnum || len(ac.Enum) != 1 || ac.Enum[0].Display != "{\"a\":1}" {
		t.Fatalf("const object = %+v", ac)
	}
}

func TestRootNonObject(t *testing.T) {
	s := mustLoad(t, `{"type": "string", "minLength": 1}`)
	if s.Kind != KScalar || s.Title != "$" {
		t.Fatalf("scalar root = %+v", s)
	}
	a := mustLoad(t, `{"type": "array", "items": {"type": "integer"}}`)
	if a.Kind != KArray || a.Items.Kind != KScalar {
		t.Fatalf("array root = %+v", a)
	}
	o := mustLoad(t, `{"oneOf": [{"type": "string"}, {"type": "integer"}]}`)
	if o.Kind != KOneOf || len(o.Variants) != 2 {
		t.Fatalf("oneOf root = %+v", o)
	}
}

func TestSharedRefTargetKeepsOwnTitle(t *testing.T) {
	// Several properties $ref-ing the same untitled definition must each
	// fall back to their own key, not the first key that touched the node
	// (sing-box schema shares duration/address defs across many fields).
	fn := mustLoad(t, `{
  "type": "object",
  "$defs": {"duration": {"type": "string"}},
  "properties": {
    "connect_timeout": {"$ref": "#/$defs/duration"},
    "udp_timeout":     {"$ref": "#/$defs/duration"},
    "fallback_delay":  {"$ref": "#/$defs/duration"}
  }
}`)
	for _, key := range []string{"connect_timeout", "udp_timeout", "fallback_delay"} {
		c := child(t, fn, key)
		if c.Title != key {
			t.Fatalf("child %q: title = %q, want own key", key, c.Title)
		}
	}
}

func TestSharedComboVariantsKeepReferencingKey(t *testing.T) {
	// Combo variants are values of the referencing property: a $ref'd anyOf
	// definition reached from several properties must hand each one its own
	// key, or value completion would look under the first property's member.
	fn := mustLoad(t, `{
	  "type": "object",
	  "$defs": {"duration": {"anyOf": [{"type": "integer"}, {"type": "string"}]}},
	  "properties": {
	    "connect_timeout": {"$ref": "#/$defs/duration"},
	    "udp_timeout":     {"$ref": "#/$defs/duration"}
	  }
	}`)
	for _, key := range []string{"connect_timeout", "udp_timeout"} {
		c := child(t, fn, key)
		if c.Key != key {
			t.Fatalf("%s: node key = %q", key, c.Key)
		}
		if len(c.Variants) != 2 {
			t.Fatalf("%s: %d variants, want 2", key, len(c.Variants))
		}
		for i, v := range c.Variants {
			if v.Key != key {
				t.Errorf("%s variant %d: key = %q, want %q", key, i, v.Key, key)
			}
		}
	}
}

func TestLoadErrors(t *testing.T) {
	if _, _, err := Load(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Fatal("missing file should error")
	}
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`{invalid`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(bad); err == nil {
		t.Fatal("invalid JSON should error")
	}
}
