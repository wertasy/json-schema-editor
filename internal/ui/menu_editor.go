package ui

// External raw-JSON editing: the root "< Edit raw JSON >" row suspends the
// TUI and runs $EDITOR (falling back to $VISUAL, then vi) on a temp file
// holding the whole document. On a successful exit the file is parsed and
// replaces the document as one undo step; invalid JSON keeps the old document
// and surfaces a notice.

import (
	"bytes"
	"os"
	"os/exec"

	tea "github.com/charmbracelet/bubbletea"

	"json-schema-editor/internal/jsondoc"
)

// rawEditorDoneMsg arrives after the external editor exits.
type rawEditorDoneMsg struct {
	path string
	err  error
}

// editRawExternal writes the document to a temp file and launches $EDITOR.
// tea.ExecProcess restores the terminal for the editor and repaints the TUI
// afterwards.
func (m *listModel) editRawExternal() tea.Cmd {
	var buf bytes.Buffer
	if err := m.doc.Write(&buf); err != nil {
		m.notice = "editor: " + err.Error()
		return nil
	}
	f, err := os.CreateTemp("", "jse-*.json")
	if err != nil {
		m.notice = "editor: " + err.Error()
		return nil
	}
	path := f.Name()
	if _, err := f.Write(buf.Bytes()); err != nil {
		f.Close()
		os.Remove(path)
		m.notice = "editor: " + err.Error()
		return nil
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		m.notice = "editor: " + err.Error()
		return nil
	}

	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = os.Getenv("VISUAL")
	}
	if editor == "" {
		editor = "vi"
	}
	// sh -c keeps EDITOR values with arguments ("code --wait") working while
	// the temp path is passed as a quoted positional argument.
	cmd := exec.Command("sh", "-c", editor+` "$1"`, "sh", path)
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return rawEditorDoneMsg{path: path, err: err}
	})
}

// applyExternalEdit parses the editor's temp file and swaps it in as one undo
// step. A failed run, unreadable file or invalid JSON leaves the document
// untouched.
func (m *listModel) applyExternalEdit(msg rawEditorDoneMsg) (tea.Model, tea.Cmd) {
	defer os.Remove(msg.path)
	if msg.err != nil {
		m.notice = "editor: " + msg.err.Error()
		return m, nil
	}
	data, err := os.ReadFile(msg.path)
	if err != nil {
		m.notice = "editor: " + err.Error()
		return m, nil
	}
	nd, err := jsondoc.Parse(bytes.NewReader(data))
	if err != nil {
		m.notice = "editor: invalid JSON: " + err.Error()
		return m, nil
	}
	pre := m.doc.Clone()
	if nodesEqual(pre, nd) {
		m.notice = "Raw JSON unchanged"
		return m, nil
	}
	// Edit in place: the live tree is shared with the session and the
	// caller; replacing the pointer would leave their copy stale (undo
	// already relies on in-place assignment).
	*m.doc = *nd
	m.pushUndo(pre)
	m.refreshVariantMenu()
	m.refreshIssues()
	m.clampCursor(menuRows(m))
	m.notice = "Raw JSON edited"
	return m, nil
}
