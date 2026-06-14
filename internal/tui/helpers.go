package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"tasks-remote/internal/storage"
	"tasks-remote/internal/syncsetup"
)

func splitTags(raw string) []string {
	seen := map[string]bool{}
	var tags []string
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' }) {
		tag := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(part, "#")))
		if tag == "" || seen[tag] {
			continue
		}
		seen[tag] = true
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	return tags
}

func parseOptionalTime(raw string) (*time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
		value := parsed.UTC().Truncate(time.Second)
		return &value, nil
	}
	if parsed, err := time.ParseInLocation("2006-01-02", raw, time.Local); err == nil {
		value := parsed.UTC().Truncate(time.Second)
		return &value, nil
	}
	return nil, fmt.Errorf("invalid time %q: use YYYY-MM-DD or RFC3339", raw)
}

func syncHealthText(status storage.SyncStatus, cfg syncsetup.Config) string {
	if !status.Initialized {
		return "not initialized"
	}
	if cfg.Kind == syncsetup.None {
		return "not configured"
	}
	if status.OpenConflicts > 0 {
		return fmt.Sprintf("%d conflict(s) open", status.OpenConflicts)
	}
	if status.PendingChanges > 0 {
		return fmt.Sprintf("%d pending local change(s)", status.PendingChanges)
	}
	return "caught up"
}

func restorePhrase(status storage.SyncStatus) string {
	if status.Initialized && status.PendingChanges > 0 {
		return "RESTORE WITH PENDING"
	}
	return "RESTORE"
}

func filterLabel(filter filterMode) string {
	switch filter {
	case filterAll:
		return "All"
	case filterDone:
		return "Done"
	case filterSearch:
		return "Search"
	default:
		return "Working"
	}
}

func formatTags(tags []string) string {
	if len(tags) == 0 {
		return ""
	}
	formatted := make([]string, 0, len(tags))
	for _, tag := range tags {
		formatted = append(formatted, "#"+tag)
	}
	return " " + strings.Join(formatted, " ")
}

func formatDates(task storage.Task) string {
	var parts []string
	if task.DueAt != nil {
		parts = append(parts, "due:"+task.DueAt.Format("2006-01-02"))
	}
	if task.ReminderAt != nil {
		parts = append(parts, "remind:"+task.ReminderAt.Format("2006-01-02"))
	}
	if len(parts) == 0 {
		return ""
	}
	return " " + strings.Join(parts, " ")
}

func formatConflictSide(side storage.ConflictSide) string {
	device := side.DeviceID
	if len(device) > 16 {
		device = device[:16]
	}
	if !side.Present {
		return fmt.Sprintf("[change %s] not stored (rejected duplicate)", side.ChangeID)
	}
	if side.Deleted {
		return fmt.Sprintf("[device %s] deleted task", device)
	}
	summary := side.Title
	if side.Body != "" {
		summary += " - " + side.Body
	}
	return fmt.Sprintf("[device %s] %s%s", device, summary, formatTags(side.Tags))
}

func tagsContain(tags []string, query string) bool {
	for _, tag := range tags {
		if strings.Contains(strings.ToLower(tag), query) {
			return true
		}
	}
	return false
}

func helpText() string {
	return `[a] add task
[e] edit selected task
[space] complete or reopen selected task
[d] delete selected task with confirmation
[/] search title, body, and tags
[1] Working View  [2] All  [3] Done
[s] Sync Now  [S] Local Sync Setup  [g] Google login  [r] Restore
[c] Sync Conflicts
[?] help  [esc] back  [q] quit
`
}

func truncate(s string, width int) string {
	if width <= 0 || len(s) <= width {
		return s
	}
	if width <= 3 {
		return s[:width]
	}
	return s[:width-3] + "..."
}

func maxWidth(width int) int {
	if width <= 0 {
		return 100
	}
	if width < 24 {
		return width
	}
	return width - 2
}

func syncBadge(status storage.SyncStatus, cfg syncsetup.Config) string {
	t := currentTheme()
	text := syncHealthText(status, cfg)
	if status.OpenConflicts > 0 || status.PendingChanges > 0 || cfg.Kind == syncsetup.None {
		return t.warningBadge.Render(text)
	}
	return t.badge.Render(text)
}

func statusBadge(status string) string {
	t := currentTheme()
	switch status {
	case "done":
		return t.badge.Render("done")
	default:
		return t.warningBadge.Render("open")
	}
}

func renderKeyHints(hints ...string) string {
	t := currentTheme()
	styled := make([]string, 0, len(hints))
	for _, hint := range hints {
		if strings.HasPrefix(hint, "[") {
			if end := strings.Index(hint, "]"); end >= 0 {
				styled = append(styled, t.key.Render(hint[:end+1])+hint[end+1:])
				continue
			}
		}
		styled = append(styled, hint)
	}
	return renderHint(styled...)
}
