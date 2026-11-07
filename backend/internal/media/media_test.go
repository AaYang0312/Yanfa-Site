package media_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"yuanfa-official/backend/internal/app"
	"yuanfa-official/backend/internal/auth"
	"yuanfa-official/backend/internal/config"
	"yuanfa-official/backend/internal/contract"
	"yuanfa-official/backend/internal/media"
)

func setup(t *testing.T) (*app.App, auth.Actor) {
	t.Helper()
	a, err := app.Open(context.Background(), config.Config{DataDir: t.TempDir(), SiteURL: "http://127.0.0.1:18004"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	password := rand.Text()
	if err = a.Auth.InitAdmin(context.Background(), "admin", password); err != nil {
		t.Fatal(err)
	}
	login, err := a.Auth.Login(context.Background(), contract.LoginInput{Username: "admin", Password: password})
	if err != nil {
		t.Fatal(err)
	}
	return a, login.Session.Actor
}
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "tests", "fixtures", "media", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func status(t *testing.T, err error, want int) {
	t.Helper()
	var e *contract.BusinessError
	if !errors.As(err, &e) || e.HTTPStatus != want {
		t.Fatalf("want %d; got %v", want, err)
	}
}
func TestImageBoundaryRejectsWithoutAssociableRecords(t *testing.T) {
	a, actor := setup(t)
	ctx := context.Background()
	for _, name := range []string{"valid.png", "valid.jpg", "valid.webp"} {
		t.Run(name, func(t *testing.T) {
			data := fixture(t, name)
			record, err := a.Media.Upload(ctx, actor, bytes.NewReader(data), name)
			if err != nil {
				t.Fatal(err)
			}
			f, meta, err := a.Media.File(ctx, actor, record.MediaID)
			if err != nil {
				t.Fatal(err)
			}
			actual, err := io.ReadAll(f)
			f.Close()
			if err != nil || !bytes.Equal(actual, data) || meta.SHA256 != record.SHA256 {
				t.Fatal("private preview changed bytes", err)
			}
			duplicate, err := a.Media.Upload(ctx, actor, bytes.NewReader(data), name)
			if err != nil || duplicate.MediaID != record.MediaID {
				t.Fatal("SHA dedup failed", err)
			}
		})
	}
	var before int
	if err := a.Store.DB().QueryRow(`SELECT count(*) FROM media`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		data   []byte
		status int
	}{{"fake.jpg", fixture(t, "valid.png"), 422}, {"fake.png", []byte("<svg xmlns='http://www.w3.org/2000/svg'/>"), 422}, {"broken.png", fixture(t, "valid.png")[:40], 422}, {"animated.png", fixture(t, "animated.png"), 422}, {"animated.webp", fixture(t, "animated.webp"), 422}, {"too-wide.png", fixture(t, "too-wide.png"), 413}, {"too-many-pixels.png", fixture(t, "too-many-pixels.png"), 413}, {"large.png", bytes.Repeat([]byte{'x'}, int(media.MaxBytes+1)), 413}} {
		t.Run(test.name, func(t *testing.T) {
			_, err := a.Media.Upload(ctx, actor, bytes.NewReader(test.data), test.name)
			status(t, err, test.status)
		})
	}
	var after int
	var revision int64
	if err := a.Store.DB().QueryRow(`SELECT count(*) FROM media`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.DB().QueryRow(`SELECT revision FROM workspace`).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	if before != after || revision != 0 {
		t.Fatal("rejected image produced a record or upload changed workspace")
	}
	files, err := os.ReadDir(filepath.Join(a.Config.DataDir, "media"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if f.Name()[0] == '.' {
			t.Fatal("temporary file leaked")
		}
	}
}
func TestMediaAfterCursorAndDeletion(t *testing.T) {
	a, actor := setup(t)
	ctx := context.Background()
	initial, err := a.Media.List(ctx, actor, contract.MediaQuery{})
	if err != nil {
		t.Fatal(err)
	}
	png, err := a.Media.Upload(ctx, actor, bytes.NewReader(fixture(t, "valid.png")), "valid.png")
	if err != nil {
		t.Fatal(err)
	}
	jpg, err := a.Media.Upload(ctx, actor, bytes.NewReader(fixture(t, "valid.jpg")), "valid.jpg")
	if err != nil {
		t.Fatal(err)
	}
	one := int64(1)
	page, err := a.Media.List(ctx, actor, contract.MediaQuery{After: &initial.After, Limit: &one})
	if err != nil || len(page.Items) != 1 || page.Items[0].MediaID != png.MediaID || page.NextCursor == nil {
		t.Fatal("upload workflow cursor", err)
	}
	next, err := a.Media.List(ctx, actor, contract.MediaQuery{After: &initial.After, Limit: &one, Cursor: page.NextCursor})
	if err != nil || len(next.Items) != 1 || next.Items[0].MediaID != jpg.MediaID {
		t.Fatal("media page boundary", err)
	}
	fresh, err := a.Media.List(ctx, actor, contract.MediaQuery{After: &page.After})
	if err != nil || len(fresh.Items) != 0 {
		t.Fatal("after watermark included old media", err)
	}
	if err = a.Media.Delete(ctx, actor, png.MediaID); err != nil {
		t.Fatal(err)
	}
	_, _, err = a.Media.File(ctx, actor, png.MediaID)
	status(t, err, 404)
}
func TestWorkspaceAndRetainedCandidateReferencesPreventDelete(t *testing.T) {
	a, actor := setup(t)
	ctx := context.Background()
	record, err := a.Media.Upload(ctx, actor, bytes.NewReader(fixture(t, "valid.png")), "valid.png")
	if err != nil {
		t.Fatal(err)
	}
	create := contract.ProductCreate{Slug: "referenced", Kind: "entry", CategoryID: "other", Visibility: "unlisted", Applications: []string{}, Features: []string{}, Notes: []string{}, LegacySeriesIDs: []string{}, SceneIDs: []contract.SceneID{}, Images: []contract.ProductImage{{MediaID: record.MediaID}}, CoverMediaID: &record.MediaID, PurchaseOptions: []contract.PurchaseOptionEdit{}, GuideProfile: contract.GuideProfileEdit{SceneIDs: []contract.SceneID{}, Materials: []string{}, Substrates: []string{}, ExcludedMaterials: []string{}, ExcludedSubstrates: []string{}, Limitations: []string{}, ConsultationNotes: []string{}, RequiredRequirements: []contract.RequirementField{}, Sources: []contract.GuideSource{}}}
	p, err := a.Content.Create(ctx, actor, create)
	if err != nil {
		t.Fatal(err)
	}
	status(t, a.Media.Delete(ctx, actor, record.MediaID), 409)
	// A retained candidate is immutable even after the draft association is removed.
	snapshot := map[string]any{"products": []any{map[string]any{"images": []any{map[string]any{"media_id": record.MediaID}}, "guide_profile": map[string]any{"sources": []any{}}}}}
	b, _ := json.Marshal(snapshot)
	hash := sha256.Sum256(b)
	_, err = a.Store.DB().Exec(`INSERT INTO candidates(id,state,schema_version,workspace_revision,code_version,snapshot_json,snapshot_sha256,diff_json,created_by,created_at) VALUES('retained','failed',1,?,'test',?,?, '{}','admin',?)`, p.WorkspaceRevision, string(b), hex.EncodeToString(hash[:]), time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Content.Delete(ctx, actor, p.Product.ID, p.Version); err != nil {
		t.Fatal(err)
	}
	status(t, a.Media.Delete(ctx, actor, record.MediaID), 409)
	if _, err = a.Store.DB().Exec(`DELETE FROM candidates WHERE id='retained'`); err != nil {
		t.Fatal(err)
	}
	if err = a.Media.Delete(ctx, actor, record.MediaID); err != nil {
		t.Fatal(err)
	}
}

// Active authority fixtures exercise deletion independently of publication's
// producer. Both snapshots are valid shared contracts; only history uses image.
func TestSuccessfulHistoryRetainsPrivateMedia(t *testing.T) {
	a, actor := setup(t)
	ctx := context.Background()
	record, err := a.Media.Upload(ctx, actor, bytes.NewReader(fixture(t, "valid.png")), "valid.png")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "seed", "initial-catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := contract.Decode[contract.CatalogSnapshot]("catalog-snapshot.schema.json#/$defs/CatalogSnapshot", data)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.ReleaseID = "history"
	snapshot.LegacySeries = []contract.LegacySeries{}
	snapshot.Products = snapshot.Products[:1]
	p := &snapshot.Products[0]
	p.LegacySeriesIDs = []string{}
	p.GuideProfile.Sources = []contract.GuideSource{}
	p.Images = []contract.SnapshotProductImage{{MediaID: record.MediaID, SHA256: record.SHA256, Format: record.Format, Bytes: record.Bytes, Width: record.Width, Height: record.Height, Path: record.SHA256 + ".png"}}
	p.CoverMediaID = &record.MediaID
	persist := func(id string, value any) []byte {
		t.Helper()
		b, e := json.Marshal(value)
		if e != nil {
			t.Fatal(e)
		}
		if e = contract.ValidateJSON("catalog-snapshot.schema.json#/$defs/CatalogSnapshot", b); e != nil {
			t.Fatal(e)
		}
		dir := filepath.Join(a.Config.DataDir, "artifacts", id)
		if e = os.MkdirAll(dir, 0700); e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(dir, "catalog.json"), b, 0600); e != nil {
			t.Fatal(e)
		}
		return b
	}
	persist("history", snapshot)
	snapshot.ReleaseID = "current"
	snapshot.Products = []contract.SnapshotProduct{}
	current := persist("current", snapshot)
	hash := sha256.Sum256(current)
	authority := contract.ActiveRelease{SchemaVersion: 1, ReleaseID: "current", SnapshotSHA256: hex.EncodeToString(hash[:]), OperationID: "operation_test", ActivatedAt: time.Now().UTC().Format(time.RFC3339Nano), SuccessfulReleaseIDs: []string{"history", "current"}, PreviouslyPublishedRoutes: []contract.PublishedRoute{}}
	b, err := json.Marshal(authority)
	if err != nil {
		t.Fatal(err)
	}
	if err = contract.ValidateJSON("catalog-snapshot.schema.json#/$defs/ActiveRelease", b); err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Join(a.Config.DataDir, "state"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(a.Config.DataDir, "state", "active.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	status(t, a.Media.Delete(ctx, actor, record.MediaID), 409)
	file, _, err := a.Media.File(ctx, actor, record.MediaID)
	if err != nil {
		t.Fatal(err)
	}
	file.Close()
}
