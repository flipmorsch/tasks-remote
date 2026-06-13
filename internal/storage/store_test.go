package storage

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestTaskPayloadsAreEncryptedAtRest(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "tasks.db")
	secret := "correct horse battery staple"
	title := "Call bank about private mortgage"
	body := "Account note: sensitive body text"

	if err := Init(ctx, dbPath, secret); err != nil {
		t.Fatalf("init: %v", err)
	}
	store, err := Open(ctx, dbPath, secret)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := store.AddTask(ctx, title, body); err != nil {
		t.Fatalf("add task: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	for _, path := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if bytes.Contains(data, []byte(title)) {
			t.Fatalf("plaintext title found in %s", path)
		}
		if bytes.Contains(data, []byte(body)) {
			t.Fatalf("plaintext body found in %s", path)
		}
	}
}

func TestWrongRecoverySecretCannotReadTask(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "tasks.db")

	if err := Init(ctx, dbPath, "right secret"); err != nil {
		t.Fatalf("init: %v", err)
	}
	store, err := Open(ctx, dbPath, "right secret")
	if err != nil {
		t.Fatalf("open right secret: %v", err)
	}
	task, err := store.AddTask(ctx, "Private task", "Private body")
	if err != nil {
		t.Fatalf("add task: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	wrongStore, err := Open(ctx, dbPath, "wrong secret")
	if err != nil {
		t.Fatalf("open wrong secret should derive a key and fail at decrypt time: %v", err)
	}
	defer wrongStore.Close()

	if _, err := wrongStore.GetTask(ctx, task.ID); err == nil {
		t.Fatal("expected wrong recovery secret to fail decrypting task")
	}
}

func TestSearchScansAfterDecrypt(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "tasks.db")
	secret := "search secret"

	if err := Init(ctx, dbPath, secret); err != nil {
		t.Fatalf("init: %v", err)
	}
	store, err := Open(ctx, dbPath, secret)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()

	if _, err := store.AddTask(ctx, "Pay invoice", "Private vendor name"); err != nil {
		t.Fatalf("add matching task: %v", err)
	}
	tagged, err := store.AddTask(ctx, "Buy coffee", "Errand")
	if err != nil {
		t.Fatalf("add nonmatching task: %v", err)
	}
	if _, err := store.AddTag(ctx, tagged.ID, "errands"); err != nil {
		t.Fatalf("add tag: %v", err)
	}
	matches, err := store.SearchTasks(ctx, "vendor")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(matches) != 1 || matches[0].Title != "Pay invoice" {
		t.Fatalf("unexpected matches: %#v", matches)
	}
	tagMatches, err := store.SearchTasks(ctx, "errand")
	if err != nil {
		t.Fatalf("search tag: %v", err)
	}
	if len(tagMatches) != 1 || tagMatches[0].Title != "Buy coffee" {
		t.Fatalf("unexpected tag matches: %#v", tagMatches)
	}
}

func TestMutationsAppendEncryptedTaskChanges(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "tasks.db")
	secret := "change secret"
	sensitiveTitle := "Private salary negotiation"
	sensitiveBody := "Ask for 20 percent"

	if err := Init(ctx, dbPath, secret); err != nil {
		t.Fatalf("init: %v", err)
	}
	store, err := Open(ctx, dbPath, secret)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	task, err := store.AddTask(ctx, "Draft message", "")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := store.EditTask(ctx, task.ID, sensitiveTitle, sensitiveBody); err != nil {
		t.Fatalf("edit: %v", err)
	}
	if _, err := store.AddTag(ctx, task.ID, "salary-secret"); err != nil {
		t.Fatalf("add tag: %v", err)
	}
	if _, err := store.SetTaskStatus(ctx, task.ID, "done"); err != nil {
		t.Fatalf("done: %v", err)
	}
	if err := store.DeleteTask(ctx, task.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	var count int
	if err := store.db.QueryRowContext(ctx, `select count(*) from task_changes where task_id = ?`, task.ID).Scan(&count); err != nil {
		t.Fatalf("count changes: %v", err)
	}
	if count != 5 {
		t.Fatalf("expected 5 changes, got %d", count)
	}
	rows, err := store.db.QueryContext(ctx, `select sequence, change_type from task_changes order by sequence`)
	if err != nil {
		t.Fatalf("query changes: %v", err)
	}
	defer rows.Close()
	wantTypes := []string{"task.created", "task.updated", "task.tags_changed", "task.status_changed", "task.deleted"}
	var gotTypes []string
	for rows.Next() {
		var sequence int
		var changeType string
		if err := rows.Scan(&sequence, &changeType); err != nil {
			t.Fatalf("scan change: %v", err)
		}
		if sequence != len(gotTypes)+1 {
			t.Fatalf("sequence mismatch at %s: got %d", changeType, sequence)
		}
		gotTypes = append(gotTypes, changeType)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read changes: %v", err)
	}
	for i, want := range wantTypes {
		if gotTypes[i] != want {
			t.Fatalf("change %d type: got %s want %s", i, gotTypes[i], want)
		}
	}
	tasks, err := store.ListTasks(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("deleted task should be hidden from projection: %#v", tasks)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	for _, path := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if bytes.Contains(data, []byte(sensitiveTitle)) {
			t.Fatalf("plaintext changed title found in %s", path)
		}
		if bytes.Contains(data, []byte(sensitiveBody)) {
			t.Fatalf("plaintext changed body found in %s", path)
		}
		if bytes.Contains(data, []byte("salary-secret")) {
			t.Fatalf("plaintext tag found in %s", path)
		}
	}
}

func TestRebuildProjectionFromTaskChanges(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "tasks.db")
	secret := "replay secret"

	if err := Init(ctx, dbPath, secret); err != nil {
		t.Fatalf("init: %v", err)
	}
	store, err := Open(ctx, dbPath, secret)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()

	task, err := store.AddTask(ctx, "Original private task", "Original private body")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := store.EditTask(ctx, task.ID, "Replayed private task", "Replayed private body"); err != nil {
		t.Fatalf("edit: %v", err)
	}
	if _, err := store.AddTag(ctx, task.ID, "replayed-tag"); err != nil {
		t.Fatalf("add tag: %v", err)
	}
	if _, err := store.SetTaskStatus(ctx, task.ID, "done"); err != nil {
		t.Fatalf("done: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `delete from tasks`); err != nil {
		t.Fatalf("damage projection: %v", err)
	}
	empty, err := store.ListTasks(ctx)
	if err != nil {
		t.Fatalf("list damaged projection: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("expected damaged projection to be empty: %#v", empty)
	}

	if err := store.RebuildProjection(ctx); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	replayed, err := store.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("get replayed task: %v", err)
	}
	if replayed.Title != "Replayed private task" || replayed.Body != "Replayed private body" || replayed.Status != "done" {
		t.Fatalf("unexpected replayed task: %#v", replayed)
	}
	if len(replayed.Tags) != 1 || replayed.Tags[0] != "replayed-tag" {
		t.Fatalf("unexpected replayed tags: %#v", replayed)
	}
}

func TestTagMutationsAreEncryptedAtRest(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "tasks.db")
	secret := "tag secret"
	tag := "private-medical"

	if err := Init(ctx, dbPath, secret); err != nil {
		t.Fatalf("init: %v", err)
	}
	store, err := Open(ctx, dbPath, secret)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	task, err := store.AddTask(ctx, "Doctor note", "")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	tagged, err := store.AddTag(ctx, task.ID, tag)
	if err != nil {
		t.Fatalf("add tag: %v", err)
	}
	if len(tagged.Tags) != 1 || tagged.Tags[0] != tag {
		t.Fatalf("unexpected tags: %#v", tagged)
	}
	untagged, err := store.RemoveTag(ctx, task.ID, tag)
	if err != nil {
		t.Fatalf("remove tag: %v", err)
	}
	if len(untagged.Tags) != 0 {
		t.Fatalf("unexpected tags after remove: %#v", untagged)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	for _, path := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if bytes.Contains(data, []byte(tag)) {
			t.Fatalf("plaintext tag found in %s", path)
		}
	}
}

func TestReadSyncStatusDoesNotNeedRecoverySecret(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "tasks.db")

	if err := Init(ctx, dbPath, "status secret"); err != nil {
		t.Fatalf("init: %v", err)
	}
	store, err := Open(ctx, dbPath, "status secret")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := store.AddTask(ctx, "Sensitive status task", "Sensitive status body"); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	status, err := ReadSyncStatus(ctx, dbPath)
	if err != nil {
		t.Fatalf("sync status: %v", err)
	}
	if !status.Initialized {
		t.Fatal("expected initialized status")
	}
	if status.TotalChanges != 1 || status.PendingChanges != 1 {
		t.Fatalf("unexpected status: %#v", status)
	}
	if status.OpenConflicts != 0 {
		t.Fatalf("unexpected open conflicts: %#v", status)
	}
	if status.LastChangeAt == nil {
		t.Fatal("expected last change timestamp")
	}
}

func TestImportDuplicateDeviceSequenceCreatesSyncConflict(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "tasks.db")
	secret := "sequence conflict secret"

	if err := Init(ctx, dbPath, secret); err != nil {
		t.Fatalf("init: %v", err)
	}
	store, err := Open(ctx, dbPath, secret)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	if _, err := store.AddTask(ctx, "Conflict source task", "Conflict source body"); err != nil {
		t.Fatalf("add: %v", err)
	}
	changes, err := store.ExportChanges(ctx)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("expected one exported change, got %d", len(changes))
	}
	conflicting := changes[0]
	conflicting.ChangeID = "change_conflicting_remote"
	if err := store.ImportChanges(ctx, []ExportedChange{conflicting}); err != nil {
		t.Fatalf("import conflicting change: %v", err)
	}
	conflicts, err := store.ListConflicts(ctx)
	if err != nil {
		t.Fatalf("list conflicts: %v", err)
	}
	if len(conflicts) != 1 {
		t.Fatalf("expected one sync conflict, got %#v", conflicts)
	}
	if conflicts[0].Type != "duplicate_device_sequence" {
		t.Fatalf("unexpected conflict type: %#v", conflicts[0])
	}
	if conflicts[0].RemoteChangeID != "change_conflicting_remote" {
		t.Fatalf("unexpected remote change id: %#v", conflicts[0])
	}
	status, err := ReadSyncStatus(ctx, dbPath)
	if err != nil {
		t.Fatalf("sync status: %v", err)
	}
	if status.OpenConflicts != 1 {
		t.Fatalf("open conflicts = %d, want 1", status.OpenConflicts)
	}
}
