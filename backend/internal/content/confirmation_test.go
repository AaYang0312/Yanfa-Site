package content_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"
	"yuanfa-official/backend/internal/content"
	"yuanfa-official/backend/internal/contract"
)

func TestBrowserConfirmationDoesNotIncludeMachineDraft(t *testing.T) {
	a, actor := fixture(t)
	ctx := context.Background()
	first, err := a.Content.Create(ctx, actor, draft("confirmed"))
	if err != nil {
		t.Fatal(err)
	}
	token, err := a.Auth.IssueToken(ctx, actor, contract.TokenInput{Name: "draft editor", ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano), Scopes: []contract.TokenScope{"products:read", "products:write"}})
	if err != nil {
		t.Fatal(err)
	}
	machine, err := a.Auth.AuthenticateToken(ctx, token.Secret)
	if err != nil {
		t.Fatal(err)
	}
	change := edit(first.Product)
	change.Summary = "unconfirmed machine text"
	updated, err := a.Content.Replace(ctx, machine, first.Product.ID, first.Version, change)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Content.Create(ctx, machine, draft("machine-only")); err != nil {
		t.Fatal(err)
	}
	tx, err := a.Store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := a.Content.ConfirmedSnapshot(ctx, tx, "c_confirmed", nil)
	tx.Rollback()
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Products) != 1 || snapshot.Products[0].Summary != "" || snapshot.Products[0].Version != first.Version {
		t.Fatal("machine draft leaked into confirmed catalog")
	}
	if _, err = a.Content.Replace(ctx, actor, first.Product.ID, updated.Version, change); err != nil {
		t.Fatal(err)
	}
	tx, err = a.Store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err = a.Content.ConfirmedSnapshot(ctx, tx, "c_next", nil)
	tx.Rollback()
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Products) != 1 || snapshot.Products[0].Summary != change.Summary {
		t.Fatal("browser confirmation missing")
	}
}

func TestBrowserWriteReplayAndPublishedDeletion(t *testing.T) {
	a, actor := fixture(t)
	ctx := context.Background()
	createCtx := content.WithRequestKey(ctx, "create-one")
	first, err := a.Content.Create(createCtx, actor, draft("replay"))
	if err != nil {
		t.Fatal(err)
	}
	again, err := a.Content.Create(createCtx, actor, draft("replay"))
	if err != nil || again.Product.ID != first.Product.ID {
		t.Fatalf("create replay duplicated: %v", err)
	}
	changed := draft("different")
	_, err = a.Content.Create(createCtx, actor, changed)
	status(t, err, 409)
	updateCtx := content.WithRequestKey(ctx, "save-one")
	change := edit(first.Product)
	change.Summary = "saved"
	updated, err := a.Content.Replace(updateCtx, actor, first.Product.ID, first.Version, change)
	if err != nil {
		t.Fatal(err)
	}
	again, err = a.Content.Replace(updateCtx, actor, first.Product.ID, first.Version, change)
	if err != nil || again.Version != updated.Version {
		t.Fatalf("save replay: %v", err)
	}
	if _, err = a.Store.DB().Exec(`UPDATE reserved_routes SET ever_published=1 WHERE slug=?`, first.Product.Slug); err != nil {
		t.Fatal(err)
	}
	deleteCtx := content.WithRequestKey(ctx, "delete-one")
	if _, err = a.Content.Delete(deleteCtx, actor, first.Product.ID, updated.Version); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Content.Delete(deleteCtx, actor, first.Product.ID, updated.Version); err != nil {
		t.Fatal(err)
	}
	var doc string
	var deleted int
	if err = a.Store.DB().QueryRow(`SELECT document_json,deleted FROM product_confirmations WHERE product_id=?`, first.Product.ID).Scan(&doc, &deleted); err != nil {
		t.Fatal(err)
	}
	var product contract.ProductDocument
	json.Unmarshal([]byte(doc), &product)
	if deleted != 1 || product.Version != 3 {
		t.Fatal("delete not confirmed")
	}
}
