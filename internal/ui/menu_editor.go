package ui

// External raw editing: the root raw row, values under schema-less keys and
// newly added schema-less members suspend the TUI and run $EDITOR (falling
// back to $VISUAL, then vi). The temp file holds the whole document for the
// root row, or just one value otherwise; its syntax follows Options.Raw
// (JSON or YAML). A successful exit parses and applies the text as one undo
// step; invalid syntax keeps the old document and surfaces a notice.

import (
	"bytes"
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// rawEditorDoneMsg arrives after the external editor exits.
type rawEditorDoneMsg struct {
	path string
	err  error
}

// externalEdit is the target of a pending $EDITOR run: the value at target.
// A non-negative insertIdx splices a new member instead of Set.
type externalEdit struct {
	target    []any
	insertIdx int
	path      string // temp file the editor is working on
}

// editRawExternal writes the whole document to a temp file in the document's
// raw syntax and launches $EDITOR. tea.ExecProcess restores the terminal for
// the editor and repaints the TUI afterwards.
func (m *listModel) editRawExternal() tea.Cmd {
	var buf bytes.Buffer
	if err := m.opts.encodeRawDoc(&buf, m.doc); err != nil {
		m.notice = "editor: " + err.Error()
		return nil
	}
	return m.editExternalJSON(buf.Bytes(), nil)
}

// editValueExternal edits one value in $EDITOR. A new member
// (insertIdx >= 0) starts with an empty file; an existing value is written
// in the document's raw syntax.
func (m *listModel) editValueExternal(path []any, insertIdx int) tea.Cmd {
	var buf bytes.Buffer
	if cur, ok := m.doc.Get(path); ok && cur != nil && !cur.Null {
		if err := m.opts.encodeRawValue(&buf, cur); err != nil {
			m.notice = "editor: " + err.Error()
			return nil
		}
	}
	edit := &externalEdit{target: clonePath(path), insertIdx: insertIdx}
	return m.editExternalJSON(buf.Bytes(), edit)
}

// editExternalJSON writes data to a temp file and launches $EDITOR. edit is
// nil for the whole-document editor.
func (m *listModel) editExternalJSON(data []byte, edit *externalEdit) tea.Cmd {
	f, err := os.CreateTemp("", "jse-*"+m.opts.rawExt())
	if err != nil {
		m.notice = "editor: " + err.Error()
		return nil
	}
	path := f.Name()
	if _, err := f.Write(data); err != nil {
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
	if edit != nil {
		edit.path = path
		m.ext = edit
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

// applyExternalEdit parses the editor's temp file and applies it as one undo
// step. A failed run, unreadable file or invalid JSON leaves the document
// untouched.
func (m *listModel) applyExternalEdit(msg rawEditorDoneMsg) (tea.Model, tea.Cmd) {
	defer os.Remove(msg.path)
	edit := m.ext
	m.ext = nil
	if msg.err != nil {
		m.notice = "editor: " + msg.err.Error()
		return m, nil
	}
	data, err := os.ReadFile(msg.path)
	if err != nil {
		m.notice = "editor: " + err.Error()
		return m, nil
	}
	if edit != nil {
		return m.applyExternalValue(edit, data)
	}

	pre := m.doc.Clone()
	nd, err := m.opts.decodeRawDoc(data)
	if err != nil {
		m.notice = "editor: invalid " + m.opts.rawLabel() + ": " + err.Error()
		return m, nil
	}
	if nodesEqual(pre, nd) {
		m.notice = "Raw " + m.opts.rawLabel() + " unchanged"
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
	m.notice = "Raw " + m.opts.rawLabel() + " edited"
	return m, nil
}

// applyExternalValue installs the editor's output at edit.target. An empty
// file removes an existing key and cancels a new member.
func (m *listModel) applyExternalValue(edit *externalEdit, data []byte) (tea.Model, tea.Cmd) {
	s := strings.TrimSpace(string(data))
	pre := m.doc.Clone()
	if s == "" {
		if edit.insertIdx < 0 {
			m.doc.Remove(edit.target)
		}
		if nodesEqual(pre, m.doc) {
			m.notice = "Value unchanged"
			return m, nil
		}
		m.pushUndo(pre)
		m.afterExternalValueEdit()
		m.notice = "Value removed"
		return m, nil
	}

	n, err := m.opts.decodeRawValue([]byte(s))
	if err != nil {
		m.notice = "editor: invalid " + m.opts.rawLabel() + ": " + err.Error()
		return m, nil
	}
	if edit.insertIdx >= 0 {
		// positioned add (a/A): splice the new member at its member index
		parent := edit.target[:len(edit.target)-1]
		key := edit.target[len(edit.target)-1].(string)
		m.doc.InsertMember(parent, key, edit.insertIdx, n)
	} else {
		m.doc.Set(edit.target, n)
	}
	if nodesEqual(pre, m.doc) {
		m.notice = "Value unchanged"
		return m, nil
	}
	m.pushUndo(pre)
	m.afterExternalValueEdit()
	m.notice = "Value edited"
	return m, nil
}

func (m *listModel) afterExternalValueEdit() {
	m.refreshVariantMenu()
	m.refreshIssues()
	m.clampCursor(menuRows(m))
}
