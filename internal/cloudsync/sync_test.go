package cloudsync

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"tasks-remote/internal/storage"
)

func TestPushAndPullEncryptedChanges(t *testing.T) {
	ctx := context.Background()
	secret := "sync secret"
	sourceDB := filepath.Join(t.TempDir(), "source.db")
	targetDB := filepath.Join(t.TempDir(), "target.db")
	syncDir := t.TempDir()
	title := "Private drive sync title"
	body := "Private drive sync body"

	if err := storage.Init(ctx, sourceDB, secret); err != nil {
		t.Fatalf("init source: %v", err)
	}
	source, err := storage.Open(ctx, sourceDB, secret)
	if err != nil {
		t.Fatalf("open source: %v", err)
	}
	if _, err := source.AddTask(ctx, title, body); err != nil {
		t.Fatalf("add source task: %v", err)
	}
	statusBefore, err := storage.ReadSyncStatus(ctx, sourceDB)
	if err != nil {
		t.Fatalf("read status before push: %v", err)
	}
	if statusBefore.PendingChanges != 1 {
		t.Fatalf("pending before push = %d, want 1", statusBefore.PendingChanges)
	}
	if err := Push(ctx, source, LocalDirClient{Dir: syncDir}); err != nil {
		t.Fatalf("push: %v", err)
	}
	statusAfter, err := storage.ReadSyncStatus(ctx, sourceDB)
	if err != nil {
		t.Fatalf("read status after push: %v", err)
	}
	if statusAfter.PendingChanges != 0 {
		t.Fatalf("pending after push = %d, want 0", statusAfter.PendingChanges)
	}
	if err := source.Close(); err != nil {
		t.Fatalf("close source: %v", err)
	}

	for _, name := range []string{ChangesName} {
		data, err := os.ReadFile(filepath.Join(syncDir, name))
		if err != nil {
			t.Fatalf("read artifact %s: %v", name, err)
		}
		if bytes.Contains(data, []byte(title)) || bytes.Contains(data, []byte(body)) {
			t.Fatalf("plaintext task content found in %s", name)
		}
	}

	client := LocalDirClient{Dir: syncDir}
	manifest, err := ReadManifest(ctx, client)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if err := storage.InitWithManifest(ctx, targetDB, secret, manifest); err != nil {
		t.Fatalf("init target: %v", err)
	}
	target, err := storage.Open(ctx, targetDB, secret)
	if err != nil {
		t.Fatalf("open target: %v", err)
	}
	defer target.Close()
	if err := Pull(ctx, target, client); err != nil {
		t.Fatalf("pull: %v", err)
	}
	tasks, err := target.ListTasks(ctx)
	if err != nil {
		t.Fatalf("list target: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Title != title || tasks[0].Body != body {
		t.Fatalf("unexpected restored tasks: %#v", tasks)
	}
}

func TestPullRejectsTamperedArtifact(t *testing.T) {
	ctx := context.Background()
	secret := "tamper secret"
	sourceDB := filepath.Join(t.TempDir(), "source.db")
	targetDB := filepath.Join(t.TempDir(), "target.db")
	syncDir := t.TempDir()
	client := LocalDirClient{Dir: syncDir}

	if err := storage.Init(ctx, sourceDB, secret); err != nil {
		t.Fatalf("init source: %v", err)
	}
	source, err := storage.Open(ctx, sourceDB, secret)
	if err != nil {
		t.Fatalf("open source: %v", err)
	}
	if _, err := source.AddTask(ctx, "Tamper task", "Tamper body"); err != nil {
		t.Fatalf("add source task: %v", err)
	}
	if err := Push(ctx, source, client); err != nil {
		t.Fatalf("push: %v", err)
	}
	if err := source.Close(); err != nil {
		t.Fatalf("close source: %v", err)
	}
	artifactPath := filepath.Join(syncDir, ChangesName)
	artifact, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	artifact[len(artifact)-8] ^= 0x01
	if err := os.WriteFile(artifactPath, artifact, 0o600); err != nil {
		t.Fatalf("write tampered artifact: %v", err)
	}

	manifest, err := ReadManifest(ctx, client)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if err := storage.InitWithManifest(ctx, targetDB, secret, manifest); err != nil {
		t.Fatalf("init target: %v", err)
	}
	target, err := storage.Open(ctx, targetDB, secret)
	if err != nil {
		t.Fatalf("open target: %v", err)
	}
	defer target.Close()
	if err := Pull(ctx, target, client); err == nil {
		t.Fatal("expected tampered artifact to fail")
	}
}

func TestFailedPushLeavesChangesPending(t *testing.T) {
	ctx := context.Background()
	secret := "failed push secret"
	dbPath := filepath.Join(t.TempDir(), "tasks.db")

	if err := storage.Init(ctx, dbPath, secret); err != nil {
		t.Fatalf("init: %v", err)
	}
	store, err := storage.Open(ctx, dbPath, secret)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	if _, err := store.AddTask(ctx, "Pending private task", "Pending private body"); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := Push(ctx, store, failingClient{failName: ChangesName}); err == nil {
		t.Fatal("expected push to fail")
	}
	status, err := storage.ReadSyncStatus(ctx, dbPath)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status.PendingChanges != 1 {
		t.Fatalf("pending after failed push = %d, want 1", status.PendingChanges)
	}
}

func TestPullDuplicateDeviceSequenceRecordsConflict(t *testing.T) {
	ctx := context.Background()
	secret := "pull conflict secret"
	dbPath := filepath.Join(t.TempDir(), "tasks.db")
	syncDir := t.TempDir()
	client := LocalDirClient{Dir: syncDir}

	if err := storage.Init(ctx, dbPath, secret); err != nil {
		t.Fatalf("init: %v", err)
	}
	store, err := storage.Open(ctx, dbPath, secret)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	if _, err := store.AddTask(ctx, "Local task", "Local body"); err != nil {
		t.Fatalf("add: %v", err)
	}
	changes, err := store.ExportChanges(ctx)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	changes[0].ChangeID = "change_conflicting_pull"
	data, err := json.MarshalIndent(changes, "", "  ")
	if err != nil {
		t.Fatalf("encode changes: %v", err)
	}
	artifact, err := store.SealArtifact(ChangesName, data)
	if err != nil {
		t.Fatalf("seal artifact: %v", err)
	}
	manifest, err := store.Manifest(ctx)
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("encode manifest: %v", err)
	}
	if err := client.Put(ctx, ManifestName, manifestData); err != nil {
		t.Fatalf("put manifest: %v", err)
	}
	if err := client.Put(ctx, ChangesName, artifact); err != nil {
		t.Fatalf("put changes: %v", err)
	}
	if err := Pull(ctx, store, client); err != nil {
		t.Fatalf("pull: %v", err)
	}
	conflicts, err := store.ListConflicts(ctx)
	if err != nil {
		t.Fatalf("list conflicts: %v", err)
	}
	if len(conflicts) != 1 || conflicts[0].RemoteChangeID != "change_conflicting_pull" {
		t.Fatalf("unexpected conflicts: %#v", conflicts)
	}
}

type failingClient struct {
	failName string
}

func (c failingClient) Put(ctx context.Context, name string, data []byte) error {
	if name == c.failName {
		return fmt.Errorf("forced failure for %s", name)
	}
	return nil
}

func (c failingClient) Get(ctx context.Context, name string) ([]byte, error) {
	return nil, fmt.Errorf("not implemented")
}
