package cloudsync

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	due := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)

	if err := storage.Init(ctx, sourceDB, secret); err != nil {
		t.Fatalf("init source: %v", err)
	}
	source, err := storage.Open(ctx, sourceDB, secret)
	if err != nil {
		t.Fatalf("open source: %v", err)
	}
	if _, err := source.AddTaskWithInput(ctx, storage.TaskInput{Title: title, Body: body, DueAt: &due}); err != nil {
		t.Fatalf("add source task: %v", err)
	}
	sourceTasks, err := source.ListTasks(ctx)
	if err != nil {
		t.Fatalf("list source tasks: %v", err)
	}
	if _, err := source.AddTag(ctx, sourceTasks[0].ID, "drive-private"); err != nil {
		t.Fatalf("add source tag: %v", err)
	}
	statusBefore, err := storage.ReadSyncStatus(ctx, sourceDB)
	if err != nil {
		t.Fatalf("read status before push: %v", err)
	}
	if statusBefore.PendingChanges != 2 {
		t.Fatalf("pending before push = %d, want 2", statusBefore.PendingChanges)
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

	assertNoPlaintextUnder(t, syncDir, title, body, "drive-private", "2026-08-05")

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
	if len(tasks[0].Tags) != 1 || tasks[0].Tags[0] != "drive-private" {
		t.Fatalf("unexpected restored tags: %#v", tasks[0])
	}
	if tasks[0].DueAt == nil || !tasks[0].DueAt.Equal(due) {
		t.Fatalf("unexpected restored due date: %#v", tasks[0])
	}
}

// TestConcurrentDevicePushesDoNotOverwrite is the core multi-device safety
// property: two devices that edited offline both push, and the device that
// pushes last must not erase the other device's already-uploaded changes.
// Under the old single shared artifact this silently lost data.
func TestConcurrentDevicePushesDoNotOverwrite(t *testing.T) {
	ctx := context.Background()
	secret := "multi device secret"
	syncDir := t.TempDir()
	client := LocalDirClient{Dir: syncDir}

	// Device A starts the collection and publishes the shared manifest.
	dbA := filepath.Join(t.TempDir(), "a.db")
	if err := storage.Init(ctx, dbA, secret); err != nil {
		t.Fatalf("init A: %v", err)
	}
	deviceA, err := storage.Open(ctx, dbA, secret)
	if err != nil {
		t.Fatalf("open A: %v", err)
	}
	defer deviceA.Close()
	if _, err := deviceA.AddTask(ctx, "Task from A", "Body from A"); err != nil {
		t.Fatalf("add A: %v", err)
	}
	if err := Push(ctx, deviceA, client); err != nil {
		t.Fatalf("push A: %v", err)
	}

	// Device B restores from the shared manifest, then edits offline.
	dbB := filepath.Join(t.TempDir(), "b.db")
	manifest, err := ReadManifest(ctx, client)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if err := storage.InitWithManifest(ctx, dbB, secret, manifest); err != nil {
		t.Fatalf("init B: %v", err)
	}
	deviceB, err := storage.Open(ctx, dbB, secret)
	if err != nil {
		t.Fatalf("open B: %v", err)
	}
	defer deviceB.Close()
	if err := Pull(ctx, deviceB, client); err != nil {
		t.Fatalf("pull B: %v", err)
	}
	if _, err := deviceB.AddTask(ctx, "Task from B", "Body from B"); err != nil {
		t.Fatalf("add B: %v", err)
	}

	// B pushes, then A pushes last. The last writer must not clobber B.
	if err := Push(ctx, deviceB, client); err != nil {
		t.Fatalf("push B: %v", err)
	}
	if err := Push(ctx, deviceA, client); err != nil {
		t.Fatalf("re-push A: %v", err)
	}

	names, err := client.List(ctx, DevicePrefix)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(names) != 2 {
		t.Fatalf("expected two device artifacts after concurrent pushes, got %v", names)
	}

	// A fresh device must recover both devices' work.
	dbC := filepath.Join(t.TempDir(), "c.db")
	if err := storage.InitWithManifest(ctx, dbC, secret, manifest); err != nil {
		t.Fatalf("init C: %v", err)
	}
	deviceC, err := storage.Open(ctx, dbC, secret)
	if err != nil {
		t.Fatalf("open C: %v", err)
	}
	defer deviceC.Close()
	if err := Pull(ctx, deviceC, client); err != nil {
		t.Fatalf("pull C: %v", err)
	}
	tasks, err := deviceC.ListTasks(ctx)
	if err != nil {
		t.Fatalf("list C: %v", err)
	}
	titles := map[string]bool{}
	for _, task := range tasks {
		titles[task.Title] = true
	}
	if !titles["Task from A"] || !titles["Task from B"] {
		t.Fatalf("restored device missing work from a device: %#v", tasks)
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
	names, err := client.List(ctx, DevicePrefix)
	if err != nil {
		t.Fatalf("list artifacts: %v", err)
	}
	if len(names) != 1 {
		t.Fatalf("expected one device artifact, got %v", names)
	}
	artifactPath := filepath.Join(syncDir, filepath.FromSlash(names[0]))
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
	if err := Push(ctx, store, failingClient{failPrefix: DevicePrefix}); err == nil {
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
	remoteName := DeviceChangesName(changes[0].DeviceID)
	data, err := json.MarshalIndent(changes, "", "  ")
	if err != nil {
		t.Fatalf("encode changes: %v", err)
	}
	artifact, err := store.SealArtifact(remoteName, data)
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
	if err := client.Put(ctx, remoteName, artifact); err != nil {
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
	failName   string
	failPrefix string
}

func (c failingClient) Put(ctx context.Context, name string, data []byte) error {
	if name == c.failName || (c.failPrefix != "" && strings.HasPrefix(name, c.failPrefix)) {
		return fmt.Errorf("forced failure for %s", name)
	}
	return nil
}

func (c failingClient) Get(ctx context.Context, name string) ([]byte, error) {
	return nil, fmt.Errorf("not implemented")
}

func (c failingClient) List(ctx context.Context, prefix string) ([]string, error) {
	return nil, fmt.Errorf("not implemented")
}

// assertNoPlaintextUnder walks every file under root and fails if any of the
// given sensitive markers appears in cleartext, covering the whole on-disk
// sync surface rather than a single named artifact.
func assertNoPlaintextUnder(t *testing.T, root string, markers ...string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, marker := range markers {
			if bytes.Contains(data, []byte(marker)) {
				t.Fatalf("plaintext %q found in %s", marker, path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk sync dir: %v", err)
	}
}
