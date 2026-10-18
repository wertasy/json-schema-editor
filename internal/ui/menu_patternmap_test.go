package ui

// patternProperties maps (KMap with KeyPattern): document keys are the rows,
// they count as known members, drills open the value schema's menu, and
// a-added keys must match the pattern.

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// jobsSchema is the GitHub-workflow jobs shape: one pattern and
// additionalProperties: false. Values are plain objects, so a drill opens
// the object menu directly.
const jobsSchema = `{
  "type": "object",
  "properties": {
    "jobs": {
      "type": "object",
      "patternProperties": {
        "^[_a-zA-Z][a-zA-Z0-9_-]*$": {
          "type": "object",
          "properties": {"runs-on": {"type": "string"}}
        }
      },
      "additionalProperties": false
    }
  }
}`

// TestPatternMapRowsAndEdit: the job keys render as plain rows (no unknown
// warning, no issue count), and drilling a row opens its value schema.
func TestPatternMapRowsAndEdit(t *testing.T) {
	root := mustSchema(t, jobsSchema)
	doc := tobj(tmem("jobs", tobj(
		tmem("build", tobj(tmem("runs-on", tstr("ubuntu-latest")))),
		tmem("release", tobj(tmem("runs-on", tstr("ubuntu-latest")))))))
	m := newListModel(root, doc, Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if len(m.issuesNow) != 0 {
		t.Fatalf("issues = %+v, want none", m.issuesNow)
	}

	press(m, "enter") // jobs
	v := m.View()
	for _, want := range []string{"build", "release"} {
		if !contains(v, want) {
			t.Fatalf("jobs map must list %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "(not in schema)") {
		t.Fatalf("pattern-matched keys are known members:\n%s", v)
	}

	press(m, "enter") // build (cursor 0)
	if m.top().kind != menuObject || !contains(m.View(), "runs-on") {
		t.Fatalf("build must drill into the value schema's object menu:\n%s", m.View())
	}
	if got := strAt(t, doc, "jobs", "build", "runs-on"); got != "ubuntu-latest" {
		t.Fatalf("jobs.build.runs-on = %q, want ubuntu-latest", got)
	}
}

// TestPatternMapAddKeyMatchesPattern: a-added keys are refused unless they
// match the pattern; the accepted key lands in the doc and opens its menu.
func TestPatternMapAddKeyMatchesPattern(t *testing.T) {
	root := mustSchema(t, jobsSchema)
	m := newListModel(root, tobj(tmem("jobs", tobj())), Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	press(m, "enter")                          // jobs
	press(m, "a", "1", "b", "a", "d", "enter") // leading digit: no match
	if m.add == nil || !strings.Contains(m.add.err, "key must match") {
		t.Fatalf("non-matching key must be refused, add=%+v", m.add)
	}
	if _, ok := m.doc.Get([]any{"jobs", "1bad"}); ok {
		t.Fatal("refused key must not reach the doc")
	}
	press(m, "esc")

	press(m, "a", "d", "e", "p", "l", "o", "y", "enter")
	if m.add != nil {
		t.Fatalf("matching key must be accepted, err=%q", m.add.err)
	}
	n, ok := m.doc.Get([]any{"jobs", "deploy"})
	if !ok || n == nil || n.Object == nil {
		t.Fatalf("jobs.deploy missing or not an object: %+v", n)
	}
	if m.top().kind != menuObject || !contains(m.View(), "runs-on") {
		t.Fatalf("must land in the new job's object menu:\n%s", m.View())
	}
}

// TestMapVariantKeysAreKnown: a oneOf whose branch is an additionalProperties
// map (workflow env / matrix / step with) matches an object value, so the
// keys of that object are map members, not unknown keys.
func TestMapVariantKeysAreKnown(t *testing.T) {
	root := mustSchema(t, `{
  "type": "object",
  "properties": {
    "env": {
      "oneOf": [
        {"type": "object", "additionalProperties": {"oneOf": [
          {"type": "string"}, {"type": "number"}, {"type": "boolean"}]}},
        {"type": "string"}
      ]
    }
  }
}`)
	doc := tobj(tmem("env", tobj(tmem("GOOS", tstr("linux")), tmem("GOARCH", tstr("amd64")))))
	m := newListModel(root, doc, Options{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if len(m.issuesNow) != 0 {
		t.Fatalf("map-variant keys must be known, issues = %+v", m.issuesNow)
	}
	if v := m.View(); !contains(v, "env") || strings.Contains(v, "(not in schema)") {
		t.Fatalf("env row must render as a map:\n%s", v)
	}
}
