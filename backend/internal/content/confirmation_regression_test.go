package content_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"yuanfa-official/backend/internal/app"
	"yuanfa-official/backend/internal/auth"
	"yuanfa-official/backend/internal/contract"
)

func confirmationToken(t *testing.T, a *app.App, browser auth.Actor, scopes ...contract.TokenScope) (auth.Actor, string) {
	t.Helper()
	issued, err := a.Auth.IssueToken(context.Background(), browser, contract.TokenInput{
		Name: "confirmation regression", ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano), Scopes: scopes,
	})
	if err != nil {
		t.Fatal(err)
	}
	actor, err := a.Auth.AuthenticateToken(context.Background(), issued.Secret)
	if err != nil {
		t.Fatal(err)
	}
	return actor, issued.Token.ID
}

func confirmationProduct(slug string) contract.ProductCreate {
	p := draft(slug)
	p.Name, p.Model, p.Summary = "回归测试工具", "REGRESSION", "用于隔离回归测试。"
	p.Applications = []string{"木工作业"}
	return p
}

func TestConfirmationRegressionDeleteBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name                         string
		confirmed, published, edited bool
	}{
		{name: "machine_only_draft"},
		{name: "pending_browser_confirmation", confirmed: true},
		{name: "newer_machine_draft", confirmed: true, edited: true},
		{name: "published_without_confirmation", published: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, browser := fixture(t)
			ctx := context.Background()
			machine, _ := confirmationToken(t, a, browser, "products:read", "products:write")
			creator := machine
			input := confirmationProduct("delete-boundary")
			if tc.confirmed {
				creator, input.Visibility = browser, "listed"
			}
			p, err := a.Content.Create(ctx, creator, input)
			if err != nil {
				t.Fatal(err)
			}
			if tc.edited {
				change := edit(p.Product)
				change.Summary = "仅机器草稿的新修改"
				p, err = a.Content.Replace(ctx, machine, p.Product.ID, p.Version, change)
				if err != nil {
					t.Fatal(err)
				}
			}
			if tc.published {
				// Model a legacy published product with no schema002 confirmation.
				if _, err = a.Store.DB().Exec(`UPDATE reserved_routes SET ever_published=1 WHERE owner_id=?`, p.Product.ID); err != nil {
					t.Fatal(err)
				}
			}
			var beforeConfirmation string
			if tc.confirmed {
				if err = a.Store.DB().QueryRow(`SELECT document_json FROM product_confirmations WHERE product_id=? AND deleted=0`, p.Product.ID).Scan(&beforeConfirmation); err != nil {
					t.Fatal(err)
				}
			}
			deleted, err := a.Content.Delete(ctx, machine, p.Product.ID, p.Version)
			if !tc.confirmed && !tc.published {
				if err != nil || deleted.DeletedID != p.Product.ID {
					t.Fatalf("machine-only draft must remain deletable: %+v %v", deleted, err)
				}
				var count int
				if err = a.Store.DB().QueryRow(`SELECT count(*) FROM product_confirmations WHERE product_id=?`, p.Product.ID).Scan(&count); err != nil || count != 0 {
					t.Fatalf("machine deletion created a browser confirmation: count=%d err=%v", count, err)
				}
			} else {
				status(t, err, 409)
				current, err := a.Content.Get(ctx, browser, p.Product.ID)
				if err != nil || current.Version != p.Version || current.WorkspaceRevision != p.WorkspaceRevision {
					t.Fatalf("rejected machine deletion changed workspace: %+v %v", current, err)
				}
				if tc.confirmed {
					var afterConfirmation string
					if err = a.Store.DB().QueryRow(`SELECT document_json FROM product_confirmations WHERE product_id=? AND deleted=0 AND state='pending'`, p.Product.ID).Scan(&afterConfirmation); err != nil || afterConfirmation != beforeConfirmation {
						t.Fatalf("rejected machine deletion changed confirmation: %v", err)
					}
				}
				// The browser remains able to confirm removal, even after publication.
				deleted, err = a.Content.Delete(ctx, browser, p.Product.ID, p.Version)
				if err != nil || deleted.DeletedID != p.Product.ID || deleted.WorkspaceRevision != p.WorkspaceRevision+1 {
					t.Fatalf("browser deletion failed: %+v %v", deleted, err)
				}
				var version int64
				var tombstone bool
				var state string
				if err = a.Store.DB().QueryRow(`SELECT version,deleted,state FROM product_confirmations WHERE product_id=?`, p.Product.ID).Scan(&version, &tombstone, &state); err != nil || !tombstone || version != p.Version+1 || state != "pending" {
					t.Fatalf("browser deletion did not atomically confirm a tombstone: version=%d deleted=%v state=%s err=%v", version, tombstone, state, err)
				}
			}
			_, err = a.Content.Get(ctx, browser, p.Product.ID)
			status(t, err, 404)
			var owner string
			if err = a.Store.DB().QueryRow(`SELECT owner_id FROM reserved_routes WHERE slug=?`, p.Product.Slug).Scan(&owner); err != nil || owner != p.Product.ID {
				t.Fatalf("deletion lost permanent route ownership: %q %v", owner, err)
			}
		})
	}
}

func TestConfirmationRegressionDeleteRetainsTokenGuards(t *testing.T) {
	a, browser := fixture(t)
	ctx := context.Background()
	writer, tokenID := confirmationToken(t, a, browser, "products:write")
	reader, _ := confirmationToken(t, a, browser, "products:read")
	p, err := a.Content.Create(ctx, writer, confirmationProduct("token-guards"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.Content.Delete(ctx, reader, p.Product.ID, p.Version)
	status(t, err, 403)
	if err = a.Auth.RevokeToken(ctx, browser, tokenID); err != nil {
		t.Fatal(err)
	}
	_, err = a.Content.Delete(ctx, writer, p.Product.ID, p.Version)
	status(t, err, 401)
	current, err := a.Content.Get(ctx, browser, p.Product.ID)
	if err != nil || current.Version != p.Version || current.WorkspaceRevision != p.WorkspaceRevision {
		t.Fatalf("unauthorized deletion changed the draft: %+v %v", current, err)
	}
}

func TestConfirmationRegressionCategoryRoundTrip(t *testing.T) {
	a, browser := fixture(t)
	ctx := context.Background()
	input := confirmationProduct("category-round-trip")
	input.Visibility = "listed"
	p, err := a.Content.Create(ctx, browser, input)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct {
		category contract.CategoryID
		kind     string
	}{{"nails", "consumable"}, {"straight", "tool"}} {
		change := edit(p.Product)
		change.CategoryID = step.category // The editor does not expose hidden Kind.
		p, err = a.Content.Replace(ctx, browser, p.Product.ID, p.Version, change)
		if err != nil {
			t.Fatal(err)
		}
		var category, kind string
		if err = a.Store.DB().QueryRow(`SELECT json_extract(document_json,'$.category_id'),json_extract(document_json,'$.kind') FROM product_confirmations WHERE product_id=? AND version=?`, p.Product.ID, p.Version).Scan(&category, &kind); err != nil {
			t.Fatal(err)
		}
		if category != string(step.category) || kind != step.kind {
			t.Fatalf("confirmed category %s has kind %s; want %s", category, kind, step.kind)
		}
	}
}

func TestConfirmationRegressionOtherRetainsApprovedHiddenKind(t *testing.T) {
	for _, kind := range []string{"tool", "entry", "consumable"} {
		t.Run(kind, func(t *testing.T) {
			a, browser := fixture(t)
			ctx := context.Background()
			input := confirmationProduct("other-hidden-kind")
			input.Kind, input.CategoryID = kind, "other"
			p, err := a.Content.Create(ctx, browser, input)
			if err != nil {
				t.Fatal(err)
			}
			machine, _ := confirmationToken(t, a, browser, "products:write")
			change := edit(p.Product)
			change.Kind = "entry"
			if kind == "entry" {
				change.Kind = "tool"
			}
			p, err = a.Content.Replace(ctx, machine, p.Product.ID, p.Version, change)
			if err != nil {
				t.Fatal(err)
			}
			change = edit(p.Product)
			change.Summary = "管理员确认的可见文字"
			p, err = a.Content.Replace(ctx, browser, p.Product.ID, p.Version, change)
			if err != nil {
				t.Fatal(err)
			}
			var raw string
			if err = a.Store.DB().QueryRow(`SELECT document_json FROM product_confirmations WHERE product_id=?`, p.Product.ID).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			var confirmed contract.ProductDocument
			if err = json.Unmarshal([]byte(raw), &confirmed); err != nil {
				t.Fatal(err)
			}
			if confirmed.Kind != kind || confirmed.Summary != change.Summary || p.Product.Kind != change.Kind {
				t.Fatalf("other category lost approved kind or published hidden draft: confirmed=%s workspace=%s", confirmed.Kind, p.Product.Kind)
			}
		})
	}
}
