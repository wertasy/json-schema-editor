package fetch

// Cache tests: fresh hits skip the network, expired entries revalidate with
// If-None-Match / If-Modified-Since, unreachable origins fall back to the
// cached copy, and hash-only / unwritable cache locations degrade silently.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestMain keeps every test off the real user cache directory.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "jse-fetch-cache-*")
	if err != nil {
		panic(err)
	}
	CacheDir = dir
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// newProcess drops the per-process memo, simulating a fresh jse run.
func newProcess() {
	mu.Lock()
	memo = map[string]memoEntry{}
	mu.Unlock()
}

// cacheTest isolates the cache in a temp dir and restores the globals.
func cacheTest(t *testing.T) {
	t.Helper()
	oldDir, oldTTL := CacheDir, CacheTTL
	CacheDir, CacheTTL = t.TempDir(), 24*time.Hour
	newProcess()
	t.Cleanup(func() {
		CacheDir, CacheTTL = oldDir, oldTTL
		newProcess()
	})
}

// schemaServer serves body with an ETag, counting hits and conditionals.
func schemaServer(t *testing.T, body, etag string, hits, conditionals *int32) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(hits, 1)
		w.Header().Set("ETag", etag)
		if r.Header.Get("If-None-Match") == etag {
			atomic.AddInt32(conditionals, 1)
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Write([]byte(body))
	}))
	t.Cleanup(ts.Close)
	return ts
}

func TestLoadHTTPUnchanged(t *testing.T) {
	cacheTest(t)
	var hits, conds int32
	ts := schemaServer(t, `{"a":1}`, `"v1"`, &hits, &conds)

	data, err := Load(ts.URL + "/ok")
	if err != nil || string(data) != `{"a":1}` {
		t.Fatalf("live load: %v %s", err, data)
	}
	if hits != 1 || conds != 0 {
		t.Fatalf("hits = %d, conditionals = %d, want 1/0", hits, conds)
	}
	if n := Note(ts.URL + "/ok"); n != "" {
		t.Fatalf("note = %q, want empty on a live fetch", n)
	}
}

// TestHTTPMemoPerProcess: the same URL is downloaded once per process even
// though both the form model and the validator ask for it.
func TestHTTPMemoPerProcess(t *testing.T) {
	cacheTest(t)
	var hits, conds int32
	ts := schemaServer(t, `{"a":1}`, `"v1"`, &hits, &conds)

	for i := 0; i < 3; i++ {
		if _, err := Load(ts.URL + "/ok"); err != nil {
			t.Fatal(err)
		}
	}
	if hits != 1 {
		t.Fatalf("hits = %d, want 1 (memoized)", hits)
	}
}

// TestCacheFreshSkipsNetwork: a fresh entry is served without any request.
func TestCacheFreshSkipsNetwork(t *testing.T) {
	cacheTest(t)
	var hits, conds int32
	ts := schemaServer(t, `{"a":1}`, `"v1"`, &hits, &conds)

	if _, err := Load(ts.URL + "/ok"); err != nil {
		t.Fatal(err)
	}
	newProcess() // next jse run
	data, err := Load(ts.URL + "/ok")
	if err != nil || string(data) != `{"a":1}` {
		t.Fatalf("cached load: %v %s", err, data)
	}
	if hits != 1 {
		t.Fatalf("hits = %d, want 1 (fresh cache must not hit the origin)", hits)
	}
}

// TestCacheRevalidates: an expired entry revalidates and a 304 keeps the
// cached bytes without transferring the body again.
func TestCacheRevalidates(t *testing.T) {
	cacheTest(t)
	var hits, conds int32
	ts := schemaServer(t, `{"a":1}`, `"v1"`, &hits, &conds)
	url := ts.URL + "/ok"

	if _, err := Load(url); err != nil {
		t.Fatal(err)
	}
	newProcess()
	CacheTTL = 0 // force revalidation
	data, err := Load(url)
	if err != nil || string(data) != `{"a":1}` {
		t.Fatalf("revalidated load: %v %s", err, data)
	}
	if hits != 2 || conds != 1 {
		t.Fatalf("hits = %d, conditionals = %d, want 2/1", hits, conds)
	}

	// A 304 refreshes the entry: with a TTL again, no further request.
	newProcess()
	CacheTTL = 24 * time.Hour
	if _, err := Load(url); err != nil {
		t.Fatal(err)
	}
	if hits != 2 {
		t.Fatalf("hits = %d, want 2 (304 must refresh the entry)", hits)
	}
}

// TestCacheUpdatesOnChange: a changed document replaces the cached copy
// (the revalidation is answered with a full body, not a 304).
func TestCacheUpdatesOnChange(t *testing.T) {
	cacheTest(t)
	var hits int32
	var sawINM atomic.Value // If-None-Match of the last request
	sawINM.Store("")
	etag := `"v1"`
	body := `{"a":1}`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		sawINM.Store(r.Header.Get("If-None-Match"))
		w.Header().Set("ETag", etag)
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Write([]byte(body))
	}))
	defer ts.Close()
	url := ts.URL + "/ok"

	if _, err := Load(url); err != nil {
		t.Fatal(err)
	}
	body, etag = `{"a":2}`, `"v2"`
	newProcess()
	CacheTTL = 0
	data, err := Load(url)
	if err != nil || string(data) != `{"a":2}` {
		t.Fatalf("updated load: %v %s", err, data)
	}
	if got := sawINM.Load().(string); got != `"v1"` {
		t.Fatalf("If-None-Match = %q, want the cached etag", got)
	}
	// The new body is cached: a fresh read must serve it without a request.
	newProcess()
	CacheTTL = 24 * time.Hour
	if data, err = Load(url); err != nil || string(data) != `{"a":2}` {
		t.Fatalf("cached update: %v %s", err, data)
	}
	if hits != 2 {
		t.Fatalf("hits = %d, want 2", hits)
	}
}

// TestCacheOfflineFallback: an unreachable origin serves the cached copy and
// reports it through Note; without a cache the error stands.
func TestCacheOfflineFallback(t *testing.T) {
	cacheTest(t)
	var hits, conds int32
	ts := schemaServer(t, `{"a":1}`, `"v1"`, &hits, &conds)
	url := ts.URL + "/ok"

	if _, err := Load(url); err != nil {
		t.Fatal(err)
	}
	ts.Close() // origin gone

	newProcess()
	CacheTTL = 0 // force a revalidation attempt
	data, err := Load(url)
	if err != nil || string(data) != `{"a":1}` {
		t.Fatalf("offline fallback: %v %s", err, data)
	}
	if n := Note(url); !strings.Contains(n, "cached schema") {
		t.Fatalf("note = %q, want a cached-schema warning", n)
	}

	// A different (uncached) URL still errors out.
	if _, err := Load(ts.URL + "/other"); err == nil {
		t.Fatal("uncached unreachable URL should error")
	}
}

// TestCacheServerErrorFallback: 5xx falls back to the cache, 404 does not.
func TestCacheServerErrorFallback(t *testing.T) {
	cacheTest(t)
	var status atomic.Int32
	status.Store(http.StatusOK)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(int(status.Load()))
	}))
	defer ts.Close()
	url := ts.URL + "/ok"

	if _, err := Load(url); err != nil { // 200 with an empty body
		t.Fatal(err)
	}
	newProcess()
	CacheTTL = 0
	status.Store(http.StatusInternalServerError)
	if _, err := Load(url); err != nil || Note(url) == "" {
		t.Fatalf("5xx should fall back to the cache: %v note=%q", err, Note(url))
	}
	status.Store(http.StatusNotFound)
	newProcess()
	if _, err := Load(url); err == nil || !strings.Contains(err.Error(), "status 404") {
		t.Fatalf("404 err = %v, want a hard error", err)
	}
}

// TestCacheUnwritableDegrades: an unusable cache root never blocks loading.
func TestCacheUnwritableDegrades(t *testing.T) {
	cacheTest(t)
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	CacheDir = file // MkdirAll under it fails

	var hits, conds int32
	ts := schemaServer(t, `{"a":1}`, `"v1"`, &hits, &conds)
	for i := 0; i < 2; i++ {
		if _, err := Load(ts.URL + "/ok"); err != nil {
			t.Fatalf("load %d: %v", i, err)
		}
		newProcess()
	}
	if hits != 2 {
		t.Fatalf("hits = %d, want 2 (no cache available)", hits)
	}
}
