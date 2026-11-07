package content

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"yuanfa-official/backend/internal/contract"
	"yuanfa-official/backend/internal/media"
)

// ImportSeed is an offline initialization operation. The caller owns app.Open's
// data-directory lock from before reading the seed until every file/DB closes.
// A matching seed is a no-op even after editing; a different seed never replaces
// an initialized workspace. Source archives are verified before accepting IDs.
func (s *Service) ImportSeed(ctx context.Context, seedPath, mediaRoot string) error {
	if seedPath == "" || mediaRoot == "" {
		return invalid("seed", "media_root")
	}
	data, err := os.ReadFile(seedPath)
	if err != nil {
		return err
	}
	snapshot, err := contract.Decode[contract.CatalogSnapshot]("catalog-snapshot.schema.json#/$defs/PublishableSnapshot", data)
	if err != nil {
		return err
	}
	seedSHA := digest(data)
	var existing sql.NullString
	var revision int64
	if err = s.db.QueryRowContext(ctx, `SELECT seed_sha256,revision FROM workspace WHERE id=1`).Scan(&existing, &revision); err != nil {
		return err
	}
	if existing.Valid && existing.String == seedSHA {
		return nil
	}
	if existing.Valid || revision != 0 {
		return contract.NewBusinessError(409, "WORKSPACE_INITIALIZED", "初始化种子不能覆盖已有工作区。")
	}
	var adminID string
	if err = s.db.QueryRowContext(ctx, `SELECT id FROM admins WHERE id='admin'`).Scan(&adminID); errors.Is(err, sql.ErrNoRows) {
		return contract.NewBusinessError(409, "ADMIN_REQUIRED", "请先交互初始化管理员。")
	}
	if err != nil {
		return err
	}
	sources, err := archiveSources(seedPath)
	if err != nil {
		return err
	}
	d := dictionaries{Categories: snapshot.Categories, Scenes: snapshot.Scenes, NailTypes: snapshot.NailTypes, LegacySeries: snapshot.LegacySeries, ArchiveSources: sources}
	root, err := os.OpenRoot(mediaRoot)
	if err != nil {
		return err
	}
	defer root.Close()
	prepared := map[string]*media.Prepared{}
	defer func() {
		for _, p := range prepared {
			p.Close()
		}
	}()
	documents := make([]contract.ProductDocument, 0, len(snapshot.Products))
	for _, p := range snapshot.Products {
		if len(p.GuideProfile.Reviews) != 0 || len(p.PurchaseOptions) != 0 {
			return invalid("products.guide_profile.reviews", "products.purchase_options")
		}
		document := documentFromSnapshot(p)
		for _, img := range p.Images {
			if prior, ok := prepared[img.MediaID]; ok {
				if prior.Meta.SHA256 != img.SHA256 || prior.Meta.Format != img.Format || prior.Meta.Bytes != img.Bytes || prior.Meta.Width != img.Width || prior.Meta.Height != img.Height {
					return invalid("products.images")
				}
				continue
			}
			f, e := root.Open(img.Path)
			if e != nil {
				return e
			}
			staged, e := s.media.Prepare(ctx, f, img.Path)
			closeErr := f.Close()
			if e != nil {
				return e
			}
			if closeErr != nil {
				staged.Close()
				return closeErr
			}
			if staged.Meta.MediaID != img.MediaID || staged.Meta.SHA256 != img.SHA256 || staged.Meta.Format != img.Format || staged.Meta.Bytes != img.Bytes || staged.Meta.Width != img.Width || staged.Meta.Height != img.Height {
				staged.Close()
				return invalid("products.images")
			}
			prepared[img.MediaID] = staged
		}
		documents = append(documents, document)
	}
	s.gate.Lock()
	defer s.gate.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM products)+(SELECT count(*) FROM media)+(SELECT count(*) FROM candidates)+(SELECT count(*) FROM reserved_routes)`).Scan(&count); err != nil {
		return err
	}
	if count != 0 {
		return contract.NewBusinessError(409, "WORKSPACE_INITIALIZED", "初始化仅适用于空工作区。")
	}
	cleanups := []func(){}
	committed := false
	defer func() {
		if !committed {
			for _, cleanup := range cleanups {
				cleanup()
			}
		}
	}()
	for _, p := range prepared {
		_, cleanup, e := s.media.Install(ctx, tx, p, adminID)
		if e != nil {
			return e
		}
		cleanups = append(cleanups, cleanup)
	}
	for _, series := range snapshot.LegacySeries {
		if _, err = tx.ExecContext(ctx, `INSERT INTO reserved_routes(slug,owner_kind,owner_id,created_at) VALUES(?,'series',?,?)`, series.Slug, series.ID, "1970-01-01T00:00:00Z"); err != nil {
			return err
		}
	}
	for _, p := range documents {
		if err = s.references(ctx, tx, &p, d); err != nil {
			return err
		}
		if err = uniqueModel(ctx, tx, p); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO reserved_routes(slug,owner_kind,owner_id,created_at) VALUES(?,'product',?,?)`, p.Slug, p.ID, p.UpdatedAt); err != nil {
			return err
		}
		if _, err = save(ctx, tx, p, d, true); err != nil {
			return err
		}
	}
	b, err := canonical(d)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE workspace SET revision=?,seed_sha256=?,dictionaries_json=? WHERE id=1`, snapshot.WorkspaceRevision, seedSHA, string(b)); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}
func archiveSources(seedPath string) (map[string]string, error) {
	dir := filepath.Dir(seedPath)
	data, err := os.ReadFile(filepath.Join(dir, "provenance", "archive-records.json"))
	if err != nil {
		return nil, err
	}
	var records struct {
		SchemaVersion int `json:"schema_version"`
		Archives      []struct {
			ID     string `json:"id"`
			Path   string `json:"archive_path"`
			SHA256 string `json:"archive_sha256"`
		} `json:"archives"`
	}
	if err = json.Unmarshal(data, &records); err != nil {
		return nil, err
	}
	if records.SchemaVersion != 1 {
		return nil, invalid("archives.schema_version")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	out := map[string]string{}
	for _, archive := range records.Archives {
		path := strings.TrimPrefix(archive.Path, filepath.Base(dir)+"/")
		f, e := root.Open(path)
		if e != nil {
			return nil, e
		}
		b, e := io.ReadAll(f)
		closeErr := f.Close()
		if e != nil {
			return nil, e
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if archive.ID == "" || digest(b) != archive.SHA256 {
			return nil, invalid("archives")
		}
		if _, ok := out[archive.ID]; ok {
			return nil, invalid("archives.id")
		}
		out[archive.ID] = archive.SHA256
	}
	return out, nil
}
