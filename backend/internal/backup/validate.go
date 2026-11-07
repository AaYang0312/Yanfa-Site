package backup

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"yuanfa-official/backend/internal/contract"
	"yuanfa-official/backend/internal/coordination"
	"yuanfa-official/backend/internal/release"
	"yuanfa-official/backend/migrations"
)

func retainedFiles(ctx context.Context, db *sql.DB, source string) ([]string, error) {
	paths := map[string]bool{}
	rows, err := db.QueryContext(ctx, "SELECT path FROM media ORDER BY id")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			break
		}
		if !safe(name) {
			err = errors.New("unsafe media path")
			break
		}
		paths["media/"+name] = true
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return nil, err
	}
	rows, err = db.QueryContext(ctx, "SELECT id,state FROM candidates ORDER BY id")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, state string
		if err = rows.Scan(&id, &state); err != nil {
			break
		}
		if !candidatePath(id) {
			err = errors.New("unsafe candidate ID")
			break
		}
		artifact := "artifacts/" + id
		if _, e := os.Lstat(filepath.Join(source, artifact)); e == nil {
			var files []string
			files, err = treeFiles(source, artifact)
			if err != nil {
				break
			}
			for _, f := range files {
				paths[f] = true
			}
		} else if !errors.Is(e, fs.ErrNotExist) || state == "ready" {
			err = fail("retained artifact missing: %s", id)
			break
		}
		// Frozen DB JSON is authoritative. Preserve existing private input/log/staging
		// outputs, excluding disposable templates, caches and dependency symlinks.
		for _, suffix := range []string{"catalog.json", "build.log", "build/catalog.json", "build/manifest.json"} {
			name := "work/" + id + "/" + suffix
			var exists bool
			exists, err = optionalFile(source, name)
			if err != nil {
				break
			}
			if exists {
				paths[name] = true
			}
		}
		if err != nil {
			break
		}
		staging := "work/" + id + "/artifact"
		if _, e := os.Lstat(filepath.Join(source, staging)); e == nil {
			var files []string
			files, err = treeFiles(source, staging)
			if err != nil {
				break
			}
			for _, f := range files {
				paths[f] = true
			}
		} else if !errors.Is(e, fs.ErrNotExist) {
			err = e
			break
		}
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return nil, err
	}
	if exists, e := optionalFile(source, "state/active.json"); e != nil {
		return nil, e
	} else if exists {
		paths["state/active.json"] = true
	}
	result := make([]string, 0, len(paths))
	for name := range paths {
		result = append(result, name)
	}
	slices.Sort(result)
	return result, nil
}
func validateSchema(ctx context.Context, db *sql.DB) error {
	var integrity string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil {
		return err
	}
	if integrity != "ok" {
		return errors.New("SQLite integrity check failed")
	}
	rows, err := db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	has := rows.Next()
	err = errors.Join(rows.Err(), rows.Close())
	if err != nil {
		return err
	}
	if has {
		return errors.New("SQLite foreign key check failed")
	}
	entries, err := migrations.Files.ReadDir(".")
	if err != nil {
		return err
	}
	rows, err = db.QueryContext(ctx, "SELECT version,name,sha256 FROM schema_migrations ORDER BY version")
	if err != nil {
		return err
	}
	count := 0
	for rows.Next() {
		var version int
		var name, hash string
		if err = rows.Scan(&version, &name, &hash); err != nil {
			break
		}
		body, e := migrations.Files.ReadFile(name)
		if e != nil || version != count+1 || hash != fmt.Sprintf("%x", sha256.Sum256(body)) {
			err = errors.New("unsupported database migration history")
			break
		}
		count++
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return err
	}
	expected := 0
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".sql" {
			expected++
		}
	}
	if count != expected {
		return errors.New("database schema does not match this runtime")
	}
	return nil
}
func validateData(ctx context.Context, db *sql.DB, data string) error {
	if err := validateSchema(ctx, db); err != nil {
		return err
	}
	media := map[string]contract.SnapshotMedia{}
	rows, err := db.QueryContext(ctx, "SELECT id,sha256,format,bytes,width,height,path FROM media ORDER BY id")
	if err != nil {
		return err
	}
	for rows.Next() {
		var m contract.SnapshotMedia
		if err = rows.Scan(&m.MediaID, &m.SHA256, &m.Format, &m.Bytes, &m.Width, &m.Height, &m.Path); err != nil {
			break
		}
		var file File
		file, err = digest(data, "media/"+m.Path)
		if err != nil {
			break
		}
		if file.Bytes != m.Bytes || file.SHA256 != m.SHA256 {
			err = fail("media checksum mismatch: %s", m.MediaID)
			break
		}
		media[m.MediaID] = m
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return err
	}
	rows, err = db.QueryContext(ctx, "SELECT id,slug,version,document_json FROM products WHERE deleted_at IS NULL")
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, slug, raw string
		var version int64
		if err = rows.Scan(&id, &slug, &version, &raw); err != nil {
			break
		}
		var p contract.ProductDocument
		p, err = contract.Decode[contract.ProductDocument]("product.schema.json#/$defs/ProductDocument", []byte(raw))
		if err != nil {
			break
		}
		if p.ID != id || p.Slug != slug || p.Version != version {
			err = errors.New("workspace product identity mismatch")
			break
		}
		for _, image := range p.Images {
			if _, ok := media[image.MediaID]; !ok {
				err = fail("workspace media missing: %s", image.MediaID)
				break
			}
		}
		if err != nil {
			break
		}
		if err = sourceRefs(p.GuideProfile.Sources, media); err != nil {
			break
		}
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return err
	}
	rows, err = db.QueryContext(ctx, "SELECT id,state,schema_version,workspace_revision,code_version,snapshot_json,snapshot_sha256,artifact_path,manifest_sha256,validation_json FROM candidates")
	if err != nil {
		return err
	}
	candidates := map[string]bool{}
	for rows.Next() {
		var id, state, code, raw, sha, validation string
		var schema, revision int64
		var artifact, manifest sql.NullString
		if err = rows.Scan(&id, &state, &schema, &revision, &code, &raw, &sha, &artifact, &manifest, &validation); err != nil {
			break
		}
		if !candidatePath(id) || schema != contract.SchemaVersion || release.Hash([]byte(raw)) != sha || !json.Valid([]byte(validation)) {
			err = errors.New("candidate schema or digest mismatch")
			break
		}
		var snapshot contract.CatalogSnapshot
		snapshot, err = contract.Decode[contract.CatalogSnapshot]("catalog-snapshot.schema.json#/$defs/CatalogSnapshot", []byte(raw))
		if err != nil {
			break
		}
		if snapshot.ReleaseID != id || snapshot.WorkspaceRevision != revision {
			err = errors.New("candidate identity mismatch")
			break
		}
		for _, p := range snapshot.Products {
			for _, image := range p.Images {
				m, ok := media[image.MediaID]
				if !ok || m.SHA256 != image.SHA256 || m.Path != image.Path || m.Bytes != image.Bytes || m.Format != image.Format || m.Width != image.Width || m.Height != image.Height {
					err = fail("candidate media mismatch: %s", image.MediaID)
					break
				}
			}
			if err != nil {
				break
			}
			if err = sourceRefs(p.GuideProfile.Sources, media); err != nil {
				break
			}
		}
		if err != nil {
			break
		}
		if artifact.Valid && artifact.String != "artifacts/"+id {
			err = errors.New("candidate artifact path mismatch")
			break
		}
		_, existsErr := os.Lstat(filepath.Join(data, "artifacts", id))
		if state == "ready" || existsErr == nil {
			var h *release.Handle
			h, err = release.Load(data, id)
			if err != nil {
				break
			}
			if h.SHA256() != sha || h.CodeVersion() != code || (manifest.Valid && h.ManifestSHA256() != manifest.String) {
				err = errors.New("candidate artifact identity mismatch")
				break
			}
			exact, e := readRegular(data, "artifacts/"+id+"/catalog.json")
			if e != nil || string(exact) != raw {
				err = errors.New("candidate artifact snapshot differs from database")
				break
			}
		} else if !errors.Is(existsErr, fs.ErrNotExist) {
			err = existsErr
			break
		}
		if exists, e := optionalFile(data, "work/"+id+"/catalog.json"); e != nil {
			err = e
			break
		} else if exists {
			b, e := readRegular(data, "work/"+id+"/catalog.json")
			if e != nil || string(b) != raw {
				err = errors.New("candidate work snapshot mismatch")
				break
			}
		}
		candidates[id] = true
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return err
	}
	manager := release.New(data, &coordination.Gate{})
	if err = manager.Restore(); err != nil {
		return err
	}
	active := manager.Active()
	if active != nil {
		for _, id := range active.SuccessfulReleaseIDs {
			if !candidates[id] {
				return errors.New("active history candidate missing")
			}
		}
		var target, sha, state string
		err = db.QueryRowContext(ctx, "SELECT COALESCE(candidate_id,release_id),snapshot_sha256,state FROM publication_requests WHERE id=?", active.OperationID).Scan(&target, &sha, &state)
		if err != nil || target != active.ReleaseID || sha != active.SnapshotSHA256 || (state != "succeeded" && state != "activating") {
			return errors.New("active operation does not match persistent request")
		}
	}
	return ctx.Err()
}
func sourceRefs(sources []contract.GuideSource, media map[string]contract.SnapshotMedia) error {
	for _, source := range sources {
		if source.Kind == "media" {
			m, ok := media[source.Reference]
			if !ok || m.SHA256 != source.SHA256 {
				return errors.New("guide source media mismatch")
			}
		}
	}
	return nil
}
