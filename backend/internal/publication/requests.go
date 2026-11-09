package publication

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"yuanfa-official/backend/internal/auth"
	"yuanfa-official/backend/internal/contract"
	"yuanfa-official/backend/internal/release"
)

const requestColumns = `id,kind,candidate_id,release_id,snapshot_sha256,expected_active_release_id,expected_workspace_revision,state,created_at,expires_at,confirmed_by,confirmed_at,completed_at,result_release_id,error_json`

func scanRequest(row scanner) (contract.PublicationRequest, error) {
	var p contract.PublicationRequest
	var failure sql.NullString
	err := row.Scan(&p.ID, &p.Kind, &p.CandidateID, &p.ReleaseID, &p.SnapshotSHA256, &p.ExpectedActiveReleaseID, &p.ExpectedWorkspaceRevision, &p.State, &p.CreatedAt, &p.ExpiresAt, &p.ConfirmedBy, &p.ConfirmedAt, &p.CompletedAt, &p.ResultReleaseID, &failure)
	if errors.Is(err, sql.ErrNoRows) {
		return p, missing()
	}
	if err != nil {
		return p, err
	}
	if failure.Valid {
		err = json.Unmarshal([]byte(failure.String), &p.Error)
	}
	return p, err
}
func (s *Service) request(ctx context.Context, id string) (contract.PublicationRequest, error) {
	p, err := scanRequest(s.db.QueryRowContext(ctx, `SELECT `+requestColumns+` FROM publication_requests WHERE id=?`, id))
	if err != nil {
		return p, err
	}
	p.ReviewURL = "/admin/publication-requests/?id=" + p.ID
	target := text(p.CandidateID)
	if p.Kind == "rollback" {
		target = text(p.ReleaseID)
	}
	c, err := s.candidate(ctx, target)
	if err != nil {
		return p, err
	}
	p.Diff = c.Diff
	if p.Kind == "rollback" {
		h, e := release.Load(s.cfg.DataDir, target)
		if e != nil {
			return p, e
		}
		var baseline *release.Handle
		if p.ExpectedActiveReleaseID != nil {
			baseline, e = release.Load(s.cfg.DataDir, *p.ExpectedActiveReleaseID)
			if e != nil {
				return p, e
			}
		}
		p.Diff = makeDiff(baseline, h.Catalog(), p.ExpectedActiveReleaseID)
		p.Diff.Warnings = append(p.Diff.Warnings, "线上回退不会覆盖编辑区，编辑区仍有未发布内容。")
	}
	if p.State == "succeeded" {
		p.SiteURL = new(s.cfg.SiteURL + "/")
	}
	return p, nil
}

// GetRequest requires products:read and revalidates the credential on each call.
func (s *Service) GetRequest(ctx context.Context, actor auth.Actor, id string) (contract.PublicationRequest, error) {
	if err := s.auth.Authorize(ctx, actor, "products:read"); err != nil {
		return contract.PublicationRequest{}, err
	}
	// Expiry is a state transition, not only a display-time flag.
	_, err := s.db.ExecContext(ctx, `UPDATE publication_requests SET state='expired',completed_at=?,error_json=? WHERE id=? AND state='awaiting_confirmation' AND julianday(expires_at)<=julianday('now')`, now(), encode(detail("REQUEST_EXPIRED", "发布意图已超过24小时。")), id)
	if err != nil {
		return contract.PublicationRequest{}, err
	}
	return s.request(ctx, id)
}
func (s *Service) target(ctx context.Context, kind, id, hash string) (contract.Candidate, *release.Handle, error) {
	c, err := s.candidate(ctx, id)
	if err != nil {
		return c, nil, err
	}
	if c.State != "ready" {
		return c, nil, business("CANDIDATE_NOT_READY", "候选尚未构建成功。")
	}
	if c.SnapshotSHA256 != hash {
		return c, nil, business("SNAPSHOT_CHANGED", "审阅的快照摘要不一致。")
	}
	if kind == "rollback" && !s.releases.Successful(id) {
		return c, nil, business("RELEASE_NOT_SUCCESSFUL", "只能回退到历史成功版本。")
	}
	h, err := release.Load(s.cfg.DataDir, id)
	if err != nil {
		return c, nil, err
	}
	if h.SHA256() != hash || h.CodeVersion() != c.CodeVersion {
		return c, nil, business("SNAPSHOT_CHANGED", "冻结产物与候选不一致。")
	}
	var manifestSHA sql.NullString
	if err = s.db.QueryRowContext(ctx, `SELECT manifest_sha256 FROM candidates WHERE id=?`, id).Scan(&manifestSHA); err != nil {
		return c, nil, err
	}
	if !manifestSHA.Valid || manifestSHA.String != h.ManifestSHA256() {
		return c, nil, business("ARTIFACT_CHANGED", "冻结产物清单已变化。")
	}
	return c, h, nil
}
func (s *Service) checkLocked(ctx context.Context, tx *sql.Tx, c contract.Candidate, kind string, expectedActive *string, expectedRevision int64) error {
	if s.releases.Status() == "unavailable" {
		return contract.NewBusinessError(503, "ARTIFACT_UNAVAILABLE", "发布权威状态不可用。")
	}
	if !same(s.releases.ActiveID(), expectedActive) {
		return business("ACTIVE_CHANGED", "线上版本已变化，请重新生成并审阅。")
	}
	var revision int64
	var approved sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT versions_json FROM site_update_batches WHERE candidate_id=? AND ?='publish'`, c.ID, kind).Scan(&approved); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if approved.Valid {
		current, err := confirmationVersions(ctx, tx)
		if err != nil {
			return err
		}
		if current != approved.String {
			return business("CONFIRMATIONS_CHANGED", "有新的产品修改，正在合并更新。")
		}
	}
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM workspace WHERE id=1`).Scan(&revision); err != nil {
		return err
	}
	if !approved.Valid && revision != expectedRevision {
		return business("WORKSPACE_CHANGED", "工作区已变化，请重新生成并审阅。")
	}
	var state, hash string
	var baselineType, baselineOperation sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT state,snapshot_sha256,json_type(validation_json,'$.baseline_operation_id'),json_extract(validation_json,'$.baseline_operation_id') FROM candidates WHERE id=?`, c.ID).Scan(&state, &hash, &baselineType, &baselineOperation); err != nil {
		return err
	}
	if state != "ready" || hash != c.SnapshotSHA256 {
		return business("CANDIDATE_NOT_READY", "候选状态或摘要已变化。")
	}
	if kind == "publish" {
		operation := s.releases.OperationID()
		baselineMatches := baselineType.Valid && ((baselineType.String == "null" && operation == "") || (baselineType.String == "text" && baselineOperation.Valid && baselineOperation.String != "" && baselineOperation.String == operation))
		// Missing metadata belongs to an older, unverifiable baseline. It must
		// be rebuilt, never inferred from an equal release ID after rollback.
		if !baselineMatches || (!approved.Valid && c.WorkspaceRevision != revision) || !same(c.ExpectedActiveReleaseID, expectedActive) {
			return business("CANDIDATE_STALE", "候选已过期，请重新生成并审阅。")
		}
	}
	if kind == "rollback" && !s.releases.Successful(c.ID) {
		return business("RELEASE_NOT_SUCCESSFUL", "只能回退到历史成功版本。")
	}
	return nil
}
func (s *Service) Request(ctx context.Context, actor auth.Actor, input contract.PublicationInput, key string) (contract.PublicationRequest, error) {
	var zero contract.PublicationRequest
	if err := s.auth.Authorize(ctx, actor, "publications:write"); err != nil {
		return zero, err
	}
	input, err := normalize("admin.schema.json#/$defs/PublicationInput", input)
	if err != nil {
		return zero, err
	}
	// Check replay before revalidating mutable state. A lost response must replay
	// the same resource even after that resource has succeeded.
	hash := release.Hash([]byte(encode(input)))
	s.gate.Lock()
	operation := s.releases.OperationID()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		s.gate.Unlock()
		return zero, err
	}
	previous, err := idempotent(ctx, tx, actor, "request", key, hash)
	tx.Rollback()
	s.gate.Unlock()
	if err != nil {
		return zero, err
	}
	if previous != "" {
		return s.request(ctx, previous)
	}
	id := text(input.CandidateID)
	if input.Kind == "rollback" {
		id = text(input.ReleaseID)
	}
	c, _, err := s.target(ctx, input.Kind, id, input.SnapshotSHA256)
	if err != nil {
		return zero, err
	}
	s.gate.Lock()
	defer s.gate.Unlock()
	tx, err = s.db.BeginTx(ctx, nil)
	if err != nil {
		return zero, err
	}
	defer tx.Rollback()
	previous, err = idempotent(ctx, tx, actor, "request", key, hash)
	if err != nil {
		return zero, err
	}
	if previous != "" {
		tx.Rollback()
		return s.request(ctx, previous)
	}
	if s.releases.OperationID() != operation {
		return zero, business("ACTIVE_CHANGED", "线上激活基线已变化，请重新审阅。")
	}
	if err = s.checkLocked(ctx, tx, c, input.Kind, input.ExpectedActiveReleaseID, input.ExpectedWorkspaceRevision); err != nil {
		return zero, err
	}
	requestID, err := randomID("pub_")
	if err != nil {
		return zero, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO publication_requests(id,kind,candidate_id,release_id,snapshot_sha256,expected_active_release_id,expected_workspace_revision,state,created_by,created_at,expires_at) VALUES(?,?,?,?,?,?,?,'awaiting_confirmation',?,?,?)`, requestID, input.Kind, input.CandidateID, input.ReleaseID, input.SnapshotSHA256, input.ExpectedActiveReleaseID, input.ExpectedWorkspaceRevision, actor.AdminID(), now(), time.Now().Add(24*time.Hour).UTC().Format(time.RFC3339Nano))
	if err != nil {
		return zero, err
	}
	if err = remember(ctx, tx, actor, "request", key, hash, requestID); err != nil {
		return zero, err
	}
	if err = tx.Commit(); err != nil {
		return zero, err
	}
	return s.request(ctx, requestID)
}
func (s *Service) Confirm(ctx context.Context, actor auth.Actor, id string, input contract.ConfirmationInput, key string) (contract.PublicationRequest, error) {
	var zero contract.PublicationRequest
	if err := actor.RequireBrowser(); err != nil {
		return zero, err
	}
	if err := s.auth.Authorize(ctx, actor, "publications:write"); err != nil {
		return zero, err
	}
	input, err := normalize("admin.schema.json#/$defs/ConfirmationInput", input)
	if err != nil {
		return zero, err
	}
	hash := release.Hash([]byte(encode(input)))
	operation := "confirm:" + id
	s.gate.Lock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		s.gate.Unlock()
		return zero, err
	}
	previous, err := idempotent(ctx, tx, actor, operation, key, hash)
	tx.Rollback()
	s.gate.Unlock()
	if err != nil {
		return zero, err
	}
	if previous != "" {
		return s.request(ctx, previous)
	}
	p, err := s.request(ctx, id)
	if err != nil {
		return zero, err
	}
	target := text(p.CandidateID)
	if p.Kind == "rollback" {
		target = text(p.ReleaseID)
	}
	c, _, err := s.target(ctx, p.Kind, target, input.SnapshotSHA256)
	if err != nil {
		return zero, err
	}
	s.gate.Lock()
	defer s.gate.Unlock()
	tx, err = s.db.BeginTx(ctx, nil)
	if err != nil {
		return zero, err
	}
	defer tx.Rollback()
	previous, err = idempotent(ctx, tx, actor, operation, key, hash)
	if err != nil {
		return zero, err
	}
	if previous != "" {
		tx.Rollback()
		return s.request(ctx, previous)
	}
	p, err = scanRequest(tx.QueryRowContext(ctx, `SELECT `+requestColumns+` FROM publication_requests WHERE id=?`, id))
	if err != nil {
		return zero, err
	}
	if p.State != "awaiting_confirmation" {
		return zero, business("REQUEST_STATE_CONFLICT", "发布意图当前不可确认。")
	}
	expiry, err := time.Parse(time.RFC3339Nano, p.ExpiresAt)
	if err != nil {
		return zero, err
	}
	var conflict error
	state := "stale"
	if !expiry.After(time.Now()) {
		state = "expired"
		conflict = business("REQUEST_EXPIRED", "发布意图已超过24小时。")
	} else if p.SnapshotSHA256 != input.SnapshotSHA256 || !same(p.ExpectedActiveReleaseID, input.ExpectedActiveReleaseID) || p.ExpectedWorkspaceRevision != input.ExpectedWorkspaceRevision {
		conflict = business("REVIEW_MISMATCH", "确认内容与已审阅的发布意图不一致。")
	} else {
		conflict = s.checkLocked(ctx, tx, c, p.Kind, input.ExpectedActiveReleaseID, input.ExpectedWorkspaceRevision)
	}
	if conflict != nil {
		var e *contract.BusinessError
		if !errors.As(conflict, &e) {
			return zero, conflict
		}
		if _, err = tx.ExecContext(ctx, `UPDATE publication_requests SET state=?,completed_at=?,error_json=? WHERE id=?`, state, now(), encode(e.Detail), id); err != nil {
			return zero, err
		}
		if err = tx.Commit(); err != nil {
			return zero, err
		}
		return zero, conflict
	}
	_, err = tx.ExecContext(ctx, `UPDATE publication_requests SET state='queued',confirmed_by=?,confirmed_at=? WHERE id=?`, actor.AdminID(), now(), id)
	if err != nil {
		return zero, err
	}
	if err = remember(ctx, tx, actor, operation, key, hash, id); err != nil {
		return zero, err
	}
	if err = tx.Commit(); err != nil {
		return zero, err
	}
	s.signal()
	return s.request(ctx, id)
}
func (s *Service) Cancel(ctx context.Context, actor auth.Actor, id string) (contract.PublicationRequest, error) {
	if err := actor.RequireBrowser(); err != nil {
		return contract.PublicationRequest{}, err
	}
	if err := s.auth.Authorize(ctx, actor, "publications:write"); err != nil {
		return contract.PublicationRequest{}, err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE publication_requests SET state='cancelled',completed_at=? WHERE id=? AND state='awaiting_confirmation'`, now(), id)
	if err != nil {
		return contract.PublicationRequest{}, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return contract.PublicationRequest{}, err
	}
	if n != 1 {
		return contract.PublicationRequest{}, business("REQUEST_STATE_CONFLICT", "只有待确认意图可以取消。")
	}
	return s.request(ctx, id)
}
func (s *Service) List(ctx context.Context, actor auth.Actor, cursor string, limit int) (contract.PublicationList, error) {
	out := contract.PublicationList{ActiveReleaseID: s.releases.ActiveID(), Items: []contract.PublicationRequest{}}
	if err := s.auth.Authorize(ctx, actor, "publications:write"); err != nil {
		return out, err
	}
	if limit == 0 {
		limit = 20
	}
	if limit < 1 || limit > 100 || len(cursor) > 4096 {
		return out, contract.NewBusinessError(422, "VALIDATION_FAILED", "分页参数不合法。")
	}
	beforeTime, beforeID := "\xff", "\xff"
	if cursor != "" {
		b, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil {
			return out, business("INVALID_CURSOR", "分页游标不合法。")
		}
		var parts []string
		if json.Unmarshal(b, &parts) != nil || len(parts) != 2 {
			return out, business("INVALID_CURSOR", "分页游标不合法。")
		}
		beforeTime, beforeID = parts[0], parts[1]
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,created_at FROM publication_requests WHERE (created_at,id)<(?,?) ORDER BY created_at DESC,id DESC LIMIT ?`, beforeTime, beforeID, limit+1)
	if err != nil {
		return out, err
	}
	type item struct{ id, created string }
	ids := []item{}
	for rows.Next() {
		var i item
		if err = rows.Scan(&i.id, &i.created); err != nil {
			rows.Close()
			return out, err
		}
		ids = append(ids, i)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if len(ids) > limit {
		last := ids[limit-1]
		out.NextCursor = new(base64.RawURLEncoding.EncodeToString([]byte(encode([]string{last.created, last.id}))))
		ids = ids[:limit]
	}
	for _, i := range ids {
		p, err := s.request(ctx, i.id)
		if err != nil {
			return out, err
		}
		out.Items = append(out.Items, p)
	}
	return out, nil
}

func (s *Service) activationLoop() {
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		if s.ctx.Err() != nil {
			return
		}
		var id string
		err := s.db.QueryRowContext(s.ctx, `UPDATE publication_requests SET state='activating' WHERE id=(SELECT id FROM publication_requests WHERE state='queued' ORDER BY created_at,id LIMIT 1) RETURNING id`).Scan(&id)
		if err == nil {
			s.activate(id)
			continue
		}
		select {
		case <-s.ctx.Done():
			return
		case <-s.wake:
		case <-tick.C:
		}
	}
}
func (s *Service) activate(id string) {
	p, err := s.request(s.ctx, id)
	if err != nil {
		s.failRequest(id, err)
		return
	}
	target := text(p.CandidateID)
	if p.Kind == "rollback" {
		target = text(p.ReleaseID)
	}
	expiry, err := time.Parse(time.RFC3339Nano, p.ExpiresAt)
	if err != nil || !expiry.After(time.Now()) {
		s.failRequest(id, business("REQUEST_EXPIRED", "发布意图已超过24小时。"))
		return
	}
	c, h, err := s.target(s.ctx, p.Kind, target, p.SnapshotSHA256)
	if err != nil {
		s.failRequest(id, err)
		return
	}
	prepared, err := s.releases.Prepare(h, id)
	if err != nil {
		s.failRequest(id, err)
		return
	}
	s.gate.Lock()
	defer s.gate.Unlock()
	tx, err := s.db.BeginTx(s.ctx, nil)
	if err != nil {
		s.failRequest(id, err)
		return
	}
	var requestState string
	if err = tx.QueryRowContext(s.ctx, `SELECT state FROM publication_requests WHERE id=?`, id).Scan(&requestState); err != nil {
		tx.Rollback()
		s.failRequest(id, err)
		return
	}
	if requestState != "activating" {
		tx.Rollback()
		s.failRequest(id, business("REQUEST_STATE_CONFLICT", "发布意图的激活基线已失效。"))
		return
	}
	if err = s.checkLocked(s.ctx, tx, c, p.Kind, p.ExpectedActiveReleaseID, p.ExpectedWorkspaceRevision); err != nil {
		tx.Rollback()
		s.failRequest(id, err)
		return
	}
	tx.Rollback() // No DB transaction spans filesystem activation; Gate still held.
	switched, err := s.releases.ActivateLocked(s.ctx, prepared)
	if err != nil {
		if !switched {
			s.failRequest(id, err)
		}
		return
	}
	// Once renamed, never mark failed. Startup operation_id reconciliation handles
	// shutdown/DB failures between the durable pointer and these result records.
	tx, err = s.db.BeginTx(s.ctx, nil)
	if err != nil {
		return
	}
	defer tx.Rollback()
	active := s.releases.Active()
	if active == nil {
		return
	}
	for _, r := range active.PreviouslyPublishedRoutes {
		if _, err = tx.ExecContext(s.ctx, `UPDATE reserved_routes SET ever_published=1 WHERE owner_kind='product' AND slug=? AND owner_id=?`, r.Slug, r.ProductID); err != nil {
			return
		}
	}
	if err = s.reconcileActivationBaseline(s.ctx, tx, active); err != nil {
		return
	}
	if _, err = tx.ExecContext(s.ctx, `UPDATE publication_requests SET state='succeeded',result_release_id=?,completed_at=?,error_json=NULL WHERE id=?`, h.ID(), active.ActivatedAt, id); err != nil {
		return
	}
	_ = tx.Commit()
}
func (s *Service) failRequest(id string, err error) {
	failure := detail("ACTIVATION_FAILED", "激活失败，线上版本未切换。请重新生成并审阅。")
	var e *contract.BusinessError
	if errors.As(err, &e) {
		failure = &e.Detail
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = s.db.ExecContext(ctx, `UPDATE publication_requests SET state='failed',completed_at=?,error_json=? WHERE id=? AND state IN ('queued','activating')`, now(), encode(failure), id)
}
