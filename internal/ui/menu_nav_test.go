package ui

// ctrl+o / ctrl+i navigation history tests: drills, level pops and jumps
// record the menu stack they leave behind; the trail traverses both ways,
// esc pops consume their matching drill entry, and restoring drops stale
// one-level return parks.

import (
	"testing"

	"github.com/wertasy/json-schema-editor/internal/jsondoc"
)

func TestNavDrillCycle(t *testing.T) {
	m := newListModel(testModel(), testDoc(), Options{})
	press(m, "down", "down", "enter") // drill into Server
	if len(m.stack) != 2 || jsondoc.PathString(m.top().path) != "$.server" {
		t.Fatalf("drill landed on %v (stack %d)", m.top().path, len(m.stack))
	}

	// ctrl+o undoes the drill; a second one has nothing older (esc's matching
	// entry is not duplicated: the drill is the only back step so far)
	press(m, "ctrl+o")
	if len(m.stack) != 1 {
		t.Fatalf("ctrl+o after drill: stack = %d, want 1", len(m.stack))
	}
	press(m, "ctrl+o")
	if m.notice != "no earlier location" || len(m.stack) != 1 {
		t.Fatalf("ctrl+o at trail start: notice = %q, stack = %d", m.notice, len(m.stack))
	}

	// ctrl+i replays the drill; tab is the same key (bubbletea reports
	// ctrl+i as tab) and must work as the forward step too
	press(m, "ctrl+i")
	if len(m.stack) != 2 || jsondoc.PathString(m.top().path) != "$.server" {
		t.Fatalf("ctrl+i after drill: stack = %d, top %v", len(m.stack), m.top().path)
	}
	press(m, "esc")
	press(m, "tab")
	if len(m.stack) != 2 || jsondoc.PathString(m.top().path) != "$.server" {
		t.Fatalf("tab forward after esc pop: stack = %d, top %v", len(m.stack), m.top().path)
	}

	// esc's pop consumed the drill entry, so back is empty right after
	// landing on the parent; forward survives the cycle
	press(m, "esc")
	press(m, "ctrl+o")
	if m.notice != "no earlier location" {
		t.Fatalf("esc must consume its drill entry, notice = %q", m.notice)
	}
	press(m, "ctrl+i")
	if len(m.stack) != 2 {
		t.Fatalf("forward survived the esc cycle: stack = %d", len(m.stack))
	}

	// the trail restores cursor memory: back lands on the drilled row
	press(m, "ctrl+o")
	if m.top().cursor != 2 {
		t.Fatalf("cursor after back = %d, want 2 (Server row)", m.top().cursor)
	}
}

func TestNavCursorJumpTrail(t *testing.T) {
	m := newListModel(testModel(), testDoc(), Options{})
	press(m, "G")
	last := m.top().cursor
	press(m, "up")
	mid := m.top().cursor
	if last == mid {
		t.Fatal("fixture must have more than two rows")
	}

	// G records the row it left: ctrl+o returns to the pre-G row; plain
	// moves ride along unrecorded, so tab lands where the back step was
	// taken (mid), not on G's target (last)
	press(m, "ctrl+o")
	if m.top().cursor != 0 {
		t.Fatalf("ctrl+o after G: cursor = %d, want 0", m.top().cursor)
	}
	press(m, "tab")
	if m.top().cursor != mid {
		t.Fatalf("forward after G back-step: cursor = %d, want %d", m.top().cursor, mid)
	}
	press(m, "ctrl+o")
	if m.top().cursor != 0 {
		t.Fatalf("second back step: cursor = %d, want 0", m.top().cursor)
	}
}

func TestNavAnchorJumpTrail(t *testing.T) {
	m := aliasJumpModel(t, "base: &x\n  p: 1\ncopy: *x\nagain: *x\n", "base", "copy", "again")
	jumpCursorTo(t, m, []any{"copy"})
	press(m, "g", "a") // jump to the anchor carrier
	if len(m.stack) != 2 || jsondoc.PathString(m.top().path) != "$.base" {
		t.Fatalf("ga landed on %v (stack %d)", m.top().path, len(m.stack))
	}

	// ctrl+o walks back to the jump origin; the one-level esc return park is
	// dropped (the trail supersedes it — esc must pop normally afterwards)
	press(m, "ctrl+o")
	if len(m.stack) != 1 || m.jumpOrigin != nil {
		t.Fatalf("ctrl+o after ga: stack = %d, jumpOrigin set = %v", len(m.stack), m.jumpOrigin != nil)
	}
	if got := menuRows(m)[m.top().cursor].path; !pathEq(got, []any{"copy"}) {
		t.Fatalf("cursor after ctrl+o = %v, want $.copy", jsondoc.PathString(got))
	}

	press(m, "tab") // forward to the anchor again
	if len(m.stack) != 2 || jsondoc.PathString(m.top().path) != "$.base" {
		t.Fatalf("forward after ga back-step: stack = %d, top %v", len(m.stack), m.top().path)
	}
	press(m, "esc") // plain pop: landing equals the trail's back top
	if len(m.stack) != 1 {
		t.Fatalf("esc after forward: stack = %d, want 1", len(m.stack))
	}
	// the root level of the forward mark is the post-jump stack, whose
	// cursor the rebuild reset to the base row — restored faithfully
	if got := jsondoc.PathString(menuRows(m)[m.top().cursor].path); got != "$.base" {
		t.Fatalf("cursor after esc = %s, want $.base", got)
	}
}

func TestNavIssueJumpClearsParks(t *testing.T) {
	m := newListModel(testModel(), testDoc(), Options{LoadErrors: []string{
		"at '$.mode': value must be one of 'http', 'grpc', 'tcp'",
	}})
	press(m, "enter") // status row → issue list
	press(m, "enter") // jump to the offending field
	// mode is a scalar: the jump lands on its parent menu with the row
	// highlighted (no extra menu level)
	if len(m.stack) != 1 || m.top().cursor != 2 {
		t.Fatalf("issue jump landed on %v (stack %d, cursor %d)", m.top().path, len(m.stack), m.top().cursor)
	}
	if m.issuesBack == nil {
		t.Fatal("issue jump must park the list for esc")
	}

	// ctrl+o supersedes the parked return: restoring must not leave esc
	// returning to a list two relocations later
	m.search.parked = true // stale park from an earlier search jump
	press(m, "ctrl+o")
	if len(m.stack) != 1 || m.issuesBack != nil || m.search.parked {
		t.Fatalf("ctrl+o after issue jump: stack = %d, issuesBack = %v, parked = %v",
			len(m.stack), m.issuesBack != nil, m.search.parked)
	}
	if m.top().cursor != 0 {
		t.Fatalf("cursor after back = %d, want 0 (status row)", m.top().cursor)
	}
	press(m, "tab")
	if m.top().cursor != 2 {
		t.Fatalf("forward after issue back-step: cursor = %d, want 2 (mode row)", m.top().cursor)
	}
}
