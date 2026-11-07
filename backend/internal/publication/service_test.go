package publication_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"yuanfa-official/backend/internal/app"
	"yuanfa-official/backend/internal/auth"
	"yuanfa-official/backend/internal/config"
	"yuanfa-official/backend/internal/contract"
	"yuanfa-official/backend/internal/release"
)

func fixture(t *testing.T) (*app.App, auth.Actor) {
	t.Helper()
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	a, err := app.Open(context.Background(), config.Config{DataDir: t.TempDir(), TemplateDir: root, SiteURL: "http://127.0.0.1:18006"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	secret := make([]byte, 24)
	if _, err = rand.Read(secret); err != nil {
		t.Fatal(err)
	}
	password := hex.EncodeToString(secret)
	if err = a.Auth.InitAdmin(context.Background(), "admin", password); err != nil {
		t.Fatal(err)
	}
	login, err := a.Auth.Login(context.Background(), contract.LoginInput{Username: "admin", Password: password})
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Content.ImportSeed(context.Background(), filepath.Join(root, "seed", "initial-catalog.json"), filepath.Join(root, "src", "assets", "products")); err != nil {
		t.Fatal(err)
	}
	return a, login.Session.Actor
}
func requireCode(t *testing.T, err error, code string) {
	t.Helper()
	var business *contract.BusinessError
	if !errors.As(err, &business) || business.Detail.Code != code {
		t.Fatalf("expected %s, got %v", code, err)
	}
}
func TestEntireFrozenWorkspaceAndIdentityScopedReplay(t *testing.T) {
	ctx := context.Background()
	a, actor := fixture(t)
	var revision int64
	if err := a.Store.DB().QueryRow(`SELECT revision FROM workspace WHERE id=1`).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	c, err := a.Publication.CreateCandidate(ctx, actor, revision, "freeze")
	if err != nil {
		t.Fatal(err)
	}
	same, err := a.Publication.CreateCandidate(ctx, actor, revision, "freeze")
	if err != nil || same.ID != c.ID {
		t.Fatalf("lost-response replay: %v", err)
	}
	p, err := a.Content.Get(ctx, actor, "p_f30m")
	if err != nil {
		t.Fatal(err)
	}
	editBytes, _ := json.Marshal(p.Product)
	var edit contract.ProductEdit
	if err = json.Unmarshal(editBytes, &edit); err != nil {
		t.Fatal(err)
	}
	edit.Summary += " edited after freeze"
	if _, err = a.Content.Replace(ctx, actor, p.Product.ID, p.Product.Version, edit); err != nil {
		t.Fatal(err)
	}
	replay, err := a.Publication.CreateCandidate(ctx, actor, revision, "freeze")
	if err != nil || replay.ID != c.ID || !replay.WorkspaceChanged {
		t.Fatal("replay lost frozen resource after edit")
	}
	_, err = a.Publication.CreateCandidate(ctx, actor, revision+1, "freeze")
	requireCode(t, err, "IDEMPOTENCY_CONFLICT")
	_, err = a.Publication.CreateCandidate(ctx, actor, revision, "different-key")
	requireCode(t, err, "WORKSPACE_CHANGED")
	var frozen, hash string
	if err = a.Store.DB().QueryRow(`SELECT snapshot_json,snapshot_sha256 FROM candidates WHERE id=?`, c.ID).Scan(&frozen, &hash); err != nil {
		t.Fatal(err)
	}
	snapshot, err := contract.Decode[contract.CatalogSnapshot]("catalog-snapshot.schema.json#/$defs/PublishableSnapshot", []byte(frozen))
	if err != nil {
		t.Fatal(err)
	}
	if release.Hash([]byte(frozen)) != hash {
		t.Fatal("snapshot digest differs from exact bytes")
	}
	found := false
	for _, product := range snapshot.Products {
		if product.ID == p.Product.ID {
			found = true
			if product.Summary != p.Product.Summary || product.Version != p.Product.Version {
				t.Fatal("frozen candidate changed with workspace")
			}
		}
	}
	if !found {
		t.Fatal("whole-workspace freeze omitted product")
	}
	// Recovery interrupts persisted queued work rather than silently publishing it.
	if err = a.Publication.Start(ctx); err != nil {
		t.Fatal(err)
	}
	a.Publication.Close()
	recovered, err := a.Publication.GetCandidate(ctx, actor, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.State != "failed" || recovered.Error.Code != "INTERRUPTED" || a.Releases.ActiveID() != nil {
		t.Fatal("restart retried interrupted build")
	}
}
func TestUnreadyCandidateAndBrowserOnlyConfirmation(t *testing.T) {
	ctx := context.Background()
	a, actor := fixture(t)
	// Browser-only confirmation is checked before accessing a target or transaction.
	_, err := a.Publication.Confirm(ctx, auth.Actor{}, "unknown", contract.ConfirmationInput{}, "key")
	requireCode(t, err, "FORBIDDEN")
	_, err = a.Publication.Preview(ctx, auth.Actor{}, "unknown")
	requireCode(t, err, "FORBIDDEN")
	_, err = a.Publication.CreateCandidate(ctx, auth.Actor{}, 0, "key")
	requireCode(t, err, "UNAUTHENTICATED")
	var revision int64
	if err = a.Store.DB().QueryRow(`SELECT revision FROM workspace WHERE id=1`).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	c, err := a.Publication.CreateCandidate(ctx, actor, revision, "queued")
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.Publication.Request(ctx, actor, contract.PublicationInput{Kind: "publish", CandidateID: new(c.ID), SnapshotSHA256: c.SnapshotSHA256, ExpectedWorkspaceRevision: revision}, "request")
	requireCode(t, err, "CANDIDATE_NOT_READY")
	var count int
	if err = a.Store.DB().QueryRow(`SELECT count(*) FROM publication_requests`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("unready intent persisted")
	}
	token, err := a.Auth.IssueToken(ctx, actor, contract.TokenInput{Name: "confirmation boundary", ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano), Scopes: []contract.TokenScope{"candidates:write", "publications:write"}})
	if err != nil {
		t.Fatal(err)
	}
	tokenActor, err := a.Auth.AuthenticateToken(ctx, token.Secret)
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.Publication.Confirm(ctx, tokenActor, "unknown", contract.ConfirmationInput{}, "token-confirm")
	requireCode(t, err, "FORBIDDEN")
	if err = a.Auth.Logout(ctx, actor); err != nil {
		t.Fatal(err)
	}
	_, err = a.Publication.GetCandidate(ctx, actor, c.ID)
	requireCode(t, err, "UNAUTHENTICATED")
	var none string
	if err = a.Store.DB().QueryRow(`SELECT result_release_id FROM publication_requests LIMIT 1`).Scan(&none); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("unexpected publication result")
	}
}
