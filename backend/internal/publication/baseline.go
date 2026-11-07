package publication

import (
	"context"
	"database/sql"

	"yuanfa-official/backend/internal/contract"
)

// reconcileActivationBaseline runs in the same transaction as the successful
// operation and route repair. The private marker records which active operation
// has already invalidated outstanding intents; it never becomes an active pointer.
// A rename-before-DB crash leaves the marker behind, so startup performs exactly
// the missing invalidation. A normal restart preserves newly created intents.
// Older records lack this marker and are invalidated once before accepting HTTP.
func (s *Service) reconcileActivationBaseline(ctx context.Context, tx *sql.Tx, active *contract.ActiveRelease) error {
	var reconciled sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT json_extract(validation_json,'$.reconciled_operation_id') FROM candidates WHERE id=?`, active.ReleaseID).Scan(&reconciled); err != nil {
		return err
	}
	if reconciled.Valid && reconciled.String == active.OperationID {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE publication_requests SET state=CASE WHEN state='awaiting_confirmation' THEN 'stale' ELSE 'failed' END,completed_at=?,error_json=? WHERE id<>? AND state IN ('awaiting_confirmation','queued','activating')`, active.ActivatedAt, encode(detail("ACTIVE_CHANGED", "线上激活基线已变化，请重新生成并审阅。")), active.OperationID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE candidates SET validation_json=json_set(validation_json,'$.reconciled_operation_id',?) WHERE id=?`, active.OperationID, active.ReleaseID)
	return err
}
