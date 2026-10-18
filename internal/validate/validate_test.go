package validate

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wertasy/json-schema-editor/internal/jsondoc"
)

const schemaSrc = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "properties": {
    "server": {
      "type": "object",
      "additionalProperties": false,
      "required": ["port"],
      "properties": {
        "port": {"type": "integer", "minimum": 1024},
        "host": {"type": "string"}
      }
    }
  },
  "required": ["server"]
}`

func writeSchema(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "schema.json")
	if err := os.WriteFile(p, []byte(schemaSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func cfg(t *testing.T, src string) *jsondoc.Node {
	t.Helper()
	n, err := jsondoc.Parse(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestValidPasses(t *testing.T) {
	v, err := New(writeSchema(t))
	if err != nil {
		t.Fatal(err)
	}
	if errs := v.Check(cfg(t, `{"server": {"port": 8080, "host": "x"}}`)); len(errs) != 0 {
		t.Fatalf("errs = %v", errs)
	}
}

func TestConstraintViolations(t *testing.T) {
	v, err := New(writeSchema(t))
	if err != nil {
		t.Fatal(err)
	}
	errs := v.Check(cfg(t, `{"server": {"port": 80}}`))
	joined := strings.Join(errs, "\n")
	if !strings.Contains(joined, "at '$.server.port':") || !strings.Contains(joined, "minimum") {
		t.Fatalf("minimum error missing or wrong path: %v", errs)
	}

	errs = v.Check(cfg(t, `{"server": {"host": "x"}}`))
	joined = strings.Join(errs, "\n")
	if !strings.Contains(joined, "at '$.server': missing property 'port'") {
		t.Fatalf("required error missing or wrong path: %v", errs)
	}

	errs = v.Check(cfg(t, `{"server": {"port": 8080, "extra": 1}}`))
	joined = strings.Join(errs, "\n")
	if !strings.Contains(joined, "at '$.server': additional properties 'extra' not allowed") {
		t.Fatalf("additionalProperties error missing or wrong path: %v", errs)
	}

	errs = v.Check(cfg(t, `{}`))
	if len(errs) != 1 || !strings.Contains(errs[0], "at '$': missing property 'server'") {
		t.Fatalf("root required error = %v, want the document-root path", errs)
	}
}

func TestPathShapes(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "shapes.schema.json")
	src := `{"type":"object","properties":{` +
		`"items":{"type":"array","items":{"type":"object","properties":` +
		`{"port":{"type":"integer","minimum":1}}}},` +
		`"123":{"type":"integer","minimum":10}}}`
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	v, err := New(p)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(v.Check(cfg(t, `{"items":[{"port":1},{"port":0}],"123":1}`)), "\n")
	if !strings.Contains(joined, "at '$.items[1].port':") {
		t.Fatalf("array index path missing: %q", joined)
	}
	if !strings.Contains(joined, "at '$.123':") {
		t.Fatalf("numeric object key must stay a field: %q", joined)
	}
}

func TestRemoteSchema(t *testing.T) {
	// Ephemeral ports get reused; a shared cache can then serve another
	// test's schema for the same URL.
	t.Setenv("JSE_CACHE_DIR", t.TempDir())
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(schemaSrc))
	}))
	defer ts.Close()

	v, err := New(ts.URL + "/schema.json")
	if err != nil {
		t.Fatal(err)
	}
	if errs := v.Check(cfg(t, `{"server": {"port": 8080}}`)); len(errs) != 0 {
		t.Fatalf("remote valid errs = %v", errs)
	}
	if errs := v.Check(cfg(t, `{"server": {"port": 1}}`)); len(errs) == 0 {
		t.Fatal("remote invalid should fail")
	}
}

func TestRemoteRefInsideSchema(t *testing.T) {
	t.Setenv("JSE_CACHE_DIR", t.TempDir())
	refServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"type": "integer", "minimum": 1024}`))
	}))
	defer refServer.Close()

	root := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{
      "type": "object",
      "properties": {"port": {"$ref": "` + refServer.URL + `/port.json` + `"}}
    }`))
	}))
	defer root.Close()

	v, err := New(root.URL + "/schema.json")
	if err != nil {
		t.Fatal(err)
	}
	if errs := v.Check(cfg(t, `{"port": 8080}`)); len(errs) != 0 {
		t.Fatalf("remote ref valid errs = %v", errs)
	}
	errs := v.Check(cfg(t, `{"port": 80}`))
	if len(errs) == 0 {
		t.Fatal("remote ref violation should fail")
	}
}

func TestCompileFailure(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(p, []byte(`{"required": "not-an-array"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := New(p); err == nil {
		t.Fatal("invalid schema should fail compile")
	}
}

func TestLoadFailure(t *testing.T) {
	if _, err := New(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Fatal("missing schema should error")
	}
}
