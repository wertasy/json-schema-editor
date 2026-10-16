package jsondoc

import (
	"strings"
	"testing"
)

const roundtripSrc = `{
  "name": "alpha",
  "port": 8080,
  "ratio": 1.0,
  "big": 1e3,
  "enabled": true,
  "nothing": null,
  "nested": {
    "a/b": "slash key",
    "m~n": "tilde key",
    "名字": "unicode"
  },
  "list": [
    "x",
    42,
    {
      "inner": true
    },
    []
  ],
  "empty_obj": {},
  "empty_arr": []
}`

func TestParseWriteRoundtrip(t *testing.T) {
	n, err := Parse(strings.NewReader(roundtripSrc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	var buf strings.Builder
	if err := n.Write(&buf); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if buf.String() != roundtripSrc+"\n" {
		t.Fatalf("roundtrip mismatch:\n%q\nwant\n%q", buf.String(), roundtripSrc+"\n")
	}
	// idempotent: parse( write(parse(src)) ) == write(parse(src))
	n2, err := Parse(strings.NewReader(buf.String()))
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	var buf2 strings.Builder
	if err := n2.Write(&buf2); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if buf2.String() != buf.String() {
		t.Fatalf("not idempotent:\n%q\nvs\n%q", buf2.String(), buf.String())
	}
	// numeric literals preserved exactly
	got, ok := n.Get([]any{"ratio"})
	if !ok || got.Num == nil || got.Num.String() != "1.0" {
		t.Fatalf("numeric literal 1.0 not preserved: %+v", got)
	}
	got, _ = n.Get([]any{"big"})
	if got.Num == nil || got.Num.String() != "1e3" {
		t.Fatalf("numeric literal 1e3 not preserved: %+v", got)
	}
	// key order preserved
	keys := make([]string, len(*n.Object))
	for i, m := range *n.Object {
		keys[i] = m.Key
	}
	want := []string{"name", "port", "ratio", "big", "enabled", "nothing", "nested", "list", "empty_obj", "empty_arr"}
	if strings.Join(keys, ",") != strings.Join(want, ",") {
		t.Fatalf("key order = %v, want %v", keys, want)
	}
}

func TestGetSetRemoveAppend(t *testing.T) {
	n, err := Parse(strings.NewReader(`{"a": {"b": 1}}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := n.Get([]any{"a", "b", "c"}); ok {
		t.Fatal("Get through scalar should fail")
	}
	if _, ok := n.Get([]any{"a", "z"}); ok {
		t.Fatal("Get missing key should fail")
	}

	// Set auto-creates intermediates: string → object, int → array.
	n.Set([]any{"x", "y", 0, "z"}, FromAny("deep"))
	got, ok := n.Get([]any{"x", "y", 0, "z"})
	if !ok || got.Str == nil || *got.Str != "deep" {
		t.Fatalf("Set auto-create failed: %+v", got)
	}

	// Existing key keeps position on replace.
	n.Set([]any{"a", "b"}, FromAny(7))
	if (*n.Object)[0].Key != "a" {
		t.Fatal("replace moved key")
	}
	got, _ = n.Get([]any{"a", "b"})
	if got.Num.String() != "7" {
		t.Fatalf("replace value = %s", got.Num.String())
	}

	// Append creates and grows arrays.
	n.Append([]any{"list"}, FromAny("one"))
	n.Append([]any{"list"}, FromAny("two"))
	arr, _ := n.Get([]any{"list"})
	if len(*arr.Array) != 2 {
		t.Fatalf("append len = %d", len(*arr.Array))
	}

	// Remove middle array element and object key.
	if !n.Remove([]any{"list", 0}) {
		t.Fatal("Remove array[0] = false")
	}
	arr, _ = n.Get([]any{"list"})
	if len(*arr.Array) != 1 || (*arr.Array)[0].Str == nil || *(*arr.Array)[0].Str != "two" {
		t.Fatalf("after remove: %+v", arr.Array)
	}
	if !n.Remove([]any{"a", "b"}) {
		t.Fatal("Remove key = false")
	}
	if _, ok := n.Get([]any{"a", "b"}); ok {
		t.Fatal("removed key still present")
	}
	if n.Remove([]any{"a", "b"}) {
		t.Fatal("double remove should be false")
	}
}

func TestInsertAndInsertMember(t *testing.T) {
	n, err := Parse(strings.NewReader(`{"list": ["a", "b", "c"]}`))
	if err != nil {
		t.Fatal(err)
	}
	// splice mid-array shifts the tail
	n.Insert([]any{"list"}, 1, FromAny("x"))
	arr, _ := n.Get([]any{"list"})
	if got := summaryArr(arr); got != "a x b c" {
		t.Fatalf("insert mid = %q, want a x b c", got)
	}
	// idx == len appends; out-of-range clamps
	n.Insert([]any{"list"}, 4, FromAny("end"))
	n.Insert([]any{"list"}, 99, FromAny("clamped"))
	arr, _ = n.Get([]any{"list"})
	if got := summaryArr(arr); got != "a x b c end clamped" {
		t.Fatalf("insert tail/clamp = %q", got)
	}
	// auto-creates the array, like Append
	n.Insert([]any{"fresh"}, 0, FromAny(1))
	arr, _ = n.Get([]any{"fresh"})
	if len(*arr.Array) != 1 || (*arr.Array)[0].Num == nil {
		t.Fatalf("insert auto-create: %+v", arr)
	}

	// member splice at idx; existing key keeps position, value replaced
	n.InsertMember(nil, "top", 0, FromAny("root")) // doc root itself
	if got, _ := n.Get([]any{"top"}); got == nil || got.Str == nil || *got.Str != "root" {
		t.Fatal("InsertMember at root failed")
	}
	n.InsertMember([]any{}, "zz", 99, FromAny(1)) // clamps to append
	n.InsertMember([]any{}, "mm", 0, FromAny(2))
	obj, _ := n.Get([]any{})
	// doc so far: top, list, fresh — zz appends, mm splices in at 0
	if keys := memberKeyList(obj); keys != "mm top list fresh zz" {
		t.Fatalf("member order = %q, want mm top list fresh zz", keys)
	}
	n.InsertMember([]any{}, "top", 0, FromAny(9)) // existing key: replace in place
	if got, _ := n.Get([]any{"top"}); got.Num == nil || got.Num.String() != "9" {
		t.Fatal("existing key replace failed")
	}
	if keys := memberKeyList(obj); keys != "mm top list fresh zz" {
		t.Fatalf("existing key must not move: %q", keys)
	}
	if err := wrapPanic(func() { n.InsertMember([]any{"list"}, "k", 0, FromAny(1)) }); err == nil {
		t.Fatal("InsertMember into array should panic")
	}
}

func TestMove(t *testing.T) {
	n, err := Parse(strings.NewReader(`{"list": ["a", "b", "c", "d"], "obj": {}}`))
	if err != nil {
		t.Fatal(err)
	}
	list := func() string {
		arr, _ := n.Get([]any{"list"})
		return summaryArr(arr)
	}
	if !n.Move([]any{"list"}, 1, 2) || list() != "a c b d" {
		t.Fatalf("move 1→2 = %q, want a c b d", list())
	}
	if !n.Move([]any{"list"}, 3, 0) || list() != "d a c b" {
		t.Fatalf("move 3→0 = %q, want d a c b", list())
	}
	if !n.Move([]any{"list"}, 0, 3) || list() != "a c b d" {
		t.Fatalf("move 0→3 = %q, want a c b d", list())
	}
	if !n.Move([]any{"list"}, 2, 2) || list() != "a c b d" {
		t.Fatalf("from == to must be a no-op success, got %q", list())
	}
	for _, tc := range [][2]int{{-1, 0}, {4, 0}, {0, -1}, {0, 4}} {
		if n.Move([]any{"list"}, tc[0], tc[1]) {
			t.Fatalf("move %d→%d must fail", tc[0], tc[1])
		}
		if list() != "a c b d" {
			t.Fatalf("failed move %d→%d changed the list: %q", tc[0], tc[1], list())
		}
	}
	// empty object and missing path: false, no panic
	if n.Move([]any{"obj"}, 0, 0) || n.Move([]any{"nope"}, 0, 0) {
		t.Fatal("move on non-array / missing path must fail")
	}

	// nested array paths
	n2, err := Parse(strings.NewReader(`{"outer": {"list": ["x", "y", "z"]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !n2.Move([]any{"outer", "list"}, 0, 2) {
		t.Fatal("nested move failed")
	}
	inner, _ := n2.Get([]any{"outer", "list"})
	if got := summaryArr(inner); got != "y z x" {
		t.Fatalf("nested move = %q, want y z x", got)
	}
}

func memberKeyList(n *Node) string {
	keys := make([]string, 0, len(*n.Object))
	for i := range *n.Object {
		keys = append(keys, (*n.Object)[i].Key)
	}
	return strings.Join(keys, " ")
}

func summaryArr(n *Node) string {
	parts := make([]string, 0, len(*n.Array))
	for _, v := range *n.Array {
		switch {
		case v.Str != nil:
			parts = append(parts, *v.Str)
		case v.Num != nil:
			parts = append(parts, v.Num.String())
		default:
			parts = append(parts, "?")
		}
	}
	return strings.Join(parts, " ")
}

func wrapPanic(f func()) (err any) {
	defer func() { err = recover() }()
	f()
	return nil
}

func TestSetPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Set into scalar should panic")
		}
	}()
	n, err := Parse(strings.NewReader(`{"s": "text"}`))
	if err != nil {
		t.Fatal(err)
	}
	n.Set([]any{"s", "k"}, FromAny(1))
}

func TestSetOutOfRangePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("out-of-range index should panic")
		}
	}()
	n, err := Parse(strings.NewReader(`{"arr": [1]}`))
	if err != nil {
		t.Fatal(err)
	}
	n.Set([]any{"arr", 5}, FromAny(1))
}

func TestToFromAny(t *testing.T) {
	n, err := Parse(strings.NewReader(`{"i": 3, "f": 1.5, "s": "x", "b": false, "n": null, "a": [1, "y"]}`))
	if err != nil {
		t.Fatal(err)
	}
	v := n.ToAny()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("ToAny = %T", v)
	}
	if m["i"] != int64(3) {
		t.Fatalf("i = %#v", m["i"])
	}
	if m["f"] != 1.5 {
		t.Fatalf("f = %#v", m["f"])
	}
	back := FromAny(m)
	got, ok := back.Get([]any{"i"})
	if !ok || got.Num == nil || got.Num.String() != "3" {
		t.Fatalf("FromAny numeric literal = %+v", got)
	}
	if _, ok := back.ToAny().(map[string]any); !ok {
		t.Fatal("FromAny(map) not object")
	}
}

func TestClone(t *testing.T) {
	src := `{"b":1.0,"a":[true,null,"x"]}`
	n, err := Parse(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	want := n.String()
	c := n.Clone()
	if c.String() != want {
		t.Fatalf("Clone reordered or changed literals: %s vs %s", c.String(), n.String())
	}
	if c == n || c.Object == n.Object {
		t.Fatal("Clone aliases the original")
	}
	// Mutating the clone must not touch the original.
	c.Set([]any{"b"}, FromAny("y"))
	if n.String() != want {
		t.Fatalf("original mutated: %s", n.String())
	}
	if got, ok := n.Get([]any{"b"}); !ok || got.Num == nil || got.Num.String() != "1.0" {
		t.Fatalf("original b = %+v", got)
	}
}

func TestSummary(t *testing.T) {
	cases := []struct {
		src  string
		path []any
		want string
	}{
		{`{"a": "hello"}`, []any{"a"}, "hello"},
		{`{"a": 8080}`, []any{"a"}, "8080"},
		{`{"a": true}`, []any{"a"}, "true"},
		{`{"a": null}`, []any{"a"}, "null"},
		{`{"a": {"title": "T", "x": 1}}`, []any{"a"}, "T"},
		{`{"a": {"x": 1}}`, []any{"a"}, "{…}"},
		{`{"a": [1, 2, 3]}`, []any{"a"}, "[3 items]"},
	}
	for _, c := range cases {
		n, err := Parse(strings.NewReader(c.src))
		if err != nil {
			t.Fatal(err)
		}
		got, _ := n.Get(c.path)
		if got.Summary() != c.want {
			t.Errorf("Summary(%s) = %q, want %q", c.src, got.Summary(), c.want)
		}
	}
}

func TestResolveJSONPointer(t *testing.T) {
	n, err := Parse(strings.NewReader(`{
  "$defs": {"srv": {"type": "string"}},
  "a/b": 1,
  "m~n": 2,
  "arr": [{"k": "v"}]
}`))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		ptr  string
		ok   bool
		want string
	}{
		{"", true, "object"},
		{"/$defs/srv/type", true, "string"},
		{"/a~1b", true, "1"},
		{"/m~0n", true, "2"},
		{"/arr/0/k", true, "v"},
		{"/arr/9", false, ""},
		{"/missing", false, ""},
		{"/m%7E0n", true, "2"}, // %7E decodes to ~, then ~0 escape applies
		{"/arr/-", false, ""},
		{"/a%2Fb", false, ""}, // %2F decodes before split → becomes a separator, not a key char
	}
	for _, c := range cases {
		got, ok := n.ResolveJSONPointer(c.ptr)
		if ok != c.ok {
			t.Errorf("pointer %q ok = %v, want %v", c.ptr, ok, c.ok)
			continue
		}
		if !ok {
			continue
		}
		switch {
		case got.Object != nil:
			if c.want != "object" {
				t.Errorf("pointer %q = object, want %s", c.ptr, c.want)
			}
		case got.Str != nil:
			if *got.Str != c.want {
				t.Errorf("pointer %q = %q, want %q", c.ptr, *got.Str, c.want)
			}
		case got.Num != nil:
			if got.Num.String() != c.want {
				t.Errorf("pointer %q = %s, want %s", c.ptr, got.Num.String(), c.want)
			}
		}
	}
}

func TestParseErrors(t *testing.T) {
	for _, src := range []string{`{"a":}`, `[1,`, `{} {}`} {
		if _, err := Parse(strings.NewReader(src)); err == nil {
			t.Errorf("Parse(%q) should fail", src)
		}
	}
}
