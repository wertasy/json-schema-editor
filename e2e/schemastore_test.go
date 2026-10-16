package e2e

// SchemaStore catalog integration: automatic fileMatch resolution, the
// accessible search picker and the pty-driven TUI picker.

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

// startCatalogServer serves a two-entry catalog plus the mini schema both
// entries point at.
func startCatalogServer(t *testing.T) *httptest.Server {
	t.Helper()
	const miniSchema = `{"type":"object","properties":{"name":{"type":"string","title":"Name"}}}`
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/catalog.json":
			// Decoy only matches other.json: never auto-applied to the
			// configs below, never listed for a "serv" search.
			w.Write([]byte(`{"schemas":[` +
				`{"name":"Server Config","description":"server settings","url":"` + ts.URL + `/server.schema.json","fileMatch":["server.json","*.srv.json"]},` +
				`{"name":"Decoy","description":"something else","url":"` + ts.URL + `/other.schema.json","fileMatch":["other.json"]}]}`))
		default:
			w.Write([]byte(miniSchema))
		}
	}))
	t.Cleanup(ts.Close)
	return ts
}

func TestSchemaStore(t *testing.T) {
	ts := startCatalogServer(t)
	t.Setenv("JSE_CATALOG_URL", ts.URL+"/catalog.json")

	t.Run("auto match", func(t *testing.T) {
		dir := t.TempDir()
		cfg := filepath.Join(dir, "server.json")
		writeFile(t, cfg, `{"name":"x"}`)
		stdout, stderr, code := runJSE(t, 60*time.Second, "\n\n", cfg, "--accessible")
		if code != 0 || !strings.Contains(stdout, "Saved to") {
			t.Fatalf("exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
		}
		if got := readJSON(t, cfg).(map[string]any); got["$schema"] != ts.URL+"/server.schema.json" {
			t.Fatalf("matched URL not saved as $schema: %#v", got["$schema"])
		}
		if !strings.Contains(stdout, "schema from SchemaStore: Server Config") {
			t.Fatalf("catalog source not reported:\n%s", stdout)
		}
	})

	t.Run("glob match", func(t *testing.T) {
		dir := t.TempDir()
		cfg := filepath.Join(dir, "odd.srv.json")
		writeFile(t, cfg, `{"name":"x"}`)
		stdout, stderr, code := runJSE(t, 60*time.Second, "\n\n", cfg, "--accessible")
		if code != 0 || !strings.Contains(stdout, "Saved to") {
			t.Fatalf("exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
		}
		if got := readJSON(t, cfg).(map[string]any); got["$schema"] != ts.URL+"/server.schema.json" {
			t.Fatalf("glob match URL not saved: %#v", got["$schema"])
		}
	})

	t.Run("manual search", func(t *testing.T) {
		dir := t.TempDir()
		cfg := filepath.Join(dir, "mystery.json")
		writeFile(t, cfg, `{"name":"y"}`)
		// search "serv" → numbered list → 1 → form accepts defaults, saves
		stdout, stderr, code := runJSE(t, 60*time.Second, "serv\n1\n\n\n", cfg, "--accessible")
		if code != 0 || !strings.Contains(stdout, "Saved to") {
			t.Fatalf("exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
		}
		if got := readJSON(t, cfg).(map[string]any); got["$schema"] != ts.URL+"/server.schema.json" {
			t.Fatalf("picked URL not saved: %#v", got["$schema"])
		}
	})

	t.Run("abort", func(t *testing.T) {
		dir := t.TempDir()
		cfg := filepath.Join(dir, "mystery.json")
		writeFile(t, cfg, `{"name":"z"}`)
		// default query "mystery" matches nothing; empty lines keep it, then
		// EOF aborts the picker
		_, stderr, code := runJSE(t, 60*time.Second, "\n\n", cfg, "--accessible")
		if code != 2 {
			t.Fatalf("exit = %d, want 2\nstderr:\n%s", code, stderr)
		}
		if !strings.Contains(stderr, "no schema found") {
			t.Fatalf("stderr missing message:\n%s", stderr)
		}
		if got := readJSON(t, cfg); got.(map[string]any)["$schema"] != nil {
			t.Fatalf("aborted session still wrote $schema: %#v", got)
		}
	})
}

// TestPtySchemaPicker drives the full-screen TUI picker: search "serv",
// select the single hit, land in the form, abort with ctrl+c.
func TestPtySchemaPicker(t *testing.T) {
	ts := startCatalogServer(t)
	dir := t.TempDir()
	cfg := filepath.Join(dir, "mystery.json")
	writeFile(t, cfg, `{"name":"z"}`)

	cmd := exec.Command(jseBin, cfg)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"JSE_CATALOG_URL="+ts.URL+"/catalog.json",
		"TERM=xterm-256color")
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
	waitFrame := func(what string, offset int, want ...string) int {
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
					return len(frame)
				}
			case <-deadline:
				t.Fatalf("timed out waiting for %s", what)
			}
		}
	}

	off := waitFrame("picker opens", 0, "Select a schema")
	tty.Write([]byte("serv\r"))
	off = waitFrame("search hit", off, "Server Config")
	tty.Write([]byte("\r"))
	waitFrame("form after pick", off, "Save and exit")
	tty.Write([]byte("\x03")) // ctrl+c abort

	waitErr := cmd.Wait()
	if ee, ok := waitErr.(*exec.ExitError); !ok || ee.ExitCode() != 130 {
		t.Fatalf("exit after ctrl+c = %v, want 130", waitErr)
	}
	if now, err := os.ReadFile(cfg); err != nil || string(now) != `{"name":"z"}` {
		t.Errorf("abort must not rewrite the config: %v %q", err, now)
	}
}
