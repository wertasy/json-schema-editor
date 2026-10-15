package fetch

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadLocalPath(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s.json")
	if err := os.WriteFile(p, []byte(`{"ok":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	data, err := Load(p)
	if err != nil {
		t.Fatalf("Load path: %v", err)
	}
	if string(data) != `{"ok":true}` {
		t.Fatalf("data = %s", data)
	}
}

func TestLoadFileURL(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s.json")
	if err := os.WriteFile(p, []byte("v"), 0o644); err != nil {
		t.Fatal(err)
	}
	data, err := Load("file://" + p)
	if err != nil {
		t.Fatalf("Load file://: %v", err)
	}
	if string(data) != "v" {
		t.Fatalf("data = %s", data)
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Fatal("missing file should error")
	}
}

func TestLoadHTTP(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			w.Write([]byte(`{"a":1}`))
		case "/redirect":
			http.Redirect(w, r, "/ok", http.StatusFound)
		case "/moved":
			http.Redirect(w, r, "/ok", http.StatusMovedPermanently)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	for _, path := range []string{"/ok", "/redirect", "/moved"} {
		data, err := Load(ts.URL + path)
		if err != nil {
			t.Fatalf("Load %s: %v", path, err)
		}
		if string(data) != `{"a":1}` {
			t.Fatalf("%s data = %s", path, data)
		}
	}

	_, err := Load(ts.URL + "/404")
	if err == nil || !strings.Contains(err.Error(), "status 404") {
		t.Fatalf("404 err = %v", err)
	}
}

func TestLoadSizeLimit(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(make([]byte, 1000))
	}))
	defer ts.Close()

	old := MaxBytes
	MaxBytes = 100
	defer func() { MaxBytes = old }()

	_, err := Load(ts.URL + "/big")
	if err == nil || !strings.Contains(err.Error(), "response exceeds size limit") {
		t.Fatalf("limit err = %v", err)
	}

	MaxBytes = 2000
	data, err := Load(ts.URL + "/big")
	if err != nil || len(data) != 1000 {
		t.Fatalf("under limit: %v len=%d", err, len(data))
	}
}
