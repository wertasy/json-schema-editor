// Package yamldoc converts between YAML and the jsondoc document tree.
//
// YAML comments follow the key or value they belong to: an edited member
// keeps its comments, a deleted member takes its comments with it, and a
// newly added member has none. Anchors and aliases are expanded while
// parsing; the returned Meta reports how many aliases were expanded.
//
// This package only depends on jsondoc: it knows nothing about the editor,
// the docfmt interface, or other formats.
package yamldoc

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"json-schema-editor/internal/jsondoc"
)

// Meta carries the format state a session needs to write the document back
// with its comments intact.
type Meta struct {
	// ByPath maps a jsondoc path key (see pathKey) to the comments captured
	// for that member, array item, or the document root.
	ByPath map[string]commentSet
	// Aliases counts the YAML aliases expanded while parsing.
	Aliases int
}

// commentSet holds the comments attached to one key/value pair. Key* belong
// to the mapping key node, Head/Line/Foot to the value node (for an array
// item or the root value there is no key, so Key* stay empty).
type commentSet struct {
	KeyHead, KeyLine, KeyFoot string
	Head, Line, Foot          string
}

// pathKey encodes a jsondoc path as a unique map key: nil → "null",
// ["a"] → `["a"]`, [0] → `[0]`. json.Marshal keeps keys with dots apart.
func pathKey(path []any) string {
	b, err := json.Marshal(path)
	if err != nil {
		return "null"
	}
	return string(b)
}

// appendPath returns path plus one element without mutating the original.
func appendPath(path []any, next any) []any {
	out := make([]any, len(path), len(path)+1)
	copy(out, path)
	return append(out, next)
}

// schemaDirectiveRE matches the yaml-language-server schema association:
//
//	# yaml-language-server: $schema=https://example.com/schema.json
var schemaDirectiveRE = regexp.MustCompile(`^#\s*yaml-language-server:\s*\$schema=(\S+)\s*$`)

// headerPaths lists the comment regions a file header can live in: the
// document root, and the first mapping member (yaml.v3 attaches a leading
// comment to the first key when no blank line separates it).
func headerPaths(doc *jsondoc.Node) [][]any {
	paths := [][]any{nil}
	if doc != nil && doc.Object != nil && len(*doc.Object) > 0 {
		paths = append(paths, []any{(*doc.Object)[0].Key})
	}
	return paths
}

// SchemaDirective returns the schema location recorded by a
// yaml-language-server comment in the document header, if any. A leading
// comment is stored as the value Head on the document root but as KeyHead
// when yaml.v3 attaches it to the first mapping key.
func (m *Meta) SchemaDirective(doc *jsondoc.Node) (string, bool) {
	if m == nil {
		return "", false
	}
	for _, path := range headerPaths(doc) {
		cs, ok := m.ByPath[pathKey(path)]
		if !ok {
			continue
		}
		for _, head := range []string{cs.Head, cs.KeyHead} {
			for _, line := range strings.Split(head, "\n") {
				if sub := schemaDirectiveRE.FindStringSubmatch(strings.TrimSpace(line)); sub != nil {
					return sub[1], true
				}
			}
		}
	}
	return "", false
}

// SetSchemaDirective records loc as a yaml-language-server comment when the
// header has no directive yet. The line goes into the document-root comment
// set, so it stays at the top of the file.
func (m *Meta) SetSchemaDirective(doc *jsondoc.Node, loc string) {
	if m == nil || loc == "" {
		return
	}
	if _, ok := m.SchemaDirective(doc); ok {
		return
	}
	if m.ByPath == nil {
		m.ByPath = map[string]commentSet{}
	}
	key := pathKey(nil)
	cs := m.ByPath[key]
	line := "# yaml-language-server: $schema=" + loc
	if cs.Head == "" {
		cs.Head = line
	} else {
		cs.Head = line + "\n" + cs.Head
	}
	m.ByPath[key] = cs
}

// ClearSchemaDirective removes yaml-language-server directives from the
// document header (a --pick must be able to replace them).
func (m *Meta) ClearSchemaDirective(doc *jsondoc.Node) {
	if m == nil {
		return
	}
	for _, path := range headerPaths(doc) {
		key := pathKey(path)
		cs, ok := m.ByPath[key]
		if !ok {
			continue
		}
		changed := false
		for _, head := range []*string{&cs.Head, &cs.KeyHead} {
			if *head == "" {
				continue
			}
			var kept []string
			for _, line := range strings.Split(*head, "\n") {
				if schemaDirectiveRE.MatchString(strings.TrimSpace(line)) {
					changed = true
					continue
				}
				kept = append(kept, line)
			}
			*head = strings.Join(kept, "\n")
		}
		if !changed {
			continue
		}
		if cs == (commentSet{}) {
			delete(m.ByPath, key)
		} else {
			m.ByPath[key] = cs
		}
	}
}

// Parse reads one YAML document into a jsondoc tree, expanding aliases and
// capturing comments.
func Parse(data []byte) (*jsondoc.Node, *Meta, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var root yaml.Node
	if err := dec.Decode(&root); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, nil, errors.New("empty YAML document")
		}
		return nil, nil, err
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); err == nil {
		return nil, nil, errors.New("multiple YAML documents are not supported")
	} else if !errors.Is(err, io.EOF) {
		return nil, nil, err
	}

	meta := &Meta{ByPath: map[string]commentSet{}}
	doc, err := walk(&root, nil, meta)
	if err != nil {
		return nil, nil, err
	}
	return doc, meta, nil
}

func walk(n *yaml.Node, path []any, meta *Meta) (*jsondoc.Node, error) {
	switch n.Kind {
	case yaml.AliasNode:
		meta.Aliases++
		if n.Alias == nil {
			return &jsondoc.Node{Null: true}, nil
		}
		return walk(n.Alias, path, meta)
	case yaml.DocumentNode:
		if len(n.Content) == 0 {
			return &jsondoc.Node{Null: true}, nil
		}
		child := n.Content[0]
		out, err := walk(child, path, meta)
		if err != nil {
			return nil, err
		}
		cs := commentSet{
			Head: n.HeadComment,
			Line: child.LineComment,
			Foot: n.FootComment,
		}
		if cs.Head == "" {
			cs.Head = child.HeadComment
		}
		if cs.Foot == "" {
			cs.Foot = child.FootComment
		}
		if cs != (commentSet{}) {
			meta.ByPath[pathKey(path)] = cs
		}
		return out, nil
	case yaml.MappingNode:
		members := make([]jsondoc.Member, 0, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			if k.Kind == yaml.AliasNode && k.Alias != nil {
				k = k.Alias
			}
			switch {
			case k.ShortTag() == "!!merge":
				return nil, errors.New("YAML merge keys are not supported")
			case k.Kind != yaml.ScalarNode || k.ShortTag() != "!!str":
				return nil, fmt.Errorf("non-string mapping key at %s: only string keys are supported", jsondoc.PathString(path))
			}
			mpath := appendPath(path, k.Value)
			val, err := walk(v, mpath, meta)
			if err != nil {
				return nil, err
			}
			cs := commentSet{
				KeyHead: k.HeadComment,
				KeyLine: k.LineComment,
				KeyFoot: k.FootComment,
				Head:    v.HeadComment,
				Line:    v.LineComment,
				Foot:    v.FootComment,
			}
			if cs != (commentSet{}) {
				meta.ByPath[pathKey(mpath)] = cs
			}
			members = append(members, jsondoc.Member{Key: k.Value, Val: val})
		}
		return &jsondoc.Node{Object: &members}, nil
	case yaml.SequenceNode:
		items := make([]*jsondoc.Node, 0, len(n.Content))
		for i, child := range n.Content {
			ipath := appendPath(path, i)
			val, err := walk(child, ipath, meta)
			if err != nil {
				return nil, err
			}
			cs := commentSet{
				Head: child.HeadComment,
				Line: child.LineComment,
				Foot: child.FootComment,
			}
			if cs != (commentSet{}) {
				meta.ByPath[pathKey(ipath)] = cs
			}
			items = append(items, val)
		}
		return &jsondoc.Node{Array: &items}, nil
	case yaml.ScalarNode:
		return scalarNode(n)
	}
	return nil, fmt.Errorf("unsupported YAML node kind %d", n.Kind)
}

// jsonNumber matches a literal that is already valid JSON.
var jsonNumber = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)

func scalarNode(n *yaml.Node) (*jsondoc.Node, error) {
	switch tag := n.ShortTag(); tag {
	case "!!null":
		return &jsondoc.Node{Null: true}, nil
	case "!!bool":
		var b bool
		if err := n.Decode(&b); err != nil {
			return nil, err
		}
		return &jsondoc.Node{Bool: &b}, nil
	case "!!int", "!!float":
		lit, err := normalizeNum(tag, n.Value)
		if err != nil {
			return nil, err
		}
		num := json.Number(lit)
		return &jsondoc.Node{Num: &num}, nil
	default:
		// Strings, timestamps, !!binary and unknown tags become plain
		// strings. A timestamp survives as its text; a binary blob is
		// written back as a regular string (YAML may quote it).
		s := n.Value
		return &jsondoc.Node{Str: &s}, nil
	}
}

// normalizeNum turns a YAML numeric literal into a JSON numeric literal: the
// validator receives jsondoc numbers as int64/float64, so "0x1F" or "1_000"
// must not survive as text.
func normalizeNum(tag, raw string) (string, error) {
	if jsonNumber.MatchString(raw) {
		return raw, nil
	}
	clean := strings.ReplaceAll(raw, "_", "")
	switch tag {
	case "!!int":
		if i, err := strconv.ParseInt(clean, 0, 64); err == nil {
			return strconv.FormatInt(i, 10), nil
		}
		if u, err := strconv.ParseUint(clean, 0, 64); err == nil {
			return strconv.FormatUint(u, 10), nil
		}
		return "", fmt.Errorf("unsupported YAML integer %q", raw)
	case "!!float":
		mag := strings.ToLower(strings.TrimLeft(clean, "+-"))
		if mag == ".inf" || mag == ".nan" || mag == "inf" || mag == "nan" {
			return "", fmt.Errorf("unsupported YAML float %q: inf/nan are not representable in JSON", raw)
		}
		f, err := strconv.ParseFloat(clean, 64)
		if err != nil {
			return "", fmt.Errorf("unsupported YAML float %q", raw)
		}
		return strconv.FormatFloat(f, 'g', -1, 64), nil
	}
	return raw, nil
}

// Write emits the document as YAML with two-space indentation, restoring the
// comments recorded in meta. meta may be nil.
func Write(w io.Writer, doc *jsondoc.Node, meta *Meta) error {
	root := build(doc, nil, meta)
	enc := yaml.NewEncoder(w)
	enc.SetIndent(2)
	if err := enc.Encode(&yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{root}}); err != nil {
		return err
	}
	return enc.Close()
}

// build converts one jsondoc node to a YAML node. Value-node comments come
// from the comment set recorded at the node's own path; mapping-key comments
// are applied by the parent loop (keys have no jsondoc node of their own).
func build(n *jsondoc.Node, path []any, meta *Meta) *yaml.Node {
	y := &yaml.Node{}
	if n == nil {
		y.Kind, y.Tag, y.Value = yaml.ScalarNode, "!!null", "null"
	} else {
		switch {
		case n.Object != nil:
			y.Kind, y.Tag = yaml.MappingNode, "!!map"
			members := *n.Object
			y.Content = make([]*yaml.Node, 0, len(members)*2)
			for _, m := range members {
				mpath := appendPath(path, m.Key)
				k := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: m.Key}
				if meta != nil {
					if cs, ok := meta.ByPath[pathKey(mpath)]; ok {
						k.HeadComment, k.LineComment, k.FootComment = cs.KeyHead, cs.KeyLine, cs.KeyFoot
					}
				}
				y.Content = append(y.Content, k, build(m.Val, mpath, meta))
			}
		case n.Array != nil:
			y.Kind, y.Tag = yaml.SequenceNode, "!!seq"
			items := *n.Array
			y.Content = make([]*yaml.Node, 0, len(items))
			for i, it := range items {
				y.Content = append(y.Content, build(it, appendPath(path, i), meta))
			}
		case n.Str != nil:
			y.Kind, y.Tag, y.Value = yaml.ScalarNode, "!!str", *n.Str
		case n.Num != nil:
			lit := n.Num.String()
			y.Kind, y.Value = yaml.ScalarNode, lit
			y.Tag = "!!int"
			if strings.ContainsAny(lit, ".eE") {
				y.Tag = "!!float"
			}
		case n.Bool != nil:
			y.Kind, y.Tag, y.Value = yaml.ScalarNode, "!!bool", strconv.FormatBool(*n.Bool)
		default:
			y.Kind, y.Tag, y.Value = yaml.ScalarNode, "!!null", "null"
		}
	}
	if meta != nil {
		if cs, ok := meta.ByPath[pathKey(path)]; ok {
			y.HeadComment, y.LineComment, y.FootComment = cs.Head, cs.Line, cs.Foot
		}
	}
	return y
}
