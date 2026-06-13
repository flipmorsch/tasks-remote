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
	if _, err := store.AddTask(ctx, "Buy coffee", "Errand"); err != nil {
		t.Fatalf("add nonmatching task: %v", err)
	}
	matches, err := store.SearchTasks(ctx, "vendor")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(matches) != 1 || matches[0].Title != "Pay invoice" {
		t.Fatalf("unexpected matches: %#v", matches)
	}
}
