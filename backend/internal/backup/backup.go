// Package backup creates sealed offline recovery sets. Callers must stop serve;
// every source/target operation holds the permanent coordination flock throughout.
package backup

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"yuanfa-official/backend/internal/coordination"
	"yuanfa-official/backend/internal/release"
)

// Manifest is private backup metadata, unrelated to the public artifact manifest.
// Files is the exact recovery payload; manifest.json is published only at success.
type Manifest struct {
	FormatVersion int    `json:"format_version"`
	SchemaVersion int    `json:"schema_version"`
	CreatedAt     string `json:"created_at"`
	Files         []File `json:"files"`
}
type File struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

// Create snapshots SQLite with its backup API, copies retained files, then verifies
// the complete recovery set before sealing it. Errors leave private evidence.
func Create(ctx context.Context, data, output string) (_ *Manifest, err error) {
	lock, err := coordination.AcquireDataDir(data)
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	data, _ = lock.Dir()
	if err = noIncomplete(data); err != nil {
		return nil, err
	}
	output, err = canonical(output)
	if err != nil {
		return nil, err
	}
	if overlap(data, output) {
		return nil, errors.New("backup output must be outside DATA_DIR")
	}
	if err = emptyDirectory(output, false); err != nil {
		return nil, err
	}
	if err = os.MkdirAll(output, 0700); err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(filepath.Join(output, "manifest.json"))
			_ = writeJSON(filepath.Join(output, "failure.json"), map[string]string{"error": err.Error(), "failed_at": time.Now().UTC().Format(time.RFC3339Nano)})
		}
	}()
	if err = release.Barrier(ctx, "backup-locked"); err != nil {
		return nil, err
	}
	payload := filepath.Join(output, "data")
	if err = os.MkdirAll(filepath.Join(payload, "db"), 0700); err != nil {
		return nil, err
	}
	if err = sqliteBackup(ctx, filepath.Join(data, "db/catalog.sqlite"), filepath.Join(payload, "db/catalog.sqlite")); err != nil {
		return nil, err
	}
	db, err := openDB(ctx, filepath.Join(payload, "db/catalog.sqlite"), true)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	paths, err := retainedFiles(ctx, db, data)
	if err != nil {
		return nil, err
	}
	for _, name := range paths {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		if err = copyFile(data, payload, name); err != nil {
			return nil, err
		}
	}
	if err = validateData(ctx, db, payload); err != nil {
		return nil, err
	}
	files, err := inventory(payload)
	if err != nil {
		return nil, err
	}
	manifest := &Manifest{1, 1, time.Now().UTC().Format(time.RFC3339Nano), files}
	// Verify the same data and inventory while still holding the source lock.
	if err = verifyPayload(ctx, output, manifest); err != nil {
		return nil, err
	}
	if err = release.Barrier(ctx, "backup-before-seal"); err != nil {
		return nil, err
	}
	if err = syncTree(payload); err != nil {
		return nil, err
	}
	if err = writeJSON(filepath.Join(output, "manifest.json"), manifest); err != nil {
		return nil, err
	}
	if err = release.SyncDir(output); err != nil {
		return nil, err
	}
	return manifest, nil
}

// Verify only reads a sealed backup, never the running DATA_DIR or its WAL.
func Verify(ctx context.Context, directory string) (*Manifest, error) {
	directory, err := canonical(directory)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.Name() != "data" && entry.Name() != "manifest.json" {
			return nil, errors.New("backup root contains incomplete or unexpected files")
		}
	}
	raw, err := readRegular(directory, "manifest.json")
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err = strictJSON(raw, &m); err != nil {
		return nil, err
	}
	if m.FormatVersion != 1 || m.SchemaVersion != 1 || len(m.Files) == 0 {
		return nil, errors.New("unsupported or empty backup manifest")
	}
	if _, err = time.Parse(time.RFC3339Nano, m.CreatedAt); err != nil {
		return nil, err
	}
	if err = verifyPayload(ctx, directory, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// Restore writes only an empty destination (permanent lock excepted). On any
// failure after writing starts, serve is blocked by .restore-incomplete.
func Restore(ctx context.Context, directory, data string) (_ *Manifest, err error) {
	lock, err := coordination.AcquireDataDir(data)
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	data, _ = lock.Dir()
	directory, err = canonical(directory)
	if err != nil {
		return nil, err
	}
	if overlap(data, directory) {
		return nil, errors.New("restore source and target overlap")
	}
	if err = emptyDirectory(data, true); err != nil {
		return nil, err
	}
	m, err := Verify(ctx, directory)
	if err != nil {
		return nil, err
	}
	marker := filepath.Join(data, ".restore-incomplete")
	if err = os.WriteFile(marker, []byte("Recovery in progress; do not start serve.\n"), 0600); err != nil {
		return nil, err
	}
	if err = release.SyncDir(data); err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = writeJSON(filepath.Join(data, "restore-failure.json"), map[string]string{"error": err.Error()})
		}
	}()
	if err = release.Barrier(ctx, "restore-locked"); err != nil {
		return nil, err
	}
	for _, f := range m.Files {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		if err = copyFile(filepath.Join(directory, "data"), data, f.Path); err != nil {
			return nil, err
		}
	}
	// Compare exact bytes before opening SQLite; restore never migrates or crops DB.
	if err = compareInventory(data, m.Files, true); err != nil {
		return nil, err
	}
	db, err := openDB(ctx, filepath.Join(data, "db/catalog.sqlite"), true)
	if err != nil {
		return nil, err
	}
	err = validateData(ctx, db, data)
	err = errors.Join(err, db.Close())
	if err != nil {
		return nil, err
	}
	if err = syncTree(data); err != nil {
		return nil, err
	}
	if err = os.Remove(marker); err != nil {
		return nil, err
	}
	if err = release.SyncDir(data); err != nil {
		_ = os.WriteFile(marker, []byte("Final directory sync failed.\n"), 0600)
		return nil, err
	}
	return m, nil
}
func noIncomplete(data string) error {
	_, err := os.Lstat(filepath.Join(data, ".restore-incomplete"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return errors.New("RESTORE_INCOMPLETE")
}
func verifyPayload(ctx context.Context, directory string, m *Manifest) error {
	payload := filepath.Join(directory, "data")
	if err := compareInventory(payload, m.Files, false); err != nil {
		return err
	}
	db, err := openDB(ctx, filepath.Join(payload, "db/catalog.sqlite"), true)
	if err != nil {
		return err
	}
	defer db.Close()
	return validateData(ctx, db, payload)
}
func fail(format string, args ...any) error { return fmt.Errorf(format, args...) }
