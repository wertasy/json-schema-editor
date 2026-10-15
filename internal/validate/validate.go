// Package validate performs authoritative save-time validation using the
// jsonschema library. It is the backstop for everything the form layer does
// not fully enforce.
package validate

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"json-schema-editor/internal/fetch"
	"json-schema-editor/internal/jsondoc"
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
// "at /server/port: minimum value is 1024". Empty result means valid.
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
	collect(verr, &out)
	return out
}

func collect(e *jsonschema.ValidationError, out *[]string) {
	if len(e.Causes) == 0 {
		*out = append(*out, e.Error())
		return
	}
	for _, c := range e.Causes {
		collect(c, out)
	}
}
