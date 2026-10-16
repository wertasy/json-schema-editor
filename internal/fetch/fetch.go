// Package fetch provides uniform loading of local paths, http(s):// and
// file:// URLs with a timeout, a size cap and an on-disk cache for remote
// documents (the schema at hand is fetched on every start otherwise).
package fetch

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"json-schema-editor/internal/icons"
)

// MaxBytes caps http response bodies (and thus schema documents). Exported
// so tests can shrink it.
var MaxBytes int64 = 32 << 20

// CacheTTL is how long a cached remote document is served without asking the
// origin again; an expired entry is revalidated with a conditional request.
// Exported so tests can shrink it.
var CacheTTL = 24 * time.Hour

// CacheDir overrides the cache root; empty means $JSE_CACHE_DIR if set, else
// <user cache dir>/jse. Without a usable directory remote documents are
// fetched normally and never cached. Cache writes are best-effort: failures
// never fail the load.
var CacheDir string

// Client is the shared HTTP client (15s timeout, proxy env honored via the
// default transport) used for remote schema documents.
var Client = &http.Client{Timeout: 15 * time.Second}

// memo keeps the documents loaded in this process: the same URL is fetched
// once even though the schema is loaded for the form model and compiled for
// validation. The stored note reports how the document was served.
var (
	mu   sync.Mutex
	memo = map[string]memoEntry{}
)

type memoEntry struct {
	data []byte
	note string
}

// entry is the cache sidecar of one remote document.
type entry struct {
	URL          string    `json:"url"`
	ETag         string    `json:"etag,omitempty"`
	LastModified string    `json:"last_modified,omitempty"`
	FetchedAt    time.Time `json:"fetched_at"`
}

// CanonicalLoc normalizes a schema location to an absolute URL:
// http(s):// and file:// pass through, bare paths become file://<abs>.
func CanonicalLoc(loc string) (string, error) {
	u, err := url.Parse(loc)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "http", "https", "file":
		return u.String(), nil
	case "":
		abs, err := filepath.Abs(loc)
		if err != nil {
			return "", err
		}
		return "file://" + abs, nil
	}
	return "", fmt.Errorf("unsupported schema location %q", loc)
}

// Load returns the bytes at loc:
//   - http:// or https:// → GET via Client (through the cache: a fresh entry
//     skips the request, a stale one is revalidated and an unreachable origin
//     falls back to the cached copy); non-2xx → "GET <loc>: status <code>"
//   - file:// → path taken from the URL
//   - anything else → treated as a local filesystem path
//
// HTTP bodies larger than MaxBytes fail with "<loc>: response exceeds size
// limit".
func Load(loc string) ([]byte, error) {
	if isHTTP(loc) {
		mu.Lock()
		e, ok := memo[loc]
		mu.Unlock()
		if ok {
			return e.data, nil
		}
		data, note, err := loadHTTP(loc)
		if err != nil {
			return nil, err
		}
		mu.Lock()
		memo[loc] = memoEntry{data: data, note: note}
		mu.Unlock()
		return data, nil
	}
	u, err := url.Parse(loc)
	if err == nil && u.Scheme == "file" {
		return os.ReadFile(u.Path)
	}
	return os.ReadFile(loc)
}

// Note returns the user-facing note about how the last Load of loc was
// served (an icons.Warn-prefixed note when the origin was unreachable and the
// cached copy was used), or "" for a live fetch, a cache hit or a local file.
func Note(loc string) string {
	mu.Lock()
	defer mu.Unlock()
	return memo[loc].note
}

func isHTTP(loc string) bool {
	return strings.HasPrefix(loc, "http://") || strings.HasPrefix(loc, "https://")
}

// loadHTTP serves loc from the cache when fresh, else revalidates it.
func loadHTTP(loc string) ([]byte, string, error) {
	cached, body, ok := readCache(loc)
	if ok && time.Since(cached.FetchedAt) < CacheTTL {
		return body, "", nil // fresh: no request at all
	}

	req, err := http.NewRequest(http.MethodGet, loc, nil)
	if err != nil {
		return nil, "", err
	}
	if ok {
		if cached.ETag != "" {
			req.Header.Set("If-None-Match", cached.ETag)
		}
		if cached.LastModified != "" {
			req.Header.Set("If-Modified-Since", cached.LastModified)
		}
	}
	resp, err := Client.Do(req)
	if err != nil {
		return staleOrErr(loc, body, ok, err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusNotModified && ok:
		if etag := resp.Header.Get("ETag"); etag != "" {
			cached.ETag = etag
		}
		if lm := resp.Header.Get("Last-Modified"); lm != "" {
			cached.LastModified = lm
		}
		cached.FetchedAt = time.Now()
		writeEntry(loc, cached)
		return body, "", nil
	case resp.StatusCode >= 200 && resp.StatusCode <= 299:
	default:
		// Origin-side failures fall back to the cached copy; other statuses
		// (404 and friends) stay hard errors so a wrong URL is not masked.
		if ok && resp.StatusCode >= 500 {
			return body, fmt.Sprintf("%s using the cached schema: GET %s: status %d", icons.Warn, loc, resp.StatusCode), nil
		}
		return nil, "", fmt.Errorf("GET %s: status %d", loc, resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxBytes+1))
	if err != nil {
		return staleOrErr(loc, body, ok, err)
	}
	if int64(len(data)) > MaxBytes {
		return nil, "", fmt.Errorf("%s: response exceeds size limit", loc)
	}
	storeBody(loc, data)
	writeEntry(loc, entry{
		URL:          loc,
		ETag:         resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified"),
		FetchedAt:    time.Now(),
	})
	return data, "", nil
}

// staleOrErr yields the cached copy when there is one, else the error.
func staleOrErr(loc string, body []byte, ok bool, err error) ([]byte, string, error) {
	if ok {
		return body, fmt.Sprintf("%s using the cached schema: %v", icons.Warn, err), nil
	}
	return nil, "", err
}

// --- cache files ---------------------------------------------------------

// cacheDir resolves the cache root, "" when caching is unavailable.
func cacheDir() string {
	if CacheDir != "" {
		return CacheDir
	}
	if d := os.Getenv("JSE_CACHE_DIR"); d != "" {
		return d
	}
	if d, err := os.UserCacheDir(); err == nil && d != "" {
		return filepath.Join(d, "jse")
	}
	return ""
}

// cachePath returns the entry stem for loc, "" when caching is unavailable.
func cachePath(loc string) string {
	dir := cacheDir()
	if dir == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(loc))
	return filepath.Join(dir, "fetch", hex.EncodeToString(sum[:]))
}

// readCache returns the cached entry and body of loc (ok=false when absent,
// unreadable or not matching loc).
func readCache(loc string) (entry, []byte, bool) {
	p := cachePath(loc)
	if p == "" {
		return entry{}, nil, false
	}
	mb, err := os.ReadFile(p + ".json")
	if err != nil {
		return entry{}, nil, false
	}
	var e entry
	if json.Unmarshal(mb, &e) != nil || e.URL != loc {
		return entry{}, nil, false
	}
	body, err := os.ReadFile(p + ".body")
	if err != nil {
		return entry{}, nil, false
	}
	return e, body, true
}

// storeBody writes the document body; the sidecar is written by writeEntry
// afterwards, so a body without a sidecar counts as a miss.
func storeBody(loc string, body []byte) {
	if p := cachePath(loc); p != "" {
		writeFileAtomic(p+".body", body)
	}
}

// writeEntry writes the cache sidecar of loc.
func writeEntry(loc string, e entry) {
	p := cachePath(loc)
	if p == "" {
		return
	}
	data, err := json.Marshal(e)
	if err != nil {
		return
	}
	if os.MkdirAll(filepath.Dir(p), 0o700) != nil {
		return
	}
	writeFileAtomic(p+".json", data)
}

// writeFileAtomic writes data to path via a temp file + rename (concurrent
// jse instances), best-effort: any failure leaves the cache untouched.
func writeFileAtomic(path string, data []byte) {
	if os.MkdirAll(filepath.Dir(path), 0o700) != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return
	}
	os.Chmod(tmp.Name(), 0o600)
	if os.Rename(tmp.Name(), path) != nil {
		os.Remove(tmp.Name())
	}
}
