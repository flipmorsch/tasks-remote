package tui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textinput"

	"tasks-remote/internal/storage"
)

func TestSplitTagsNormalizesAndDeduplicates(t *testing.T) {
	got := splitTags("#Work, home work,, Urgent")
	want := []string{"home", "urgent", "work"}
	if len(got) != len(want) {
		t.Fatalf("tags = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tags = %#v, want %#v", got, want)
		}
	}
}

func TestVisibleTasksWorkingAllDoneAndSearch(t *testing.T) {
	now := time.Now()
	dueSoon := now.Add(24 * time.Hour)
	dueLater := now.Add(14 * 24 * time.Hour)
	model := newModel(t.Context(), "tasks.db")
	model.tasks = []storage.Task{
		{ID: "1", Title: "Open inbox", Status: "open", CreatedAt: now},
		{ID: "2", Title: "Long range", Status: "open", DueAt: &dueLater, CreatedAt: now},
		{ID: "3", Title: "Done item", Status: "done", CreatedAt: now},
		{ID: "4", Title: "Pay invoice", Body: "vendor acme", Tags: []string{"finance"}, Status: "open", DueAt: &dueSoon, CreatedAt: now},
	}

	model.filter = filterWorking
	if got := taskTitles(model.visibleTasks()); len(got) != 2 || got[0] != "Open inbox" || got[1] != "Pay invoice" {
		t.Fatalf("working titles = %#v", got)
	}

	model.filter = filterDone
	if got := taskTitles(model.visibleTasks()); len(got) != 1 || got[0] != "Done item" {
		t.Fatalf("done titles = %#v", got)
	}

	model.filter = filterSearch
	model.searchQuery = "acme"
	if got := taskTitles(model.visibleTasks()); len(got) != 1 || got[0] != "Pay invoice" {
		t.Fatalf("search titles = %#v", got)
	}
}

func TestRestorePhraseWarnsWhenPendingLocalChangesExist(t *testing.T) {
	if got := restorePhrase(storage.SyncStatus{Initialized: true, PendingChanges: 1}); got != "RESTORE WITH PENDING" {
		t.Fatalf("restore phrase = %q", got)
	}
	if got := restorePhrase(storage.SyncStatus{Initialized: true}); got != "RESTORE" {
		t.Fatalf("restore phrase = %q", got)
	}
}

func TestPickerStartDirUsesExistingFileParent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.json")
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write credentials: %v", err)
	}
	inputs := []textinput.Model{textinput.New(), textinput.New(), textinput.New()}
	inputs[1].SetValue(path)

	if got := pickerStartDir(pickerCredentials, inputs); got != dir {
		t.Fatalf("start dir = %q, want %q", got, dir)
	}
}

func TestPickerStartDirUsesExistingDirectory(t *testing.T) {
	dir := t.TempDir()
	inputs := []textinput.Model{textinput.New(), textinput.New(), textinput.New()}
	inputs[2].SetValue(dir)

	if got := pickerStartDir(pickerSyncDir, inputs); got != dir {
		t.Fatalf("start dir = %q, want %q", got, dir)
	}
}

func taskTitles(tasks []storage.Task) []string {
	titles := make([]string, 0, len(tasks))
	for _, task := range tasks {
		titles = append(titles, task.Title)
	}
	return titles
}
