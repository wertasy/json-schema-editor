// Package e2e drives the built jse binary end to end: deterministic
// accessible-mode line protocols plus a pty smoke test for the TUI.
package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"

	"gopkg.in/yaml.v3"

	"github.com/wertasy/json-schema-editor/internal/icons"
	"github.com/wertasy/json-schema-editor/internal/jsondoc"
)

var jseBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "jse-e2e-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer os.RemoveAll(dir)
	jseBin = filepath.Join(dir, "jse")
	// Keep the spawned binary off the real user schema cache.
	os.Setenv("JSE_CACHE_DIR", filepath.Join(dir, "cache"))
	build := exec.Command("go", "build", "-o", jseBin, "./cmd/jse")
	build.Dir = ".."
	build.Env = append(os.Environ(), "GOFLAGS=-buildvcs=false")
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build jse: %v\n%s", err, out)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func runJSE(t *testing.T, timeout time.Duration, stdin string, args ...string) (string, string, int) {
	t.Helper()
	return runJSEIn(t, "..", timeout, stdin, args...)
}

// runJSEIn is runJSE with an explicit process working directory, for schema
// locations that resolve relative to it.
func runJSEIn(t *testing.T, dir string, timeout time.Duration, stdin string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(jseBin, args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(stdin)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatalf("run jse: %v", err)
		}
		return out.String(), errb.String(), code
	case <-time.After(timeout):
		cmd.Process.Kill()
		t.Fatalf("jse timed out after %v; stdout so far:\n%s", timeout, out.String())
		return "", "", -1
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readJSON(t *testing.T, path string) any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("parse %s: %v\n%s", path, err, data)
	}
	return v
}

func topLevelKeys(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	n, err := jsondoc.Parse(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	keys := []string{}
	for _, m := range *n.Object {
		keys = append(keys, m.Key)
	}
	return keys
}

// createScript drives a full create session against examples/server.schema.json:
// fills every section once and saves (required inputs are re-typed on each
// root rerun: huh validates the raw line, so empty cannot accept a default).
const createScript = "demo\n" + // Name
	"2\n" + // Mode → http
	"1\n" + // Navigate → Server
	"8080\nlocalhost\ny\n\n\n" + // Port, Host, Debug=y, Ratio=omit, Done
	"demo\n\n2\n" + // root rerun: Name, Mode=http, Navigate → Logging
	"2\n0\n" + // Levels: toggle info, confirm
	"2\n" + // Logging navigate → tags items
	"1\nprod\n6\n" + // mgr: add item, value, done
	"0\n1\n" + // Logging rerun: confirm levels, Done
	"demo\n\n3\n" + // root: → Mounts
	"1\ndemos/ROOT\n/var/www\n\n6\n" + // mgr: add mount {src,dst}, done
	"demo\n\n4\n" + // root: → Auth
	"2\nsecret123\n\n" + // variant Token, token, done
	"demo\n\n5\n" // root: save

func TestCreateAccessible(t *testing.T) {
	out := t.TempDir()
	outFile := filepath.Join(out, "out.json")
	stdout, _, code := runJSE(t, 60*time.Second, createScript,
		"-s", "examples/server.schema.json", outFile, "--accessible")
	if code != 0 {
		t.Fatalf("exit = %d\nstdout:\n%s", code, stdout)
	}
	if !strings.Contains(stdout, "Saved to") {
		t.Fatalf("stdout missing result line:\n%s", stdout)
	}
	want := map[string]any{
		"$schema": "examples/server.schema.json", // -s auto-fills the field
		"name":    "demo",
		"mode":    "http",
		"server": map[string]any{
			"port":  8080.0,
			"host":  "localhost",
			"debug": true,
		},
		"logging": map[string]any{
			"levels": []any{"info"},
			"tags":   []any{"prod"},
		},
		"mounts": []any{map[string]any{"src": "demos/ROOT", "dst": "/var/www"}},
		"auth":   map[string]any{"token": "secret123"},
	}
	if got := readJSON(t, outFile); !reflect.DeepEqual(got, want) {
		t.Fatalf("output mismatch:\ngot  %#v\nwant %#v", got, want)
	}
	if keys := topLevelKeys(t, outFile); strings.Join(keys, ",") != "$schema,name,mode,server,logging,mounts,auth" {
		t.Fatalf("top-level key order = %v", keys)
	}
}

func TestPrefillAccessible(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "edit.json")
	src, err := os.ReadFile("../examples/server.config.json")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, cfg, string(src))

	// Re-type required Name, replace the illegal enum value with grpc, save.
	script := "demo\n3\n5\n"
	stdout, _, code := runJSE(t, 60*time.Second, script,
		"-s", "examples/server.schema.json", cfg, "--accessible")
	if code != 0 {
		t.Fatalf("exit = %d\nstdout:\n%s", code, stdout)
	}
	if !strings.Contains(stdout, "Saved to") {
		t.Fatalf("stdout missing result line:\n%s", stdout)
	}
	got := readJSON(t, cfg).(map[string]any)
	if got["mode"] != "grpc" {
		t.Fatalf("illegal enum not replaced: mode = %v", got["mode"])
	}
	if got["extra_unknown_key"] != 42.0 {
		t.Fatalf("schema-external key lost: %v", got["extra_unknown_key"])
	}
	if got["name"] != "demo" || got["server"].(map[string]any)["port"] != 8080.0 {
		t.Fatalf("untouched values changed: %#v", got)
	}
	if keys := topLevelKeys(t, cfg); strings.Join(keys, ",") !=
		"$schema,name,mode,server,logging,mounts,extra_unknown_key" {
		t.Fatalf("key order not preserved: %v", keys)
	}
}

func TestMoveAccessible(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "move.json")
	writeFile(t, cfg, `{"$schema": "server.schema.json", "name": "demo", "server": {"port": 8080}, "logging": {"tags": ["alpha", "beta", "gamma"]}}`)
	script := "demo\n" + // Name re-typed (required)
		"2\n2\n" + // Mode → http, Navigate → Logging
		"2\n0\n" + // Levels: toggle info, confirm
		"2\n" + // Navigate → Tags items (n=3)
		"5\n2\n" + // "Move item up…" → pick 1: beta
		"8\n0\n1\n" + // items manager Done, levels confirm, Logging Done
		"demo\n\n5\n" // Name, Mode default, Save and exit
	stdout, _, code := runJSE(t, 60*time.Second, script, "-s", "examples/server.schema.json", cfg, "--accessible")
	if code != 0 || !strings.Contains(stdout, "Saved to") {
		t.Fatalf("exit=%d stdout:\n%s", code, stdout)
	}
	got := readJSON(t, cfg).(map[string]any)
	tags := got["logging"].(map[string]any)["tags"].([]any)
	if !reflect.DeepEqual(tags, []any{"beta", "alpha", "gamma"}) {
		t.Fatalf("tags after move = %#v", tags)
	}
}

func TestValidationBlock(t *testing.T) {
	dir := t.TempDir()
	schema := filepath.Join(dir, "strict.schema.json")
	cfg := filepath.Join(dir, "strict.json")
	writeFile(t, schema, `{
  "type": "object",
  "properties": {"port": {"type": "integer", "title": "Port", "minimum": 1024}},
  "required": ["port"],
  "additionalProperties": false
}`)
	writeFile(t, cfg, `{"port": 80, "rogue": 1}`)

	// empty → required error; 1 → minimum error; 8080 → accepted.
	// Save → save-time errors (rogue key) → decline; fix via raw editor; save.
	script := "\n1\n8080\n\n" + // Port attempts, navigate=save
		"n\n" + // decline save-anyway
		"8080\n2\n" + // Port, navigate → raw editor
		`{"port":8080}` + "\n" + // raw JSON replaces doc
		"8080\n1\n" // Port, navigate=save → valid
	stdout, _, code := runJSE(t, 60*time.Second, script,
		"-s", schema, cfg, "--accessible")
	if code != 0 {
		t.Fatalf("exit = %d\nstdout:\n%s", code, stdout)
	}
	if !strings.Contains(stdout, "required") {
		t.Fatalf("required error not shown:\n%s", stdout)
	}
	if !strings.Contains(stdout, "must be at least 1024") {
		t.Fatalf("minimum error not shown:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Saved to") {
		t.Fatalf("not saved:\n%s", stdout)
	}
	got := readJSON(t, cfg)
	if !reflect.DeepEqual(got, map[string]any{"port": 8080.0}) {
		t.Fatalf("final doc = %#v", got)
	}
}

func TestSchemaResolution(t *testing.T) {
	const miniSchema = `{"type":"object","properties":{"name":{"type":"string","title":"Name"}}}`

	t.Run("relative $schema", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "mini.schema.json"), miniSchema)
		cfg := filepath.Join(dir, "cfg.json")
		writeFile(t, cfg, `{"$schema": "mini.schema.json", "name": "x"}`)
		stdout, _, code := runJSE(t, 60*time.Second, "\n\n", cfg, "--accessible")
		if code != 0 || !strings.Contains(stdout, "Saved to") {
			t.Fatalf("exit=%d stdout:\n%s", code, stdout)
		}
	})

	t.Run("relative $schema falls back to cwd", func(t *testing.T) {
		dir := t.TempDir()
		work := t.TempDir()
		writeFile(t, filepath.Join(work, "mini.schema.json"), miniSchema)
		cfg := filepath.Join(dir, "cfg.json")
		writeFile(t, cfg, `{"$schema": "mini.schema.json", "name": "x"}`)
		stdout, stderr, code := runJSEIn(t, work, 60*time.Second, "\n\n", cfg, "--accessible")
		if code != 0 || !strings.Contains(stdout, "Saved to") {
			t.Fatalf("exit=%d stdout:\n%s\nstderr:\n%s", code, stdout, stderr)
		}
	})

	t.Run("url $schema", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(miniSchema))
		}))
		defer ts.Close()
		dir := t.TempDir()
		cfg := filepath.Join(dir, "cfg.json")
		writeFile(t, cfg, fmt.Sprintf(`{"$schema": %q, "name": "y"}`, ts.URL+"/mini.json"))
		stdout, _, code := runJSE(t, 60*time.Second, "\n\n", cfg, "--accessible")
		if code != 0 || !strings.Contains(stdout, "Saved to") {
			t.Fatalf("exit=%d stdout:\n%s", code, stdout)
		}
	})

	t.Run("no schema anywhere", func(t *testing.T) {
		dir := t.TempDir()
		cfg := filepath.Join(dir, "cfg.json")
		writeFile(t, cfg, `{"name": "z"}`)
		// closed port: the catalog lookup fails instantly, no network
		t.Setenv("JSE_CATALOG_URL", "http://127.0.0.1:1/catalog.json")
		_, stderr, code := runJSE(t, 10*time.Second, "", cfg, "--accessible")
		if code != 2 {
			t.Fatalf("exit = %d, want 2\nstderr:\n%s", code, stderr)
		}
		if !strings.Contains(stderr, "no schema found") {
			t.Fatalf("stderr missing message:\n%s", stderr)
		}
		if !strings.Contains(stderr, "schema catalog unavailable") {
			t.Fatalf("stderr missing catalog note:\n%s", stderr)
		}
	})
}

// TestYAMLEditingAccessible edits a comment-bearing YAML config through the
// real binary: the value changes and its comments survive the save.
func TestYAMLEditingAccessible(t *testing.T) {
	dir := t.TempDir()
	schema := filepath.Join(dir, "mini.schema.json")
	cfg := filepath.Join(dir, "config.yaml")
	writeFile(t, schema, `{"type":"object","properties":{"name":{"type":"string","title":"Name"}},"required":["name"]}`)
	writeFile(t, cfg, "# config header\nname: demo # keep me\n")

	stdout, stderr, code := runJSE(t, 60*time.Second, "newname\n\n",
		"-s", schema, cfg, "--accessible")
	if code != 0 {
		t.Fatalf("exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "Saved to") {
		t.Fatalf("stdout missing result line:\n%s", stdout)
	}
	data, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	wantDirective := "# yaml-language-server: $schema=" + schema
	if !strings.Contains(text, wantDirective) {
		t.Fatalf("yaml schema directive missing:\n%s", text)
	}
	if !strings.Contains(text, "# config header") {
		t.Fatalf("header comment lost:\n%s", text)
	}
	if !strings.Contains(text, "name: newname # keep me") {
		t.Fatalf("value or line comment lost:\n%s", text)
	}
	if strings.Contains(text, "name: demo") {
		t.Fatalf("old value survived:\n%s", text)
	}
	if strings.Contains(text, "$schema:") {
		t.Fatalf("JSON-style $schema key written into YAML:\n%s", text)
	}
	var got map[string]any
	if err := yaml.Unmarshal(data, &got); err != nil {
		t.Fatalf("saved YAML does not parse: %v\n%s", err, text)
	}
	if !reflect.DeepEqual(got, map[string]any{"name": "newname"}) {
		t.Fatalf("saved doc = %#v", got)
	}

	// The directive alone supplies the schema on the next run (no -s).
	stdout, stderr, code = runJSE(t, 60*time.Second, "newname\n\n", cfg, "--accessible")
	if code != 0 {
		t.Fatalf("directive run exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "Saved to") {
		t.Fatalf("directive run not saved:\n%s", stdout)
	}
	data, err = os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	text = string(data)
	if !strings.Contains(text, wantDirective) || !strings.Contains(text, "# config header") ||
		!strings.Contains(text, "name: newname # keep me") {
		t.Fatalf("comment/directive lost on second save:\n%s", text)
	}
}

// TestJSONCEditingAccessible proves both .jsonc and .json files go through the
// JSONC superset parser and keep their comments.
func TestJSONCEditingAccessible(t *testing.T) {
	for _, name := range []string{"config.jsonc", "config.json"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			schema := filepath.Join(dir, "mini.schema.json")
			cfg := filepath.Join(dir, name)
			writeFile(t, schema, `{"type":"object","properties":{"name":{"type":"string","title":"Name"}},"required":["name"]}`)
			writeFile(t, cfg, "// config header\n{\n  \"name\": \"demo\" // keep me\n}\n")

			stdout, stderr, code := runJSE(t, 60*time.Second, "newname\n\n",
				"-s", schema, cfg, "--accessible")
			if code != 0 {
				t.Fatalf("exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
			}
			if !strings.Contains(stdout, "Saved to") {
				t.Fatalf("stdout missing result line:\n%s", stdout)
			}
			data, err := os.ReadFile(cfg)
			if err != nil {
				t.Fatal(err)
			}
			text := string(data)
			if !strings.Contains(text, "// config header") {
				t.Fatalf("header comment lost:\n%s", text)
			}
			if !strings.Contains(text, `"name": "newname" // keep me`) {
				t.Fatalf("value or line comment lost:\n%s", text)
			}
			if strings.Contains(text, `"name": "demo"`) {
				t.Fatalf("old value survived:\n%s", text)
			}
		})
	}
}

func TestRemoteSchema(t *testing.T) {
	src, err := os.ReadFile("../examples/server.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(src)
	}))
	defer ts.Close()

	out := t.TempDir()
	outFile := filepath.Join(out, "out.json")
	stdout, _, code := runJSE(t, 60*time.Second, createScript,
		"-s", ts.URL+"/server.schema.json", outFile, "--accessible")
	if code != 0 {
		t.Fatalf("exit = %d\nstdout:\n%s", code, stdout)
	}
	if !strings.Contains(stdout, "Saved to") {
		t.Fatalf("stdout missing result line:\n%s", stdout)
	}
	got := readJSON(t, outFile)
	if got.(map[string]any)["mode"] != "http" {
		t.Fatalf("remote schema session produced wrong doc: %#v", got)
	}
	if got.(map[string]any)["$schema"] != ts.URL+"/server.schema.json" {
		t.Fatalf("$schema should record the remote URL: %#v", got.(map[string]any)["$schema"])
	}
}

func TestPtySmoke(t *testing.T) {
	out := t.TempDir()
	outFile := filepath.Join(out, "o.json")
	cmd := exec.Command(jseBin, "-s", "examples/server.schema.json", outFile)
	cmd.Dir = ".."
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	tty, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 30, Cols: 120})
	if err != nil {
		t.Skipf("no pty available: %v", err)
	}
	defer tty.Close()

	saw := make(chan string, 1)
	go func() {
		buf := make([]byte, 4096)
		var acc bytes.Buffer
		for {
			n, err := tty.Read(buf)
			if n > 0 {
				acc.Write(buf[:n])
				if strings.Contains(acc.String(), "Save and exit") {
					saw <- acc.String()
					return
				}
			}
			if err != nil {
				saw <- acc.String()
				return
			}
		}
	}()
	select {
	case frame := <-saw:
		if !strings.Contains(frame, "Save and exit") {
			t.Fatalf("form frame not rendered:\n%s", frame)
		}
	case <-time.After(15 * time.Second):
		cmd.Process.Kill()
		t.Fatal("timed out waiting for the form")
	}

	if _, err := tty.Write([]byte("\x03")); err != nil {
		t.Fatalf("send ctrl+c: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected non-zero exit after ctrl+c")
		}
		if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 130 {
			t.Fatalf("exit after ctrl+c = %v, want 130", err)
		}
	case <-time.After(15 * time.Second):
		cmd.Process.Kill()
		t.Fatal("timed out waiting for exit")
	}
	if _, err := os.Stat(outFile); !os.IsNotExist(err) {
		t.Fatalf("output file written despite abort: %v", err)
	}
}

// TestPtyIssuesUseDocumentPaths proves validator messages reach the issues
// overlay in the editor's path style, array indexes included.
func TestPtyIssuesUseDocumentPaths(t *testing.T) {
	dir := t.TempDir()
	schema := filepath.Join(dir, "strict.schema.json")
	cfg := filepath.Join(dir, "strict.json")
	writeFile(t, schema, `{"type":"object","properties":{
		"port":{"type":"integer","title":"Port","minimum":1024},
		"items":{"type":"array","title":"Items","items":{"type":"object","properties":{
			"tag":{"type":"integer","title":"Tag","minimum":1}}}}}}`)
	writeFile(t, cfg, `{"port":1,"items":[{"tag":0}]}`)

	cmd := exec.Command(jseBin, "-s", schema, cfg)
	cmd.Dir = ".."
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	tty, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 30, Cols: 120})
	if err != nil {
		t.Skipf("no pty available: %v", err)
	}
	defer tty.Close()

	frames := make(chan string, 64)
	go func() {
		buf := make([]byte, 4096)
		var acc bytes.Buffer
		for {
			n, err := tty.Read(buf)
			if n > 0 {
				acc.Write(buf[:n])
				frames <- acc.String()
			}
			if err != nil {
				close(frames)
				return
			}
		}
	}()
	waitFrame := func(what string, offset int, want ...string) (string, int) {
		t.Helper()
		deadline := time.After(15 * time.Second)
		for {
			select {
			case frame, ok := <-frames:
				if !ok {
					t.Fatalf("pty closed while waiting for %s", what)
				}
				ready := true
				for _, w := range want {
					if !strings.Contains(frame[offset:], w) {
						ready = false
						break
					}
				}
				if ready {
					return frame, len(frame)
				}
			case <-deadline:
				t.Fatalf("timed out waiting for %s", what)
			}
		}
	}

	_, off := waitFrame("root issues row", 0, "Save and exit", "validation error")
	tty.Write([]byte("\r")) // the issues row is first at the root
	_, off = waitFrame("issues overlay", off, "at '$.port'", "at '$.items[0].tag'")
	tty.Write([]byte("\n")) // ctrl+j: move down
	_, off = waitFrame("ctrl+j moves down", off, "> at '$.items[0].tag'")
	tty.Write([]byte("\x0b")) // ctrl+k: move up
	waitFrame("ctrl+k moves up", off, "> at '$.port'")
	tty.Write([]byte("\x03"))

	waitErr := cmd.Wait()
	if ee, ok := waitErr.(*exec.ExitError); !ok || ee.ExitCode() != 130 {
		t.Fatalf("exit after ctrl+c = %v, want 130", waitErr)
	}
}

// TestPtyExternalRawEditor drives the root <Edit raw JSON> row through
// $EDITOR: the editor receives the current document, rewrites it, and ctrl+s
// persists the result.
func TestPtyExternalRawEditor(t *testing.T) {
	dir := t.TempDir()
	schema := filepath.Join(dir, "mini.schema.json")
	cfg := filepath.Join(dir, "o.json")
	editor := filepath.Join(dir, "editor.sh")
	writeFile(t, schema, `{"type":"object","properties":{"name":{"type":"string","title":"Name"}}}`)
	writeFile(t, cfg, `{"name":"z"}`)
	writeFile(t, editor, "#!/bin/sh\n"+
		"grep -q '\"name\": \"z\"' \"$1\" || exit 3\n"+
		"printf '%s\\n' '{\"name\":\"from-editor\"}' > \"$1\"\n")
	if err := os.Chmod(editor, 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(jseBin, "-s", schema, cfg)
	cmd.Dir = ".."
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "EDITOR="+editor)
	tty, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 30, Cols: 120})
	if err != nil {
		t.Skipf("no pty available: %v", err)
	}
	defer tty.Close()

	frames := make(chan string, 64)
	go func() {
		buf := make([]byte, 4096)
		var acc bytes.Buffer
		for {
			n, err := tty.Read(buf)
			if n > 0 {
				acc.Write(buf[:n])
				frames <- acc.String()
			}
			if err != nil {
				close(frames)
				return
			}
		}
	}()
	var lastFrame string
	waitFrame := func(what string, offset int, want ...string) (string, int) {
		t.Helper()
		deadline := time.After(15 * time.Second)
		for {
			select {
			case frame, ok := <-frames:
				if !ok {
					t.Fatalf("pty closed while waiting for %s; last output:\n%s", what, lastFrame)
				}
				lastFrame = frame
				ready := true
				for _, w := range want {
					if !strings.Contains(frame[offset:], w) {
						ready = false
						break
					}
				}
				if ready {
					return frame, len(frame)
				}
			case <-deadline:
				t.Fatalf("timed out waiting for %s; last output:\n%s", what, lastFrame)
			}
		}
	}

	_, off := waitFrame("root menu", 0, "Save and exit")
	tty.Write([]byte("G\r")) // jump to the last row: <Edit raw JSON>
	_, off = waitFrame("editor applied", off, "Raw JSON edited")
	tty.Write([]byte("\x13")) // ctrl+s save
	_, _ = waitFrame("saved", off, "Saved")
	tty.Write([]byte("\x03"))

	waitErr := cmd.Wait()
	if ee, ok := waitErr.(*exec.ExitError); !ok || ee.ExitCode() != 130 {
		t.Fatalf("exit after ctrl+c = %v, want 130", waitErr)
	}
	if got := readJSON(t, cfg).(map[string]any)["name"]; got != "from-editor" {
		t.Fatalf("saved name = %#v, want from-editor", got)
	}
}

// TestPtyExternalRawYAMLEditor: for a YAML file the root raw editor opens
// $EDITOR on YAML text (not JSON) and parses the result back as YAML.
func TestPtyExternalRawYAMLEditor(t *testing.T) {
	dir := t.TempDir()
	schema := filepath.Join(dir, "mini.schema.json")
	cfg := filepath.Join(dir, "o.yaml")
	editor := filepath.Join(dir, "editor.sh")
	writeFile(t, schema, `{"type":"object","properties":{"name":{"type":"string","title":"Name"}}}`)
	writeFile(t, cfg, "name: demo\n")
	writeFile(t, editor, "#!/bin/sh\n"+
		`case "$1" in *.yaml) ;; *) exit 5;; esac`+"\n"+
		`grep -q '^name: demo$' "$1" || exit 3`+"\n"+
		`grep -q '"name"' "$1" && exit 4`+"\n"+
		`printf '# edited comment\nname: edited\n' > "$1"`+"\n")
	if err := os.Chmod(editor, 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(jseBin, "-s", schema, cfg)
	cmd.Dir = ".."
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "EDITOR="+editor)
	tty, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 30, Cols: 120})
	if err != nil {
		t.Skipf("no pty available: %v", err)
	}
	defer tty.Close()

	frames := make(chan string, 64)
	go func() {
		buf := make([]byte, 4096)
		var acc bytes.Buffer
		for {
			n, err := tty.Read(buf)
			if n > 0 {
				acc.Write(buf[:n])
				frames <- acc.String()
			}
			if err != nil {
				close(frames)
				return
			}
		}
	}()
	var lastFrame string
	waitFrame := func(what string, offset int, want ...string) (string, int) {
		t.Helper()
		deadline := time.After(15 * time.Second)
		for {
			select {
			case frame, ok := <-frames:
				if !ok {
					t.Fatalf("pty closed while waiting for %s; last output:\n%s", what, lastFrame)
				}
				lastFrame = frame
				ready := true
				for _, w := range want {
					if !strings.Contains(frame[offset:], w) {
						ready = false
						break
					}
				}
				if ready {
					return frame, len(frame)
				}
			case <-deadline:
				t.Fatalf("timed out waiting for %s; last output:\n%s", what, lastFrame)
			}
		}
	}

	_, off := waitFrame("root menu", 0, "Save and exit", "Edit raw YAML")
	tty.Write([]byte("G\r")) // jump to the last row: <Edit raw YAML>
	_, off = waitFrame("YAML editor applied", off, "Raw YAML edited")
	tty.Write([]byte("\x13")) // ctrl+s save
	_, _ = waitFrame("saved", off, "Saved")
	tty.Write([]byte("\x03"))

	waitErr := cmd.Wait()
	if ee, ok := waitErr.(*exec.ExitError); !ok || ee.ExitCode() != 130 {
		t.Fatalf("exit after ctrl+c = %v, want 130", waitErr)
	}
	data, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "# edited comment") ||
		!strings.Contains(string(data), "name: edited") ||
		strings.Contains(string(data), `"name"`) {
		t.Fatalf("saved config did not keep the raw YAML (and its comment):\n%s", data)
	}
}

// TestPtyExternalUnknownKeyEditor: schema-less scalars edit in place (typed
// inline, no editor), while a schema-less object opens $EDITOR on a temp file
// holding only that value; both changes persist on save.
func TestPtyExternalUnknownKeyEditor(t *testing.T) {
	dir := t.TempDir()
	schema := filepath.Join(dir, "mini.schema.json")
	cfg := filepath.Join(dir, "o.json")
	editor := filepath.Join(dir, "editor.sh")
	writeFile(t, schema, `{"type":"object","properties":{"name":{"type":"string","title":"Name"}}}`)
	writeFile(t, cfg, `{"name":"z","inline":"old","extra":{"old":1}}`)
	writeFile(t, editor, "#!/bin/sh\n"+
		`grep -q '"old"' "$1" || exit 3`+"\n"+
		`grep -q '"name"' "$1" && exit 4`+"\n"+
		`echo '{"from":"editor"}' > "$1"`+"\n")
	if err := os.Chmod(editor, 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(jseBin, "-s", schema, cfg)
	cmd.Dir = ".."
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "EDITOR="+editor)
	tty, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 30, Cols: 120})
	if err != nil {
		t.Skipf("no pty available: %v", err)
	}
	defer tty.Close()

	frames := make(chan string, 64)
	go func() {
		buf := make([]byte, 4096)
		var acc bytes.Buffer
		for {
			n, err := tty.Read(buf)
			if n > 0 {
				acc.Write(buf[:n])
				frames <- acc.String()
			}
			if err != nil {
				close(frames)
				return
			}
		}
	}()
	var lastFrame string
	waitFrame := func(what string, offset int, want ...string) (string, int) {
		t.Helper()
		deadline := time.After(15 * time.Second)
		for {
			select {
			case frame, ok := <-frames:
				if !ok {
					t.Fatalf("pty closed while waiting for %s; last output:\n%s", what, lastFrame)
				}
				lastFrame = frame
				ready := true
				for _, w := range want {
					if !strings.Contains(frame[offset:], w) {
						ready = false
						break
					}
				}
				if ready {
					return frame, len(frame)
				}
			case <-deadline:
				t.Fatalf("timed out waiting for %s; last output:\n%s", what, lastFrame)
			}
		}
	}

	_, off := waitFrame("root menu", 0, "inline", "extra", "(not in schema)")
	tty.Write([]byte("j")) // issues row → Name
	_, off = waitFrame("cursor on Name", off, "> Name")
	tty.Write([]byte("j")) // Name → $schema
	_, off = waitFrame("cursor on $schema", off, "> $schema")
	tty.Write([]byte("j")) // $schema → inline
	_, off = waitFrame("cursor on scalar unknown key", off, "> inline")
	tty.Write([]byte("\r")) // open the in-place scalar editor
	_, off = waitFrame("inline scalar editor", off, "> inline", "old")
	tty.Write([]byte("\x15")) // clear the in-place scalar input
	for _, ch := range []string{"n", "e", "w"} {
		tty.Write([]byte(ch)) // type the new scalar value in place
		_, off = waitFrame("typed inline character "+ch, off, "> inline", ch)
	}
	tty.Write([]byte("\r")) // apply the scalar edit
	_, off = waitFrame("scalar edited in place", off, "inline", "new")
	tty.Write([]byte("j")) // inline → extra
	_, off = waitFrame("cursor on object unknown key", off, "> extra")
	tty.Write([]byte("\r")) // launch $EDITOR on the object value
	_, off = waitFrame("value edited", off, "Value edited")
	tty.Write([]byte("\x13")) // ctrl+s save
	_, _ = waitFrame("saved", off, "Saved")
	tty.Write([]byte("\x03"))

	waitErr := cmd.Wait()
	if ee, ok := waitErr.(*exec.ExitError); !ok || ee.ExitCode() != 130 {
		t.Fatalf("exit after ctrl+c = %v, want 130", waitErr)
	}
	got := readJSON(t, cfg).(map[string]any)
	extra, ok := got["extra"].(map[string]any)
	if !ok || extra["from"] != "editor" || got["inline"] != "new" || got["name"] != "z" {
		t.Fatalf("saved doc = %#v, want extra {from: editor}, inline new and name z", got)
	}
}

// TestPtyQuitSavePrompt drives the root esc exit path on a real terminal:
// an unchanged document quits immediately (exit 0, no write), a changed one
// asks whether to save, y writes and exits 0, n discards and exits 130.
func TestPtyQuitSavePrompt(t *testing.T) {
	dir := t.TempDir()
	schema := filepath.Join(dir, "mini.schema.json")
	outFile := filepath.Join(dir, "o.json")
	writeFile(t, schema, `{"type":"object","properties":{"name":{"type":"string","title":"Name"}}}`)

	start := func() (*exec.Cmd, *os.File, func(string, int, ...string) (string, int)) {
		cmd := exec.Command(jseBin, "-s", schema, outFile)
		cmd.Dir = ".."
		cmd.Env = append(os.Environ(), "TERM=xterm-256color")
		tty, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 30, Cols: 120})
		if err != nil {
			t.Skipf("no pty available: %v", err)
		}
		frames := make(chan string, 64)
		go func() {
			buf := make([]byte, 4096)
			var acc bytes.Buffer
			for {
				n, err := tty.Read(buf)
				if n > 0 {
					acc.Write(buf[:n])
					frames <- acc.String()
				}
				if err != nil {
					close(frames)
					return
				}
			}
		}()
		waitFrame := func(what string, offset int, want ...string) (string, int) {
			t.Helper()
			deadline := time.After(15 * time.Second)
			for {
				select {
				case frame, ok := <-frames:
					if !ok {
						t.Fatalf("pty closed while waiting for %s", what)
					}
					ready := true
					for _, w := range want {
						if !strings.Contains(frame[offset:], w) {
							ready = false
							break
						}
					}
					if ready {
						return frame, len(frame)
					}
				case <-deadline:
					t.Fatalf("timed out waiting for %s", what)
				}
			}
		}
		return cmd, tty, waitFrame
	}

	waitExit := func(cmd *exec.Cmd, want int) {
		t.Helper()
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case err := <-done:
			code := 0
			if ee, ok := err.(*exec.ExitError); ok {
				code = ee.ExitCode()
			} else if err != nil {
				t.Fatalf("wait: %v", err)
			}
			if code != want {
				t.Fatalf("exit = %d, want %d", code, want)
			}
		case <-time.After(15 * time.Second):
			cmd.Process.Kill()
			t.Fatal("timed out waiting for exit")
		}
	}

	// unchanged: esc quits at once, nothing written, no prompt
	cmd, tty, waitFrame := start()
	_, off := waitFrame("root menu", 0, "Save and exit")
	tty.Write([]byte("\x1b"))
	all, _ := waitFrame("clean exit", off, "No changes, nothing written")
	if strings.Contains(all, "Save changes?") {
		t.Fatalf("clean quit must not prompt:\n%s", all)
	}
	waitExit(cmd, 0)
	if _, err := os.Stat(outFile); !os.IsNotExist(err) {
		t.Fatalf("clean quit wrote the file: %v", err)
	}
	tty.Close()

	// changed: esc asks whether to save; y writes and exits 0
	cmd, tty, waitFrame = start()
	_, off = waitFrame("root menu", 0, "Save and exit")
	tty.Write([]byte("\rhello\r"))
	_, off = waitFrame("edited name", off, "●")
	tty.Write([]byte("\x1b"))
	_, off = waitFrame("save prompt", off, "Save changes?", "n discard & exit")
	tty.Write([]byte("y"))
	_, _ = waitFrame("saved exit", off, "Saved to")
	waitExit(cmd, 0)
	if got := readJSON(t, outFile).(map[string]any)["name"]; got != "hello" {
		t.Fatalf("name = %#v, want hello", got)
	}
	tty.Close()

	// changed: esc then n discards the edit and exits 130
	cmd, tty, waitFrame = start()
	_, off = waitFrame("root menu", 0, "Save and exit")
	tty.Write([]byte("\r\x15bye\r"))
	_, off = waitFrame("edited name", off, "●")
	tty.Write([]byte("\x1b"))
	_, off = waitFrame("save prompt", off, "Save changes?")
	tty.Write([]byte("n"))
	_, _ = waitFrame("discard exit", off, "Aborted, no changes written")
	waitExit(cmd, 130)
	if got := readJSON(t, outFile).(map[string]any)["name"]; got != "hello" {
		t.Fatalf("discard rewrote the file: name = %#v", got)
	}
	tty.Close()
}

func TestPtySave(t *testing.T) {
	dir := t.TempDir()
	schema := filepath.Join(dir, "mini.schema.json")
	outFile := filepath.Join(dir, "o.json")
	writeFile(t, schema, `{"type":"object","properties":{"name":{"type":"string","title":"Name"}}}`)

	cmd := exec.Command(jseBin, "-s", schema, outFile)
	cmd.Dir = ".."
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	tty, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 30, Cols: 120})
	if err != nil {
		t.Skipf("no pty available: %v", err)
	}
	defer tty.Close()

	go func() {
		// type the name on the field row, enter to confirm, ctrl+s to save
		time.Sleep(300 * time.Millisecond)
		tty.Write([]byte("\rhello\r")) // enter edit on Name, type, confirm
		time.Sleep(300 * time.Millisecond)
		tty.Write([]byte("\x13"))
	}()

	// ctrl+s must write the file and keep the session open: wait for the
	// Saved notice instead of an exit
	buf := make([]byte, 4096)
	var all bytes.Buffer
	saved := false
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		n, err := tty.Read(buf)
		if n > 0 {
			all.Write(buf[:n])
			if strings.Contains(all.String(), "Saved") {
				saved = true
				break
			}
		}
		if err != nil {
			break
		}
	}
	if !saved {
		t.Fatalf("ctrl+s must save without exiting; screen:\n%s", all.String())
	}
	tty.Write([]byte("\x03")) // session continues after ctrl+s; abort now
	waitErr := cmd.Wait()
	if ee, ok := waitErr.(*exec.ExitError); !ok || ee.ExitCode() != 130 {
		t.Fatalf("exit after abort = %v, want 130", waitErr)
	}
	content, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("output not written:\n%s", all.String())
	}
	if !strings.Contains(string(content), `"name": "hello"`) {
		t.Fatalf("output = %s", content)
	}
}

// TestPtyMenuFlow drives the menuconfig flow on a mini schema: drill into
// Server, edit port in place, esc back, save with ctrl+s.
func TestPtyMenuFlow(t *testing.T) {
	dir := t.TempDir()
	schema := filepath.Join(dir, "mini.schema.json")
	outFile := filepath.Join(dir, "o.json")
	writeFile(t, schema, `{"type":"object","properties":{
		"name":{"type":"string","title":"Name"},
		"server":{"type":"object","title":"Server","properties":{
			"port":{"type":"integer","title":"Port"}}}}}`)

	cmd := exec.Command(jseBin, "-s", schema, outFile)
	cmd.Dir = ".."
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	tty, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 30, Cols: 120})
	if err != nil {
		t.Skipf("no pty available: %v", err)
	}
	defer tty.Close()

	go func() {
		time.Sleep(300 * time.Millisecond)
		tty.Write([]byte("\x1b[B\r")) // ↓ → Server, enter → drill
		time.Sleep(300 * time.Millisecond)
		tty.Write([]byte("\r9050\r")) // enter edit on Port, type, confirm
		time.Sleep(300 * time.Millisecond)
		tty.Write([]byte("\x1b")) // esc back to the root menu
		time.Sleep(300 * time.Millisecond)
		tty.Write([]byte("\x13")) // ctrl+s save
	}()

	// ctrl+s writes and keeps the editor open; wait for the notice, then quit
	buf := make([]byte, 4096)
	var all bytes.Buffer
	saved := false
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		n, err := tty.Read(buf)
		if n > 0 {
			all.Write(buf[:n])
			if strings.Contains(all.String(), "Saved") {
				saved = true
				break
			}
		}
		if err != nil {
			break
		}
	}
	if !saved {
		t.Fatalf("ctrl+s must save without exiting; screen:\n%s", all.String())
	}
	tty.Write([]byte("\x03"))
	waitErr := cmd.Wait()
	if ee, ok := waitErr.(*exec.ExitError); !ok || ee.ExitCode() != 130 {
		t.Fatalf("exit after abort = %v, want 130", waitErr)
	}
	want := map[string]any{
		"$schema": schema, // -s auto-fills the field
		"server":  map[string]any{"port": 9050.0},
	}
	if got := readJSON(t, outFile); !reflect.DeepEqual(got, want) {
		t.Fatalf("output mismatch:\ngot  %#v\nwant %#v\nscreen:\n%s", got, want, all.String())
	}
}

// TestPtyMoveItem proves the J binding reaches the app through a real
// terminal: drill into a scalar array, move the first item down, save.
func TestPtyMoveItem(t *testing.T) {
	dir := t.TempDir()
	schema := filepath.Join(dir, "tags.schema.json")
	outFile := filepath.Join(dir, "o.json")
	writeFile(t, schema, `{"type":"object","properties":{"tags":{"type":"array","title":"Tags","items":{"type":"string"}}}}`)
	writeFile(t, outFile, fmt.Sprintf(`{"$schema": %q, "tags": ["alpha", "beta"]}`, schema))

	cmd := exec.Command(jseBin, "-s", schema, outFile)
	cmd.Dir = ".."
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	tty, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 30, Cols: 120})
	if err != nil {
		t.Skipf("no pty available: %v", err)
	}
	defer tty.Close()

	frames := make(chan string, 64)
	go func() {
		buf := make([]byte, 4096)
		var acc bytes.Buffer
		for {
			n, err := tty.Read(buf)
			if n > 0 {
				acc.Write(buf[:n])
				frames <- acc.String()
			}
			if err != nil {
				close(frames)
				return
			}
		}
	}()
	waitFrame := func(what string, offset int, want ...string) (string, int) {
		t.Helper()
		deadline := time.After(15 * time.Second)
		for {
			select {
			case frame, ok := <-frames:
				if !ok {
					t.Fatalf("pty closed while waiting for %s", what)
				}
				ready := true
				for _, w := range want {
					if !strings.Contains(frame[offset:], w) {
						ready = false
						break
					}
				}
				if ready {
					return frame, len(frame)
				}
			case <-deadline:
				t.Fatalf("timed out waiting for %s", what)
			}
		}
	}

	_, off := waitFrame("root menu", 0, "Save and exit")
	tty.Write([]byte("\r")) // enter → Tags items
	frame, off := waitFrame("items menu", off, "alpha")
	if !strings.Contains(frame, "$.tags") {
		t.Fatalf("items breadcrumb should use the JSON path style:\n%s", frame)
	}
	tty.Write([]byte("\x1b[BJ")) // ↓ to item 0 (alpha), J moves it down
	// the swap is visible once the rows render beta above alpha
	moved := false
	deadline := time.After(15 * time.Second)
	for !moved {
		select {
		case frame, ok := <-frames:
			if !ok {
				t.Fatalf("pty closed before the moved rows rendered")
			}
			seg := frame[off:]
			if iB, iA := strings.Index(seg, "beta"), strings.Index(seg, "alpha"); iB >= 0 && (iA < 0 || iB < iA) {
				moved = true
			}
		case <-deadline:
			t.Fatalf("timed out waiting for the moved rows")
		}
	}
	tty.Write([]byte("\x13")) // ctrl+s save
	_, _ = waitFrame("saved notice", off, "Saved")
	tty.Write([]byte("\x03")) // ctrl+c abort

	waitErr := cmd.Wait()
	if ee, ok := waitErr.(*exec.ExitError); !ok || ee.ExitCode() != 130 {
		t.Fatalf("exit after ctrl+c = %v, want 130", waitErr)
	}
	tags := readJSON(t, outFile).(map[string]any)["tags"]
	if !reflect.DeepEqual(tags, []any{"beta", "alpha"}) {
		t.Fatalf("tags after move = %#v, want [beta alpha]", tags)
	}
}

// TestPtySearch drives the "/" search end to end: value hits labeled with
// breadcrumb-style paths, and scoping to the menu the breadcrumb shows.
// Keys are only sent after the previous frame rendered: input written before
// the TUI switches the pty to raw mode is line-buffered and garbled.
func TestPtySearch(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "server.config.json")
	src, err := os.ReadFile("../examples/server.config.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, src, 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(jseBin, "-s", "examples/server.schema.json", cfg)
	cmd.Dir = ".."
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	tty, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 30, Cols: 120})
	if err != nil {
		t.Skipf("no pty available: %v", err)
	}
	defer tty.Close()

	frames := make(chan string, 64)
	go func() {
		buf := make([]byte, 4096)
		var acc bytes.Buffer
		for {
			n, err := tty.Read(buf)
			if n > 0 {
				acc.Write(buf[:n])
				frames <- acc.String()
			}
			if err != nil {
				close(frames)
				return
			}
		}
	}()
	// waitFrame blocks until every want appears in output written after
	// offset and returns the frame plus its length, so negative checks can
	// stay scoped to what came later.
	waitFrame := func(what string, offset int, want ...string) (string, int) {
		t.Helper()
		deadline := time.After(15 * time.Second)
		for {
			select {
			case frame, ok := <-frames:
				if !ok {
					t.Fatalf("pty closed while waiting for %s", what)
				}
				ready := true
				for _, w := range want {
					if !strings.Contains(frame[offset:], w) {
						ready = false
						break
					}
				}
				if ready {
					return frame, len(frame)
				}
			case <-deadline:
				t.Fatalf("timed out waiting for %s", what)
			}
		}
	}
	frame, off := waitFrame("root menu", 0, "Save and exit")
	tty.Write([]byte("/")) // open search (single key: chunked runes won't match)
	_, off = waitFrame("search view", off, "search:", "alt+x regex")
	tty.Write([]byte("8080")) // root search: value hit on the row that owns it
	prev := off
	frame, off = waitFrame("value hits", off,
		"$.server.port  8080") // leaf value hit
	if strings.Contains(frame[prev:], "$.server  port=8080 host=localhost +1") {
		t.Errorf("a container must not match through its child roll-up:\n%s", frame[prev:])
	}
	tty.Write([]byte("\x1b")) // close search
	time.Sleep(200 * time.Millisecond)
	tty.Write([]byte("/")) // reopen: the "$." path syntax works in regex mode too
	_, off = waitFrame("search view again", off, "search:")
	tty.Write([]byte("$.server.port"))
	frame, off = waitFrame("anchored path hit", off, "$.server.port  8080")
	if strings.Contains(frame[off:], "invalid regex") {
		t.Errorf("the path anchor must not reach the regexp engine:\n%s", frame[off:])
	}
	tty.Write([]byte("\x1b")) // close search
	time.Sleep(200 * time.Millisecond)
	tty.Write([]byte("\x1b[B\x1b[B\x1b[B\r")) // ↓↓↓ enter → Server ($.server); row 0 is the issues row
	_, off = waitFrame("server menu", off, "$.server")
	tty.Write([]byte("/")) // scoped search: nothing below $.server
	_, off = waitFrame("scoped search view", off, "search:")
	tty.Write([]byte("name"))
	frame, _ = waitFrame("scoped search settles", off, "search: name")
	if strings.Contains(frame[off:], "$.name") {
		t.Errorf("scoped search leaked $.name:\n%s", frame[off:])
	}
	tty.Write([]byte("\x1b"))
	time.Sleep(200 * time.Millisecond)
	tty.Write([]byte("\x03")) // ctrl+c abort

	waitErr := cmd.Wait()
	if ee, ok := waitErr.(*exec.ExitError); !ok || ee.ExitCode() != 130 {
		t.Fatalf("exit after ctrl+c = %v, want 130", waitErr)
	}
	if now, err := os.ReadFile(cfg); err != nil || !bytes.Equal(now, src) {
		t.Errorf("abort must not rewrite the config: %v", err)
	}
}

// TestPtySearchReplace drives the r-search-replace end to end: string-value
// hits preview as old → new, ctrl+r rewrites them all in the doc, and ctrl+s
// persists the file with the replaced host.
func TestPtySearchReplace(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "server.config.json")
	src, err := os.ReadFile("../examples/server.config.json")
	if err != nil {
		t.Fatal(err)
	}
	// the shipped example carries mode "quic", illegal for the schema enum:
	// fix it so ctrl+s can actually save after the replace, and add a
	// schema-unknown string key so the replace must reach it too
	fixed := strings.Replace(string(src), `"mode": "quic"`,
		`"mode": "tcp", "comment": "localhost box"`, 1)
	if err := os.WriteFile(cfg, []byte(fixed), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(jseBin, "-s", "examples/server.schema.json", cfg)
	cmd.Dir = ".."
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	tty, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 30, Cols: 120})
	if err != nil {
		t.Skipf("no pty available: %v", err)
	}
	defer tty.Close()

	frames := make(chan string, 64)
	go func() {
		buf := make([]byte, 4096)
		var acc bytes.Buffer
		for {
			n, err := tty.Read(buf)
			if n > 0 {
				acc.Write(buf[:n])
				frames <- acc.String()
			}
			if err != nil {
				close(frames)
				return
			}
		}
	}()
	waitFrame := func(what string, offset int, want ...string) (string, int) {
		t.Helper()
		deadline := time.After(15 * time.Second)
		for {
			select {
			case frame, ok := <-frames:
				if !ok {
					t.Fatalf("pty closed while waiting for %s", what)
				}
				ready := true
				for _, w := range want {
					if !strings.Contains(frame[offset:], w) {
						ready = false
						break
					}
				}
				if ready {
					return frame, len(frame)
				}
			case <-deadline:
				t.Fatalf("timed out waiting for %s", what)
			}
		}
	}

	frame, off := waitFrame("root menu", 0, "Save and exit", icons.Warn+" 2 warnings")
	_ = frame
	tty.Write([]byte("r")) // open search-and-replace directly
	_, off = waitFrame("replace view", off, "replace:", "case-insensitive")
	tty.Write([]byte("localhost"))
	_, off = waitFrame("replace preview", off, "$.server.host  localhost →")
	tty.Write([]byte("\t127.0.0.1")) // focus the replace input, type the text
	_, off = waitFrame("replacement typed", off, "$.server.host  localhost → 127.0.0.1")
	tty.Write([]byte("\x12")) // ctrl+r: replace all in scope
	_, off = waitFrame("replace-all notice", off, "replaced 2 occurrence")
	tty.Write([]byte("\x13")) // ctrl+s: persist
	_, _ = waitFrame("saved", off, "Saved")
	content, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), `"host": "127.0.0.1"`) {
		t.Errorf("replaced host missing after save:\n%s", content)
	}
	if !strings.Contains(string(content), "127.0.0.1 box") {
		t.Errorf("unknown-key comment not replaced after save:\n%s", content)
	}
	if strings.Contains(string(content), "localhost") {
		t.Errorf("old host still present after save:\n%s", content)
	}
	tty.Write([]byte("\x03")) // ctrl+c abort
	waitErr := cmd.Wait()
	if ee, ok := waitErr.(*exec.ExitError); !ok || ee.ExitCode() != 130 {
		t.Fatalf("exit after ctrl+c = %v, want 130", waitErr)
	}
}

// TestPtyUndoRedo drives u / ctrl+r end to end: an in-place name edit
// commits, u reverts it (undone notice, saved file back to the original),
// ctrl+r re-applies it (redone notice, saved file shows the edit again).
func TestPtyUndoRedo(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "server.config.json")
	src, err := os.ReadFile("../examples/server.config.json")
	if err != nil {
		t.Fatal(err)
	}
	// the shipped example carries mode "quic", illegal for the schema enum:
	// fix it so ctrl+s can actually save around the undo/redo steps
	fixed := strings.Replace(string(src), `"mode": "quic"`, `"mode": "tcp"`, 1)
	if err := os.WriteFile(cfg, []byte(fixed), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(jseBin, "-s", "examples/server.schema.json", cfg)
	cmd.Dir = ".."
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	tty, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 30, Cols: 120})
	if err != nil {
		t.Skipf("no pty available: %v", err)
	}
	defer tty.Close()

	frames := make(chan string, 64)
	go func() {
		buf := make([]byte, 4096)
		var acc bytes.Buffer
		for {
			n, err := tty.Read(buf)
			if n > 0 {
				acc.Write(buf[:n])
				frames <- acc.String()
			}
			if err != nil {
				close(frames)
				return
			}
		}
	}()
	waitFrame := func(what string, offset int, want ...string) (string, int) {
		t.Helper()
		deadline := time.After(15 * time.Second)
		for {
			select {
			case frame, ok := <-frames:
				if !ok {
					t.Fatalf("pty closed while waiting for %s", what)
				}
				ready := true
				for _, w := range want {
					if !strings.Contains(frame[offset:], w) {
						ready = false
						break
					}
				}
				if ready {
					return frame, len(frame)
				}
			case <-deadline:
				t.Fatalf("timed out waiting for %s", what)
			}
		}
	}

	_, off := waitFrame("root menu", 0, "Server Configuration", "Name")
	time.Sleep(300 * time.Millisecond) // let the TUI finish raw-mode setup
	// the example carries a schema-unknown key, so a status row sits above
	// Name; step over it. name is pattern-bound (^[a-z][a-z0-9-]*$), so -x
	// is a legal suffix where "!" would be refused.
	tty.Write([]byte("\x1b[B")) // ↓ off the status row
	tty.Write([]byte("\r-x\r")) // enter on Name (input prefills demo), append, commit
	_, off = waitFrame("name edit committed", off, "demo-x")
	tty.Write([]byte("u")) // undo
	_, off = waitFrame("undone notice", off, "undone")
	tty.Write([]byte("\x13")) // ctrl+s: persist the reverted doc
	_, off = waitFrame("save after undo", off, "Saved")
	content, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), `"name": "demo"`) ||
		strings.Contains(string(content), `"name": "demo-x"`) {
		t.Errorf("undo did not revert the saved name:\n%s", content)
	}
	tty.Write([]byte("\x12")) // ctrl+r: redo
	_, off = waitFrame("redone notice", off, "redone", "demo-x")
	tty.Write([]byte("\x13")) // ctrl+s: persist the re-applied doc
	_, off = waitFrame("save after redo", off, "Saved")
	content, err = os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), `"name": "demo-x"`) {
		t.Errorf("redo did not re-apply the saved name:\n%s", content)
	}
	tty.Write([]byte("\x03")) // ctrl+c abort
	waitErr := cmd.Wait()
	if ee, ok := waitErr.(*exec.ExitError); !ok || ee.ExitCode() != 130 {
		t.Fatalf("exit after ctrl+c = %v, want 130", waitErr)
	}
}

// aliasPtyFiles writes the schema and YAML document shared by the alias
// tests: copy references base through *x.
func aliasPtyFiles(t *testing.T) (dir, schema, cfg string) {
	t.Helper()
	dir = t.TempDir()
	schema = filepath.Join(dir, "alias.schema.json")
	cfg = filepath.Join(dir, "o.yaml")
	writeFile(t, schema, `{"type":"object","properties":{
		"base":{"type":"object","title":"Base","properties":{
			"p":{"type":"integer","title":"P"}}},
		"copy":{"type":"object","title":"Copy","properties":{
			"p":{"type":"integer","title":"P"}}}}}`)
	writeFile(t, cfg, "base: &x\n  p: 1\ncopy: *x\n")
	return dir, schema, cfg
}

// TestPtyYAMLAliasEditKeepsReference proves the core alias contract on the
// real TUI: an aliased object's menu shows the anchored original with
// < Expand > on top, editing through it writes the original, and the saved
// file still carries the "*x" reference (no auto-expansion).
func TestPtyYAMLAliasEditKeepsReference(t *testing.T) {
	_, schema, cfg := aliasPtyFiles(t)

	cmd := exec.Command(jseBin, "-s", schema, cfg)
	cmd.Dir = ".."
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	tty, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 30, Cols: 120})
	if err != nil {
		t.Skipf("no pty available: %v", err)
	}
	defer tty.Close()

	go func() {
		time.Sleep(300 * time.Millisecond)
		tty.Write([]byte("\x1b[B\r")) // ↓ → Copy, enter → drill into the alias
		time.Sleep(300 * time.Millisecond)
		tty.Write([]byte("\x1b[B\r\x7f9\r")) // ↓ past < Expand >, edit P (backspace clears the prefilled 1)
		time.Sleep(300 * time.Millisecond)
		tty.Write([]byte("\x13")) // ctrl+s save
	}()

	buf := make([]byte, 4096)
	var all bytes.Buffer
	saved := false
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		n, err := tty.Read(buf)
		if n > 0 {
			all.Write(buf[:n])
			if strings.Contains(all.String(), "Saved") {
				saved = true
				break
			}
		}
		if err != nil {
			break
		}
	}
	if !saved {
		t.Fatalf("ctrl+s must save; screen:\n%s", all.String())
	}
	screen := all.String()
	if !strings.Contains(screen, "< Expand >") {
		t.Errorf("alias menu must offer < Expand > on top; screen:\n%s", screen)
	}
	tty.Write([]byte("\x03"))
	waitErr := cmd.Wait()
	if ee, ok := waitErr.(*exec.ExitError); !ok || ee.ExitCode() != 130 {
		t.Fatalf("exit after abort = %v, want 130", waitErr)
	}

	content, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	// The yaml-language-server comment heads the file; check the data lines.
	if !strings.Contains(text, "base: &x") || !strings.Contains(text, "p: 9") {
		t.Errorf("edit through the alias must write the anchored original:\n%s", text)
	}
	if !strings.Contains(text, "copy: *x") {
		t.Errorf("an unexpanded alias must be written back as a reference:\n%s", text)
	}
}

// TestPtyYAMLAliasExpand proves < Expand > materializes the reference: the
// saved file turns copy into plain content while base keeps its anchor, and
// the later edit stays local to copy.
func TestPtyYAMLAliasExpand(t *testing.T) {
	_, schema, cfg := aliasPtyFiles(t)

	cmd := exec.Command(jseBin, "-s", schema, cfg)
	cmd.Dir = ".."
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	tty, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 30, Cols: 120})
	if err != nil {
		t.Skipf("no pty available: %v", err)
	}
	defer tty.Close()

	go func() {
		time.Sleep(300 * time.Millisecond)
		tty.Write([]byte("\x1b[B\r")) // ↓ → Copy, enter → drill (Expand is row 0)
		time.Sleep(300 * time.Millisecond)
		tty.Write([]byte("\r")) // enter on < Expand >
		time.Sleep(300 * time.Millisecond)
		tty.Write([]byte("\r\x7f7\r")) // edit P on the now-independent copy
		time.Sleep(300 * time.Millisecond)
		tty.Write([]byte("\x13")) // ctrl+s save
	}()

	buf := make([]byte, 4096)
	var all bytes.Buffer
	saved := false
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		n, err := tty.Read(buf)
		if n > 0 {
			all.Write(buf[:n])
			if strings.Contains(all.String(), "Saved") {
				saved = true
				break
			}
		}
		if err != nil {
			break
		}
	}
	if !saved {
		t.Fatalf("ctrl+s must save; screen:\n%s", all.String())
	}
	tty.Write([]byte("\x03"))
	waitErr := cmd.Wait()
	if ee, ok := waitErr.(*exec.ExitError); !ok || ee.ExitCode() != 130 {
		t.Fatalf("exit after abort = %v, want 130", waitErr)
	}

	content, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	if !strings.Contains(text, "base: &x") || !strings.Contains(text, "p: 1") {
		t.Errorf("the anchor and its content must survive the expand:\n%s", text)
	}
	if strings.Contains(text, "*x") {
		t.Errorf("an expanded alias must be written as plain content:\n%s", text)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(content, &doc); err != nil {
		t.Fatalf("saved file is not valid YAML: %v\n%s", err, text)
	}
	copyObj, _ := doc["copy"].(map[string]any)
	if copyObj == nil || fmt.Sprint(copyObj["p"]) != "7" {
		t.Errorf("expanded copy must hold its own edit, got %#v:\n%s", doc["copy"], text)
	}
}

// TestPtyYAMLAnchorRename proves the & flow end to end: the anchor input
// prefills the current name, renaming rewrites every reference (they share
// the node), and the saved file carries the new name on both sides.
func TestPtyYAMLAnchorRename(t *testing.T) {
	_, schema, cfg := aliasPtyFiles(t)

	cmd := exec.Command(jseBin, "-s", schema, cfg)
	cmd.Dir = ".."
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	tty, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 30, Cols: 120})
	if err != nil {
		t.Skipf("no pty available: %v", err)
	}
	defer tty.Close()

	go func() {
		time.Sleep(300 * time.Millisecond)
		tty.Write([]byte("&\x7fy\r")) // & on the base row: rename x → y
		time.Sleep(300 * time.Millisecond)
		tty.Write([]byte("\x13")) // ctrl+s save
	}()

	buf := make([]byte, 4096)
	var all bytes.Buffer
	saved := false
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		n, err := tty.Read(buf)
		if n > 0 {
			all.Write(buf[:n])
			if strings.Contains(all.String(), "Saved") {
				saved = true
				break
			}
		}
		if err != nil {
			break
		}
	}
	if !saved {
		t.Fatalf("ctrl+s must save; screen:\n%s", all.String())
	}
	tty.Write([]byte("\x03"))
	waitErr := cmd.Wait()
	if ee, ok := waitErr.(*exec.ExitError); !ok || ee.ExitCode() != 130 {
		t.Fatalf("exit after abort = %v, want 130", waitErr)
	}

	content, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	if !strings.Contains(text, "base: &y") || !strings.Contains(text, "copy: *y") {
		t.Errorf("rename must rewrite the anchor and its references:\n%s", text)
	}
	if strings.Contains(text, "*x") || strings.Contains(text, "&x") {
		t.Errorf("the old anchor name must be gone:\n%s", text)
	}
}
