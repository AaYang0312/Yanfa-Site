package content_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"
	"yuanfa-official/backend/internal/contract"
)

func TestHiddenMachineFieldsStayOutOfBrowserConfirmation(t *testing.T) {
	a, actor := fixture(t)
	ctx := context.Background()
	input := draft("hidden-fields")
	input.GuideProfile.Materials = []string{"wood"}
	input.PurchaseOptions = []contract.PurchaseOptionEdit{{Platform: "商城", StoreName: "原店铺", URL: "https://item.example.test/original", MatchScope: "exact_model", Enabled: true}}
	first, err := a.Content.Create(ctx, actor, input)
	if err != nil {
		t.Fatal(err)
	}
	// Existing approved data, as imported by seed or an earlier public release.
	raw, _ := json.Marshal(first.Product)
	if _, err = a.Store.DB().Exec(`UPDATE product_confirmations SET document_json=? WHERE product_id=?`, string(raw), first.Product.ID); err != nil {
		t.Fatal(err)
	}
	token, err := a.Auth.IssueToken(ctx, actor, contract.TokenInput{Name: "draft hidden data", ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano), Scopes: []contract.TokenScope{"products:write"}})
	if err != nil {
		t.Fatal(err)
	}
	machine, err := a.Auth.AuthenticateToken(ctx, token.Secret)
	if err != nil {
		t.Fatal(err)
	}
	changes := edit(first.Product)
	changes.PurchaseOptions[0].URL = "https://item.example.test/unseen"
	changes.GuideProfile.Materials = []string{"steel"}
	pending, err := a.Content.Replace(ctx, machine, first.Product.ID, first.Version, changes)
	if err != nil {
		t.Fatal(err)
	}
	changes = edit(pending.Product)
	changes.Summary = "visible edit"
	saved, err := a.Content.Replace(ctx, actor, first.Product.ID, pending.Version, changes)
	if err != nil {
		t.Fatal(err)
	}
	var confirmedRaw string
	if err = a.Store.DB().QueryRow(`SELECT document_json FROM product_confirmations WHERE product_id=?`, first.Product.ID).Scan(&confirmedRaw); err != nil {
		t.Fatal(err)
	}
	var confirmed contract.ProductDocument
	json.Unmarshal([]byte(confirmedRaw), &confirmed)
	if confirmed.Summary != "visible edit" || confirmed.PurchaseOptions[0].URL != "https://item.example.test/original" || len(confirmed.GuideProfile.Materials) != 1 || confirmed.GuideProfile.Materials[0] != "wood" {
		t.Fatal("unseen machine fields were approved")
	}
	if saved.Product.PurchaseOptions[0].URL != "https://item.example.test/unseen" {
		t.Fatal("machine workspace data was discarded")
	}
	if saved.Product.GuideProfile.Materials[0] != "steel" {
		t.Fatal("confirmation mutated the workspace response")
	}
}

func TestBrowserConfirmsLegacyNailEntryAsConsumable(t *testing.T) {
	a, actor := fixture(t)
	ctx := context.Background()
	input := draft("legacy-nail")
	input.Kind = "entry"
	input.CategoryID = "nails"
	first, err := a.Content.Create(ctx, actor, input)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a legacy approved generic nail record.
	raw, _ := json.Marshal(first.Product)
	if _, err = a.Store.DB().Exec(`UPDATE product_confirmations SET document_json=? WHERE product_id=?`, string(raw), first.Product.ID); err != nil {
		t.Fatal(err)
	}
	change := edit(first.Product)
	change.Kind = "consumable"
	if _, err = a.Content.Replace(ctx, actor, first.Product.ID, first.Version, change); err != nil {
		t.Fatal(err)
	}
	var kind string
	if err = a.Store.DB().QueryRow(`SELECT json_extract(document_json,'$.kind') FROM product_confirmations WHERE product_id=?`, first.Product.ID).Scan(&kind); err != nil {
		t.Fatal(err)
	}
	if kind != "consumable" {
		t.Fatalf("visible nail category retained legacy entry type: %s", kind)
	}
}

func TestBrowserCanRemoveTextWithHiddenSource(t *testing.T) {
	a, actor := fixture(t)
	ctx := context.Background()
	input := draft("source-list")
	input.Applications = []string{"first", "second"}
	input.GuideProfile.Sources = []contract.GuideSource{{ID: "item", Kind: "product_text", Reference: "/applications/1", SHA256: sha("second")}}
	first, err := a.Content.Create(ctx, actor, input)
	if err != nil {
		t.Fatal(err)
	}
	change := edit(first.Product)
	change.Applications = []string{"first"}
	saved, err := a.Content.Replace(ctx, actor, first.Product.ID, first.Version, change)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Product.GuideProfile.Sources) != 0 {
		t.Fatal("vanished text reference remains")
	}
}

func TestDraftModelChangeDoesNotFreeConfirmedModel(t *testing.T) {
	a, actor := fixture(t)
	ctx := context.Background()
	input := draft("model-owner")
	input.Model = "M"
	first, err := a.Content.Create(ctx, actor, input)
	if err != nil {
		t.Fatal(err)
	}
	token, err := a.Auth.IssueToken(ctx, actor, contract.TokenInput{Name: "model draft", ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano), Scopes: []contract.TokenScope{"products:write"}})
	if err != nil {
		t.Fatal(err)
	}
	machine, err := a.Auth.AuthenticateToken(ctx, token.Secret)
	if err != nil {
		t.Fatal(err)
	}
	change := edit(first.Product)
	change.Model = "N"
	if _, err = a.Content.Replace(ctx, machine, first.Product.ID, first.Version, change); err != nil {
		t.Fatal(err)
	}
	next := draft("model-collision")
	next.Model = "m"
	_, err = a.Content.Create(ctx, actor, next)
	status(t, err, 409)
	var count int
	a.Store.DB().QueryRow(`SELECT count(*) FROM products WHERE slug='model-collision'`).Scan(&count)
	if count != 0 {
		t.Fatal("model collision committed before confirmation validation")
	}
}
