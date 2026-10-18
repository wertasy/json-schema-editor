package ui

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/huh"

	"github.com/wertasy/json-schema-editor/internal/icons"
	"github.com/wertasy/json-schema-editor/internal/jsondoc"
	"github.com/wertasy/json-schema-editor/internal/smodel"
)

// entry couples a huh field with the doc mutation to perform on submit.
type entry struct {
	field huh.Field
	apply func(doc *jsondoc.Node)
}

// ShouldOmit implements the empty-input omission rule: an empty value for a
// non-required field without a default removes the key.
func ShouldOmit(empty, required, hasDefault bool) bool {
	return empty && !required && !hasDefault
}

func fieldDesc(fn *smodel.FormNode, extra ...string) string {
	parts := make([]string, 0, 3)
	if fn.Desc != "" {
		parts = append(parts, fn.Desc)
	}
	parts = append(parts, extra...)
	return strings.Join(parts, " ")
}

func numStr(f float64) string {
	return strconv.FormatFloat(f, 'g', -1, 64)
}

// ValidateInput validates a raw input string against scalar constraints.
// It is the Validate closure used by every text input field.
func ValidateInput(fn *smodel.FormNode, s string) error {
	if s == "" {
		if fn.Required {
			return fmt.Errorf("required")
		}
		return nil
	}
	switch fn.JSONType {
	case "integer", "number":
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return fmt.Errorf("must be a number")
		}
		if fn.JSONType == "integer" && v != math.Trunc(v) {
			return fmt.Errorf("must be an integer")
		}
		if fn.Min != nil {
			if fn.ExclusiveMin && v <= *fn.Min {
				return fmt.Errorf("must be greater than %s", numStr(*fn.Min))
			}
			if !fn.ExclusiveMin && v < *fn.Min {
				return fmt.Errorf("must be at least %s", numStr(*fn.Min))
			}
		}
		if fn.Max != nil {
			if fn.ExclusiveMax && v >= *fn.Max {
				return fmt.Errorf("must be less than %s", numStr(*fn.Max))
			}
			if !fn.ExclusiveMax && v > *fn.Max {
				return fmt.Errorf("must be at most %s", numStr(*fn.Max))
			}
		}
		if fn.MultipleOf != nil && *fn.MultipleOf != 0 {
			m := *fn.MultipleOf
			q := v / m
			if math.Abs(q-math.Round(q)) > 1e-9 {
				return fmt.Errorf("must be a multiple of %s", numStr(m))
			}
		}
	default: // string
		n := utf8.RuneCountInString(s)
		if fn.MinLen != nil && n < *fn.MinLen {
			return fmt.Errorf("minimum length is %d", *fn.MinLen)
		}
		if fn.MaxLen != nil && n > *fn.MaxLen {
			return fmt.Errorf("maximum length is %d", *fn.MaxLen)
		}
		if fn.Pattern != "" {
			if re, err := regexp.Compile(fn.Pattern); err == nil && !re.MatchString(s) {
				return fmt.Errorf("must match %s", fn.Pattern)
			}
		}
	}
	return nil
}

// ValidateJSON validates raw-JSON textarea content (empty allowed unless
// required).
func ValidateJSON(fn *smodel.FormNode, s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		if fn.Required {
			return fmt.Errorf("required")
		}
		return nil
	}
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return fmt.Errorf("invalid JSON: %v", err)
	}
	return nil
}

// scalarText renders a current/default scalar node as input text.
func scalarText(n *jsondoc.Node) string {
	if n == nil {
		return ""
	}
	switch {
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
	return ""
}

// omitOrSet applies the empty-input omission rule and sets the value.
// An empty input removes the key only when it holds a value: a null
// placeholder (a key seeded by a, a null in the source JSON) stays null —
// there is nothing to omit, and clearing must not delete the key the user
// just created (d removes it explicitly).
func omitOrSet(doc *jsondoc.Node, path []any, empty bool, fn *smodel.FormNode, val func() *jsondoc.Node) {
	if empty {
		if ShouldOmit(empty, fn.Required, fn.Default != nil) {
			if cur, ok := doc.Get(path); ok && cur != nil && cur.Null {
				return
			}
			doc.Remove(path)
			return
		}
		if fn.Default != nil {
			doc.Set(path, fn.Default)
			return
		}
		doc.Remove(path)
		return
	}
	doc.Set(path, val())
}

// applyScalar validates a raw scalar input and writes it at path under the
// empty-input omission rule shared with the menu TUI. It returns the
// validation error without touching the doc.
func applyScalar(doc *jsondoc.Node, fn *smodel.FormNode, path []any, s string) error {
	if err := ValidateInput(fn, s); err != nil {
		return err
	}
	omitOrSet(doc, path, s == "", fn, func() *jsondoc.Node {
		if fn.JSONType == "integer" || fn.JSONType == "number" {
			return &jsondoc.Node{Num: new(json.Number(s))}
		}
		return &jsondoc.Node{Str: &s}
	})
	return nil
}

func (a *app) scalarEntry(fn *smodel.FormNode, path []any) entry {
	cur, _ := a.doc.Get(path)
	bind := scalarText(cur)
	if bind == "" && fn.Default != nil {
		bind = scalarText(fn.Default)
	}
	b := bind
	in := huh.NewInput().
		Title(fn.Title).
		Description(fieldDesc(fn)).
		Value(&b)
	if fn.Placeholder != "" {
		in = in.Placeholder(fn.Placeholder)
	}
	if fn.JSONType != "" || fn.Required || fn.Min != nil || fn.Max != nil ||
		fn.MinLen != nil || fn.MaxLen != nil || fn.Pattern != "" || fn.MultipleOf != nil {
		in = in.Validate(func(s string) error { return ValidateInput(fn, s) })
	}
	return entry{
		field: in,
		apply: func(doc *jsondoc.Node) {
			_ = applyScalar(doc, fn, path, b)
		},
	}
}

func (a *app) enumEntry(fn *smodel.FormNode, path []any) entry {
	cur, hasCur := a.doc.Get(path)
	bind := ""
	warn := ""
	if hasCur && cur != nil && !cur.Null {
		bind = smodel.EncodeValue(cur)
		found := false
		for _, o := range fn.Enum {
			if o.Encoded == bind {
				found = true
				break
			}
		}
		if !found {
			warn = icons.Warn + " current value not in options"
			bind = ""
		}
	}
	if bind == "" && fn.Default != nil {
		if enc := smodel.EncodeValue(fn.Default); enc != "" {
			bind = enc
		}
	}
	b := bind
	hopts := make([]huh.Option[string], 0, len(fn.Enum)+1)
	if !fn.Required {
		hopts = append(hopts, huh.NewOption("(none)", ""))
	}
	for _, o := range fn.Enum {
		hopts = append(hopts, huh.NewOption(o.Display, o.Encoded).Selected(o.Encoded == b))
	}
	sel := huh.NewSelect[string]().
		Title(fn.Title).
		Description(fieldDesc(fn, warn)).
		Value(&b).
		Options(hopts...)
	if len(hopts) <= 5 {
		sel = sel.Inline(true)
	}
	return entry{
		field: sel,
		apply: func(doc *jsondoc.Node) {
			omitOrSet(doc, path, b == "", fn, func() *jsondoc.Node {
				for _, o := range fn.Enum {
					if o.Encoded == b {
						return o.Val
					}
				}
				return nil
			})
		},
	}
}
func (a *app) boolEntry(fn *smodel.FormNode, path []any) entry {
	cur, _ := a.doc.Get(path)
	b := false
	if cur != nil && cur.Bool != nil {
		b = *cur.Bool
	} else if fn.Default != nil && fn.Default.Bool != nil {
		b = *fn.Default.Bool
	}
	c := huh.NewConfirm().
		Title(fn.Title).
		Description(fieldDesc(fn)).
		Value(&b)
	return entry{
		field: c,
		apply: func(doc *jsondoc.Node) {
			doc.Set(path, &jsondoc.Node{Bool: &b})
		},
	}
}

// multiEnumEntry edits arrays whose items are enums as a single multiselect.
func (a *app) multiEnumEntry(fn *smodel.FormNode, path []any) entry {
	cur, _ := a.doc.Get(path)
	var bind []string
	if cur != nil && cur.Array != nil {
		for _, it := range *cur.Array {
			bind = append(bind, smodel.EncodeValue(it))
		}
	}
	b := bind
	opts := make([]huh.Option[string], 0, len(fn.Items.Enum))
	for _, o := range fn.Items.Enum {
		sel := false
		for _, e := range b {
			if e == o.Encoded {
				sel = true
				break
			}
		}
		opts = append(opts, huh.NewOption(o.Display, o.Encoded).Selected(sel))
	}
	ms := huh.NewMultiSelect[string]().
		Title(fn.Title).
		Description(fieldDesc(fn)).
		Value(&b).
		Options(opts...)
	if fn.Required || fn.MinItems >= 1 {
		min := fn.MinItems
		if min < 1 {
			min = 1
		}
		ms = ms.Validate(func(sel []string) error {
			if len(sel) < min {
				return fmt.Errorf("select at least %d", min)
			}
			return nil
		})
	}
	return entry{
		field: ms,
		apply: func(doc *jsondoc.Node) {
			if len(b) == 0 && !fn.Required {
				doc.Remove(path)
				return
			}
			items := make([]*jsondoc.Node, 0, len(b))
			for _, enc := range b {
				for _, o := range fn.Items.Enum {
					if o.Encoded == enc {
						items = append(items, o.Val)
						break
					}
				}
			}
			doc.Set(path, &jsondoc.Node{Array: &items})
		},
	}
}

func (a *app) rawEntry(fn *smodel.FormNode, path []any) entry {
	cur, _ := a.doc.Get(path)
	prefill := ""
	if cur != nil && !cur.Null {
		if buf, err := json.Marshal(cur.ToAny()); err == nil {
			prefill = string(buf)
		}
	}
	b := prefill
	txt := huh.NewText().
		Title(fn.Title).
		Description(fieldDesc(fn, "edit as JSON")).
		Value(&b).
		Lines(5).
		CharLimit(0).
		Validate(func(s string) error { return ValidateJSON(fn, strings.TrimSpace(s)) })
	return entry{
		field: txt,
		apply: func(doc *jsondoc.Node) {
			s := strings.TrimSpace(b)
			omitOrSet(doc, path, s == "", fn, func() *jsondoc.Node {
				n, err := jsondoc.Parse(strings.NewReader(s))
				if err != nil {
					return nil
				}
				return n
			})
		},
	}
}
