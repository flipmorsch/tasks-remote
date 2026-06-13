package unlock

import (
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestSaveLoadAndClear(t *testing.T) {
	keyring.MockInit()
	dbPath := "/tmp/tasks-a.db"

	if _, err := Load(dbPath); err == nil || !strings.Contains(err.Error(), "device is locked") {
		t.Fatalf("expected locked error, got %v", err)
	}
	if err := Save(dbPath, "secret-a"); err != nil {
		t.Fatalf("save: %v", err)
	}
	secret, err := Load(dbPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if secret != "secret-a" {
		t.Fatalf("secret = %q, want secret-a", secret)
	}
	if err := Clear(dbPath); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if _, err := Load(dbPath); err == nil {
		t.Fatal("expected locked error after clear")
	}
}

func TestLoadPrefersEnvironment(t *testing.T) {
	keyring.MockInit()
	t.Setenv(EnvSecret, "env-secret")
	if err := Save("/tmp/tasks-b.db", "keyring-secret"); err != nil {
		t.Fatalf("save: %v", err)
	}
	secret, err := Load("/tmp/tasks-b.db")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if secret != "env-secret" {
		t.Fatalf("secret = %q, want env-secret", secret)
	}
}

func TestSeparateDatabasesUseSeparateEntries(t *testing.T) {
	keyring.MockInit()
	if err := Save("/tmp/tasks-a.db", "secret-a"); err != nil {
		t.Fatalf("save a: %v", err)
	}
	if err := Save("/tmp/tasks-b.db", "secret-b"); err != nil {
		t.Fatalf("save b: %v", err)
	}
	secretA, err := Load("/tmp/tasks-a.db")
	if err != nil {
		t.Fatalf("load a: %v", err)
	}
	secretB, err := Load("/tmp/tasks-b.db")
	if err != nil {
		t.Fatalf("load b: %v", err)
	}
	if secretA != "secret-a" || secretB != "secret-b" {
		t.Fatalf("secrets crossed: a=%q b=%q", secretA, secretB)
	}
}
