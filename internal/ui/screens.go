package ui

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/charmbracelet/huh"

	"json-schema-editor/internal/jsondoc"
	"json-schema-editor/internal/smodel"
)

// section renders one object screen: its scalar fields plus a trailing
// Navigate select for children. It re-renders after every drill-down until
// the user goes back, saves, or aborts.
func (a *app) section(node *smodel.FormNode, path []any, root bool) result {
	for {
		if r, eof := a.eofResult(root); eof {
			return r
		}
		var fields []huh.Field
		var entries []entry
		var navs []navOpt

		if root && a.opts.Status != "" {
			fields = append(fields, huh.NewNote().Title(a.opts.Status))
		}
		for _, c := range node.Children {
			cpath := pathFor(path, c.Key)
			switch {
			case c.Kind == smodel.KScalar:
				e := a.scalarEntry(c, cpath)
				fields, entries = append(fields, e.field), append(entries, e)
			case c.Kind == smodel.KEnum:
				e := a.enumEntry(c, cpath)
				fields, entries = append(fields, e.field), append(entries, e)
			case c.Kind == smodel.KBoolean:
				e := a.boolEntry(c, cpath)
				fields, entries = append(fields, e.field), append(entries, e)
			case c.Kind == smodel.KRaw:
				e := a.rawEntry(c, cpath)
				fields, entries = append(fields, e.field), append(entries, e)
			case c.Kind == smodel.KArray && c.Items != nil && c.Items.Kind == smodel.KEnum:
				e := a.multiEnumEntry(c, cpath)
				fields, entries = append(fields, e.field), append(entries, e)
			default: // KObject / KArray / KOneOf → drill down
				label := "Edit " + c.Title + " ▸"
				if c.Kind == smodel.KArray {
					label = fmt.Sprintf("Edit items (%d) ▸", a.arrayLen(cpath))
				}
				navs = append(navs, navOpt{key: "c:" + itoa(len(navs)), label: label})
			}
		}

		first := "back"
		label := "Done (back)"
		if root {
			first = "save"
			label = "Save and exit"
			navs = append(navs,
				navOpt{key: first, label: label},
				navOpt{key: "raw", label: "Edit raw JSON"})
		} else {
			navs = append([]navOpt{{key: first, label: label}}, navs...)
		}

		if len(entries) == 0 {
			fields = append(fields, huh.NewNote().Title("Nothing to edit here"))
		}
		var nav string
		hopts := make([]huh.Option[string], 0, len(navs))
		for _, o := range navs {
			hopts = append(hopts, huh.NewOption(o.label, o.key).Selected(o.key == first))
		}
		sel := huh.NewSelect[string]().
			Title("Navigate").
			Value(&nav).
			Options(hopts...)
		if len(navs) <= 5 {
			sel = sel.Inline(true)
		}
		fields = append(fields, sel)

		form := huh.NewForm(huh.NewGroup(fields...).Description(helpLine))
		if err := a.runForm(form); err != nil {
			if root {
				return resAbort
			}
			return resPop
		}
		for _, e := range entries {
			e.apply(a.doc)
		}

		switch {
		case nav == "save":
			if a.saveFlow() == resSaved {
				return resSaved
			}
		case nav == "back":
			return resPop
		case nav == "raw":
			a.rawEditor(nil)
		default: // "c:<i>"
			idx := 0
			fmt.Sscanf(nav, "c:%d", &idx)
			navIdx, child := 0, (*smodel.FormNode)(nil)
			for _, c := range node.Children {
				if c.Kind == smodel.KObject || c.Kind == smodel.KArray || c.Kind == smodel.KOneOf {
					if !(c.Kind == smodel.KArray && c.Items != nil && c.Items.Kind == smodel.KEnum) {
						if navIdx == idx {
							child = c
							break
						}
						navIdx++
					}
				}
			}
			if child == nil {
				continue
			}
			cpath := pathFor(path, child.Key)
			var r result
			switch child.Kind {
			case smodel.KObject:
				r = a.section(child, cpath, false)
			case smodel.KArray:
				r = a.arrayManager(child, cpath, false)
			case smodel.KOneOf:
				r = a.variant(child, cpath)
			}
			if r == resSaved {
				return resSaved
			}
		}
	}
}

// arrayManager renders the item manager for an array node.
func (a *app) arrayManager(node *smodel.FormNode, path []any, root bool) result {
	for {
		if r, eof := a.eofResult(root); eof {
			return r
		}
		n := a.arrayLen(path)
		opts := []navOpt{{key: "add", label: "< Add item"}}
		if n > 0 {
			cur, _ := a.doc.Get(path)
			for i, it := range *cur.Array {
				opts = append(opts, navOpt{key: "i:" + itoa(i), label: fmt.Sprintf("%d: %s", i, it.Summary())})
			}
			opts = append(opts, navOpt{key: "remove", label: "Remove item…"})
		}
		if root {
			opts = append(opts, navOpt{key: "save", label: "Save and exit"})
		} else {
			opts = append(opts, navOpt{key: "back", label: "Done (back)"})
		}

		title := node.Title + " items"
		choice, ok := a.choose(title, fmt.Sprintf("%d items", n), opts, opts[0].key)
		if !ok {
			if root {
				return resAbort
			}
			return resPop
		}
		switch {
		case choice == "add":
			a.editItem(node.Items, path, -1)
		case choice == "remove":
			a.removeItem(path, n)
		case choice == "save":
			if a.saveFlow() == resSaved {
				return resSaved
			}
		case choice == "back":
			return resPop
		default: // "i:<n>"
			idx := 0
			fmt.Sscanf(choice, "i:%d", &idx)
			a.editItem(node.Items, path, idx)
		}
	}
}

// removeItem runs the two-step removal flow (pick item, confirm).
func (a *app) removeItem(path []any, n int) {
	opts := make([]navOpt, 0, n)
	cur, _ := a.doc.Get(path)
	for i, it := range *cur.Array {
		opts = append(opts, navOpt{key: itoa(i), label: fmt.Sprintf("%d: %s", i, it.Summary())})
	}
	choice, ok := a.choose("Remove item", "pick an item to remove", opts, opts[0].key)
	if !ok {
		return
	}
	idx := 0
	fmt.Sscanf(choice, "%d", &idx)
	confirm := true
	form := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().Title(fmt.Sprintf("Remove item %d?", idx)).Value(&confirm),
	).Description(helpLine))
	if err := a.runForm(form); err != nil || !confirm {
		return
	}
	a.doc.Remove(pathIdx(path, idx))
}

// editItem edits array element idx (idx < 0 appends a new one).
func (a *app) editItem(items *smodel.FormNode, parentPath []any, idx int) {
	itemPath := parentPath
	if idx < 0 {
		itemPath = pathIdx(parentPath, a.arrayLen(parentPath))
	} else {
		itemPath = pathIdx(parentPath, idx)
	}
	if items == nil {
		return
	}
	switch items.Kind {
	case smodel.KObject:
		a.section(items, itemPath, false)
	case smodel.KArray:
		a.arrayManager(items, itemPath, false)
	case smodel.KOneOf:
		a.variant(items, itemPath)
	default:
		a.editSingle(items, itemPath)
	}
}

// variant renders the oneOf/anyOf variant picker followed by the chosen
// variant's editor.
func (a *app) variant(node *smodel.FormNode, path []any) result {
	opts := make([]navOpt, 0, len(node.Variants))
	for i, v := range node.Variants {
		opts = append(opts, navOpt{key: "v:" + itoa(i), label: v.Title})
	}
	choice, ok := a.choose(node.Title, "choose a variant", opts, opts[0].key)
	if !ok {
		return resPop
	}
	idx := 0
	fmt.Sscanf(choice, "v:%d", &idx)
	v := node.Variants[idx]
	switch v.Kind {
	case smodel.KObject:
		return a.section(v, path, false)
	case smodel.KArray:
		return a.arrayManager(v, path, false)
	default:
		a.editSingle(v, path)
		return resPop
	}
}

func (a *app) editSingle(fn *smodel.FormNode, path []any) bool {
	var e entry
	switch fn.Kind {
	case smodel.KScalar:
		e = a.scalarEntry(fn, path)
	case smodel.KEnum:
		e = a.enumEntry(fn, path)
	case smodel.KBoolean:
		e = a.boolEntry(fn, path)
	default:
		e = a.rawEntry(fn, path)
	}
	form := huh.NewForm(huh.NewGroup(e.field).Description(helpLine))
	if err := a.runForm(form); err != nil {
		return false
	}
	e.apply(a.doc)
	return true
}

// rawEditor edits a whole subtree (nil path = entire document) as JSON.
func (a *app) rawEditor(path []any) {
	var cur *jsondoc.Node
	if path == nil {
		cur = a.doc
	} else {
		cur, _ = a.doc.Get(path)
	}
	prefill := ""
	if cur != nil && !cur.Null {
		if buf, err := json.Marshal(cur.ToAny()); err == nil {
			prefill = string(buf)
		}
	}
	b := prefill
	fn := &smodel.FormNode{Key: "$", Title: "Edit raw JSON"}
	txt := huh.NewText().
		Title(fn.Title).
		Description(fieldDesc(fn, "edit as JSON, submit to apply")).
		Value(&b).
		Lines(12).
		CharLimit(0).
		Validate(func(s string) error { return ValidateJSON(fn, strings.TrimSpace(s)) })
	form := huh.NewForm(huh.NewGroup(txt).Description(helpLine))
	if err := a.runForm(form); err != nil {
		return
	}
	s := strings.TrimSpace(b)
	if s == "" {
		if path != nil {
			a.doc.Remove(path)
		}
		return
	}
	n, err := jsondoc.Parse(strings.NewReader(s))
	if err != nil {
		return
	}
	if path == nil {
		*a.doc = *n
	} else {
		a.doc.Set(path, n)
	}
}

// saveFlow validates (error list → --force confirm) and writes the document
// through opts.Save. Returns resSaved when the write succeeded — or when it
// failed, after recording the error in a.saveErr (resSaved unwinds the
// screen loop; Run surfaces the error).
func (a *app) saveFlow() result {
	if a.opts.Validate != nil {
		errs := a.opts.Validate(a.doc)
		if len(errs) > 0 {
			const maxShown = 15
			shown := append([]string{}, errs...)
			if len(shown) > maxShown {
				shown = append(shown[:maxShown], fmt.Sprintf("… and %d more", len(errs)-maxShown))
			}
			desc := strings.Join(shown, "\n")
			var proceed bool
			confirm := huh.NewConfirm().
				Title("Save anyway?").
				Description(desc).
				Value(&proceed)
			if a.opts.Force {
				confirm = confirm.Affirmative("Save anyway (--force)")
			} else {
				confirm = confirm.Affirmative("OK")
				confirm = confirm.Description(desc + "\nfix the errors above, or rerun with --force")
			}
			form := huh.NewForm(huh.NewGroup(confirm).Description(helpLine))
			if err := a.runForm(form); err != nil {
				return resPop
			}
			if !a.opts.Force || !proceed {
				return resPop
			}
			a.savedWithErrors = true
		}
	}
	if a.opts.Save != nil {
		if err := a.opts.Save(a.doc); err != nil {
			a.saveErr = fmt.Errorf("write: %w", err)
		}
	}
	return resSaved
}

// variantRoot handles a oneOf root: pick → edit → save.
func (a *app) variantRoot(node *smodel.FormNode) result {
	for {
		if r, eof := a.eofResult(true); eof {
			return r
		}
		opts := make([]navOpt, 0, len(node.Variants))
		for i, v := range node.Variants {
			opts = append(opts, navOpt{key: "v:" + itoa(i), label: v.Title})
		}
		choice, ok := a.choose(node.Title, "choose a variant", opts, opts[0].key)
		if !ok {
			return resAbort
		}
		idx := 0
		fmt.Sscanf(choice, "v:%d", &idx)
		v := node.Variants[idx]
		switch v.Kind {
		case smodel.KObject:
			if r := a.section(v, nil, false); r == resSaved {
				return resSaved
			}
		case smodel.KArray:
			if r := a.arrayManager(v, nil, false); r == resSaved {
				return resSaved
			}
		default:
			a.editSingle(v, nil)
		}
		if a.saveFlow() == resSaved {
			return resSaved
		}
	}
}

// simpleRoot handles scalar/enum/boolean/raw root schemas: edit then save.
func (a *app) simpleRoot(model *smodel.FormNode) result {
	for {
		if r, eof := a.eofResult(true); eof {
			return r
		}
		if !a.editSingle(model, nil) {
			return resAbort
		}
		if a.saveFlow() == resSaved {
			return resSaved
		}
	}
}
