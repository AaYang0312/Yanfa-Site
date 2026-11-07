package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"yuanfa-official/backend/internal/config"
	"yuanfa-official/backend/internal/coordination"
)

func TestRestoreRefusedBeforeDatabaseAndStopOrder(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, ".restore-incomplete")
	if err := os.WriteFile(marker, []byte("incomplete"), 0600); err != nil {
		t.Fatal(err)
	}
	c := config.Config{DataDir: dir, SiteURL: "http://127.0.0.1:18003"}
	if a, err := Open(context.Background(), c); !errors.Is(err, ErrRestoreIncomplete) || a != nil {
		t.Fatalf("incomplete restore accepted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "db")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("DB touched before restore guard: %v", err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	a, err := Open(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.OnStop(func() error {
		if err := a.Store.DB().Ping(); err != nil {
			t.Error("DB closed before worker joined")
		}
		other, err := coordination.AcquireDataDir(dir)
		if other != nil {
			other.Close()
		}
		if !errors.Is(err, coordination.ErrDataDirLocked) {
			t.Error("lock released before worker joined")
		}
		return nil
	})
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	if err = a.Store.DB().Ping(); err == nil {
		t.Fatal("database left open")
	}
	lock, err := coordination.AcquireDataDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	lock.Close()
}
