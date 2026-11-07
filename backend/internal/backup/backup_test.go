package backup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"yuanfa-official/backend/internal/coordination"
	"yuanfa-official/backend/internal/release"
	"yuanfa-official/backend/internal/storage"
)

// Keep the WAL connection open but relinquish the external writer lock after all
// writes. This models committed WAL pages left by an exited writer, without a
// second active writer and without checkpointing them away before the export.
func walFixture(t *testing.T) (string, func()) {
	t.Helper()
	data := filepath.Join(t.TempDir(), "source")
	lock, err := coordination.AcquireDataDir(data)
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(context.Background(), lock)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.DB().Exec("PRAGMA wal_autocheckpoint=0; UPDATE workspace SET revision=42 WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	if err = lock.Close(); err != nil {
		t.Fatal(err)
	}
	return data, func() { store.Close() }
}
func TestCommittedWALSurvivesBackupAndEmptyRestore(t *testing.T) {
	ctx := context.Background()
	data, closeDB := walFixture(t)
	defer closeDB()
	wal, err := os.Stat(filepath.Join(data, "db/catalog.sqlite-wal"))
	if err != nil || wal.Size() == 0 {
		t.Fatal("fixture requires committed WAL", err)
	}
	lockBefore, err := os.Stat(filepath.Join(data, ".yuanfa-data.lock"))
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "backup")
	if _, err = Create(ctx, data, output); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "restored")
	if _, err = Restore(ctx, output, target); err != nil {
		t.Fatal(err)
	}
	db, err := openDB(ctx, filepath.Join(target, "db/catalog.sqlite"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var revision int
	if err = db.QueryRow("SELECT revision FROM workspace WHERE id=1").Scan(&revision); err != nil || revision != 42 {
		t.Fatalf("committed WAL revision = %d: %v", revision, err)
	}
	if _, err = os.Stat(filepath.Join(target, ".restore-incomplete")); !os.IsNotExist(err) {
		t.Fatal("successful restore retains incomplete marker", err)
	}
	lockAfter, _ := os.Stat(filepath.Join(data, ".yuanfa-data.lock"))
	if !os.SameFile(lockBefore, lockAfter) {
		t.Fatal("backup replaced permanent lock inode")
	}
	if _, err = Restore(ctx, output, target); err == nil {
		t.Fatal("restore overwrote an existing instance")
	}
}
func TestLockConflictNeverStartsBackup(t *testing.T) {
	data, closeDB := walFixture(t)
	defer closeDB()
	lock, err := coordination.AcquireDataDir(data)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	out := filepath.Join(t.TempDir(), "new")
	if _, err = Create(context.Background(), data, out); !errors.Is(err, coordination.ErrDataDirLocked) {
		t.Fatal(err)
	}
	if _, err = os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("conflicting backup touched output", err)
	}
}
func TestVerifyRejectsChangedAndMissingPayload(t *testing.T) {
	data, closeDB := walFixture(t)
	defer closeDB()
	ctx := context.Background()
	out := filepath.Join(t.TempDir(), "backup")
	if _, err := Create(ctx, data, out); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(out, "data/db/catalog.sqlite")
	original, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	changed := append([]byte(nil), original...)
	changed[len(changed)-1] ^= 1
	if err = os.WriteFile(name, changed, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Verify(ctx, out); err == nil {
		t.Fatal("verify accepted changed database")
	}
	if err = os.Remove(name); err != nil {
		t.Fatal(err)
	}
	if _, err = Verify(ctx, out); err == nil {
		t.Fatal("verify accepted missing database")
	}
	target := filepath.Join(t.TempDir(), "target")
	if _, err = Restore(ctx, out, target); err == nil {
		t.Fatal("restored invalid backup")
	}
	entries, _ := os.ReadDir(target)
	for _, entry := range entries {
		if entry.Name() != ".yuanfa-data.lock" {
			t.Fatal("invalid input wrote recovery data")
		}
	}
}
func TestMissingRetainedMediaDoesNotSealBackup(t *testing.T) {
	data, closeDB := walFixture(t)
	defer closeDB()
	// Use a real migrated DB and a retained, unassociated media row. Every retained
	// row is required even when no product, candidate or active pointer references it.
	lock, err := coordination.AcquireDataDir(data)
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(context.Background(), lock)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.DB().Exec("INSERT INTO admins VALUES ('admin','admin','hash','2026-11-07T00:00:00Z'); INSERT INTO media VALUES ('m_private',printf('%064d',0),'png',1,1,1,'missing.png','admin','2026-11-07T00:00:00Z')")
	store.Close()
	lock.Close()
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "failed")
	if _, err = Create(context.Background(), data, out); err == nil {
		t.Fatal("missing unassociated media accepted")
	}
	if _, err = os.Stat(filepath.Join(out, "manifest.json")); !os.IsNotExist(err) {
		t.Fatal("failed backup was sealed")
	}
	if _, err = os.Stat(filepath.Join(out, "failure.json")); err != nil {
		t.Fatal("failed backup lost diagnostics", err)
	}
}

// Real retained work directories contain build/catalog.json beside build.log.
// A valid sealed manifest orders complete paths, not depth-first directory walks.
func TestRestorePreservesNestedFilesBesideDottedFilenames(t *testing.T) {
	data, closeDB := walFixture(t)
	defer closeDB()
	ctx := context.Background()
	out := filepath.Join(t.TempDir(), "backup")
	m, err := Create(ctx, data, out)
	if err != nil {
		t.Fatal(err)
	}
	contents := map[string]string{
		"work/c_retained/build/catalog.json": "{}\n",
		"work/c_retained/build.log":          "retained build diagnostics\n",
	}
	for name, content := range contents {
		full := filepath.Join(out, "data", name)
		if err = os.MkdirAll(filepath.Dir(full), 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(full, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		m.Files = append(m.Files, File{Path: name, Bytes: int64(len(content)), SHA256: release.Hash([]byte(content))})
	}
	slices.SortFunc(m.Files, func(a, b File) int { return strings.Compare(a.Path, b.Path) })
	if err = os.Remove(filepath.Join(out, "manifest.json")); err != nil {
		t.Fatal(err)
	}
	if err = writeJSON(filepath.Join(out, "manifest.json"), m); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "restored")
	if _, err = Restore(ctx, out, target); err != nil {
		t.Fatal(err)
	}
	for name, want := range contents {
		got, err := os.ReadFile(filepath.Join(target, name))
		if err != nil || string(got) != want {
			t.Fatalf("retained file %s: %q, %v", name, got, err)
		}
	}
}
