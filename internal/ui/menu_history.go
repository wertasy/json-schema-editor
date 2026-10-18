package ui

// Document history for the menu TUI: dispatchKey snapshots the document
// before each key and pushes the snapshot when the key changed it, so one
// undo step reverts exactly one key's worth of edits.

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/wertasy/json-schema-editor/internal/jsondoc"
)

// maxHistory caps each history stack; the oldest snapshot is dropped first.
const maxHistory = 100

// pushUndo records pre (the document before an edit) and drops the oldest
// entry beyond the cap. A new edit forks the timeline: the redo branch dies.
func (m *listModel) pushUndo(pre *jsondoc.Node) {
	m.undoStack = append(m.undoStack, pre)
	if len(m.undoStack) > maxHistory {
		m.undoStack = m.undoStack[1:]
	}
	m.redoStack = nil
}

// undo restores the document as it was before the last change, keeping the
// current state for redo.
func (m *listModel) undo() {
	if len(m.undoStack) == 0 {
		m.notice = "nothing to undo"
		return
	}
	m.redoStack = append(m.redoStack, m.doc.Clone())
	*m.doc = *m.undoStack[len(m.undoStack)-1]
	m.undoStack = m.undoStack[:len(m.undoStack)-1]
	m.notice = "undone"
	m.repairStack()
}

// redo re-applies the most recently undone change.
func (m *listModel) redo() {
	if len(m.redoStack) == 0 {
		m.notice = "nothing to redo"
		return
	}
	m.undoStack = append(m.undoStack, m.doc.Clone())
	*m.doc = *m.redoStack[len(m.redoStack)-1]
	m.redoStack = m.redoStack[:len(m.redoStack)-1]
	m.notice = "redone"
	m.repairStack()
}

// repairStack drops navigation levels whose path no longer resolves in the
// restored document (undo can shrink an array or remove a container the user
// is inside) and clamps the cursor to the surviving rows. The root level
// (nil path) always stays.
func (m *listModel) repairStack() {
	for len(m.stack) > 1 {
		p := m.top().path
		if len(p) == 0 {
			break
		}
		if _, ok := m.doc.Get(p); ok {
			break
		}
		m.stack = m.stack[:len(m.stack)-1]
	}
	m.clampCursor(menuRows(m))
}

// typing reports whether a text input has focus (in-place editor, raw
// textarea, search, add-key input): q/@/. are ordinary characters
// there and history keys must not fire.
func (m *listModel) typing() bool {
	return m.edit != nil || m.text != nil || m.search.active || m.add != nil
}

func (m *listModel) listMode() bool {
	return m.confirm == nil && m.issues == nil && m.errs == nil &&
		m.sel == nil && !m.help && !m.typing()
}

// applyHistoryKey handles u (undo) and ctrl+r (redo); consumed reports
// whether the key was a history key.
func (m *listModel) applyHistoryKey(msg tea.KeyMsg) bool {
	switch msg.String() {
	case "u":
		m.undo()
	case "ctrl+r":
		m.redo()
	default:
		return false
	}
	return true
}
