package tui

import (
	"fmt"
	"strings"
	"time"

	"tasks-remote/internal/storage"
	"tasks-remote/internal/syncsetup"
)

func (m model) View() string {
	var b strings.Builder
	t := currentTheme()
	title := "tasks-remote"
	if m.width > 0 && m.width < 56 {
		title = "tasks"
	}
	b.WriteString(t.header.Render(title))
	if m.status.Initialized {
		b.WriteString(" ")
		b.WriteString(syncBadge(m.status, m.config))
	}
	b.WriteString("\n")
	if m.busy != "" {
		b.WriteString(t.muted.Render(m.busy))
		b.WriteString("\n")
		return b.String()
	}
	if m.err != "" {
		b.WriteString(t.error.Render(m.err))
		b.WriteString("\n")
	}
	if m.notice != "" {
		b.WriteString(t.notice.Render(m.notice))
		b.WriteString("\n")
	}
	switch m.mode {
	case modeLoading:
		b.WriteString("Loading...\n")
	case modeSetup:
		b.WriteString("No local Task Collection is available for this database.\n\n")
		b.WriteString("[c] Create New Task Collection\n")
		b.WriteString("[r] Restore Existing Task Collection\n")
		b.WriteString("[q] Quit\n")
	case modeLocked:
		b.WriteString("Locked Device. Sensitive Task Data is hidden until unlock.\n\n")
		b.WriteString(fmt.Sprintf("Sync Health: %s\n\n", syncHealthText(m.status, m.config)))
		b.WriteString("[u] Unlock  [r] Restore  [s] Sync Health  [g] Sync Setup/Login  [q] Quit\n")
	case modeUnlock, modeCreateSecret, modeCreateConfirm, modeSearch, modeRestoreSecret, modeRestoreConfirm:
		b.WriteString(renderInputs(m.inputs, m.focus))
		b.WriteString("\nenter: continue  esc: cancel\n")
	case modeSyncSetup:
		b.WriteString("Local Sync Setup\n")
		b.WriteString("Type: google or dir. Google uses Drive app data; dir is for local testing.\n\n")
		b.WriteString(renderInputs(m.inputs, m.focus))
		b.WriteString("\n")
		b.WriteString(renderKeyHints("[enter] save", "[tab] next", "[ctrl+f] pick credentials", "[ctrl+d] pick directory", "[esc] cancel"))
		b.WriteString("\n")
	case modeFilePicker:
		b.WriteString(m.renderFilePicker())
	case modeWork:
		b.WriteString(m.renderWork())
	case modeForm:
		if m.formID == "" {
			b.WriteString("Create Task\n\n")
		} else {
			b.WriteString("Edit Task\n\n")
		}
		b.WriteString(renderInputs(m.inputs, m.focus))
		b.WriteString("\nenter: next/save  tab: next  esc: cancel\n")
	case modeDetail:
		b.WriteString(m.renderDetail())
	case modeDeleteConfirm:
		if task, ok := selectedTask(m.visibleTasks(), m.selected); ok {
			b.WriteString(fmt.Sprintf("Delete task %q?\n\n[y] Delete  [n] Cancel\n", task.Title))
		}
	case modeConflicts:
		b.WriteString(m.renderConflicts())
	case modeConflictDetail:
		b.WriteString(m.renderConflictDetail())
	case modeHelp:
		b.WriteString(helpText())
	}
	return b.String()
}

func (m model) renderFilePicker() string {
	var b strings.Builder
	t := currentTheme()
	switch m.pickFor {
	case pickerCredentials:
		b.WriteString(t.section.Render("Select Google credentials JSON"))
		b.WriteString("\n")
		b.WriteString(t.muted.Render("Choose a .json OAuth desktop client credentials file."))
	case pickerSyncDir:
		b.WriteString(t.section.Render("Select local sync directory"))
		b.WriteString("\n")
		b.WriteString(t.muted.Render("Open folders with enter; press p to use the current folder."))
	default:
		b.WriteString(t.section.Render("Select path"))
	}
	b.WriteString("\n")
	b.WriteString(t.muted.Render(m.picker.CurrentDirectory))
	b.WriteString("\n\n")
	b.WriteString(m.picker.View())
	b.WriteString("\n")
	b.WriteString(renderKeyHints("[enter] open/select", "[p] choose current directory", "[h] back", "[q] cancel"))
	b.WriteString("\n")
	return b.String()
}

func (m model) renderWork() string {
	if m.width >= wideLayoutMinWidth {
		return m.renderWideWork()
	}
	var b strings.Builder
	t := currentTheme()
	b.WriteString(t.section.Render("Working View"))
	b.WriteString(" ")
	b.WriteString(t.muted.Render(filterLabel(m.filter)))
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("Sync Health: %s\n\n", syncHealthText(m.status, m.config)))
	tasks := m.visibleTasks()
	if len(tasks) == 0 {
		b.WriteString(t.muted.Render("No tasks in this view."))
		b.WriteString("\n")
	} else {
		for i, task := range tasks {
			prefix := "  "
			if i == m.selected {
				prefix = "> "
			}
			line := fmt.Sprintf("%s[%s] %s%s%s", prefix, task.Status, task.Title, formatTags(task.Tags), formatDates(task))
			line = truncate(line, maxWidth(m.width))
			if i == m.selected {
				line = t.selected.Width(maxWidth(m.width)).Render(line)
			}
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	b.WriteString("\n")
	if m.width > 0 && m.width < 70 {
		b.WriteString(renderKeyHints("[?] help", "[q] quit"))
		b.WriteString("\n")
	} else {
		b.WriteString(renderKeyHints("[a] add", "[e] edit", "[space] done/reopen", "[d] delete", "[/] search", "[s] Sync Now", "[?] help", "[q] quit"))
		b.WriteString("\n")
		b.WriteString(renderKeyHints("[1] Working", "[2] All", "[3] Done", "[c] Conflicts", "[S] Sync Setup", "[g] Google Login", "[r] Restore"))
		b.WriteString("\n")
	}
	return b.String()
}

func (m model) renderWideWork() string {
	tasks := m.visibleTasks()
	leftWidth := max(42, m.width*58/100)
	rightWidth := max(32, m.width-leftWidth-3)
	left := m.renderTaskListPanel(tasks, leftWidth)
	right := m.renderSidePanel(tasks, rightWidth)
	return joinPanels(left, right, leftWidth, rightWidth) + "\n" +
		renderKeyHints("[a] add", "[e] edit", "[space] done/reopen", "[d] delete", "[/] search", "[s] Sync Now", "[?] help", "[q] quit") + "\n" +
		renderKeyHints("[1] Working", "[2] All", "[3] Done", "[c] Conflicts", "[S] Sync Setup", "[g] Google Login", "[r] Restore") + "\n"
}

func (m model) renderTaskListPanel(tasks []storage.Task, width int) string {
	t := currentTheme()
	var b strings.Builder
	b.WriteString(t.section.Render("Tasks"))
	b.WriteString(" ")
	b.WriteString(t.muted.Render(filterLabel(m.filter)))
	b.WriteString("\n\n")
	if len(tasks) == 0 {
		b.WriteString(t.muted.Render("No tasks in this view."))
		b.WriteString("\n")
		return t.panel.Width(width - 2).Render(b.String())
	}
	lineWidth := max(20, width-6)
	for i, task := range tasks {
		prefix := "  "
		if i == m.selected {
			prefix = "> "
		}
		line := fmt.Sprintf("%s%s %s%s%s", prefix, statusBadge(task.Status), task.Title, formatTags(task.Tags), formatDates(task))
		line = truncate(line, lineWidth)
		if i == m.selected {
			line = t.selected.Width(lineWidth).Render(line)
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return t.panel.Width(width - 2).Render(b.String())
}

func (m model) renderSidePanel(tasks []storage.Task, width int) string {
	if task, ok := selectedTask(tasks, m.selected); ok {
		return currentTheme().panel.Width(width - 2).Render(renderTaskSummary(task, width-6, m.status, m.config))
	}
	return currentTheme().panel.Width(width - 2).Render(renderDashboard(m.status, m.config))
}

func (m model) renderDetail() string {
	task, ok := selectedTask(m.visibleTasks(), m.selected)
	if !ok {
		return "No task selected.\n"
	}
	return renderTaskSummary(task, maxWidth(m.width), m.status, m.config) + "\n[e] edit  [d] delete  [esc] back\n"
}

func renderTaskSummary(task storage.Task, width int, status storage.SyncStatus, cfg syncsetup.Config) string {
	var b strings.Builder
	t := currentTheme()
	b.WriteString(t.section.Render("Selected Task"))
	b.WriteString("\n\n")
	b.WriteString(t.title.Render(truncate(task.Title, width)))
	b.WriteString(" ")
	b.WriteString(statusBadge(task.Status))
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("id: %s\n", task.ID))
	if len(task.Tags) > 0 {
		b.WriteString(fmt.Sprintf("tags: %s\n", strings.Join(task.Tags, ", ")))
	}
	if task.DueAt != nil {
		b.WriteString(fmt.Sprintf("due: %s\n", task.DueAt.Format(time.RFC3339)))
	}
	if task.ReminderAt != nil {
		b.WriteString(fmt.Sprintf("reminder: %s\n", task.ReminderAt.Format(time.RFC3339)))
	}
	if task.Body != "" {
		b.WriteString("\n")
		b.WriteString(truncate(task.Body, width))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(t.section.Render("Sync"))
	b.WriteString("\n")
	b.WriteString(syncHealthText(status, cfg))
	b.WriteString("\n")
	return b.String()
}

func renderDashboard(status storage.SyncStatus, cfg syncsetup.Config) string {
	var b strings.Builder
	t := currentTheme()
	b.WriteString(t.section.Render("Dashboard"))
	b.WriteString("\n\n")
	b.WriteString("No task selected in this view.\n\n")
	b.WriteString(t.section.Render("Sync"))
	b.WriteString("\n")
	b.WriteString(syncHealthText(status, cfg))
	b.WriteString("\n")
	return b.String()
}

func (m model) renderConflicts() string {
	var b strings.Builder
	b.WriteString("Sync Conflicts\n\n")
	if len(m.conflicts) == 0 {
		b.WriteString("No open conflicts.\n")
	} else {
		for i, conflict := range m.conflicts {
			prefix := "  "
			if i == m.conflictSel {
				prefix = "> "
			}
			b.WriteString(fmt.Sprintf("%s%s %s task=%s\n", prefix, conflict.ID, conflict.Type, conflict.TaskID))
		}
	}
	b.WriteString("\n[enter] view  [esc] back\n")
	return b.String()
}

func (m model) renderConflictDetail() string {
	if len(m.conflicts) == 0 {
		return "No conflict selected.\n"
	}
	conflict := m.conflicts[m.conflictSel]
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%s %s task=%s\n\n", conflict.ID, conflict.Type, conflict.TaskID))
	b.WriteString("local:  " + formatConflictSide(conflict.Local) + "\n")
	b.WriteString("remote: " + formatConflictSide(conflict.Remote) + "\n\n")
	if conflict.Type == "duplicate_device_sequence" {
		b.WriteString("[d] dismiss duplicate  [esc] back\n")
	} else {
		b.WriteString("[l] use local  [r] use remote  [esc] back\n")
	}
	return b.String()
}
