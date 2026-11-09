package publication_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"
	"yuanfa-official/backend/internal/app"
	"yuanfa-official/backend/internal/auth"
	"yuanfa-official/backend/internal/contract"
)

func waitUpdate(t *testing.T, a *app.App, actor auth.Actor, id, state string, version int64) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		list, err := a.Publication.SiteUpdates(context.Background(), actor)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range list.Items {
			if item.ProductID == id && item.Version >= version && item.State == state {
				return
			}
			if item.ProductID == id && item.State == "failed" && state != "failed" {
				t.Fatalf("auto update failed: %+v", item)
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("automatic update did not reach", state)
}
func changeProduct(t *testing.T, a *app.App, actor auth.Actor, id, summary string) contract.ProductEnvelope {
	t.Helper()
	p, err := a.Content.Get(context.Background(), actor, id)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(p.Product)
	var edit contract.ProductEdit
	json.Unmarshal(b, &edit)
	edit.Summary = summary
	saved, err := a.Content.Replace(context.Background(), actor, id, p.Version, edit)
	if err != nil {
		t.Fatal(err)
	}
	return saved
}
func TestAutomaticUpdatesIsolateDraftsSupersedeRetryAndDelete(t *testing.T) {
	a, actor := fixture(t)
	ctx := context.Background()
	issued, err := a.Auth.IssueToken(ctx, actor, contract.TokenInput{Name: "drafts", ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano), Scopes: []contract.TokenScope{"products:read", "products:write"}})
	if err != nil {
		t.Fatal(err)
	}
	machine, err := a.Auth.AuthenticateToken(ctx, issued.Secret)
	if err != nil {
		t.Fatal(err)
	}
	before, err := a.Content.Get(ctx, actor, "p_f30a")
	if err != nil {
		t.Fatal(err)
	}
	changeProduct(t, a, machine, "p_f30a", "must remain a draft")
	if err = a.Publication.Start(ctx); err != nil {
		t.Fatal(err)
	}
	first := changeProduct(t, a, actor, "p_f30m", "first confirmed edit")
	// A second confirmation while the first build is queued or building must
	// never allow the older artifact to replace the newer confirmed catalog.
	deadline := time.Now().Add(10 * time.Second)
	var oldID string
	for time.Now().Before(deadline) {
		if a.Store.DB().QueryRow(`SELECT candidate_id FROM site_update_batches WHERE state='building' LIMIT 1`).Scan(&oldID) == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if oldID == "" {
		t.Fatal("save did not queue a build")
	}
	latest := changeProduct(t, a, actor, first.Product.ID, "latest confirmed edit")
	changeProduct(t, a, machine, "p_f30a", "newer unrelated machine draft")
	waitUpdate(t, a, actor, latest.Product.ID, "succeeded", latest.Version)
	if *a.Releases.ActiveID() == oldID {
		t.Fatal("superseded build became active")
	}
	h, err := a.Releases.Capture(*a.Releases.ActiveID())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range h.Catalog().Products {
		if p.ID == latest.Product.ID && p.Summary != latest.Product.Summary {
			t.Fatal("latest edit missing")
		}
		if p.ID == before.Product.ID && p.Summary != before.Product.Summary {
			t.Fatal("machine draft published")
		}
	}
	previous := *a.Releases.ActiveID()
	path := os.Getenv("PATH")
	t.Setenv("PATH", "")
	failed := changeProduct(t, a, actor, latest.Product.ID, "retry this edit")
	waitUpdate(t, a, actor, failed.Product.ID, "failed", failed.Version)
	if *a.Releases.ActiveID() != previous {
		t.Fatal("failed build replaced live site")
	}
	t.Setenv("PATH", path)
	if err = a.Publication.RetrySiteUpdate(ctx, machine); err == nil {
		t.Fatal("machine retried browser publication")
	}
	if err = a.Publication.RetrySiteUpdate(ctx, actor); err != nil {
		t.Fatal(err)
	}
	waitUpdate(t, a, actor, failed.Product.ID, "succeeded", failed.Version)
	prior, err := a.Publication.GetCandidate(ctx, actor, previous)
	if err != nil {
		t.Fatal(err)
	}
	rollback, err := a.Publication.Request(ctx, actor, publicationInput(t, a, prior, "rollback"), "auto-rollback-check")
	if err != nil {
		t.Fatal(err)
	}
	finishPublication(t, a, actor, rollback, "auto-rollback-confirm")
	if *a.Releases.ActiveID() != previous {
		t.Fatal("automatic success cannot be restored")
	}
	if _, err = a.Content.Delete(ctx, actor, failed.Product.ID, failed.Version); err != nil {
		t.Fatal(err)
	}
	waitUpdate(t, a, actor, failed.Product.ID, "succeeded", failed.Version+1)
	h, err = a.Releases.Capture(*a.Releases.ActiveID())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range h.Catalog().Products {
		if p.ID == failed.Product.ID {
			t.Fatal("deleted product remains in active catalog")
		}
	}
	found := false
	for _, r := range h.Catalog().PreviouslyPublishedRoutes {
		if r.ProductID == failed.Product.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("business deletion lost old route ownership")
	}
}
