// Package catalog integrates the SchemaStore catalog (www.schemastore.org):
// it loads the public schema directory and matches a config file against the
// directory's fileMatch patterns, the way VSCode suggests schemas for
// otherwise unschemed JSON files.
package catalog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"json-schema-editor/internal/fetch"
)

// DefaultURL is the SchemaStore catalog endpoint.
const DefaultURL = "https://www.schemastore.org/api/json/catalog.json"

// Entry is one schema directory listing.
type Entry struct {
	Name        string
	Description string
	URL         string
	FileMatch   []string
}

// Loc returns $JSE_CATALOG_URL when set, else DefaultURL.
func Loc() string {
	if u := os.Getenv("JSE_CATALOG_URL"); u != "" {
		return u
	}
	return DefaultURL
}

// wire is the catalog.json shape consumed here; everything else
// (versions, ...)
type wire struct {
	Schemas []struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		URL         string   `json:"url"`
		FileMatch   []string `json:"fileMatch"`
	} `json:"schemas"`
}

// Load fetches and parses the catalog at loc — anything fetch.Load takes
// (https URL or local path), including its 24h cache and offline fallback.
// Entries without a URL are skipped; per-version listings are ignored (the
// latest URL is the entry's url).
func Load(loc string) ([]Entry, error) {
	data, err := fetch.Load(loc)
	if err != nil {
		return nil, err
	}
	var w wire
	if err := json.Unmarshal(data, &w); err != nil {
		return nil, fmt.Errorf("parse catalog %s: %w", loc, err)
	}
	entries := make([]Entry, 0, len(w.Schemas))
	for _, s := range w.Schemas {
		if s.URL == "" {
			continue
		}
		entries = append(entries, Entry{
			Name:        s.Name,
			Description: s.Description,
			URL:         s.URL,
			FileMatch:   s.FileMatch,
		})
	}
	return entries, nil
}

// Match returns the entries whose fileMatch patterns match the file, in
// catalog order, deduplicated by URL (the catalog lists some schemas under
// aliasing names sharing one URL). path is the config's absolute path.
func Match(entries []Entry, path string) []Entry {
	base := filepath.Base(path)
	full := filepath.ToSlash(path)
	var out []Entry
	seen := make(map[string]bool, len(entries))
	for _, e := range entries {
		if seen[e.URL] {
			continue
		}
		for _, pattern := range e.FileMatch {
			if matchGlob(pattern, base, full) {
				seen[e.URL] = true
				out = append(out, e)
				break
			}
		}
	}
	return out
}
