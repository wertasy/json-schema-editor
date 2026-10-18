package ui

import (
	"strings"
	"testing"

	"github.com/wertasy/json-schema-editor/internal/jsondoc"
	"github.com/wertasy/json-schema-editor/internal/smodel"
)

func TestValidateInputNumbers(t *testing.T) {
	cases := []struct {
		name string
		fn   smodel.FormNode
		in   string
		want string // "" = ok
	}{
		{"integer ok", smodel.FormNode{JSONType: "integer"}, "42", ""},
		{"integer bad", smodel.FormNode{JSONType: "integer"}, "4.5", "must be an integer"},
		{"not a number", smodel.FormNode{JSONType: "number"}, "abc", "must be a number"},
		{"min ok", smodel.FormNode{JSONType: "integer", Min: new(1024.0)}, "1024", ""},
		{"min violation", smodel.FormNode{JSONType: "integer", Min: new(1024.0)}, "80", "must be at least 1024"},
		{"exclusive min", smodel.FormNode{JSONType: "number", Min: new(1.0), ExclusiveMin: true}, "1", "must be greater than 1"},
		{"max violation", smodel.FormNode{JSONType: "number", Max: new(10.0)}, "11", "must be at most 10"},
		{"exclusive max", smodel.FormNode{JSONType: "number", Max: new(10.0), ExclusiveMax: true}, "10", "must be less than 10"},
		{"multiple of ok", smodel.FormNode{JSONType: "integer", MultipleOf: new(5.0)}, "10", ""},
		{"multiple of bad", smodel.FormNode{JSONType: "integer", MultipleOf: new(5.0)}, "7", "must be a multiple of 5"},
		{"empty required", smodel.FormNode{JSONType: "string", Required: true}, "", "required"},
		{"empty optional", smodel.FormNode{JSONType: "string"}, "", ""},
	}
	for _, c := range cases {
		err := ValidateInput(&c.fn, c.in)
		got := ""
		if err != nil {
			got = err.Error()
		}
		if got != c.want && !strings.Contains(got, c.want) {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestValidateInputStrings(t *testing.T) {
	cases := []struct {
		name string
		fn   smodel.FormNode
		in   string
		want string
	}{
		{"minlen", smodel.FormNode{JSONType: "string", MinLen: new(2)}, "a", "minimum length is 2"},
		{"maxlen", smodel.FormNode{JSONType: "string", MaxLen: new(3)}, "abcd", "maximum length is 3"},
		{"pattern ok", smodel.FormNode{JSONType: "string", Pattern: "^[a-z]+$"}, "abc", ""},
		{"pattern bad", smodel.FormNode{JSONType: "string", Pattern: "^[a-z]+$"}, "ABC", "must match ^[a-z]+$"},
		{"bad pattern ignored", smodel.FormNode{JSONType: "string", Pattern: "([unclosed"}, "any", ""},
		{"unicode runes", smodel.FormNode{JSONType: "string", MaxLen: new(3)}, "名字字", ""},
	}
	for _, c := range cases {
		err := ValidateInput(&c.fn, c.in)
		got := ""
		if err != nil {
			got = err.Error()
		}
		if got != c.want && !strings.Contains(got, c.want) {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestScalarValidation(t *testing.T) {
	cases := []struct {
		empty, required, hasDefault, want bool
	}{
		{true, false, false, true},
		{true, true, false, false},
		{true, false, true, false},
		{false, false, false, false},
	}
	for _, c := range cases {
		if got := ShouldOmit(c.empty, c.required, c.hasDefault); got != c.want {
			t.Errorf("ShouldOmit(%v,%v,%v) = %v, want %v", c.empty, c.required, c.hasDefault, got, c.want)
		}
	}
}

func TestOmitOrSetSemantics(t *testing.T) {
	doc, err := jsondoc.Parse(strings.NewReader(`{"keep": 1, "drop": "x"}`))
	if err != nil {
		t.Fatal(err)
	}
	strVal := func() *jsondoc.Node { return &jsondoc.Node{Str: new("v")} }

	// empty optional without default → key removed
	omitOrSet(doc, []any{"drop"}, true, &smodel.FormNode{}, strVal)
	if _, ok := doc.Get([]any{"drop"}); ok {
		t.Fatal("drop should be removed")
	}
	// empty optional with default → default written
	fn := &smodel.FormNode{Default: jsondoc.FromAny("dflt")}
	omitOrSet(doc, []any{"keep"}, true, fn, strVal)
	got, _ := doc.Get([]any{"keep"})
	if got.Str == nil || *got.Str != "dflt" {
		t.Fatalf("default not applied: %v", got)
	}
	// non-empty → value written
	omitOrSet(doc, []any{"new"}, false, &smodel.FormNode{}, strVal)
	got, _ = doc.Get([]any{"new"})
	if got.Str == nil || *got.Str != "v" {
		t.Fatalf("value not set: %v", got)
	}
}

func TestScalarText(t *testing.T) {
	if scalarText(jsondoc.FromAny("s")) != "s" {
		t.Error("string")
	}
	if scalarText(jsondoc.FromAny(1.5)) != "1.5" {
		t.Error("number")
	}
	if scalarText(jsondoc.FromAny(true)) != "true" {
		t.Error("bool")
	}
	if scalarText(nil) != "" {
		t.Error("nil")
	}
}

func TestPathHelpers(t *testing.T) {
	base := []any{"a", 0}
	p := pathFor(base, "k")
	if len(base) != 2 || len(p) != 3 || p[2] != "k" {
		t.Fatalf("pathFor mutated base: %v %v", base, p)
	}
	i := pathIdx(p, 7)
	if i[len(i)-1] != 7 || len(p) != 3 {
		t.Fatalf("pathIdx: %v", i)
	}
}
