package backup

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"

	"modernc.org/sqlite"
)

func openDB(ctx context.Context, name string, immutable bool) (*sql.DB, error) {
	u := url.URL{Scheme: "file", Path: name}
	q := url.Values{"mode": {"ro"}}
	if immutable {
		q.Set("immutable", "1")
	}
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err = db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}
func sqliteBackup(ctx context.Context, source, target string) error {
	if _, err := regular(filepath.Dir(filepath.Dir(source)), "db/catalog.sqlite"); err != nil {
		return err
	}
	db, err := openDB(ctx, source, false)
	if err != nil {
		return err
	}
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	err = conn.Raw(func(driver any) error {
		copier, ok := driver.(interface {
			NewBackup(string) (*sqlite.Backup, error)
		})
		if !ok {
			return errors.New("SQLite driver does not support backup API")
		}
		b, err := copier.NewBackup(target)
		if err != nil {
			return err
		}
		for {
			if err = ctx.Err(); err != nil {
				break
			}
			more, e := b.Step(256)
			if e != nil {
				err = e
				break
			}
			if !more {
				break
			}
		}
		return errors.Join(err, b.Finish())
	})
	if err != nil {
		return err
	}
	// Convert the exported copy to a self-contained non-WAL database. Source pages
	// are never copied as files and committed source WAL pages are included above.
	copyDB, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: target}).String())
	if err != nil {
		return err
	}
	_, err = copyDB.ExecContext(ctx, "PRAGMA journal_mode=DELETE")
	err = errors.Join(err, copyDB.Close())
	if err != nil {
		return err
	}
	return os.Chmod(target, 0600)
}
