// Package docfmt dispatches configuration-file loading and saving to the
// format selected by the file extension.
//
// A format is a pure converter package (jsoncdoc, yamldoc, and future TOML,
// …) plus a small adapter here that implements Format and Session. The editor
// and cmd/jse only know this interface, so adding a format is:
//
//  1. a new converter package depending only on internal/jsondoc;
//  2. a Format + Session adapter in this package;
//  3. one Register call in init.
package docfmt

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/wertasy/json-schema-editor/internal/jsoncdoc"
	"github.com/wertasy/json-schema-editor/internal/jsondoc"
	"github.com/wertasy/json-schema-editor/internal/yamldoc"
)

// Format edits one configuration file format.
type Format interface {
	// Extensions lists the lower-case, dot-prefixed file extensions this
	// format handles (".yaml", ".yml", …).
	Extensions() []string
	// Load parses one document.
	Load(data []byte) (Session, error)
	// NewEmpty returns the session for a file that does not exist yet: an
	// empty object document.
	NewEmpty() Session
}

// Session is one open document: the live jsondoc tree the UI edits, the
// format-specific state needed to write it back faithfully, and any
// session-level notices.
type Session interface {
	Doc() *jsondoc.Node
	Write(w io.Writer) error
	// Notices returns session status lines (for example YAML aliases
	// expanded on load); nil when there is nothing to report.
	Notices() []string
	// SchemaLocation returns the schema location recorded in the file's own
	// convention: the top-level "$schema" member, or the YAML
	// "# yaml-language-server: $schema=…" comment.
	SchemaLocation() (string, bool)
	// SetSchemaLocation records loc in the file's own convention when no
	// non-empty location exists yet; an existing value is kept.
	SetSchemaLocation(loc string)
	// ClearSchemaLocation removes any recorded location; a fresh --pick must
	// overwrite the old value.
	ClearSchemaLocation()
}

var formats []Format

// defaultFormat handles unknown or missing extensions. JSONC is a superset of
// JSON, so it keeps the historical "everything is JSON" behavior.
var defaultFormat Format = jsoncFormat{}

func init() {
	Register(jsoncFormat{})
	Register(yamlFormat{})
}

// Register adds a format to the extension lookup.
func Register(f Format) {
	formats = append(formats, f)
}

// ForFormat returns the first registered format whose Extensions contains the
// lower-cased extension of path. No match falls back to defaultFormat; it
// never returns nil.
func ForFormat(path string) Format {
	ext := strings.ToLower(filepath.Ext(path))
	for _, f := range formats {
		for _, e := range f.Extensions() {
			if e == ext {
				return f
			}
		}
	}
	return defaultFormat
}

// --- schema location helpers ----------------------------------------------

// schemaFromDoc returns the first non-empty top-level "$schema" string.
func schemaFromDoc(doc *jsondoc.Node) (string, bool) {
	if doc == nil || doc.Object == nil {
		return "", false
	}
	for _, m := range *doc.Object {
		if m.Key != "$schema" {
			continue
		}
		if m.Val != nil && m.Val.Str != nil && *m.Val.Str != "" {
			return *m.Val.Str, true
		}
	}
	return "", false
}

// setSchemaMember sets the top-level "$schema" member to loc when the
// document has none (or an empty/null one), keeping the file's own value
// otherwise. A new member goes first, the conventional position.
func setSchemaMember(doc *jsondoc.Node, loc string) {
	if doc == nil || doc.Object == nil {
		return
	}
	for i, m := range *doc.Object {
		if m.Key != "$schema" {
			continue
		}
		if m.Val != nil && m.Val.Str != nil && *m.Val.Str != "" {
			return
		}
		(*doc.Object)[i].Val = &jsondoc.Node{Str: &loc}
		return
	}
	members := make([]jsondoc.Member, 0, len(*doc.Object)+1)
	members = append(members, jsondoc.Member{Key: "$schema", Val: &jsondoc.Node{Str: &loc}})
	members = append(members, *doc.Object...)
	*doc.Object = members
}

// clearSchemaMember removes every top-level "$schema" member.
func clearSchemaMember(doc *jsondoc.Node) {
	if doc == nil || doc.Object == nil {
		return
	}
	members := (*doc.Object)[:0]
	for _, m := range *doc.Object {
		if m.Key == "$schema" {
			continue
		}
		members = append(members, m)
	}
	*doc.Object = members
}

// --- JSONC -----------------------------------------------------------------

type jsoncFormat struct{}

func (jsoncFormat) Extensions() []string { return []string{".json", ".jsonc"} }

func (jsoncFormat) Load(data []byte) (Session, error) {
	doc, meta, err := jsoncdoc.Parse(data)
	if err != nil {
		return nil, err
	}
	return jsoncSession{doc, meta}, nil
}

func (jsoncFormat) NewEmpty() Session {
	members := []jsondoc.Member{}
	return jsoncSession{&jsondoc.Node{Object: &members}, nil}
}

type jsoncSession struct {
	doc  *jsondoc.Node
	meta *jsoncdoc.Meta
}

func (s jsoncSession) Doc() *jsondoc.Node             { return s.doc }
func (s jsoncSession) Notices() []string              { return nil }
func (s jsoncSession) SchemaLocation() (string, bool) { return schemaFromDoc(s.doc) }
func (s jsoncSession) SetSchemaLocation(loc string)   { setSchemaMember(s.doc, loc) }
func (s jsoncSession) ClearSchemaLocation()           { clearSchemaMember(s.doc) }
func (s jsoncSession) Write(w io.Writer) error {
	if s.meta == nil || (len(s.meta.ByPath) == 0 && s.meta.Tail == "") {
		// No comments: byte-for-byte the historical JSON output.
		return s.doc.Write(w)
	}
	return jsoncdoc.Write(w, s.doc, s.meta)
}

// --- YAML ------------------------------------------------------------------

type yamlFormat struct{}

func (yamlFormat) Extensions() []string { return []string{".yaml", ".yml"} }

func (yamlFormat) Load(data []byte) (Session, error) {
	doc, meta, err := yamldoc.Parse(data)
	if err != nil {
		return nil, err
	}
	return yamlSession{doc, meta}, nil
}

func (yamlFormat) NewEmpty() Session {
	members := []jsondoc.Member{}
	return yamlSession{&jsondoc.Node{Object: &members}, &yamldoc.Meta{}}
}

type yamlSession struct {
	doc  *jsondoc.Node
	meta *yamldoc.Meta
}

func (s yamlSession) Doc() *jsondoc.Node      { return s.doc }
func (s yamlSession) Write(w io.Writer) error { return yamldoc.Write(w, s.doc, s.meta) }

// SchemaLocation prefers an explicit "$schema" member (files that already use
// the JSON convention) over the yaml-language-server comment.
func (s yamlSession) SchemaLocation() (string, bool) {
	if loc, ok := schemaFromDoc(s.doc); ok {
		return loc, true
	}
	if s.meta != nil {
		return s.meta.SchemaDirective(s.doc)
	}
	return "", false
}

// SetSchemaLocation keeps YAML idiomatic: it writes a yaml-language-server
// comment and never creates or fills a "$schema" member. A non-empty
// "$schema" member already in the file stays authoritative and untouched.
func (s yamlSession) SetSchemaLocation(loc string) {
	if _, ok := schemaFromDoc(s.doc); ok {
		return
	}
	if s.meta != nil {
		s.meta.SetSchemaDirective(s.doc, loc)
	}
}

func (s yamlSession) ClearSchemaLocation() {
	clearSchemaMember(s.doc)
	if s.meta != nil {
		s.meta.ClearSchemaDirective(s.doc)
	}
}

func (s yamlSession) Notices() []string {
	if s.meta == nil || s.meta.Aliases == 0 {
		return nil
	}
	noun := "alias"
	if s.meta.Aliases > 1 {
		noun = "aliases"
	}
	return []string{fmt.Sprintf("yaml: %d %s expanded", s.meta.Aliases, noun)}
}
