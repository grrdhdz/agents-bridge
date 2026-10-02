package control

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestReadyFileAppearsOnlyWhenComplete is the regression for a reader that
// polls for the file: it must not exist after Reserve, and must hold the full
// record, owner-only, as soon as it exists — with no temporary file left.
func TestReadyFileAppearsOnlyWhenComplete(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ready.json")
	ready, err := ReserveReadyFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ready file must not exist before Publish, got %v", err)
	}
	if err := ready.Publish([]byte("{\"type\":\"ready\"}\n")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "{\"type\":\"ready\"}\n" {
		t.Fatalf("unexpected content %q, err %v", data, err)
	}
	if err := VerifyOwnerOnly(path); err != nil {
		t.Fatalf("ready file is not owner-only: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("temporary files left behind: %v", entries)
	}
}

func TestReserveReadyFileRejectsExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ready.json")
	if err := os.WriteFile(path, []byte("ya existe\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReserveReadyFile(path); !errors.Is(err, os.ErrExist) {
		t.Fatalf("expected ErrExist, got %v", err)
	}
}

// TestReadyFilePublishNeverOverwrites covers a file created by someone else
// between Reserve and Publish: Publish must fail, not replace it.
func TestReadyFilePublishNeverOverwrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ready.json")
	ready, err := ReserveReadyFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("otro\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ready.Publish([]byte("nuevo\n")); !errors.Is(err, os.ErrExist) {
		t.Fatalf("expected ErrExist, got %v", err)
	}
	if data, _ := os.ReadFile(path); string(data) != "otro\n" {
		t.Fatalf("existing file was overwritten: %q", data)
	}
}
