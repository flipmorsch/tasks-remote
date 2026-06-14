package tui

import (
	"sort"
	"strings"
	"time"

	"tasks-remote/internal/storage"
)

func (m model) visibleTasks() []storage.Task {
	tasks := append([]storage.Task(nil), m.tasks...)
	sort.SliceStable(tasks, func(i, j int) bool {
		return taskRank(tasks[i]) < taskRank(tasks[j])
	})
	filtered := tasks[:0]
	query := strings.ToLower(strings.TrimSpace(m.searchQuery))
	now := time.Now()
	for _, task := range tasks {
		switch m.filter {
		case filterWorking:
			if task.Status != "done" && isWorkingTask(task, now) {
				filtered = append(filtered, task)
			}
		case filterAll:
			filtered = append(filtered, task)
		case filterDone:
			if task.Status == "done" {
				filtered = append(filtered, task)
			}
		case filterSearch:
			if query == "" || taskMatches(task, query) {
				filtered = append(filtered, task)
			}
		}
	}
	return filtered
}

func isWorkingTask(task storage.Task, now time.Time) bool {
	if task.Status == "done" {
		return false
	}
	if task.DueAt == nil && task.ReminderAt == nil {
		return true
	}
	horizon := now.Add(7 * 24 * time.Hour)
	return (task.DueAt != nil && task.DueAt.Before(horizon)) || (task.ReminderAt != nil && task.ReminderAt.Before(horizon))
}

func taskRank(task storage.Task) int64 {
	var t time.Time
	switch {
	case task.ReminderAt != nil:
		t = *task.ReminderAt
	case task.DueAt != nil:
		t = *task.DueAt
	default:
		t = task.CreatedAt
	}
	return t.Unix()
}

func taskMatches(task storage.Task, query string) bool {
	return strings.Contains(strings.ToLower(task.Title), query) ||
		strings.Contains(strings.ToLower(task.Body), query) ||
		tagsContain(task.Tags, query)
}

func (m *model) clampSelection() {
	tasks := m.visibleTasks()
	if m.selected >= len(tasks) {
		m.selected = max(0, len(tasks)-1)
	}
}

func selectedTask(tasks []storage.Task, selected int) (storage.Task, bool) {
	if selected < 0 || selected >= len(tasks) {
		return storage.Task{}, false
	}
	return tasks[selected], true
}
