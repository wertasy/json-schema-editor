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
	// Anchor carries the YAML anchor name when the node was defined with
	// "&name" ("" for plain JSON data). Alias occurrences of the same YAML
	// node share one Node pointer, so a write through any reference reaches
	// all of them; yamldoc emits the anchor at the first occurrence and
	// alias references at the later ones. JSON parsers and writers ignore
	// it (JSON documents never set it).
	Anchor string
	// Merge carries a YAML merge-key spec: the anchored mapping nodes this
	// mapping merges ("<<: *a" / "<<: [*a, *b]"), in sequence order. Only
	// the value of a "<<" member ever holds it; such a node sets no value
	// field. JSON round-trips never create it, and every consumer that
	// flattens values (ToAny, Write, Compact, CloneDetached) resolves the
	// merged members per YAML precedence: own keys win, earlier anchors win
	// over later ones.
	Merge []*Node
}

// MergeRefs lists the anchored mapping nodes merged by this mapping's "<<"
// members in document order (a member keyed "<<" whose value carries a
// Merge spec). nil when the node merges nothing or is not a mapping.
func (n *Node) MergeRefs() []*Node {
	if n == nil || n.Object == nil {
		return nil
	}
	var refs []*Node
	for _, m := range *n.Object {
		if m.Val != nil && m.Val.Merge != nil {
			refs = append(refs, m.Val.Merge...)
		}
	}
	return refs
}

// ResolvedMembers returns the mapping's own members plus the ones its merge
// keys pull in, with YAML precedence applied (own keys win, earlier anchors
// win over later ones). The member values are the document's own nodes —
// for merged keys those live in the anchor's object, so edits through them
// reach the source mapping.
func (n *Node) ResolvedMembers() []Member {
	if n == nil || n.Object == nil {
		return nil
	}
	return resolveMembers(*n.Object)
}

// resolveMembers flattens one mapping's member list for value-oriented
// consumers (JSON output, detached copies): own members keep their order,
// then the merged members follow in "<<: *a *b" order, each key at most
// once — a key the mapping defines itself, or an earlier anchor already
// provided, shadows the later ones exactly as YAML merge semantics read the
// document. The rule applies at every level of the merge chain: a mapping's
// own keys win over the keys it merges, wherever its "<<" sits in the member
// list (b2: "<<: *b1" + own name keeps b2's name, matching yaml.v3). A
// reference already being resolved (a merge cycle) contributes nothing.
func resolveMembers(members []Member) []Member {
	out := make([]Member, 0, len(members))
	seen := map[string]bool{}
	var add func(members []Member, busy map[*Node]bool)
	add = func(members []Member, busy map[*Node]bool) {
		// own members first: explicit keys win over merged ones
		for _, m := range members {
			if m.Val != nil && m.Val.Merge != nil {
				continue
			}
			if !seen[m.Key] {
				seen[m.Key] = true
				out = append(out, m)
			}
		}
		// then the merge members in order: earlier anchors win over later
		for _, m := range members {
			if m.Val == nil || m.Val.Merge == nil {
				continue
			}
			for _, ref := range m.Val.Merge {
				if ref == nil || ref.Object == nil || busy[ref] {
					continue
				}
				busy[ref] = true
				add(*ref.Object, busy)
				delete(busy, ref)
			}
		}
	}
	add(members, map[*Node]bool{})
	return out
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
		// merge members resolve inline: JSON output carries no "<<"
		members := resolveMembers(*n.Object)
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

// empty reports whether n holds no value at all (zero Node or JSON null),
// i.e. it can be replaced by a container without data loss.
func empty(n *Node) bool {
	return n == nil || n.Null || (n.Object == nil && n.Array == nil &&
		n.Str == nil && n.Num == nil && n.Bool == nil)
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
				*cur = *containerFor(path[i])
			}
			if last {
				overwriteMember(cur, k, v)
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
				*cur = *containerFor(path[i])
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
					overwriteItem(cur, k, v)
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

// overwriteMember is Set's member write at the last path segment. A current
// value carrying a YAML anchor is written through in place with its anchor
// kept: the node may be shared with alias occurrences, and swapping the
// pointer would silently detach them — every reference sees the write, as
// the YAML reference semantics demand. Plain nodes keep the historical
// pointer store.
func overwriteMember(n *Node, key string, v *Node) {
	for i := range *n.Object {
		if (*n.Object)[i].Key == key {
			if old := (*n.Object)[i].Val; old != nil && old.Anchor != "" {
				anchor := old.Anchor
				*old = *v
				old.Anchor = anchor
				return
			}
			(*n.Object)[i].Val = v
			return
		}
	}
	*n.Object = append(*n.Object, Member{Key: key, Val: v})
}

// overwriteItem is Set's array write at the last path segment, with the
// same anchored write-through as overwriteMember.
func overwriteItem(n *Node, idx int, v *Node) {
	if old := (*n.Array)[idx]; old != nil && old.Anchor != "" {
		anchor := old.Anchor
		*old = *v
		old.Anchor = anchor
		return
	}
	(*n.Array)[idx] = v
}

// Detach replaces the node at path with v even when that node is an anchored
// (possibly alias-shared) node: materializing an alias into an independent
// copy must break the reference, unlike Set which writes through to the
// shared node. It reports whether the path existed.
func (n *Node) Detach(path []any, v *Node) bool {
	if len(path) == 0 {
		*n = *v
		return true
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
				(*parent.Object)[i].Val = v
				return true
			}
		}
		return false
	case int:
		if parent.Array == nil || k < 0 || k >= len(*parent.Array) {
			return false
		}
		(*parent.Array)[k] = v
		return true
	default:
		panic(fmt.Sprintf("jsondoc: invalid path element %T", k))
	}
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
		*arr = *containerFor(path[len(path)-1])
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
		*arr = *containerFor(path[len(path)-1])
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
// Auto-creates missing intermediates and the object itself, like Append and
// Insert do for arrays; it panics if the path resolves to a non-object.
func (n *Node) InsertMember(path []any, key string, idx int, v *Node) {
	obj, ok := n.Get(path)
	if !ok {
		// the menu target was never written to the document: build the chain
		n.Set(path, &Node{Object: &[]Member{}})
		obj, _ = n.Get(path)
	} else if obj.Object == nil {
		if !empty(obj) {
			panic("jsondoc: InsertMember into non-object")
		}
		*obj = *containerFor("")
	}
	for i := range *obj.Object {
		if (*obj.Object)[i].Key == key {
			if old := (*obj.Object)[i].Val; old != nil && old.Anchor != "" {
				anchor := old.Anchor
				*old = *v
				old.Anchor = anchor
				return
			}
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
// YAML merge keys resolve along the way, so the validator sees the mapping's
// effective keys just as a YAML reader would.
func (n *Node) ToAny() any {
	return n.toAny(map[*Node]bool{})
}

func (n *Node) toAny(busy map[*Node]bool) any {
	if n == nil {
		return nil
	}
	switch {
	case n.Object != nil:
		if busy[n] {
			return nil // a merge cycle: contribute nothing
		}
		busy[n] = true
		defer delete(busy, n)
		m := make(map[string]any, len(*n.Object))
		for _, mem := range resolveMembers(*n.Object) {
			m[mem.Key] = mem.Val.toAny(busy)
		}
		return m
	case n.Array != nil:
		out := make([]any, len(*n.Array))
		for i, it := range *n.Array {
			out[i] = it.toAny(busy)
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
// document nor reorder it. YAML alias sharing is preserved within the copy:
// an anchored node and its occurrences stay one node, so a restored document
// keeps its references intact (the anchor name rides along in Node.Anchor).
func (n *Node) Clone() *Node {
	return n.clone(map[*Node]*Node{})
}

func (n *Node) clone(memo map[*Node]*Node) *Node {
	if n == nil {
		return nil
	}
	if c, ok := memo[n]; ok {
		return c
	}
	c := &Node{Anchor: n.Anchor, Null: n.Null}
	memo[n] = c // registered before the children: recursive sharing survives
	if n.Merge != nil {
		refs := make([]*Node, len(n.Merge))
		for i, ref := range n.Merge {
			refs[i] = ref.clone(memo)
		}
		c.Merge = refs
	}
	if n.Object != nil {
		obj := make([]Member, len(*n.Object))
		for i, mem := range *n.Object {
			obj[i] = Member{Key: mem.Key, Val: mem.Val.clone(memo)}
		}
		c.Object = &obj
	}
	if n.Array != nil {
		arr := make([]*Node, len(*n.Array))
		for i, v := range *n.Array {
			arr[i] = v.clone(memo)
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

// CloneDetached returns a deep copy with alias sharing dissolved: every node
// becomes its own and the anchor identity is dropped throughout, so writing
// the copy emits plain content and edits to it never reach the anchored
// original. This is how an alias occurrence is materialized (expanded).
// Merge-key members resolve into plain members on the way — a materialized
// copy carries no references at all.
func (n *Node) CloneDetached() *Node {
	if n == nil {
		return nil
	}
	c := &Node{Null: n.Null}
	if n.Object != nil {
		obj := make([]Member, 0, len(*n.Object))
		for _, mem := range resolveMembers(*n.Object) {
			obj = append(obj, Member{Key: mem.Key, Val: mem.Val.CloneDetached()})
		}
		c.Object = &obj
	}
	if n.Array != nil {
		arr := make([]*Node, len(*n.Array))
		for i, v := range *n.Array {
			arr[i] = v.CloneDetached()
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

// PathString renders a document path in the editor's JSON-path style: "$"
// for the root, object fields appended with ".", array indexes as "[N]" —
// "$.server.port", "$.mounts[0].src", "$[0]".
func PathString(path []any) string {
	var b strings.Builder
	b.WriteByte('$')
	for _, seg := range path {
		switch s := seg.(type) {
		case string:
			b.WriteByte('.')
			b.WriteString(s)
		case int:
			fmt.Fprintf(&b, "[%d]", s)
		}
	}
	return b.String()
}

// ParsePath parses the PathString form back into a path. ok is false unless
// the whole string is "$", "$.field", or "$[N]" followed by more segments.
// Keys are read up to the next "." or "[N]" index, so a key containing a dot
// or a complete index suffix does not round-trip (the display does not quote
// keys).
func ParsePath(s string) (path []any, ok bool) {
	if !strings.HasPrefix(s, "$") {
		return nil, false
	}
	for i := 1; i < len(s); {
		switch s[i] {
		case '.':
			i++
			start := i
			for i < len(s) && s[i] != '.' && !(s[i] == '[' && closesIndex(s, i) >= 0) {
				i++
			}
			if i == start {
				return nil, false // empty key
			}
			path = append(path, s[start:i])
		case '[':
			end := closesIndex(s, i)
			if end < 0 {
				return nil, false
			}
			n, err := strconv.Atoi(s[i+1 : end])
			if err != nil {
				return nil, false
			}
			path = append(path, n)
			i = end + 1
		default:
			return nil, false
		}
	}
	return path, true
}

// closesIndex returns the position of the "]" closing a "[digits]" array
// index starting at i, or -1 when s[i:] does not begin a complete index.
func closesIndex(s string, i int) int {
	if i >= len(s) || s[i] != '[' {
		return -1
	}
	j := i + 1
	for j < len(s) && s[j] >= '0' && s[j] <= '9' {
		j++
	}
	if j == i+1 || j >= len(s) || s[j] != ']' {
		return -1
	}
	return j
}

// String renders the document to a string (2-space indent, trailing newline).
func (n *Node) String() string {
	var buf bytes.Buffer
	if err := n.Write(&buf); err != nil {
		return ""
	}
	return buf.String()
}

// Compact renders the value as one-line JSON, preserving member order and
// exact numeric literals. The menu's copy shortcuts use it for non-string
// values and for the JSON side of a `"key": value` pair.
func (n *Node) Compact() string {
	var b strings.Builder
	writeCompact(&b, n)
	return b.String()
}

func writeCompact(b *strings.Builder, n *Node) {
	switch {
	case n == nil:
		b.WriteString("null")
	case n.Object != nil:
		b.WriteByte('{')
		// merges resolve inline: JSON output carries no "<<"
		for i, m := range resolveMembers(*n.Object) {
			if i > 0 {
				b.WriteByte(',')
			}
			raw, err := json.Marshal(m.Key)
			if err != nil {
				b.WriteString(`""`)
			} else {
				b.Write(raw)
			}
			b.WriteByte(':')
			writeCompact(b, m.Val)
		}
		b.WriteByte('}')
	case n.Array != nil:
		b.WriteByte('[')
		for i, it := range *n.Array {
			if i > 0 {
				b.WriteByte(',')
			}
			writeCompact(b, it)
		}
		b.WriteByte(']')
	case n.Str != nil:
		raw, err := json.Marshal(*n.Str)
		if err != nil {
			b.WriteString(`""`)
		} else {
			b.Write(raw)
		}
	case n.Num != nil:
		b.WriteString(n.Num.String())
	case n.Bool != nil:
		if *n.Bool {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case n.Null:
		b.WriteString("null")
	default:
		b.WriteString("null")
	}
}
