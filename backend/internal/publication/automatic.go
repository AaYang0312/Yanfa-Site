package publication

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"yuanfa-official/backend/internal/auth"
	"yuanfa-official/backend/internal/contract"
	"yuanfa-official/backend/internal/release"
)

// The auto coordinator uses the existing serial builder and atomic activation
// worker. Its immutable input is ONLY the browser confirmation ledger.
func (s *Service) automaticLoop() {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-tick.C:
			_ = s.automaticStep(s.ctx)
		}
	}
}
func confirmationVersions(ctx context.Context, tx *sql.Tx) (string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT product_id,version FROM product_confirmations ORDER BY product_id`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	versions := map[string]int64{}
	for rows.Next() {
		var id string
		var version int64
		if err = rows.Scan(&id, &version); err != nil {
			return "", err
		}
		versions[id] = version
	}
	if err = rows.Err(); err != nil {
		return "", err
	}
	return encode(versions), nil
}
func (s *Service) automaticStep(ctx context.Context) error {
	s.gate.Lock()
	defer s.gate.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var id, versions, state string
	var operation sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT candidate_id,versions_json,state,operation_id FROM site_update_batches WHERE state IN ('building','activating') ORDER BY rowid LIMIT 1`).Scan(&id, &versions, &state, &operation)
	if err == nil {
		var candidateState string
		if err = tx.QueryRowContext(ctx, `SELECT state FROM candidates WHERE id=?`, id).Scan(&candidateState); err != nil {
			return err
		}
		current, e := confirmationVersions(ctx, tx)
		if e != nil {
			return e
		}
		terminal := ""
		confirmationState := ""
		if state == "activating" {
			var requestState string
			if err = tx.QueryRowContext(ctx, `SELECT state FROM publication_requests WHERE id=?`, operation.String).Scan(&requestState); err != nil {
				return err
			}
			if requestState == "succeeded" {
				terminal = "succeeded"
				confirmationState = "succeeded"
			} else if requestState == "failed" || requestState == "stale" || requestState == "cancelled" || requestState == "expired" {
				terminal = "failed"
				confirmationState = "failed"
				if current != versions {
					terminal = "superseded"
					confirmationState = "pending"
				}
			}
		} else if candidateState == "failed" {
			terminal = "failed"
			confirmationState = "failed"
		} else if candidateState == "ready" {
			if current != versions {
				terminal = "superseded"
				confirmationState = "pending"
			} else {
				c, e := scanCandidate(tx.QueryRowContext(ctx, `SELECT `+candidateColumns+` FROM candidates WHERE id=?`, id))
				if e != nil {
					return e
				}
				if e = s.checkLocked(ctx, tx, c, "publish", c.ExpectedActiveReleaseID, c.WorkspaceRevision); e != nil {
					terminal = "superseded"
					confirmationState = "pending"
				} else {
					op, e := randomID("pub_")
					if e != nil {
						return e
					}
					_, e = tx.ExecContext(ctx, `INSERT INTO publication_requests(id,kind,candidate_id,snapshot_sha256,expected_active_release_id,expected_workspace_revision,state,created_by,confirmed_by,created_at,expires_at,confirmed_at) VALUES(?,'publish',?,?,?,?,'queued','admin','admin',?,?,?)`, op, id, c.SnapshotSHA256, c.ExpectedActiveReleaseID, c.WorkspaceRevision, now(), time.Now().Add(24*time.Hour).UTC().Format(time.RFC3339Nano), now())
					if e != nil {
						return e
					}
					_, e = tx.ExecContext(ctx, `UPDATE site_update_batches SET state='activating',operation_id=? WHERE candidate_id=?`, op, id)
					if e != nil {
						return e
					}
				}
			}
		}
		if terminal != "" {
			if _, err = tx.ExecContext(ctx, `UPDATE site_update_batches SET state=? WHERE candidate_id=?`, terminal, id); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `UPDATE product_confirmations SET state=? WHERE state='updating' AND EXISTS(SELECT 1 FROM json_each(?) v WHERE v.key=product_id AND v.value=version)`, confirmationState, versions); err != nil {
				return err
			}
		}
		return tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var pending int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM product_confirmations WHERE state='pending'`).Scan(&pending); err != nil || pending == 0 {
		return err
	}
	if s.releases.Status() == "unavailable" {
		_, err = tx.ExecContext(ctx, `UPDATE product_confirmations SET state='failed' WHERE state='pending'`)
		if err != nil {
			return err
		}
		return tx.Commit()
	}
	id, err = randomID("c_")
	if err != nil {
		return err
	}
	var baseline *contract.CatalogSnapshot
	var handle *release.Handle
	active := s.releases.ActiveID()
	if active != nil {
		handle, err = s.releases.Capture(*active)
		if err != nil {
			return err
		}
		catalog := handle.Catalog()
		baseline = &catalog
	}
	snapshot, err := s.content.ConfirmedSnapshot(ctx, tx, id, baseline)
	if err != nil {
		return s.failPending(ctx, tx)
	}
	frozen, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	if err = contract.ValidateJSON("catalog-snapshot.schema.json#/$defs/PublishableSnapshot", frozen); err != nil {
		return s.failPending(ctx, tx)
	}
	for _, p := range snapshot.Products {
		for _, option := range p.PurchaseOptions {
			if p.Visibility == "listed" && option.Enabled {
				if err = contract.ValidatePurchaseURL(option.URL, s.cfg.PurchaseAllowedHosts); err != nil {
					return s.failPending(ctx, tx)
				}
			}
		}
	}
	versions, err = confirmationVersions(ctx, tx)
	if err != nil {
		return err
	}
	metadata := map[string]any{"baseline_operation_id": nil}
	if op := s.releases.OperationID(); op != "" {
		metadata["baseline_operation_id"] = op
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO candidates(id,state,schema_version,workspace_revision,code_version,snapshot_json,snapshot_sha256,expected_active_release_id,validation_json,diff_json,created_by,created_at) VALUES(?,'queued',1,?,?,?,?,?,?,?,'admin',?)`, id, snapshot.WorkspaceRevision, s.code, string(frozen), release.Hash(frozen), active, encode(metadata), encode(makeDiff(handle, snapshot, active)), now())
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO site_update_batches(candidate_id,versions_json,state) VALUES(?,?,'building')`, id, versions); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE product_confirmations SET state='updating' WHERE state IN ('pending','failed')`); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Service) failPending(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `UPDATE product_confirmations SET state='failed' WHERE state='pending'`); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) SiteUpdates(ctx context.Context, actor auth.Actor) (contract.SiteUpdateList, error) {
	out := contract.SiteUpdateList{Items: []contract.SiteUpdate{}}
	if err := actor.RequireBrowser(); err != nil {
		return out, err
	}
	if err := s.auth.Authorize(ctx, actor, "products:read"); err != nil {
		return out, err
	}
	s.gate.RLock()
	defer s.gate.RUnlock()
	public := map[string]int64{}
	if active := s.releases.ActiveID(); active != nil {
		h, err := s.releases.Capture(*active)
		if err != nil {
			return out, err
		}
		for _, p := range h.Catalog().Products {
			public[p.ID] = p.Version
		}
	}
	rows, err := s.db.QueryContext(ctx, `SELECT p.id,p.version,p.slug,p.deleted_at,c.version,c.state,c.deleted FROM products p LEFT JOIN product_confirmations c ON c.product_id=p.id ORDER BY p.id`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, slug string
		var version int64
		var deleted sql.NullString
		var cv sql.NullInt64
		var state sql.NullString
		var confirmedDelete sql.NullBool
		if err = rows.Scan(&id, &version, &slug, &deleted, &cv, &state, &confirmedDelete); err != nil {
			return out, err
		}
		status := "draft"
		if cv.Valid && cv.Int64 == version {
			status = state.String
			if status == "succeeded" && !(public[id] == version || confirmedDelete.Bool && public[id] == 0) {
				status = "draft"
			}
		} else if !cv.Valid && public[id] == version {
			status = "succeeded"
		}
		message := map[string]string{"draft": "有修改待确认", "pending": "等待更新官网", "updating": "正在更新官网", "succeeded": "官网已更新", "failed": "修改已保存，官网更新失败，请重试"}[status]
		out.Items = append(out.Items, contract.SiteUpdate{ProductID: id, Version: version, State: status, Message: message, PublicURL: "/products/" + slug + "/"})
	}
	return out, rows.Err()
}
func (s *Service) RetrySiteUpdate(ctx context.Context, actor auth.Actor) error {
	if err := actor.RequireBrowser(); err != nil {
		return err
	}
	if err := s.auth.Authorize(ctx, actor, "products:write"); err != nil {
		return err
	}
	s.gate.Lock()
	defer s.gate.Unlock()
	_, err := s.db.ExecContext(ctx, `UPDATE product_confirmations SET state='pending' WHERE state='failed'`)
	return err
}
