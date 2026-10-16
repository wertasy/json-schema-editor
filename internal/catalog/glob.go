package catalog

import (
	"path"
	"strings"
)

// matchGlob implements the catalog fileMatch semantics (aligned with
// VSCode's schema associations), case-insensitively:
//
//   - patterns without "/" match the file NAME only ("package.json",
//     "tsconfig*.json", "*.srv.json");
//   - patterns with "/" match the FULL path segment-wise, anchored to the
//     tail of the path at a segment boundary: "**" spans zero or more
//     directories ("**/.cdf/config.yaml"), while "*" and "?" stay within
//     one segment (".platform/applications.yml" requires that directory
//     right above the file).
//
// Within-segment wildcards follow path.Match.
func matchGlob(pattern, base, full string) bool {
	pattern = strings.ToLower(pattern)
	if !strings.Contains(pattern, "/") {
		ok, err := path.Match(pattern, strings.ToLower(base))
		return err == nil && ok
	}
	segPat := strings.Split(pattern, "/")
	segPath := strings.Split(strings.ToLower(strings.TrimPrefix(full, "/")), "/")
	return matchSegs(segPat, segPath)
}

// matchSegs matches pat against a tail of p (segment lists): the pattern
// must anchor at a segment boundary, anything before it is free. "**"
// consumes zero or more segments.
func matchSegs(pat, p []string) bool {
	if len(pat) == 0 {
		return true
	}
	for start := range len(p) {
		if matchAnchored(pat, p[start:]) {
			return true
		}
	}
	return false
}

// matchAnchored matches pat against exactly p: "**" consumes zero or more
// segments, every other segment must path.Match the same-position segment.
func matchAnchored(pat, p []string) bool {
	i := 0
	for pi, seg := range pat {
		if seg == "**" {
			rest := pat[pi+1:]
			for skip := range len(p) - i + 1 {
				if matchAnchored(rest, p[i+skip:]) {
					return true
				}
			}
			return false
		}
		if i >= len(p) {
			return false
		}
		ok, err := path.Match(seg, p[i])
		if err != nil || !ok {
			return false
		}
		i++
	}
	return i == len(p)
}
