package publication_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"yuanfa-official/backend/internal/app"
	"yuanfa-official/backend/internal/auth"
	"yuanfa-official/backend/internal/contract"
)

// These consumers run the real persisted worker and integrated T05 builder.
// V06 prerequisites include Node22 and npm ci in the checkout template.
func readyCandidate(t *testing.T, a *app.App, actor auth.Actor, key string) contract.Candidate {
	t.Helper()
	var revision int64
	if err := a.Store.DB().QueryRow(`SELECT revision FROM workspace WHERE id=1`).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	c, err := a.Publication.CreateCandidate(context.Background(), actor, revision, key)
	if err != nil {
		t.Fatal(err)
	}
	timer := time.NewTimer(3 * time.Minute)
	defer timer.Stop()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		current, err := a.Publication.GetCandidate(context.Background(), actor, c.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.State == "ready" {
			return current
		}
		if current.State == "failed" {
			t.Fatalf("real candidate build failed: %+v", current.Error)
		}
		select {
		case <-timer.C:
			t.Fatal("real candidate build did not complete")
		case <-tick.C:
		}
	}
}
func publicationInput(t *testing.T, a *app.App, c contract.Candidate, kind string) contract.PublicationInput {
	t.Helper()
	var revision int64
	if err := a.Store.DB().QueryRow(`SELECT revision FROM workspace WHERE id=1`).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	in := contract.PublicationInput{Kind: kind, SnapshotSHA256: c.SnapshotSHA256, ExpectedActiveReleaseID: a.Releases.ActiveID(), ExpectedWorkspaceRevision: revision}
	if kind == "publish" {
		in.CandidateID = new(c.ID)
	} else {
		in.ReleaseID = new(c.ID)
	}
	return in
}
func confirmation(p contract.PublicationRequest) contract.ConfirmationInput {
	return contract.ConfirmationInput{SnapshotSHA256: p.SnapshotSHA256, ExpectedActiveReleaseID: p.ExpectedActiveReleaseID, ExpectedWorkspaceRevision: p.ExpectedWorkspaceRevision}
}
func finishPublication(t *testing.T, a *app.App, actor auth.Actor, p contract.PublicationRequest, key string) contract.PublicationRequest {
	t.Helper()
	if _, err := a.Publication.Confirm(context.Background(), actor, p.ID, confirmation(p), key); err != nil {
		t.Fatal(err)
	}
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		current, err := a.Publication.GetRequest(context.Background(), actor, p.ID)
		if err != nil {
			t.Fatal(err)
		}
		switch current.State {
		case "succeeded":
			return current
		case "failed", "stale", "expired", "cancelled":
			t.Fatalf("publication did not succeed: %+v", current)
		}
		select {
		case <-timer.C:
			t.Fatal("publication did not complete")
		case <-tick.C:
		}
	}
}
func requireConflict(t *testing.T, err error) {
	t.Helper()
	var business *contract.BusinessError
	if !errors.As(err, &business) || business.HTTPStatus != 409 {
		t.Fatalf("expected consumer-visible 409, got %v", err)
	}
}

func TestPublishRoundTripNeverRevivesOldCandidateOrPendingIntent(t *testing.T) {
	ctx := context.Background()
	a, actor := fixture(t)
	if err := a.Publication.Start(ctx); err != nil {
		t.Fatal(err)
	}
	first := readyCandidate(t, a, actor, "candidate-A")
	intentA, err := a.Publication.Request(ctx, actor, publicationInput(t, a, first, "publish"), "request-A")
	if err != nil {
		t.Fatal(err)
	}
	finishPublication(t, a, actor, intentA, "confirm-A")
	originalOperation := a.Releases.OperationID()
	next := readyCandidate(t, a, actor, "candidate-B")
	inputB := publicationInput(t, a, next, "publish")
	pending, err := a.Publication.Request(ctx, actor, inputB, "pending-B")
	if err != nil {
		t.Fatal(err)
	}
	pendingRollback, err := a.Publication.Request(ctx, actor, publicationInput(t, a, first, "rollback"), "pending-rollback")
	if err != nil {
		t.Fatal(err)
	}
	successful, err := a.Publication.Request(ctx, actor, inputB, "successful-B")
	if err != nil {
		t.Fatal(err)
	}
	successful = finishPublication(t, a, actor, successful, "confirm-B")
	rollback, err := a.Publication.Request(ctx, actor, publicationInput(t, a, first, "rollback"), "rollback-A")
	if err != nil {
		t.Fatal(err)
	}
	finishPublication(t, a, actor, rollback, "confirm-rollback")
	if id := a.Releases.ActiveID(); id == nil || *id != first.ID {
		t.Fatal("round trip did not return to A")
	}
	if a.Releases.OperationID() == originalOperation {
		t.Fatal("round trip reused the old activation identity")
	}
	assertRejectedAndReplay := func(current *app.App, label string) {
		t.Helper()
		before := current.Releases.OperationID()
		_, err := current.Publication.Request(ctx, actor, publicationInput(t, current, next, "publish"), "old-candidate-"+label)
		requireCode(t, err, "CANDIDATE_STALE")
		requireConflict(t, err)
		_, err = current.Publication.Confirm(ctx, actor, pending.ID, confirmation(pending), "old-pending-"+label)
		requireConflict(t, err)
		old, err := current.Publication.GetRequest(ctx, actor, pending.ID)
		if err != nil {
			t.Fatal(err)
		}
		if old.State != "stale" {
			t.Fatalf("old pending intent revived: %s", old.State)
		}
		_, err = current.Publication.Confirm(ctx, actor, pendingRollback.ID, confirmation(pendingRollback), "old-rollback-"+label)
		requireConflict(t, err)
		replay, err := current.Publication.Confirm(ctx, actor, successful.ID, confirmation(successful), "confirm-B")
		if err != nil || replay.ID != successful.ID || replay.State != "succeeded" || replay.ResultReleaseID == nil || *replay.ResultReleaseID != next.ID {
			t.Fatalf("successful confirmation replay lost original resource: %+v %v", replay, err)
		}
		replay, err = current.Publication.Request(ctx, actor, inputB, "successful-B")
		if err != nil || replay.ID != successful.ID || replay.State != "succeeded" {
			t.Fatalf("successful request replay changed resource: %+v %v", replay, err)
		}
		if current.Releases.OperationID() != before {
			t.Fatal("replay changed active authority")
		}
	}
	assertRejectedAndReplay(a, "before-restart")
	// An intent created after the latest activation must survive a normal restart.
	fresh, err := a.Publication.Request(ctx, actor, publicationInput(t, a, first, "rollback"), "fresh-rollback")
	if err != nil {
		t.Fatal(err)
	}
	cfg := a.Config
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := app.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restarted.Close() })
	if err = restarted.Releases.Restore(); err != nil {
		t.Fatal(err)
	}
	if err = restarted.Publication.Start(ctx); err != nil {
		t.Fatal(err)
	}
	assertRejectedAndReplay(restarted, "after-restart")
	current, err := restarted.Publication.GetRequest(ctx, actor, fresh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.State != "awaiting_confirmation" {
		t.Fatalf("normal restart invalidated a current intent: %s", current.State)
	}
	finishPublication(t, restarted, actor, current, "confirm-fresh")
	assertRejectedAndReplay(restarted, "after-fresh-activation")
}
