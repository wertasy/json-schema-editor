package main

// Version metadata: ldflags-injected release builds versus the build info of
// `go install module@version` and plain checkout builds.

import (
	"runtime/debug"
	"testing"
)

func TestWithBuildInfo(t *testing.T) {
	vcs := &debug.BuildInfo{
		Main: debug.Module{Version: "v0.2.1", Sum: "h1:abc"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "b2b73a4f0e3c1d2a9b8c7d6e5f40312233445566"},
			{Key: "vcs.time", Value: "2026-10-18T13:18:54Z"},
		},
	}

	// ldflags values win over whatever the build info carries
	if v, c, d := withBuildInfo("v9.9.9", "abc1234", "2026-01-01T00:00:00Z", vcs); v != "v9.9.9" || c != "abc1234" || d != "2026-01-01T00:00:00Z" {
		t.Fatalf("ldflags metadata must win, got %q %q %q", v, c, d)
	}

	// go install pkg@version: the module version, no VCS stamps
	installed := &debug.BuildInfo{Main: debug.Module{Version: "v0.2.1"}}
	if v, c, d := withBuildInfo("dev", "none", "unknown", installed); v != "v0.2.1" || c != "none" || d != "unknown" {
		t.Fatalf("go install = %q %q %q, want v0.2.1 none unknown", v, c, d)
	}

	// a plain checkout build keeps the "(devel)" version but gains the stamps
	checkout := &debug.BuildInfo{
		Main:     debug.Module{Version: "(devel)"},
		Settings: vcs.Settings,
	}
	if v, c, d := withBuildInfo("dev", "none", "unknown", checkout); v != "dev" || c != "b2b73a4" || d != "2026-10-18T13:18:54Z" {
		t.Fatalf("checkout build = %q %q %q, want dev b2b73a4 <time>", v, c, d)
	}

	// no build info at all (runtime without it) leaves the defaults alone
	if v, c, d := withBuildInfo("dev", "none", "unknown", nil); v != "dev" || c != "none" || d != "unknown" {
		t.Fatalf("nil info = %q %q %q", v, c, d)
	}

	// a short/absent revision must not produce a truncated commit
	if _, c, _ := withBuildInfo("dev", "none", "unknown",
		&debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "abc"}}}); c != "none" {
		t.Fatalf("short revision = %q, want none", c)
	}
}

func TestVersionLine(t *testing.T) {
	cases := []struct {
		v, c, d, want string
	}{
		{"v0.2.1", "b2b73a4", "2026-10-18T13:18:54Z", "jse v0.2.1 (commit b2b73a4, built 2026-10-18T13:18:54Z)"},
		{"v0.2.1", "none", "unknown", "jse v0.2.1"},
		{"dev", "b2b73a4", "unknown", "jse dev (commit b2b73a4)"},
		{"dev", "none", "2026-10-18T13:18:54Z", "jse dev (built 2026-10-18T13:18:54Z)"},
	}
	for _, c := range cases {
		if got := versionLine(c.v, c.c, c.d); got != c.want {
			t.Errorf("versionLine(%q, %q, %q) = %q, want %q", c.v, c.c, c.d, got, c.want)
		}
	}
}
