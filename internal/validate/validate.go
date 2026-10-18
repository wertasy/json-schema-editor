// Package validate performs authoritative save-time validation using the
// jsonschema library. It is the backstop for everything the form layer does
// not fully enforce.
package validate

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/wertasy/json-schema-editor/internal/fetch"
	"github.com/wertasy/json-schema-editor/internal/jsondoc"
)

// httpLoader adapts fetch.Load to jsonschema.URLLoader so remote documents
// (the root schema and nested remote $refs) share the schema cache and the
// per-process memo instead of downloading again.
type httpLoader struct{}

func (httpLoader) Load(url string) (any, error) {
	data, err := fetch.Load(url)
	if err != nil {
		return nil, err
	}
	return jsonschema.UnmarshalJSON(bytes.NewReader(data))
}

// V is a compiled schema ready to validate configuration documents.
type V struct {
	sch *jsonschema.Schema
}

// New compiles the schema at loc (local path, http(s):// or file:// URL).
// Local schemas are registered via AddResource; remote ones are compiled
// straight from their URL (nested remote $refs flow through the loaders).
func New(loc string) (*V, error) {
	canon, err := fetch.CanonicalLoc(loc)
	if err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	c.UseLoader(jsonschema.SchemeURLLoader{
		"file":  jsonschema.FileLoader{},
		"http":  httpLoader{},
		"https": httpLoader{},
	})

	if strings.HasPrefix(canon, "http://") || strings.HasPrefix(canon, "https://") {
		sch, err := c.Compile(canon)
		if err != nil {
			return nil, err
		}
		return &V{sch: sch}, nil
	}

	data, err := fetch.Load(canon)
	if err != nil {
		return nil, err
	}
	doc, err := jsondoc.Parse(strings.NewReader(string(data)))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", canon, err)
	}
	if err := c.AddResource(canon, doc.ToAny()); err != nil {
		return nil, err
	}
	sch, err := c.Compile(canon)
	if err != nil {
		return nil, err
	}
	return &V{sch: sch}, nil
}

// Check validates cfg and returns one message per leaf error, like
// "at '$.server.port': minimum: got 80, want 1,024". Empty result means valid.
func (v *V) Check(cfg *jsondoc.Node) []string {
	err := v.sch.Validate(cfg.ToAny())
	if err == nil {
		return nil
	}
	verr, ok := err.(*jsonschema.ValidationError)
	if !ok {
		return []string{err.Error()}
	}
	var out []string
	collect(verr, cfg, &out)
	return out
}

func collect(e *jsonschema.ValidationError, doc *jsondoc.Node, out *[]string) {
	if len(e.Causes) == 0 {
		*out = append(*out, withDocPath(e, doc))
		return
	}
	for _, c := range e.Causes {
		collect(c, doc, out)
	}
}

// withDocPath rewrites the library's JSON Pointer location ("at '/a/b/0':")
// to the editor's document path ("at '$.a.b[0]':"). The path is rebuilt from
// e.InstanceLocation rather than parsed back out of the message; e.Error()
// provides the localized detail after the quoted location. The document at
// each level decides whether a segment is an array index or an object key,
// so a numeric key stays ".123" instead of turning into "[123]".
func withDocPath(e *jsonschema.ValidationError, doc *jsondoc.Node) string {
	msg := e.Error()
	const at = "at '"
	i := strings.Index(msg, at)
	if i < 0 {
		return msg // not a located leaf (e.g. the root schema error)
	}
	rest := msg[i+len(at):]
	j := strings.IndexByte(rest, '\'')
	if j < 0 {
		return msg
	}
	path := make([]any, 0, len(e.InstanceLocation))
	cur := doc
	for _, seg := range e.InstanceLocation {
		var step any = seg
		if cur != nil && cur.Array != nil {
			if n, err := strconv.Atoi(seg); err == nil {
				step = n
			}
		}
		path = append(path, step)
		if cur != nil {
			cur, _ = cur.Get([]any{step})
		}
	}
	return msg[:i] + at + jsondoc.PathString(path) + rest[j:]
}
