// Command jse edits JSON configuration files through a TUI generated from
// a JSON Schema.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/x/term"

	"json-schema-editor/internal/catalog"
	"json-schema-editor/internal/fetch"
	"json-schema-editor/internal/jsondoc"
	"json-schema-editor/internal/smodel"
	"json-schema-editor/internal/ui"
	"json-schema-editor/internal/validate"
)

var errNoSchema = errors.New(`no schema found: provide -s or set "$schema" in config`)

const usageText = `Usage: jse [-s <schema path|http(s)://URL>] [--pick] [--force] [--accessible] <config.json>

Edit a JSON configuration file guided by a JSON Schema.

Schema source (checked in order):
  1. -s <local path or http(s)://URL>
  2. the "$schema" key at the top level of the config file;
     relative paths resolve against the config file's directory
  3. the SchemaStore catalog: a unique fileMatch hit for the file name is
     used directly; an ambiguous or missing match opens an interactive
     search over the catalog (--pick forces the search)
  4. error if none of the above applies

Remote schemas are cached under $XDG_CACHE_HOME/jse (JSE_CACHE_DIR overrides
the location): a copy from the last 24 hours is reused without a request, and
an unreachable origin falls back to the cached copy instead of failing. The
schema catalog shares that cache; JSE_CATALOG_URL overrides the catalog
endpoint.

Arguments:
  config.json   the configuration file to edit (local path); a missing
                file starts empty and is created on save

Flags:
  -s string     JSON Schema: local path or http(s):// URL
  -pick         choose the schema from the SchemaStore catalog (overrides
                -s and "$schema")
  -force        save even when schema validation fails
  -accessible   line-based prompts instead of the full-screen TUI
  -h, --help    show this help

Keys: ↑/↓ move · space toggle · enter select · tab next · esc back · ctrl+c quit

Supported form keywords: type, enum/const, numeric and string constraints,
properties/required, items/minItems/maxItems, local and remote $ref,
oneOf/anyOf/allOf. Everything else is still validated on save; unsupported
constructs fall back to a raw JSON editor.`

func main() {
	os.Exit(run())
}

func run() int {
	schemaFlag := flag.String("s", "", "schema: local path or http(s):// URL")
	force := flag.Bool("force", false, "save even when validation fails")
	accessible := flag.Bool("accessible", false, "line-based prompts")
	pick := flag.Bool("pick", false, "choose the schema from the SchemaStore catalog")
	flag.Usage = func() { fmt.Fprintln(os.Stderr, usageText) }
	flagArgs, positional := splitArgs(os.Args[1:])
	_ = flag.CommandLine.Parse(flagArgs)

	if len(positional) != 1 {
		if len(positional) > 1 {
			fmt.Fprintf(os.Stderr, "unexpected argument: %s\n", positional[1])
		} else {
			fmt.Fprintln(os.Stderr, "no config file: pass <config.json> as a positional argument")
		}
		flag.Usage()
		return 2
	}
	config := positional[0]
	if httpPrefix(config) != "" {
		fmt.Fprintln(os.Stderr, "config must be a local file path")
		return 2
	}

	// Load config; a missing file starts empty and is created on save.
	var cfg *jsondoc.Node
	loaded := false
	if data, err := os.ReadFile(config); err == nil {
		cfg, err = jsondoc.Parse(strings.NewReader(string(data)))
		if err != nil {
			fmt.Fprintf(os.Stderr, "parse config %s: %v\n", config, err)
			return 1
		}
		loaded = true
	} else if !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(os.Stderr, "read config %s: %v\n", config, err)
		return 1
	} else {
		members := []jsondoc.Member{}
		cfg = &jsondoc.Node{Object: &members}
	}

	var status []string

	// A -s location also lands in the document's top-level "$schema" field
	// (when the file has none), keeping the saved config self-describing.
	if *schemaFlag != "" {
		fillSchemaField(cfg, *schemaFlag)
	}
	// A catalog pick must overwrite an existing "$schema", so drop it first.
	if *pick {
		clearSchemaField(cfg)
	}

	schemaLoc := ""
	var resolveErr error
	if !*pick {
		schemaLoc, resolveErr = resolveSchemaLoc(*schemaFlag, config, cfg)
	}
	if resolveErr != nil && !errors.Is(resolveErr, errNoSchema) {
		fmt.Fprintln(os.Stderr, resolveErr.Error())
		return 2
	}
	if *pick || resolveErr != nil {
		loc := catalog.Loc()
		entries, cerr := catalog.Load(loc)
		if cerr != nil {
			if *pick {
				fmt.Fprintf(os.Stderr, "load schema catalog: %v\n", cerr)
				return 1
			}
			fmt.Fprintln(os.Stderr, errNoSchema.Error())
			fmt.Fprintf(os.Stderr, "schema catalog unavailable: %v\n", cerr)
			return 2
		}
		if n := fetch.Note(loc); n != "" {
			status = append(status, strings.Replace(n, "cached schema", "cached catalog", 1))
		}
		chosen := catalog.Entry{}
		if !*pick {
			abs, _ := filepath.Abs(config) // Match needs the absolute path
			if m := catalog.Match(entries, abs); len(m) == 1 {
				chosen = m[0]
			}
		}
		if chosen.URL == "" {
			if !*accessible && !term.IsTerminal(os.Stdin.Fd()) {
				if resolveErr != nil {
					fmt.Fprintln(os.Stderr, errNoSchema.Error())
				}
				fmt.Fprintln(os.Stderr, "schema selection needs a terminal; rerun with --accessible or provide -s")
				return 2
			}
			c, ok := ui.PickSchema(entries, ui.PickOptions{
				Accessible: *accessible,
				Filename:   filepath.Base(config),
				Query:      searchStem(filepath.Base(config)),
			})
			if !ok {
				fmt.Fprintln(os.Stderr, errNoSchema.Error())
				return 2
			}
			chosen = c
		}
		schemaLoc = chosen.URL
		fillSchemaField(cfg, schemaLoc)
		status = append(status, "schema from SchemaStore: "+chosen.Name)
	}

	model, _, err := smodel.Load(schemaLoc)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load schema %s: %v\n", schemaLoc, err)
		return 1
	}

	if !*accessible {
		if !term.IsTerminal(os.Stdin.Fd()) {
			fmt.Fprintln(os.Stderr, "stdin is not a terminal; rerun with --accessible for line-based prompts")
			return 1
		}
	}

	var check func(*jsondoc.Node) []string
	var loadErrors []string
	if note := fetch.Note(schemaLoc); note != "" {
		status = append(status, note) // offline: cached schema in use
	}
	if v, verr := validate.New(schemaLoc); verr != nil {
		msg := strings.ReplaceAll(verr.Error(), "\n", " ")
		status = append(status, "⚠ schema compile failed, save-time validation disabled: "+msg)
	} else {
		check = v.Check
		if loaded {
			loadErrors = v.Check(cfg)
		}
	}

	// The UI writes on every save action (TUI ctrl+s stays open; the
	// Save-and-exit row and the accessible save quit afterwards).
	outcome := ui.Run(model, cfg, ui.Options{
		Accessible: *accessible,
		Force:      *force,
		Validate:   check,
		Save: func(d *jsondoc.Node) error { return atomicWrite(config, d) },
		Status:     strings.Join(status, "\n"),
		LoadErrors: loadErrors,
	})
	if outcome.Err != nil {
		fmt.Fprintf(os.Stderr, "ui: %v\n", outcome.Err)
		return 1
	}
	if outcome.Aborted {
		if outcome.Saved {
			// A ctrl+s during the session already wrote the file; edits
			// made after that save are discarded.
			fmt.Printf("Saved to %s; aborted (changes after the last save are lost)\n", config)
		} else {
			fmt.Println("Aborted, no changes written")
		}
		return 130
	}
	if !outcome.Saved {
		fmt.Println("Aborted, no changes written")
		return 130
	}
	if outcome.WithErrors {
		fmt.Printf("Saved with validation errors (--force): %s\n", config)
	} else {
		fmt.Printf("Saved to %s\n", config)
	}
	return 0
}

func httpPrefix(p string) string {
	if strings.HasPrefix(p, "http://") || strings.HasPrefix(p, "https://") {
		return p
	}
	return ""
}

// resolveSchemaLoc implements the schema source resolution order.
func resolveSchemaLoc(sFlag, cFlag string, cfg *jsondoc.Node) (string, error) {
	if sFlag != "" {
		return sFlag, nil
	}
	if cFlag != "" && cfg != nil {
		if n, ok := cfg.Get([]any{"$schema"}); ok && n.Str != nil && *n.Str != "" {
			u := *n.Str
			if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
				return u, nil
			}
			if filepath.IsAbs(u) {
				return u, nil
			}
			return filepath.Join(filepath.Dir(cFlag), u), nil
		}
	}
	return "", errNoSchema
}

// atomicWrite writes doc to path via a same-directory temp file + rename,
// preserving the mode of an existing file.
func atomicWrite(path string, doc *jsondoc.Node) error {
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	dir, base := filepath.Split(path)
	if dir == "" {
		dir = "."
	}
	tmp, err := os.CreateTemp(dir, ".jse-*-"+base)
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after successful rename
	if err := doc.Write(tmp); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// fillSchemaField sets the top-level "$schema" member to loc when the
// document has none (or an empty one), keeping the file's own value
// otherwise. The member goes first, the conventional position.
func fillSchemaField(doc *jsondoc.Node, loc string) {
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

// clearSchemaField removes the top-level "$schema" member; a freshly chosen
// schema (-pick) must overwrite an existing value, which fillSchemaField
// never does.
func clearSchemaField(doc *jsondoc.Node) {
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

// searchStem is the file name without its extension, the initial catalog
// search term. A name with no extension stays whole (".cshrc").
func searchStem(base string) string {
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	if stem == "" {
		return base
	}
	return stem
}

// splitArgs separates flag tokens (with their values) from positional
// arguments so flags may appear before or after the config path. Only -s
// takes a value; everything else starting with "-" is passed through to the
// flag package for validation.
func splitArgs(args []string) (flags, positional []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-s" || a == "--s":
			flags = append(flags, a)
			if i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
		case strings.HasPrefix(a, "-"):
			flags = append(flags, a)
		default:
			positional = append(positional, a)
		}
	}
	return flags, positional
}
