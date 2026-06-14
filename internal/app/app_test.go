package app

import (
	"path/filepath"
	"testing"

	"tasks-remote/internal/storage"
	"tasks-remote/internal/unlock"
)

func TestServiceTaskLifecycleReconcilesTags(t *testing.T) {
	ctx := t.Context()
	dbPath := filepath.Join(t.TempDir(), "tasks.db")
	secret := "recovery secret"
	if err := storage.Init(ctx, dbPath, secret); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := unlock.Save(dbPath, secret); err != nil {
		t.Fatalf("save unlock: %v", err)
	}
	service := Service{DBPath: dbPath}

	task, err := service.CreateTask(ctx, TaskInput{
		Title: "Pay invoice",
		Body:  "vendor ACME",
		Tags:  []string{"Finance", "#urgent", "finance"},
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if got, want := task.Tags, []string{"finance", "urgent"}; !equalStrings(got, want) {
		t.Fatalf("created tags = %#v, want %#v", got, want)
	}

	task, err = service.UpdateTask(ctx, task.ID, TaskInput{
		Title: "Pay invoice today",
		Body:  "updated",
		Tags:  []string{"home"},
	})
	if err != nil {
		t.Fatalf("update task: %v", err)
	}
	if got, want := task.Tags, []string{"home"}; !equalStrings(got, want) {
		t.Fatalf("updated tags = %#v, want %#v", got, want)
	}

	if _, err := service.CompleteTask(ctx, task.ID); err != nil {
		t.Fatalf("complete task: %v", err)
	}
	tasks, status, err := service.ListTasks(ctx)
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Status != "done" {
		t.Fatalf("tasks = %#v", tasks)
	}
	if status.PendingChanges == 0 {
		t.Fatalf("expected pending changes after local task operations")
	}

	if _, err := service.ReopenTask(ctx, task.ID); err != nil {
		t.Fatalf("reopen task: %v", err)
	}
	if err := service.DeleteTask(ctx, task.ID); err != nil {
		t.Fatalf("delete task: %v", err)
	}
	tasks, _, err = service.ListTasks(ctx)
	if err != nil {
		t.Fatalf("list after delete: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("tasks after delete = %#v", tasks)
	}
}

func TestServiceStartupStates(t *testing.T) {
	ctx := t.Context()
	dbPath := filepath.Join(t.TempDir(), "tasks.db")
	service := Service{DBPath: dbPath}

	startup, err := service.Startup(ctx)
	if err != nil {
		t.Fatalf("startup missing: %v", err)
	}
	if startup.State != StateSetup {
		t.Fatalf("missing state = %s, want %s", startup.State, StateSetup)
	}

	if err := storage.Init(ctx, dbPath, "secret"); err != nil {
		t.Fatalf("init: %v", err)
	}
	startup, err = service.Startup(ctx)
	if err != nil {
		t.Fatalf("startup locked: %v", err)
	}
	if startup.State != StateLocked {
		t.Fatalf("locked state = %s, want %s", startup.State, StateLocked)
	}

	if err := unlock.Save(dbPath, "secret"); err != nil {
		t.Fatalf("save unlock: %v", err)
	}
	startup, err = service.Startup(ctx)
	if err != nil {
		t.Fatalf("startup unlocked: %v", err)
	}
	if startup.State != StateUnlocked {
		t.Fatalf("unlocked state = %s, want %s", startup.State, StateUnlocked)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
