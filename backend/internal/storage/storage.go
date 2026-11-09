// Package storage owns SQLite configuration and transactional SQL migrations.
package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	_ "modernc.org/sqlite"
	"yuanfa-official/backend/internal/coordination"
	"yuanfa-official/backend/migrations"
)

// Store uses one connection: all PRAGMAs are applied on every connection by the
// driver DSN. Services share DB(), using transactions for consumer invariants.
// The caller owns the data-directory lock and closes Store before that lock.
type Store struct{ db *sql.DB }

func (s *Store) DB() *sql.DB  { return s.db }
func (s *Store) Close() error { return s.db.Close() }

// Open requires a live exclusive data-directory lock. Migration failure returns
// no Store, so HTTP writers cannot start against a partially migrated schema.
func Open(ctx context.Context, lock *coordination.DataDirLock) (*Store, error) {
	return open(ctx, lock, migrations.Files)
}

func open(ctx context.Context, lock *coordination.DataDirLock, files fs.FS) (*Store, error) {
	dir, err := lock.Dir()
	if err != nil {
		return nil, err
	}
	// Read-only containers configure TMPDIR under the writable data volume.
	// Create it before SQLite can spill temporary tables or sort results to disk.
	if err = os.MkdirAll(os.TempDir(), 0700); err != nil {
		return nil, fmt.Errorf("create temporary directory: %w", err)
	}
	dbDir := filepath.Join(dir, "db")
	if err = os.MkdirAll(dbDir, 0700); err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: filepath.Join(dbDir, "catalog.sqlite")}
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "synchronous(FULL)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err = db.PingContext(ctx); err == nil {
		err = migrate(ctx, db, files)
	}
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("database migration: %w", err)
	}
	if err = os.Chmod(filepath.Join(dbDir, "catalog.sqlite"), 0600); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

type migration struct {
	version         int
	name, hash, sql string
}

var migrationName = regexp.MustCompile(`^([0-9]{3})_[a-z0-9_]+\.sql$`)

// migrate validates the complete history before executing pending SQL. All
// pending migrations and their ledger entries commit together or roll back.
func migrate(ctx context.Context, db *sql.DB, files fs.FS) error {
	entries, err := fs.ReadDir(files, ".")
	if err != nil {
		return err
	}
	history := make([]migration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".sql" {
			continue
		}
		m := migrationName.FindStringSubmatch(entry.Name())
		if m == nil {
			return errors.New("invalid migration filename")
		}
		v, _ := strconv.Atoi(m[1])
		if v != len(history)+1 {
			return errors.New("non-contiguous migration history")
		}
		body, err := fs.ReadFile(files, entry.Name())
		if err != nil {
			return err
		}
		history = append(history, migration{v, entry.Name(), fmt.Sprintf("%x", sha256.Sum256(body)), string(body)})
	}
	if len(history) == 0 {
		return errors.New("empty migration history")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL, sha256 TEXT NOT NULL, applied_at TEXT NOT NULL) STRICT`); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT version,name,sha256 FROM schema_migrations ORDER BY version`)
	if err != nil {
		return err
	}
	applied := 0
	for rows.Next() {
		var v int
		var name, hash string
		if err = rows.Scan(&v, &name, &hash); err != nil {
			rows.Close()
			return err
		}
		if v != applied+1 || v > len(history) || name != history[v-1].name || hash != history[v-1].hash {
			rows.Close()
			return errors.New("applied migration history mismatch")
		}
		applied++
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, m := range history[applied:] {
		if _, err = tx.ExecContext(ctx, m.sql); err != nil {
			return fmt.Errorf("migration %03d failed: %w", m.version, err)
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO schema_migrations VALUES(?,?,?,?)`, m.version, m.name, m.hash, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	return tx.Commit()
}
