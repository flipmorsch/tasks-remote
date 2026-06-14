package tui

import (
	"fmt"
	"strings"
	"time"
)

func (m model) View() string {
	var b strings.Builder
	title := "tasks-remote"
	if m.width > 0 && m.width < 56 {
		title = "tasks"
	}
	b.WriteString(headerStyle().Render(title))
	b.WriteString("\n")
	if m.busy != "" {
		b.WriteString(m.busy)
		b.WriteString("\n")
		return b.String()
	}
	if m.err != "" {
		b.WriteString(errorStyle().Render(m.err))
		b.WriteString("\n")
	}
	if m.notice != "" {
		b.WriteString(noticeStyle().Render(m.notice))
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
		b.WriteString("\nenter: save  tab: next  esc: cancel\n")
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

func (m model) renderWork() string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Sync Health: %s\n", syncHealthText(m.status, m.config)))
	b.WriteString(fmt.Sprintf("View: %s\n\n", filterLabel(m.filter)))
	tasks := m.visibleTasks()
	if len(tasks) == 0 {
		b.WriteString("No tasks in this view.\n")
	} else {
		for i, task := range tasks {
			prefix := "  "
			if i == m.selected {
				prefix = "> "
			}
			line := fmt.Sprintf("%s[%s] %s%s%s", prefix, task.Status, task.Title, formatTags(task.Tags), formatDates(task))
			b.WriteString(truncate(line, maxWidth(m.width)))
			b.WriteString("\n")
		}
	}
	b.WriteString("\n")
	if m.width > 0 && m.width < 70 {
		b.WriteString("[?] help  [q] quit\n")
	} else {
		b.WriteString("[a] add  [e] edit  [space] done/reopen  [d] delete  [/] search  [s] Sync Now  [?] help  [q] quit\n")
		b.WriteString("[1] Working  [2] All  [3] Done  [c] Conflicts  [S] Sync Setup  [g] Google Login  [r] Restore\n")
	}
	return b.String()
}

func (m model) renderDetail() string {
	task, ok := selectedTask(m.visibleTasks(), m.selected)
	if !ok {
		return "No task selected.\n"
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%s [%s]\n", task.Title, task.Status))
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
		b.WriteString(task.Body)
		b.WriteString("\n")
	}
	b.WriteString("\n[e] edit  [d] delete  [esc] back\n")
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
