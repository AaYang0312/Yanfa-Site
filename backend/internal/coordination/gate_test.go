package coordination

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestProcessLockAndPermanentInode(t *testing.T) {
	dir := t.TempDir()
	lock, err := AcquireDataDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	before, err := os.Stat(filepath.Join(dir, ".yuanfa-data.lock"))
	if err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err = os.Symlink(dir, alias); err != nil {
		t.Fatal(err)
	}
	if second, err := AcquireDataDir(alias); !errors.Is(err, ErrDataDirLocked) {
		if second != nil {
			second.Close()
		}
		t.Fatalf("symlink bypassed lock: %v", err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command(binary, "-test.run=^TestLockChild$")
	child.Env = append(os.Environ(), "YUANFA_LOCK_TEST_DIR="+alias)
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("independent process: %s %v", output, err)
	}
	if err = lock.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := AcquireDataDir(alias)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	after, err := os.Stat(filepath.Join(dir, ".yuanfa-data.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("lock inode replaced")
	}
	canonical, err := next.Dir()
	if err != nil || canonical != dir {
		t.Fatalf("noncanonical DATA_DIR: %q %v", canonical, err)
	}
}
func TestLockChild(t *testing.T) {
	dir := os.Getenv("YUANFA_LOCK_TEST_DIR")
	if dir == "" {
		t.Skip("subprocess helper")
	}
	lock, err := AcquireDataDir(dir)
	if lock != nil {
		lock.Close()
	}
	if !errors.Is(err, ErrDataDirLocked) {
		t.Fatalf("child lock conflict: %v", err)
	}
}
