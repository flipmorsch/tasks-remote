package syncsetup

import (
	"path/filepath"
	"testing"
)

func TestLoadMissingConfigReturnsEmpty(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	got, err := Load(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatalf("load missing config: %v", err)
	}
	if got != (Config{}) {
		t.Fatalf("config = %#v, want empty", got)
	}
}

func TestSaveAndLoadPerDatabase(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dbA := filepath.Join(t.TempDir(), "a.db")
	dbB := filepath.Join(t.TempDir(), "b.db")

	cfgA := Config{Kind: Dir, Dir: "/tmp/a"}
	cfgB := Config{Kind: Google, CredentialsPath: "/tmp/credentials.json"}
	if err := Save(dbA, cfgA); err != nil {
		t.Fatalf("save A: %v", err)
	}
	if err := Save(dbB, cfgB); err != nil {
		t.Fatalf("save B: %v", err)
	}

	gotA, err := Load(dbA)
	if err != nil {
		t.Fatalf("load A: %v", err)
	}
	gotB, err := Load(dbB)
	if err != nil {
		t.Fatalf("load B: %v", err)
	}
	if gotA != cfgA {
		t.Fatalf("config A = %#v, want %#v", gotA, cfgA)
	}
	if gotB != cfgB {
		t.Fatalf("config B = %#v, want %#v", gotB, cfgB)
	}
}
