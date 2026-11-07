// Package media owns validated private image storage. Uploads do not change the
// workspace. Association and deletion share the application's transaction Gate.
//
// HTTP validates a complete single-file multipart envelope before UploadPrepared.
// Offline initialization uses Prepare and Install under its own transaction and
// data-directory lock. List's after is the latest upload watermark; keep the
// original after while following next_cursor to enumerate new uploads. Upload,
// deletion and private File access require a browser actor; MCP may list media.
// Original filenames are not persisted in the storage contract and return empty.
package media

import (
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "golang.org/x/image/webp"
	"yuanfa-official/backend/internal/auth"
	"yuanfa-official/backend/internal/contract"
	"yuanfa-official/backend/internal/coordination"
)

const MaxBytes int64 = 10 * 1024 * 1024
const stamp = "2006-01-02T15:04:05.000000000Z"

type Service struct {
	db   *sql.DB
	auth *auth.Service
	gate *coordination.Gate
	dir  string
}

func New(db *sql.DB, identity *auth.Service, gate *coordination.Gate, dataDir string) *Service {
	return &Service{db: db, auth: identity, gate: gate, dir: dataDir}
}
func invalid() error {
	return contract.NewBusinessError(422, "INVALID_IMAGE", "仅支持可解码的 JPEG、PNG 和静态 WebP 图片。", "file")
}
func tooLarge() error {
	return contract.NewBusinessError(413, "IMAGE_TOO_LARGE", "图片超过体积或尺寸上限。", "file")
}
func missing() error { return contract.NewBusinessError(404, "MEDIA_NOT_FOUND", "素材不存在。") }

// Prepared is a bounded, decoded temporary file on the destination filesystem.
// Close removes the staging file; callers must defer it even after installation.
type Prepared struct {
	Meta contract.SnapshotMedia
	path string
}

func (p *Prepared) Close() error {
	err := os.Remove(p.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(b)
}

// Prepare is shared by multipart upload and the offline seed importer. It checks
// extension against decoded format and rejects animation before pixel decoding.
func (s *Service) Prepare(ctx context.Context, r io.Reader, filename string) (*Prepared, error) {
	dir := filepath.Join(s.dir, "media")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	file, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		return nil, err
	}
	p := &Prepared{path: file.Name()}
	ok := false
	defer func() {
		file.Close()
		if !ok {
			p.Close()
		}
	}()
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(contextReader{ctx, r}, MaxBytes+1))
	if err != nil {
		return nil, err
	}
	if n > MaxBytes {
		return nil, tooLarge()
	}
	if n == 0 {
		return nil, invalid()
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	cfg, format, err := image.DecodeConfig(bufio.NewReader(file))
	if err != nil {
		return nil, invalid()
	}
	ext := strings.ToLower(filepath.Ext(filename))
	if !(format == "jpeg" && (ext == ".jpg" || ext == ".jpeg") || format == "png" && ext == ".png" || format == "webp" && ext == ".webp") {
		return nil, invalid()
	}
	if cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 12000 || cfg.Height > 12000 || int64(cfg.Width)*int64(cfg.Height) > 40000000 {
		return nil, tooLarge()
	}
	if err = staticImage(file, format, n); err != nil {
		return nil, err
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	decoded, actual, err := image.Decode(bufio.NewReader(file))
	if err != nil || actual != format || decoded.Bounds().Dx() != cfg.Width || decoded.Bounds().Dy() != cfg.Height {
		return nil, invalid()
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if err = file.Sync(); err != nil {
		return nil, err
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	extension := format
	if format == "jpeg" {
		extension = "jpg"
	}
	p.Meta = contract.SnapshotMedia{MediaID: "m_" + digest, SHA256: digest, Format: format, Bytes: n, Width: int64(cfg.Width), Height: int64(cfg.Height), Path: digest + "." + extension}
	ok = true
	return p, nil
}
func staticImage(file *os.File, format string, size int64) error {
	if format == "jpeg" {
		return nil
	}
	start := int64(8)
	little := false
	if format == "webp" {
		start = 12
		little = true
		var header [12]byte
		if _, err := file.ReadAt(header[:], 0); err != nil || string(header[:4]) != "RIFF" || string(header[8:]) != "WEBP" || int64(binary.LittleEndian.Uint32(header[4:8]))+8 != size {
			return invalid()
		}
	}
	for pos := start; pos < size; {
		var header [8]byte
		if _, err := file.ReadAt(header[:], pos); err != nil {
			return invalid()
		}
		var length int64
		var kind string
		if little {
			length = int64(binary.LittleEndian.Uint32(header[4:]))
			kind = string(header[:4])
		} else {
			length = int64(binary.BigEndian.Uint32(header[:4]))
			kind = string(header[4:])
		}
		if kind == "acTL" || kind == "fcTL" || kind == "fdAT" || kind == "ANIM" || kind == "ANMF" {
			return invalid()
		}
		if little && kind == "VP8X" {
			var flags [1]byte
			if length < 1 {
				return invalid()
			}
			if _, err := file.ReadAt(flags[:], pos+8); err != nil || flags[0]&2 != 0 {
				return invalid()
			}
		}
		next := pos + 8 + length
		if little {
			next += length % 2
		} else {
			next += 4
		}
		if next > size || next <= pos {
			return invalid()
		}
		pos = next
		if !little && kind == "IEND" {
			if length != 0 || pos != size {
				return invalid()
			}
			return nil
		}
	}
	if little {
		return nil
	}
	return invalid()
}

func record(meta contract.SnapshotMedia, uploaded string) contract.MediaRecord {
	return contract.MediaRecord{MediaID: meta.MediaID, SHA256: meta.SHA256, Format: meta.Format, Bytes: meta.Bytes, Width: meta.Width, Height: meta.Height, OriginalFilename: "", UploadedAt: uploaded, PreviewURL: "/api/admin/media/" + meta.MediaID + "/file"}
}

type scanner interface{ Scan(...any) error }

func scan(row scanner) (contract.SnapshotMedia, string, error) {
	var m contract.SnapshotMedia
	var t string
	err := row.Scan(&m.MediaID, &m.SHA256, &m.Format, &m.Bytes, &m.Width, &m.Height, &m.Path, &t)
	return m, t, err
}

const columns = "id,sha256,format,bytes,width,height,path,created_at"

// Install installs a prepared image in the caller's transaction under Gate.
// The returned cleanup removes only a newly installed file on transaction failure.
// Seed IDs must agree with the SHA identity, so deduplication cannot alias IDs.
func (s *Service) Install(ctx context.Context, tx *sql.Tx, p *Prepared, adminID string) (contract.MediaRecord, func(), error) {
	m, t, err := scan(tx.QueryRowContext(ctx, "SELECT "+columns+" FROM media WHERE sha256=?", p.Meta.SHA256))
	if err == nil {
		return record(m, t), func() {}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return contract.MediaRecord{}, nil, err
	}
	destination := filepath.Join(s.dir, "media", p.Meta.Path)
	if err = os.Rename(p.path, destination); err != nil {
		return contract.MediaRecord{}, nil, err
	}
	cleanup := func() { _ = os.Remove(destination) }
	if err = syncDirectory(filepath.Dir(destination)); err != nil {
		cleanup()
		return contract.MediaRecord{}, nil, err
	}
	t = time.Now().UTC().Format(stamp)
	m = p.Meta
	_, err = tx.ExecContext(ctx, `INSERT INTO media(id,sha256,format,bytes,width,height,path,uploaded_by,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, m.MediaID, m.SHA256, m.Format, m.Bytes, m.Width, m.Height, m.Path, adminID, t)
	if err != nil {
		cleanup()
		return contract.MediaRecord{}, nil, err
	}
	return record(m, t), cleanup, nil
}
func syncDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
func (s *Service) Upload(ctx context.Context, actor auth.Actor, r io.Reader, filename string) (contract.MediaRecord, error) {
	var out contract.MediaRecord
	if err := actor.RequireBrowser(); err != nil {
		return out, err
	}
	if err := s.auth.Authorize(ctx, actor, ""); err != nil {
		return out, err
	}
	p, err := s.Prepare(ctx, r, filename)
	if err != nil {
		return out, err
	}
	defer p.Close()
	return s.UploadPrepared(ctx, actor, p)
}

// UploadPrepared is used after the HTTP adapter has verified the entire single
// file multipart envelope. Authorization is rechecked before starting the write.
func (s *Service) UploadPrepared(ctx context.Context, actor auth.Actor, p *Prepared) (contract.MediaRecord, error) {
	var out contract.MediaRecord
	if err := actor.RequireBrowser(); err != nil {
		return out, err
	}
	if err := s.auth.Authorize(ctx, actor, ""); err != nil {
		return out, err
	}
	s.gate.Lock()
	defer s.gate.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	out, cleanup, err := s.Install(ctx, tx, p, actor.AdminID())
	if err != nil {
		return out, err
	}
	if err = tx.Commit(); err != nil {
		cleanup()
	}
	return out, err
}

type position struct {
	Version int    `json:"v"`
	Time    string `json:"t"`
	ID      string `json:"id"`
}

func encode(p position) string {
	b, _ := json.Marshal(p)
	return base64.RawURLEncoding.EncodeToString(b)
}
func decode(text string) (position, error) {
	var p position
	b, err := base64.RawURLEncoding.DecodeString(text)
	if err != nil {
		return p, invalidCursor()
	}
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	if d.Decode(&p) != nil || p.Version != 1 || len(p.ID) > 120 {
		return p, invalidCursor()
	}
	if p.Time != "" {
		if _, err = time.Parse(stamp, p.Time); err != nil {
			return p, invalidCursor()
		}
	}
	return p, nil
}
func invalidCursor() error {
	return contract.NewBusinessError(422, "INVALID_CURSOR", "素材游标不合法。", "cursor")
}
func (s *Service) List(ctx context.Context, actor auth.Actor, q contract.MediaQuery) (contract.MediaList, error) {
	out := contract.MediaList{Items: []contract.MediaRecord{}}
	if err := s.auth.Authorize(ctx, actor, "media:read"); err != nil {
		return out, err
	}
	b, err := json.Marshal(q)
	if err != nil {
		return out, err
	}
	q, err = contract.Decode[contract.MediaQuery]("admin.schema.json#/$defs/MediaQuery", b)
	if err != nil {
		return out, err
	}
	var lower position
	lower.Version = 1
	for _, v := range []*string{q.After, q.Cursor} {
		if v != nil {
			p, e := decode(*v)
			if e != nil {
				return out, e
			}
			if p.Time > lower.Time || p.Time == lower.Time && p.ID > lower.ID {
				lower = p
			}
		}
	}
	limit := int64(20)
	if q.Limit != nil {
		limit = *q.Limit
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	latest := position{Version: 1}
	err = tx.QueryRowContext(ctx, `SELECT created_at,id FROM media ORDER BY created_at DESC,id DESC LIMIT 1`).Scan(&latest.Time, &latest.ID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	out.After = encode(latest)
	rows, err := tx.QueryContext(ctx, "SELECT "+columns+" FROM media WHERE (created_at>? OR (created_at=? AND id>?)) ORDER BY created_at,id LIMIT ?", lower.Time, lower.Time, lower.ID, limit+1)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	var last position
	for rows.Next() {
		m, t, e := scan(rows)
		if e != nil {
			return out, e
		}
		if int64(len(out.Items)) == limit {
			cursor := encode(last)
			out.NextCursor = &cursor
			break
		}
		out.Items = append(out.Items, record(m, t))
		last = position{Version: 1, Time: t, ID: m.MediaID}
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	return out, nil
}

// File returns an already-open descriptor while holding Gate against deletion.
// The HTTP adapter closes it after serving; no private pathname is exposed.
func (s *Service) File(ctx context.Context, actor auth.Actor, id string) (*os.File, contract.MediaRecord, error) {
	if err := actor.RequireBrowser(); err != nil {
		return nil, contract.MediaRecord{}, err
	}
	if err := s.auth.Authorize(ctx, actor, "media:read"); err != nil {
		return nil, contract.MediaRecord{}, err
	}
	s.gate.RLock()
	defer s.gate.RUnlock()
	m, t, err := scan(s.db.QueryRowContext(ctx, "SELECT "+columns+" FROM media WHERE id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		err = missing()
	}
	if err != nil {
		return nil, contract.MediaRecord{}, err
	}
	root, err := os.OpenRoot(filepath.Join(s.dir, "media"))
	if err != nil {
		return nil, contract.MediaRecord{}, err
	}
	defer root.Close()
	f, err := root.Open(m.Path)
	return f, record(m, t), err
}
func (s *Service) Delete(ctx context.Context, actor auth.Actor, id string) error {
	if err := actor.RequireBrowser(); err != nil {
		return err
	}
	if err := s.auth.Authorize(ctx, actor, ""); err != nil {
		return err
	}
	s.gate.Lock()
	defer s.gate.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	m, _, err := scan(tx.QueryRowContext(ctx, "SELECT "+columns+" FROM media WHERE id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return missing()
	}
	if err != nil {
		return err
	}
	var used int
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM products p WHERE p.deleted_at IS NULL AND (EXISTS(SELECT 1 FROM json_each(p.document_json,'$.images') i WHERE json_extract(i.value,'$.media_id')=?) OR EXISTS(SELECT 1 FROM json_each(p.document_json,'$.guide_profile.sources') s WHERE json_extract(s.value,'$.kind')='media' AND json_extract(s.value,'$.reference')=?))) OR EXISTS(SELECT 1 FROM candidates c,json_each(c.snapshot_json,'$.products') p WHERE EXISTS(SELECT 1 FROM json_each(p.value,'$.images') i WHERE json_extract(i.value,'$.media_id')=?) OR EXISTS(SELECT 1 FROM json_each(p.value,'$.guide_profile.sources') s WHERE json_extract(s.value,'$.kind')='media' AND json_extract(s.value,'$.reference')=?))`, id, id, id, id).Scan(&used)
	if err != nil {
		return err
	}
	if used != 0 {
		return contract.NewBusinessError(409, "MEDIA_REFERENCED", "素材仍被工作区或保留版本引用。")
	}
	// active.json remains authoritative even if a crash left task rows incomplete.
	active, err := os.ReadFile(filepath.Join(s.dir, "state", "active.json"))
	if err == nil {
		authority, e := contract.Decode[contract.ActiveRelease]("catalog-snapshot.schema.json#/$defs/ActiveRelease", active)
		if e != nil {
			return e
		}
		for _, release := range authority.SuccessfulReleaseIDs {
			data, e := os.ReadFile(filepath.Join(s.dir, "artifacts", release, "catalog.json"))
			if e != nil {
				return e
			}
			snapshot, e := contract.Decode[contract.CatalogSnapshot]("catalog-snapshot.schema.json#/$defs/CatalogSnapshot", data)
			if e != nil {
				return e
			}
			for _, p := range snapshot.Products {
				for _, image := range p.Images {
					if image.MediaID == id {
						return contract.NewBusinessError(409, "MEDIA_REFERENCED", "素材仍被成功版本引用。")
					}
				}
				for _, source := range p.GuideProfile.Sources {
					if source.Kind == "media" && source.Reference == id {
						return contract.NewBusinessError(409, "MEDIA_REFERENCED", "素材仍被成功版本引用。")
					}
				}
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	// Commit record removal first: interruption can leave an unreferenced file,
	// but must never leave a retained media record pointing at a missing file.
	if _, err = tx.ExecContext(ctx, `DELETE FROM media WHERE id=?`, id); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if err = os.Remove(filepath.Join(s.dir, "media", m.Path)); err != nil {
		return err
	}
	return syncDirectory(filepath.Join(s.dir, "media"))
}

// Metadata reads association metadata inside the caller's transaction. It never
// opens a second database connection while that transaction owns the pool.
func Metadata(ctx context.Context, tx *sql.Tx, id string) (contract.SnapshotMedia, error) {
	m, _, err := scan(tx.QueryRowContext(ctx, "SELECT "+columns+" FROM media WHERE id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return m, contract.NewBusinessError(422, "INVALID_MEDIA_REFERENCE", "引用的素材不存在。", "images")
	}
	return m, err
}
