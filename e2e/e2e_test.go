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

	"json-schema-editor/internal/jsondoc"
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
	cmd := exec.Command(jseBin, args...)
	cmd.Dir = ".."
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
const createScript = "webapp\n" + // Name
	"2\n" + // Mode → http
	"1\n" + // Navigate → Server
	"8080\nlocalhost\ny\n\n\n" + // Port, Host, Debug=y, Ratio=omit, Done
	"webapp\n\n2\n" + // root rerun: Name, Mode=http, Navigate → Logging
	"2\n0\n" + // Levels: toggle info, confirm
	"2\n" + // Logging navigate → tags items
	"1\nprod\n4\n" + // mgr: add item, value, done
	"0\n1\n" + // Logging rerun: confirm levels, Done
	"webapp\n\n3\n" + // root: → Mounts
	"1\nwebapps/ROOT\n/var/www\n\n4\n" + // mgr: add mount {src,dst}, done
	"webapp\n\n4\n" + // root: → Auth
	"2\nsecret123\n\n" + // variant Token, token, done
	"webapp\n\n5\n" // root: save

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
		"name":    "webapp",
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
		"mounts": []any{map[string]any{"src": "webapps/ROOT", "dst": "/var/www"}},
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
	_, off = waitFrame("search view", off, "search:")
	tty.Write([]byte("8080")) // root search: value hits
	frame, off = waitFrame("value hits", off,
		"$/server  port=8080 host=localhost +1", // container overview hit
		"$/server/port  8080")                   // leaf value hit
	tty.Write([]byte("\x1b")) // close search
	time.Sleep(200 * time.Millisecond)
	tty.Write([]byte("\x1b[B\x1b[B\x1b[B\r")) // ↓↓↓ enter → Server ($/server); row 0 is the issues row
	_, off = waitFrame("server menu", off, "$/server")
	tty.Write([]byte("/")) // scoped search: nothing below $/server
	_, off = waitFrame("scoped search view", off, "search:")
	tty.Write([]byte("name"))
	frame, _ = waitFrame("scoped search settles", off, "search: name")
	if strings.Contains(frame[off:], "$/name") {
		t.Errorf("scoped search leaked $/name:\n%s", frame[off:])
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
	// fix it so ctrl+s can actually save after the replace
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

	frame, off := waitFrame("root menu", 0, "Save and exit")
	_ = frame
	tty.Write([]byte("r")) // open search-and-replace directly
	_, off = waitFrame("replace view", off, "replace:", "case-insensitive")
	tty.Write([]byte("localhost"))
	_, off = waitFrame("replace preview", off, "$/server/host  localhost →")
	tty.Write([]byte("\t127.0.0.1")) // focus the replace input, type the text
	_, off = waitFrame("replacement typed", off, "$/server/host  localhost → 127.0.0.1")
	tty.Write([]byte("\x12")) // ctrl+r: replace all in scope
	_, off = waitFrame("replace-all notice", off, "replaced 1 occurrence")
	tty.Write([]byte("\x13")) // ctrl+s: persist
	_, _ = waitFrame("saved", off, "Saved")
	content, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), `"host": "127.0.0.1"`) {
		t.Errorf("replaced host missing after save:\n%s", content)
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
