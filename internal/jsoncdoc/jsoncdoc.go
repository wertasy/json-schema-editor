// Package jsoncdoc converts between JSON-with-comments (JSONC) and the
// jsondoc document tree.
//
// The parser accepts strict JSON plus // line comments, /* block */ comments,
// and trailing commas in objects and arrays. Comments follow the key or value
// they belong to: an edited member keeps its comments, a deleted member takes
// its comments with it, and a newly added member has none.
//
// A document without comments is written by delegating to jsondoc.Node.Write,
// so plain JSON output stays byte-for-byte identical to the historical format.
//
// This package only depends on jsondoc: it knows nothing about the editor,
// the docfmt interface, or other formats.
package jsoncdoc

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"json-schema-editor/internal/jsondoc"
)

// Meta carries the comments captured while parsing so a session can write
// them back.
type Meta struct {
	// ByPath maps a jsondoc path key (see pathKey) to the comments captured
	// for that member, array item, or the document root.
	ByPath map[string]commentSet
	// Tail holds comments after the root value, outside a root container.
	Tail string
}

// commentSet holds the comments attached to one key/value pair. Key* belong
// to the object key, Head/Line/Foot to the value (for an array item or the
// root value there is no key, so Key* stay empty). Foot of a container value
// holds the comments just inside its closing brace/bracket.
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

// Parse reads one JSONC document into a jsondoc tree and captures comments.
func Parse(data []byte) (*jsondoc.Node, *Meta, error) {
	p := &parser{data: data, meta: &Meta{ByPath: map[string]commentSet{}}}
	head, err := p.skip()
	if err != nil {
		return nil, nil, err
	}
	doc, err := p.parseValue(nil)
	if err != nil {
		return nil, nil, err
	}
	valueLine := p.lineAt(p.pos)
	tailToks, err := p.skipTokens()
	if err != nil {
		return nil, nil, err
	}
	if p.pos < len(p.data) {
		return nil, nil, errors.New("trailing data after JSON document")
	}
	same, rest := splitLine(tailToks, valueLine)
	if len(same) > 0 {
		cs := p.meta.ByPath[pathKey(nil)]
		cs.Line = joinTokens(same)
		p.meta.ByPath[pathKey(nil)] = cs
	}
	p.meta.Tail = joinTokens(rest)
	if head != "" {
		cs := p.meta.ByPath[pathKey(nil)]
		cs.Head = head
		p.meta.ByPath[pathKey(nil)] = cs
	}
	return doc, p.meta, nil
}

type parser struct {
	data []byte
	pos  int
	meta *Meta
}

// commentTok is one comment with the line it starts on, used to keep a
// comment written after a member's comma on that member's line.
type commentTok struct {
	text string
	line int
}

func (p *parser) lineAt(pos int) int {
	return bytes.Count(p.data[:pos], []byte{'\n'})
}

// skipTokens consumes whitespace and comments, returning the comments in
// order with their start lines.
func (p *parser) skipTokens() ([]commentTok, error) {
	var comments []commentTok
	for p.pos < len(p.data) {
		switch c := p.data[p.pos]; c {
		case ' ', '\t', '\r', '\n':
			p.pos++
		case '/':
			if p.pos+1 >= len(p.data) {
				return nil, fmt.Errorf("invalid character %q looking for beginning of value", c)
			}
			line := p.lineAt(p.pos)
			switch p.data[p.pos+1] {
			case '/':
				start := p.pos
				p.pos += 2
				for p.pos < len(p.data) && p.data[p.pos] != '\n' {
					p.pos++
				}
				comments = append(comments, commentTok{strings.TrimRight(string(p.data[start:p.pos]), " \t\r"), line})
			case '*':
				start := p.pos
				p.pos += 2
				for p.pos+1 < len(p.data) && !(p.data[p.pos] == '*' && p.data[p.pos+1] == '/') {
					p.pos++
				}
				if p.pos+1 >= len(p.data) {
					return nil, errors.New("unexpected end of JSON input")
				}
				p.pos += 2
				comments = append(comments, commentTok{string(p.data[start:p.pos]), line})
			default:
				return nil, fmt.Errorf("invalid character %q looking for beginning of value", c)
			}
		default:
			return comments, nil
		}
	}
	return comments, nil
}

// skip consumes whitespace and comments, returning the comments in order,
// one per line.
func (p *parser) skip() (string, error) {
	toks, err := p.skipTokens()
	if err != nil {
		return "", err
	}
	return joinTokens(toks), nil
}

func joinTokens(toks []commentTok) string {
	parts := make([]string, len(toks))
	for i, t := range toks {
		parts[i] = t.text
	}
	return strings.Join(parts, "\n")
}

// splitLine separates the tokens that start on line from the rest.
func splitLine(toks []commentTok, line int) (same, rest []commentTok) {
	for _, t := range toks {
		if t.line == line {
			same = append(same, t)
		} else {
			rest = append(rest, t)
		}
	}
	return same, rest
}

func (p *parser) unexpected() error {
	if p.pos >= len(p.data) {
		return errors.New("unexpected end of JSON input")
	}
	return fmt.Errorf("invalid character %q looking for beginning of value", p.data[p.pos])
}

func (p *parser) parseValue(path []any) (*jsondoc.Node, error) {
	if p.pos >= len(p.data) {
		return nil, p.unexpected()
	}
	switch c := p.data[p.pos]; {
	case c == '{':
		return p.parseObject(path)
	case c == '[':
		return p.parseArray(path)
	case c == '"':
		s, err := p.parseString()
		if err != nil {
			return nil, err
		}
		return &jsondoc.Node{Str: &s}, nil
	case c == '-' || (c >= '0' && c <= '9'):
		num, err := p.parseNumber()
		if err != nil {
			return nil, err
		}
		return &jsondoc.Node{Num: &num}, nil
	case c == 't' && strings.HasPrefix(string(p.data[p.pos:]), "true"):
		p.pos += 4
		b := true
		return &jsondoc.Node{Bool: &b}, nil
	case c == 'f' && strings.HasPrefix(string(p.data[p.pos:]), "false"):
		p.pos += 5
		b := false
		return &jsondoc.Node{Bool: &b}, nil
	case c == 'n' && strings.HasPrefix(string(p.data[p.pos:]), "null"):
		p.pos += 4
		return &jsondoc.Node{Null: true}, nil
	default:
		return nil, p.unexpected()
	}
}

// parseObject parses an object. A comment written after a member's comma on
// the same line stays with that member (Line); comments on later lines become
// the next key's Head. Comments after the last member become the container's
// Foot unless they sit on the value's own line.
func (p *parser) parseObject(path []any) (*jsondoc.Node, error) {
	p.pos++ // '{'
	var members []jsondoc.Member
	carry := ""
	for {
		pending, err := p.skip()
		if err != nil {
			return nil, err
		}
		pending = joinComments(carry, pending)
		carry = ""
		if p.pos >= len(p.data) {
			return nil, p.unexpected()
		}
		if p.data[p.pos] == '}' {
			p.pos++
			p.recordContainerFoot(path, pending)
			return &jsondoc.Node{Object: &members}, nil
		}
		if p.data[p.pos] != '"' {
			return nil, fmt.Errorf("invalid character %q looking for beginning of object key string", p.data[p.pos])
		}
		key, err := p.parseString()
		if err != nil {
			return nil, err
		}
		afterKey, err := p.skip()
		if err != nil {
			return nil, err
		}
		pending = joinComments(pending, afterKey)
		if p.pos >= len(p.data) || p.data[p.pos] != ':' {
			return nil, fmt.Errorf("invalid character %q after object key", p.peekByte())
		}
		p.pos++
		vhead, err := p.skip()
		if err != nil {
			return nil, err
		}
		mpath := appendPath(path, key)
		val, err := p.parseValue(mpath)
		if err != nil {
			return nil, err
		}
		valueLine := p.lineAt(p.pos)
		postToks, err := p.skipTokens()
		if err != nil {
			return nil, err
		}
		cs := commentSet{KeyHead: pending, Head: vhead}
		switch {
		case p.pos < len(p.data) && p.data[p.pos] == ',':
			cs.Line = joinTokens(postToks)
			p.pos++
			afterToks, err := p.skipTokens()
			if err != nil {
				return nil, err
			}
			same, rest := splitLine(afterToks, valueLine)
			cs.Line = joinComments(cs.Line, joinTokens(same))
			carry = joinTokens(rest)
			p.record(mpath, cs)
			members = append(members, jsondoc.Member{Key: key, Val: val})
		case p.pos < len(p.data) && p.data[p.pos] == '}':
			same, rest := splitLine(postToks, valueLine)
			cs.Line = joinTokens(same)
			p.record(mpath, cs)
			p.recordContainerFoot(path, joinTokens(rest))
			p.pos++
			members = append(members, jsondoc.Member{Key: key, Val: val})
			return &jsondoc.Node{Object: &members}, nil
		default:
			return nil, fmt.Errorf("invalid character %q after object key:value pair", p.peekByte())
		}
	}
}

// parseArray parses an array with the same comment rules as parseObject.
func (p *parser) parseArray(path []any) (*jsondoc.Node, error) {
	p.pos++ // '['
	var items []*jsondoc.Node
	carry := ""
	for {
		head, err := p.skip()
		if err != nil {
			return nil, err
		}
		head = joinComments(carry, head)
		carry = ""
		if p.pos >= len(p.data) {
			return nil, p.unexpected()
		}
		if p.data[p.pos] == ']' {
			p.pos++
			p.recordContainerFoot(path, head)
			return &jsondoc.Node{Array: &items}, nil
		}
		ipath := appendPath(path, len(items))
		val, err := p.parseValue(ipath)
		if err != nil {
			return nil, err
		}
		valueLine := p.lineAt(p.pos)
		postToks, err := p.skipTokens()
		if err != nil {
			return nil, err
		}
		switch {
		case p.pos < len(p.data) && p.data[p.pos] == ',':
			cs := commentSet{Head: head, Line: joinTokens(postToks)}
			p.pos++
			afterToks, err := p.skipTokens()
			if err != nil {
				return nil, err
			}
			same, rest := splitLine(afterToks, valueLine)
			cs.Line = joinComments(cs.Line, joinTokens(same))
			carry = joinTokens(rest)
			p.record(ipath, cs)
			items = append(items, val)
		case p.pos < len(p.data) && p.data[p.pos] == ']':
			same, rest := splitLine(postToks, valueLine)
			p.record(ipath, commentSet{Head: head, Line: joinTokens(same)})
			p.recordContainerFoot(path, joinTokens(rest))
			p.pos++
			items = append(items, val)
			return &jsondoc.Node{Array: &items}, nil
		default:
			return nil, fmt.Errorf("invalid character %q after array element", p.peekByte())
		}
	}
}

func (p *parser) peekByte() byte {
	if p.pos >= len(p.data) {
		return 0
	}
	return p.data[p.pos]
}

// record stores cs at path, preserving a container Foot already captured for
// a nested value parsed before its parent member is recorded.
func (p *parser) record(path []any, cs commentSet) {
	key := pathKey(path)
	if prev, ok := p.meta.ByPath[key]; ok && cs.Foot == "" {
		cs.Foot = prev.Foot
	}
	if cs != (commentSet{}) {
		p.meta.ByPath[key] = cs
	}
}

// recordContainerFoot stores the comments just inside path's closing brace or
// bracket, preserving the member/value comments already recorded there.
func (p *parser) recordContainerFoot(path []any, foot string) {
	if foot == "" {
		return
	}
	key := pathKey(path)
	cs := p.meta.ByPath[key]
	cs.Foot = foot
	p.meta.ByPath[key] = cs
}

func joinComments(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	return a + "\n" + b
}

func (p *parser) parseString() (string, error) {
	start := p.pos
	p.pos++ // opening quote
	for p.pos < len(p.data) {
		switch p.data[p.pos] {
		case '\\':
			p.pos += 2
		case '"':
			p.pos++
			var s string
			if err := json.Unmarshal(p.data[start:p.pos], &s); err != nil {
				return "", err
			}
			return s, nil
		default:
			p.pos++
		}
	}
	return "", errors.New("unexpected end of JSON input")
}

func (p *parser) parseNumber() (json.Number, error) {
	start := p.pos
	if p.data[p.pos] == '-' {
		p.pos++
	}
	if p.pos >= len(p.data) || p.data[p.pos] < '0' || p.data[p.pos] > '9' {
		return "", fmt.Errorf("invalid character %q in numeric literal", p.peekByte())
	}
	if p.data[p.pos] == '0' {
		p.pos++
	} else {
		for p.pos < len(p.data) && p.data[p.pos] >= '0' && p.data[p.pos] <= '9' {
			p.pos++
		}
	}
	if p.pos < len(p.data) && p.data[p.pos] == '.' {
		p.pos++
		if p.pos >= len(p.data) || p.data[p.pos] < '0' || p.data[p.pos] > '9' {
			return "", fmt.Errorf("invalid character %q after decimal point in numeric literal", p.peekByte())
		}
		for p.pos < len(p.data) && p.data[p.pos] >= '0' && p.data[p.pos] <= '9' {
			p.pos++
		}
	}
	if p.pos < len(p.data) && (p.data[p.pos] == 'e' || p.data[p.pos] == 'E') {
		p.pos++
		if p.pos < len(p.data) && (p.data[p.pos] == '+' || p.data[p.pos] == '-') {
			p.pos++
		}
		if p.pos >= len(p.data) || p.data[p.pos] < '0' || p.data[p.pos] > '9' {
			return "", fmt.Errorf("invalid character %q in exponent of numeric literal", p.peekByte())
		}
		for p.pos < len(p.data) && p.data[p.pos] >= '0' && p.data[p.pos] <= '9' {
			p.pos++
		}
	}
	return json.Number(string(p.data[start:p.pos])), nil
}

// Write emits the document. Without comments it delegates to jsondoc, keeping
// the historical plain-JSON bytes; with comments it mirrors jsondoc's
// two-space layout and re-inserts the comments.
func Write(w io.Writer, doc *jsondoc.Node, meta *Meta) error {
	if doc == nil {
		doc = &jsondoc.Node{Null: true}
	}
	if meta == nil || (len(meta.ByPath) == 0 && meta.Tail == "") {
		return doc.Write(w)
	}
	bw := bufio.NewWriter(w)
	root := meta.ByPath[pathKey(nil)]
	writeComments(bw, root.Head, 0)
	if err := writeValue(bw, doc, nil, 0, meta); err != nil {
		return err
	}
	if root.Line != "" {
		writeTrailingComment(bw, root.Line, 0)
	}
	bw.WriteByte('\n')
	writeComments(bw, meta.Tail, 0)
	return bw.Flush()
}

func lookup(meta *Meta, path []any) commentSet {
	if meta == nil {
		return commentSet{}
	}
	return meta.ByPath[pathKey(path)]
}

func writeIndent(w *bufio.Writer, level int) {
	for range level {
		w.WriteString("  ")
	}
}

// writeComments emits each comment on its own line at the given indentation.
func writeComments(w *bufio.Writer, comment string, level int) {
	if comment == "" {
		return
	}
	for _, line := range strings.Split(comment, "\n") {
		writeIndent(w, level)
		w.WriteString(strings.TrimRight(line, " \t\r"))
		w.WriteByte('\n')
	}
}

// writeTrailingComment appends a comment after a value (and after its comma,
// when there is one) on the same line; extra lines become own-line comments.
func writeTrailingComment(w *bufio.Writer, comment string, level int) {
	lines := strings.Split(comment, "\n")
	w.WriteString(" ")
	w.WriteString(strings.TrimRight(lines[0], " \t\r"))
	for _, line := range lines[1:] {
		w.WriteByte('\n')
		writeIndent(w, level)
		w.WriteString(strings.TrimRight(line, " \t\r"))
	}
}

// writeEmptyContainer emits an empty object/array, keeping comments that were
// written between its delimiters.
func writeEmptyContainer(w *bufio.Writer, open, close byte, foot string, indent int) {
	if foot == "" {
		w.WriteByte(open)
		w.WriteByte(close)
		return
	}
	w.WriteByte(open)
	w.WriteByte('\n')
	writeComments(w, foot, indent+1)
	writeIndent(w, indent)
	w.WriteByte(close)
}

func writeJSONString(w *bufio.Writer, s string) {
	b, err := json.Marshal(s)
	if err != nil {
		w.WriteString(`""`)
		return
	}
	w.Write(b)
}

func writeValue(w *bufio.Writer, n *jsondoc.Node, path []any, indent int, meta *Meta) error {
	switch {
	case n == nil:
		w.WriteString("null")
	case n.Object != nil:
		members := *n.Object
		if len(members) == 0 {
			writeEmptyContainer(w, '{', '}', lookup(meta, path).Foot, indent)
			return nil
		}
		w.WriteString("{\n")
		for i, m := range members {
			mpath := appendPath(path, m.Key)
			cs := lookup(meta, mpath)
			writeComments(w, cs.KeyHead, indent+1)
			writeComments(w, cs.Head, indent+1)
			writeIndent(w, indent+1)
			writeJSONString(w, m.Key)
			w.WriteString(": ")
			if err := writeValue(w, m.Val, mpath, indent+1, meta); err != nil {
				return err
			}
			if i < len(members)-1 {
				w.WriteString(",")
			}
			if cs.Line != "" {
				writeTrailingComment(w, cs.Line, indent+1)
			}
			w.WriteByte('\n')
		}
		writeComments(w, lookup(meta, path).Foot, indent)
		writeIndent(w, indent)
		w.WriteString("}")
	case n.Array != nil:
		items := *n.Array
		if len(items) == 0 {
			writeEmptyContainer(w, '[', ']', lookup(meta, path).Foot, indent)
			return nil
		}
		w.WriteString("[\n")
		for i, it := range items {
			ipath := appendPath(path, i)
			cs := lookup(meta, ipath)
			writeComments(w, cs.Head, indent+1)
			writeIndent(w, indent+1)
			if err := writeValue(w, it, ipath, indent+1, meta); err != nil {
				return err
			}
			if i < len(items)-1 {
				w.WriteString(",")
			}
			if cs.Line != "" {
				writeTrailingComment(w, cs.Line, indent+1)
			}
			w.WriteByte('\n')
		}
		writeComments(w, lookup(meta, path).Foot, indent)
		writeIndent(w, indent)
		w.WriteString("]")
	case n.Str != nil:
		writeJSONString(w, *n.Str)
	case n.Num != nil:
		w.WriteString(n.Num.String())
	case n.Bool != nil:
		if *n.Bool {
			w.WriteString("true")
		} else {
			w.WriteString("false")
		}
	case n.Null:
		w.WriteString("null")
	default:
		return errors.New("invalid node: no value set")
	}
	return nil
}
