// Package jsondoc provides an order-preserving JSON document tree.
// encoding/json maps lose key order, which would break schema-ordered forms
// and prefill fidelity, so we keep an explicit ordered representation.
package jsondoc

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// Member is one key/value entry of an object, in document order.
type Member struct {
	Key string
	Val *Node
}

// Node is a JSON value; exactly one field is set (Null for JSON null).
type Node struct {
	Object *[]Member
	Array  *[]*Node
	Str    *string
	Num    *json.Number // preserves the exact numeric literal ("1.0" stays "1.0")
	Bool   *bool
	Null   bool
}

// Parse reads one JSON document from r, preserving key order and numeric
// literals.
func Parse(r io.Reader) (*Node, error) {
	dec := json.NewDecoder(r)
	dec.UseNumber()
	n, err := parseValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("trailing data after JSON document")
	}
	return n, nil
}

func parseValue(dec *json.Decoder) (*Node, error) {
	t, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch v := t.(type) {
	case json.Delim:
		switch v {
		case '{':
			var members []Member
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				key, ok := kt.(string)
				if !ok {
					return nil, errors.New("invalid object key")
				}
				val, err := parseValue(dec)
				if err != nil {
					return nil, err
				}
				members = append(members, Member{Key: key, Val: val})
			}
			if _, err := dec.Token(); err != nil { // consume '}'
				return nil, err
			}
			return &Node{Object: &members}, nil
		case '[':
			var items []*Node
			for dec.More() {
				val, err := parseValue(dec)
				if err != nil {
					return nil, err
				}
				items = append(items, val)
			}
			if _, err := dec.Token(); err != nil { // consume ']'
				return nil, err
			}
			return &Node{Array: &items}, nil
		}
		return nil, fmt.Errorf("unexpected delimiter %q", string(rune(v)))
	case string:
		return &Node{Str: &v}, nil
	case json.Number:
		return &Node{Num: new(v)}, nil
	case bool:
		return &Node{Bool: &v}, nil
	case nil:
		return &Node{Null: true}, nil
	}
	return nil, fmt.Errorf("unsupported token %T", t)
}

// Write emits the document with 2-space indentation and a trailing newline.
func (n *Node) Write(w io.Writer) error {
	bw := bufio.NewWriter(w)
	if err := n.write(bw, 0); err != nil {
		return err
	}
	if err := bw.WriteByte('\n'); err != nil {
		return err
	}
	return bw.Flush()
}

func writeIndent(w *bufio.Writer, level int) {
	for range level {
		w.WriteString("  ")
	}
}

func writeJSONString(w *bufio.Writer, s string) {
	b, err := json.Marshal(s) // proper escaping
	if err != nil {
		w.WriteString(`""`)
		return
	}
	w.Write(b)
}

func (n *Node) write(w *bufio.Writer, indent int) error {
	switch {
	case n == nil:
		return errors.New("nil node")
	case n.Object != nil:
		members := *n.Object
		if len(members) == 0 {
			w.WriteString("{}")
			return nil
		}
		w.WriteString("{\n")
		for i, m := range members {
			writeIndent(w, indent+1)
			writeJSONString(w, m.Key)
			w.WriteString(": ")
			if err := m.Val.write(w, indent+1); err != nil {
				return err
			}
			if i < len(members)-1 {
				w.WriteString(",")
			}
			w.WriteString("\n")
		}
		writeIndent(w, indent)
		w.WriteString("}")
	case n.Array != nil:
		items := *n.Array
		if len(items) == 0 {
			w.WriteString("[]")
			return nil
		}
		w.WriteString("[\n")
		for i, it := range items {
			writeIndent(w, indent+1)
			if err := it.write(w, indent+1); err != nil {
				return err
			}
			if i < len(items)-1 {
				w.WriteString(",")
			}
			w.WriteString("\n")
		}
		writeIndent(w, indent)
		w.WriteString("]")
	case n.Str != nil:
		writeJSONString(w, *n.Str)
	case n.Num != nil:
		w.WriteString(string(*n.Num))
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

// Get returns the node at path; string elements index object keys, int
// elements index array items.
func (n *Node) Get(path []any) (*Node, bool) {
	cur := n
	for _, p := range path {
		switch k := p.(type) {
		case string:
			if cur.Object == nil {
				return nil, false
			}
			next, ok := memberNode(cur, k)
			if !ok {
				return nil, false
			}
			cur = next
		case int:
			if cur.Array == nil {
				return nil, false
			}
			if k < 0 || k >= len(*cur.Array) {
				return nil, false
			}
			cur = (*cur.Array)[k]
		default:
			return nil, false
		}
	}
	return cur, true
}

func memberNode(n *Node, key string) (*Node, bool) {
	for i := range *n.Object {
		if (*n.Object)[i].Key == key {
			return (*n.Object)[i].Val, true
		}
	}
	return nil, false
}

// empty reports whether n holds no value at all (zero Node) or JSON null,
// i.e. it can be replaced by a container without data loss.
func empty(n *Node) bool {
	return n == nil || (n.Object == nil && n.Array == nil && n.Str == nil &&
		n.Num == nil && n.Bool == nil && !n.Null)
}

// containerFor returns a fresh node suited for indexing by p
// (object for string keys, array for int indexes).
func containerFor(p any) *Node {
	if _, ok := p.(int); ok {
		items := []*Node{}
		return &Node{Array: &items}
	}
	members := []Member{}
	return &Node{Object: &members}
}

// Set writes v at path, auto-creating missing intermediate containers
// (object for string elements, array for int elements). Existing keys keep
// their position; new keys are appended. It panics on structural conflicts
// (indexing a scalar) or out-of-range array indexes — paths come from the
// schema-driven navigation stack, so those are programmer errors.
func (n *Node) Set(path []any, v *Node) {
	if len(path) == 0 {
		*n = *v
		return
	}
	cur := n
	for i := range len(path) {
		last := i == len(path)-1
		switch k := path[i].(type) {
		case string:
			if cur.Object == nil {
				if !empty(cur) {
					panic(fmt.Sprintf("jsondoc: Set string key %q into non-object", k))
				}
				cur.Object = containerFor(path[i]).Object
			}
			if last {
				setMember(cur, k, v)
				return
			}
			child, ok := memberNode(cur, k)
			if !ok || empty(child) {
				if !ok {
					child = containerFor(path[i+1])
					setMember(cur, k, child)
				} else if !suitable(child, path[i+1]) {
					child = containerFor(path[i+1])
					setMember(cur, k, child)
				}
			}
			cur = child
		case int:
			if cur.Array == nil {
				if !empty(cur) {
					panic(fmt.Sprintf("jsondoc: Set index %d into non-array", k))
				}
				cur.Array = containerFor(path[i]).Array
			}
			items := *cur.Array
			switch {
			case k == len(items): // append
				if last {
					*cur.Array = append(items, v)
					return
				}
				child := containerFor(path[i+1])
				*cur.Array = append(items, child)
				cur = child
			case k >= 0 && k < len(items):
				if last {
					(*cur.Array)[k] = v
					return
				}
				child := items[k]
				if empty(child) || !suitable(child, path[i+1]) {
					child = containerFor(path[i+1])
					(*cur.Array)[k] = child
				}
				cur = child
			default:
				panic(fmt.Sprintf("jsondoc: Set array index %d out of range (len %d)", k, len(items)))
			}
		default:
			panic(fmt.Sprintf("jsondoc: invalid path element %T", path[i]))
		}
	}
}

func suitable(n *Node, next any) bool {
	if _, ok := next.(int); ok {
		return n.Array != nil || empty(n)
	}
	return n.Object != nil || empty(n)
}

func setMember(n *Node, key string, v *Node) {
	for i := range *n.Object {
		if (*n.Object)[i].Key == key {
			(*n.Object)[i].Val = v
			return
		}
	}
	*n.Object = append(*n.Object, Member{Key: key, Val: v})
}

// Remove deletes the node at path, reporting whether it existed.
func (n *Node) Remove(path []any) bool {
	if len(path) == 0 {
		return false
	}
	parent, ok := n.Get(path[:len(path)-1])
	if !ok {
		return false
	}
	switch k := path[len(path)-1].(type) {
	case string:
		if parent.Object == nil {
			return false
		}
		for i := range *parent.Object {
			if (*parent.Object)[i].Key == k {
				*parent.Object = append((*parent.Object)[:i], (*parent.Object)[i+1:]...)
				return true
			}
		}
		return false
	case int:
		if parent.Array == nil || k < 0 || k >= len(*parent.Array) {
			return false
		}
		*parent.Array = append((*parent.Array)[:k], (*parent.Array)[k+1:]...)
		return true
	}
	return false
}

// Append adds v to the array at path, auto-creating intermediates and the
// array itself. It panics if the path resolves to a non-array.
func (n *Node) Append(path []any, v *Node) {
	arr, ok := n.Get(path)
	if !ok {
		n.Set(path, &Node{Array: &[]*Node{}})
		arr, _ = n.Get(path)
	} else if arr.Array == nil {
		if !empty(arr) {
			panic("jsondoc: Append into non-array")
		}
		arr.Array = &[]*Node{}
	}
	*arr.Array = append(*arr.Array, v)
}

// Insert splices v into the array at path at index idx, shifting the
// elements from idx on by one; idx clamps to [0, len]. Auto-creates
// intermediates and the array itself, like Append. It panics if the path
// resolves to a non-array.
func (n *Node) Insert(path []any, idx int, v *Node) {
	arr, ok := n.Get(path)
	if !ok {
		n.Set(path, &Node{Array: &[]*Node{}})
		arr, _ = n.Get(path)
	} else if arr.Array == nil {
		if !empty(arr) {
			panic("jsondoc: Insert into non-array")
		}
		arr.Array = &[]*Node{}
	}
	a := *arr.Array
	if idx < 0 {
		idx = 0
	}
	if idx > len(a) {
		idx = len(a)
	}
	a = append(a, nil)
	copy(a[idx+1:], a[idx:])
	a[idx] = v
	*arr.Array = a
}

// Move relocates the array element at index from so that it ends up at
// index to, keeping the other elements' relative order; from == to is a
// no-op success. It reports false when the path is missing, does not
// resolve to an array, or either index is out of range. In-place shift,
// no allocation.
func (n *Node) Move(path []any, from, to int) bool {
	parent, ok := n.Get(path)
	if !ok || parent.Array == nil {
		return false
	}
	a := *parent.Array
	if from < 0 || from >= len(a) || to < 0 || to >= len(a) {
		return false
	}
	if from == to {
		return true
	}
	el := a[from]
	if to > from {
		copy(a[from:to], a[from+1:to+1])
	} else {
		copy(a[to+1:from+1], a[to:from])
	}
	a[to] = el
	return true
}

// InsertMember adds key=v to the object at path at member index idx,
// shifting the members from idx on by one; idx clamps to [0, len]. An
// existing key keeps its position and only gets its value replaced.
func (n *Node) InsertMember(path []any, key string, idx int, v *Node) {
	obj, ok := n.Get(path)
	if !ok || obj.Object == nil {
		if ok && empty(obj) {
			obj.Object = &[]Member{}
		} else {
			panic("jsondoc: InsertMember into non-object")
		}
	}
	for i := range *obj.Object {
		if (*obj.Object)[i].Key == key {
			(*obj.Object)[i].Val = v
			return
		}
	}
	mem := *obj.Object
	if idx < 0 {
		idx = 0
	}
	if idx > len(mem) {
		idx = len(mem)
	}
	mem = append(mem, Member{})
	copy(mem[idx+1:], mem[idx:])
	mem[idx] = Member{Key: key, Val: v}
	*obj.Object = mem
}

// ToAny converts the node tree to plain Go values (map[string]any, []any,
// string, int64/float64, bool, nil) for use by the jsonschema validator.
func (n *Node) ToAny() any {
	if n == nil {
		return nil
	}
	switch {
	case n.Object != nil:
		m := make(map[string]any, len(*n.Object))
		for _, mem := range *n.Object {
			m[mem.Key] = mem.Val.ToAny()
		}
		return m
	case n.Array != nil:
		out := make([]any, len(*n.Array))
		for i, it := range *n.Array {
			out[i] = it.ToAny()
		}
		return out
	case n.Str != nil:
		return *n.Str
	case n.Num != nil:
		return numToAny(*n.Num)
	case n.Bool != nil:
		return *n.Bool
	}
	return nil
}

func numToAny(num json.Number) any {
	if iv, err := strconv.ParseInt(num.String(), 10, 64); err == nil {
		return iv
	}
	fv, err := strconv.ParseFloat(num.String(), 64)
	if err != nil {
		return num.String() // invalid literal: hand the validator a string
	}
	return fv
}

// FromAny builds a node tree from a decoded Go value. Numbers become
// json.Number with their canonical literal; map keys are sorted since map
// iteration order is undefined.
func FromAny(v any) *Node {
	switch x := v.(type) {
	case nil:
		return &Node{Null: true}
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		members := make([]Member, 0, len(keys))
		for _, k := range keys {
			members = append(members, Member{Key: k, Val: FromAny(x[k])})
		}
		return &Node{Object: &members}
	case []any:
		items := make([]*Node, 0, len(x))
		for _, it := range x {
			items = append(items, FromAny(it))
		}
		return &Node{Array: &items}
	case []Member:
		return &Node{Object: &x}
	case string:
		return &Node{Str: new(x)}
	case bool:
		return &Node{Bool: new(x)}
	case json.Number:
		return &Node{Num: new(x)}
	default: // float64, int64, ... via canonical literal
		b, err := json.Marshal(x)
		if err != nil {
			return &Node{Null: true}
		}
		return &Node{Num: new(json.Number(string(b)))}
	}
}

// Clone returns a deep copy of the node tree, preserving member order and
// exact numeric literals. Snapshots (undo history) must not alias the live
// document nor reorder it.
func (n *Node) Clone() *Node {
	if n == nil {
		return nil
	}
	c := &Node{Null: n.Null}
	if n.Object != nil {
		obj := make([]Member, len(*n.Object))
		for i, mem := range *n.Object {
			obj[i] = Member{Key: mem.Key, Val: mem.Val.Clone()}
		}
		c.Object = &obj
	}
	if n.Array != nil {
		arr := make([]*Node, len(*n.Array))
		for i, v := range *n.Array {
			arr[i] = v.Clone()
		}
		c.Array = &arr
	}
	if n.Str != nil {
		s := *n.Str
		c.Str = &s
	}
	if n.Num != nil {
		v := *n.Num
		c.Num = &v
	}
	if n.Bool != nil {
		b := *n.Bool
		c.Bool = &b
	}
	return c
}

// Summary renders a single-line description for array manager screens:
// scalars verbatim, objects show their "title" member or "{…}", arrays show
// "[N items]".
func (n *Node) Summary() string {
	if n == nil {
		return "null"
	}
	switch {
	case n.Object != nil:
		if t, ok := memberNode(n, "title"); ok && t.Str != nil {
			return *t.Str
		}
		return "{…}"
	case n.Array != nil:
		return fmt.Sprintf("[%d items]", len(*n.Array))
	case n.Str != nil:
		return *n.Str
	case n.Num != nil:
		return n.Num.String()
	case n.Bool != nil:
		if *n.Bool {
			return "true"
		}
		return "false"
	}
	return "null"
}

// ResolveJSONPointer resolves an RFC 6901 pointer (without the leading '#')
// against the document. It percent-decodes the fragment, applies ~0/~1
// unescaping, and supports array indexes ("-" and object keys by name).
func (n *Node) ResolveJSONPointer(ptr string) (*Node, bool) {
	ptr = strings.TrimPrefix(ptr, "#")
	if ptr == "" {
		return n, true
	}
	if dec, err := url.PathUnescape(ptr); err == nil {
		ptr = dec
	}
	cur := n
	if ptr == "" {
		return cur, true
	}
	parts := strings.Split(strings.TrimPrefix(ptr, "/"), "/")
	for _, raw := range parts {
		tok := strings.ReplaceAll(strings.ReplaceAll(raw, "~1", "/"), "~0", "~")
		if cur.Object != nil {
			next, ok := memberNode(cur, tok)
			if !ok {
				return nil, false
			}
			cur = next
			continue
		}
		if cur.Array != nil {
			idx, err := strconv.Atoi(tok)
			if err != nil || idx < 0 || idx >= len(*cur.Array) {
				return nil, false
			}
			cur = (*cur.Array)[idx]
			continue
		}
		return nil, false
	}
	return cur, true
}

// String renders the document to a string (2-space indent, trailing newline).
func (n *Node) String() string {
	var buf bytes.Buffer
	if err := n.Write(&buf); err != nil {
		return ""
	}
	return buf.String()
}
