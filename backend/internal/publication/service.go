// Package publication freezes entire workspaces and owns persistent serial build
// and activation queues. It shares the application's Store/Auth/Gate lifetime.
package publication

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"yuanfa-official/backend/internal/auth"
	"yuanfa-official/backend/internal/config"
	"yuanfa-official/backend/internal/content"
	"yuanfa-official/backend/internal/contract"
	"yuanfa-official/backend/internal/coordination"
	"yuanfa-official/backend/internal/release"
)

type Service struct {
	db       *sql.DB
	auth     *auth.Service
	content  *content.Service
	gate     *coordination.Gate
	releases *release.Manager
	cfg      config.Config
	code     string
	ctx      context.Context
	cancel   context.CancelFunc
	wake     chan struct{}
	done     sync.WaitGroup
	ready    sync.Map // candidate ID -> verified immutable *release.Handle
}

func New(db *sql.DB, identity *auth.Service, workspace *content.Service, gate *coordination.Gate, releases *release.Manager, cfg config.Config, code string) *Service {
	return &Service{db: db, auth: identity, content: workspace, gate: gate, releases: releases, cfg: cfg, code: code, wake: make(chan struct{}, 1)}
}
func now() string { return time.Now().UTC().Format("2006-01-02T15:04:05.000000000Z") }
func text(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
func same(a, b *string) bool { return text(a) == text(b) }
func randomID(prefix string) (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(b), nil
}
func business(code, message string) error { return contract.NewBusinessError(409, code, message) }
func missing() error                      { return contract.NewBusinessError(404, "NOT_FOUND", "记录不存在。") }
func normalize[T any](schema string, v T) (T, error) {
	b, err := json.Marshal(v)
	if err != nil {
		var z T
		return z, err
	}
	return contract.Decode[T](schema, b)
}
func encode(v any) string { b, _ := json.Marshal(v); return string(b) }
func detail(code, message string) *contract.ErrorDetail {
	return &contract.ErrorDetail{Code: code, Message: message, Fields: []string{}}
}
func (s *Service) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Start is called only by serve, after recovery and before accepting requests.
// Offline CLI composition never starts workers or interrupts pending records.
func (s *Service) Start(parent context.Context) error {
	if err := s.recover(parent); err != nil {
		return err
	}
	s.ctx, s.cancel = context.WithCancel(parent)
	s.done.Add(2)
	go func() { defer s.done.Done(); s.buildLoop() }()
	go func() { defer s.done.Done(); s.activationLoop() }()
	return nil
}
func (s *Service) Close() error {
	if s.cancel != nil {
		s.cancel()
		s.done.Wait()
	}
	return nil
}

type scanner interface{ Scan(...any) error }

const candidateColumns = `id,state,schema_version,workspace_revision,code_version,snapshot_sha256,expected_active_release_id,created_at,started_at,completed_at,error_json,diff_json`

func scanCandidate(row scanner) (contract.Candidate, error) {
	var c contract.Candidate
	var failure, diff sql.NullString
	err := row.Scan(&c.ID, &c.State, &c.SchemaVersion, &c.WorkspaceRevision, &c.CodeVersion, &c.SnapshotSHA256, &c.ExpectedActiveReleaseID, &c.CreatedAt, &c.StartedAt, &c.CompletedAt, &failure, &diff)
	if errors.Is(err, sql.ErrNoRows) {
		return c, missing()
	}
	if err != nil {
		return c, err
	}
	if failure.Valid {
		if err = json.Unmarshal([]byte(failure.String), &c.Error); err != nil {
			return c, err
		}
	}
	err = json.Unmarshal([]byte(diff.String), &c.Diff)
	return c, err
}
func (s *Service) candidate(ctx context.Context, id string) (contract.Candidate, error) {
	c, err := scanCandidate(s.db.QueryRowContext(ctx, `SELECT `+candidateColumns+` FROM candidates WHERE id=?`, id))
	if err != nil {
		return c, err
	}
	var revision int64
	if err = s.db.QueryRowContext(ctx, `SELECT revision FROM workspace WHERE id=1`).Scan(&revision); err != nil {
		return c, err
	}
	c.WorkspaceChanged = revision != c.WorkspaceRevision
	c.ReviewURL = "/admin/candidates/?id=" + c.ID
	if c.State == "ready" {
		c.PreviewURL = new("/")
	}
	return c, nil
}

// GetCandidate requires products:read and revalidates the credential on each call.
func (s *Service) GetCandidate(ctx context.Context, actor auth.Actor, id string) (contract.Candidate, error) {
	if err := s.auth.Authorize(ctx, actor, "products:read"); err != nil {
		return contract.Candidate{}, err
	}
	return s.candidate(ctx, id)
}

// idempotent is called inside the same transaction as its resource mutation.
func idempotent(ctx context.Context, tx *sql.Tx, actor auth.Actor, operation, key, hash string) (string, error) {
	if key == "" || len(key) > 200 || strings.ContainsAny(key, "\r\n\x00") {
		return "", contract.NewBusinessError(422, "IDEMPOTENCY_KEY_REQUIRED", "需要有效的 Idempotency-Key。", "Idempotency-Key")
	}
	var resource, oldHash, expiry string
	err := tx.QueryRowContext(ctx, `SELECT resource_id,request_sha256,expires_at FROM idempotency_keys WHERE actor_id=? AND operation=? AND key=?`, actor.IdentityKey(), operation, key).Scan(&resource, &oldHash, &expiry)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	expires, err := time.Parse(time.RFC3339Nano, expiry)
	if err != nil {
		return "", err
	}
	if !expires.After(time.Now()) {
		_, err = tx.ExecContext(ctx, `DELETE FROM idempotency_keys WHERE actor_id=? AND operation=? AND key=?`, actor.IdentityKey(), operation, key)
		return "", err
	}
	if oldHash != hash {
		return "", business("IDEMPOTENCY_CONFLICT", "同一幂等键已用于不同请求。")
	}
	return resource, nil
}
func remember(ctx context.Context, tx *sql.Tx, actor auth.Actor, operation, key, hash, id string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO idempotency_keys(actor_id,operation,key,request_sha256,resource_id,created_at,expires_at) VALUES(?,?,?,?,?,?,?)`, actor.IdentityKey(), operation, key, hash, id, now(), time.Now().Add(24*time.Hour).UTC().Format(time.RFC3339Nano))
	return err
}
func (s *Service) CreateCandidate(ctx context.Context, actor auth.Actor, revision int64, key string) (contract.Candidate, error) {
	var zero contract.Candidate
	if err := s.auth.Authorize(ctx, actor, "candidates:write"); err != nil {
		return zero, err
	}
	if revision < 0 || revision > 9007199254740991 {
		return zero, contract.NewBusinessError(422, "VALIDATION_FAILED", "工作区版本不合法。", "expected_workspace_revision")
	}
	if s.releases.Status() == "unavailable" {
		return zero, contract.NewBusinessError(503, "ARTIFACT_UNAVAILABLE", "当前发布状态损坏，须先恢复。")
	}
	s.gate.Lock()
	defer s.gate.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return zero, err
	}
	defer tx.Rollback()
	hash := release.Hash([]byte(encode(contract.CandidateInput{ExpectedWorkspaceRevision: revision})))
	previous, err := idempotent(ctx, tx, actor, "candidate", key, hash)
	if err != nil {
		return zero, err
	}
	if previous != "" {
		tx.Rollback()
		return s.candidate(ctx, previous)
	}
	id, err := randomID("c_")
	if err != nil {
		return zero, err
	}
	snapshot, err := s.content.Snapshot(ctx, tx, id)
	if err != nil {
		return zero, err
	}
	if snapshot.WorkspaceRevision != revision {
		return zero, business("WORKSPACE_CHANGED", "工作区已变化，请重新读取。")
	}
	frozen, err := json.Marshal(snapshot)
	if err != nil {
		return zero, err
	}
	if err = contract.ValidateJSON("catalog-snapshot.schema.json#/$defs/PublishableSnapshot", frozen); err != nil {
		return zero, err
	}
	for _, p := range snapshot.Products {
		if p.Visibility == "listed" {
			for _, option := range p.PurchaseOptions {
				if option.Enabled {
					if err = contract.ValidatePurchaseURL(option.URL, s.cfg.PurchaseAllowedHosts); err != nil {
						return zero, err
					}
				}
			}
		}
	}
	active := s.releases.ActiveID()
	var baseline *release.Handle
	if active != nil {
		baseline, err = s.releases.Capture(*active)
		if err != nil {
			return zero, err
		}
	}
	diff := makeDiff(baseline, snapshot, active)
	metadata := struct {
		BaselineOperationID *string `json:"baseline_operation_id"`
	}{}
	if operation := s.releases.OperationID(); operation != "" {
		metadata.BaselineOperationID = new(operation)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO candidates(id,state,schema_version,workspace_revision,code_version,snapshot_json,snapshot_sha256,expected_active_release_id,validation_json,diff_json,created_by,created_at) VALUES(?,'queued',1,?,?,?,?,?,?,?,?,?)`, id, revision, s.code, string(frozen), release.Hash(frozen), active, encode(metadata), encode(diff), actor.AdminID(), now())
	if err != nil {
		return zero, err
	}
	if err = remember(ctx, tx, actor, "candidate", key, hash, id); err != nil {
		return zero, err
	}
	if err = tx.Commit(); err != nil {
		return zero, err
	}
	s.signal()
	return s.candidate(ctx, id)
}

// Preview binds the candidate to one authenticated management session. The
// HttpOnly cookie contains only its ID; it grants nothing without that session.
func (s *Service) Preview(ctx context.Context, actor auth.Actor, id string) (contract.PreviewSession, error) {
	var out contract.PreviewSession
	if err := actor.RequireBrowser(); err != nil {
		return out, err
	}
	if err := s.auth.Authorize(ctx, actor, ""); err != nil {
		return out, err
	}
	if id != "" {
		c, err := s.candidate(ctx, id)
		if err != nil {
			return out, err
		}
		if c.State != "ready" {
			return out, business("CANDIDATE_NOT_READY", "候选尚未构建成功。")
		}
		h, err := release.Load(s.cfg.DataDir, id)
		if err != nil {
			return out, err
		}
		s.ready.Store(id, h)
	}
	var candidate, expires any
	if id != "" {
		candidate = id
		expires = time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339Nano)
	}
	result, err := s.db.ExecContext(ctx, `UPDATE sessions SET preview_candidate_id=?,preview_expires_at=CASE WHEN julianday(expires_at)<julianday(?) THEN expires_at ELSE ? END WHERE 'session:'||secret_hash=? AND revoked_at IS NULL`, candidate, expires, expires, actor.IdentityKey())
	if err != nil {
		return out, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return out, err
	}
	if n != 1 {
		return out, contract.NewBusinessError(401, "UNAUTHENTICATED", "请重新登录。")
	}
	if id != "" {
		out.CandidateID = &id
		out.Active = true
	}
	return out, nil
}
func (s *Service) PreviewHandle(ctx context.Context, actor auth.Actor, cookieID string) (*release.Handle, error) {
	if err := actor.RequireBrowser(); err != nil {
		return nil, err
	}
	if err := s.auth.Authorize(ctx, actor, ""); err != nil {
		return nil, err
	}
	var id, expires sql.NullString
	if err := s.db.QueryRowContext(ctx, `SELECT preview_candidate_id,preview_expires_at FROM sessions WHERE 'session:'||secret_hash=?`, actor.IdentityKey()).Scan(&id, &expires); err != nil {
		return nil, err
	}
	expiry, err := time.Parse(time.RFC3339Nano, expires.String)
	if err != nil || !id.Valid || id.String != cookieID || !expiry.After(time.Now()) {
		return nil, contract.NewBusinessError(401, "PREVIEW_EXPIRED", "预览已结束，请重新进入预览。")
	}
	var state string
	if err = s.db.QueryRowContext(ctx, `SELECT state FROM candidates WHERE id=?`, id.String).Scan(&state); err != nil {
		return nil, err
	}
	if state != "ready" {
		return nil, business("CANDIDATE_NOT_READY", "候选不可预览。")
	}
	if cached, ok := s.ready.Load(id.String); ok {
		return cached.(*release.Handle), nil
	}
	h, err := release.Load(s.cfg.DataDir, id.String)
	if err != nil {
		return nil, err
	}
	actual, _ := s.ready.LoadOrStore(id.String, h)
	return actual.(*release.Handle), nil
}

// recover never retries interrupted publication. Only active.operation_id can
// establish a switched operation as successful. Filesystem authority is loaded
// before repairing ever-published flags and task records in one transaction.
func (s *Service) recover(ctx context.Context) error {
	s.gate.Lock()
	defer s.gate.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	interrupted := encode(detail("INTERRUPTED", "任务在完成前中断，请重新创建并审阅。"))
	if _, err = tx.ExecContext(ctx, `UPDATE candidates SET state='failed',completed_at=?,error_json=? WHERE state IN ('queued','building')`, now(), interrupted); err != nil {
		return err
	}
	active := s.releases.Active()
	if active != nil {
		for _, id := range active.SuccessfulReleaseIDs {
			h, loadErr := s.releases.Retained(id)
			if loadErr != nil {
				return loadErr
			}
			var state, hash, manifest, code string
			if err = tx.QueryRowContext(ctx, `SELECT state,snapshot_sha256,manifest_sha256,code_version FROM candidates WHERE id=?`, id).Scan(&state, &hash, &manifest, &code); err != nil {
				return err
			}
			if state != "ready" || hash != h.SHA256() || manifest != h.ManifestSHA256() || code != h.CodeVersion() {
				return errors.New("successful artifact differs from frozen record")
			}
		}
		result, e := tx.ExecContext(ctx, `UPDATE publication_requests SET state='succeeded',result_release_id=?,completed_at=?,error_json=NULL WHERE id=? AND snapshot_sha256=? AND COALESCE(candidate_id,release_id)=? AND confirmed_by IS NOT NULL`, active.ReleaseID, active.ActivatedAt, active.OperationID, active.SnapshotSHA256, active.ReleaseID)
		if e != nil {
			return e
		}
		n, e := result.RowsAffected()
		if e != nil {
			return e
		}
		if n != 1 {
			return errors.New("active operation missing or inconsistent")
		}
		for _, r := range active.PreviouslyPublishedRoutes {
			result, e = tx.ExecContext(ctx, `UPDATE reserved_routes SET ever_published=1 WHERE slug=? AND owner_kind='product' AND owner_id=?`, r.Slug, r.ProductID)
			if e != nil {
				return e
			}
			n, e = result.RowsAffected()
			if e != nil || n != 1 {
				return errors.New("published route ownership missing")
			}
		}
		if err = s.reconcileActivationBaseline(ctx, tx, active); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE publication_requests SET state='failed',completed_at=?,error_json=? WHERE state IN ('queued','activating')`, now(), interrupted); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE publication_requests SET state='expired',completed_at=?,error_json=? WHERE state='awaiting_confirmation' AND julianday(expires_at)<=julianday('now')`, now(), encode(detail("REQUEST_EXPIRED", "发布意图已超过24小时。"))); err != nil {
		return err
	}
	return tx.Commit()
}

// dataPath is used only with internally generated IDs, never HTTP paths.
func (s *Service) dataPath(parts ...string) string {
	return filepath.Join(append([]string{s.cfg.DataDir}, parts...)...)
}
func ensurePrivate(directory string) error { return os.MkdirAll(directory, 0700) }
