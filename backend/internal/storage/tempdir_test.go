package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenCreatesConfiguredTemporaryDirectory(t *testing.T) {
	lock := testLock(t)
	dir, err := lock.Dir()
	if err != nil {
		t.Fatal(err)
	}
	tempDir := filepath.Join(dir, "runtime", "tmp")
	t.Setenv("TMPDIR", tempDir)

	store, err := Open(context.Background(), lock)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	info, err := os.Stat(tempDir)
	if err != nil {
		t.Fatalf("configured temporary directory was not created: %v", err)
	}
	if !info.IsDir() || info.Mode().Perm() != 0700 {
		t.Fatalf("temporary directory is not private: %v", info.Mode())
	}
	file, err := os.CreateTemp("", "storage-regression-*")
	if err != nil {
		t.Fatalf("configured temporary directory is not usable: %v", err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(file.Name()) != tempDir {
		t.Fatalf("temporary file created outside configured directory: %s", file.Name())
	}
	if _, err = store.DB().Exec(`PRAGMA temp_store=FILE; CREATE TEMP TABLE temp_probe(payload BLOB); INSERT INTO temp_probe VALUES(zeroblob(2097152))`); err != nil {
		t.Fatalf("SQLite temporary storage failed: %v", err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(context.Background(), lock)
	if err != nil {
		t.Fatalf("reopen with an existing temporary directory: %v", err)
	}
	defer reopened.Close()
	if _, err = os.Stat(file.Name()); err != nil {
		t.Fatalf("reopen changed existing temporary files: %v", err)
	}
}

func TestOpenRejectsInvalidTemporaryDirectoryBeforeDatabase(t *testing.T) {
	lock := testLock(t)
	dir, err := lock.Dir()
	if err != nil {
		t.Fatal(err)
	}
	tempDir := filepath.Join(dir, "tmp")
	if err = os.WriteFile(tempDir, []byte("occupied"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", tempDir)

	store, err := Open(context.Background(), lock)
	if store != nil {
		store.Close()
		t.Fatal("invalid temporary directory exposed a writable Store")
	}
	if err == nil || !strings.Contains(err.Error(), "temporary directory") {
		t.Fatalf("missing actionable temporary directory error: %v", err)
	}
	if _, err = os.Stat(filepath.Join(dir, "db")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("database touched before temporary directory check: %v", err)
	}
	body, err := os.ReadFile(tempDir)
	if err != nil || string(body) != "occupied" {
		t.Fatalf("existing path changed: %q, %v", body, err)
	}
}
