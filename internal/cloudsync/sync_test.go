package cloudsync

import (
	"bytes"
	"context"
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
	if err := Push(ctx, source, LocalDirClient{Dir: syncDir}); err != nil {
		t.Fatalf("push: %v", err)
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
