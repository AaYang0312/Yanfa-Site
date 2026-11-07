package auth_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"yuanfa-official/backend/internal/auth"
	"yuanfa-official/backend/internal/contract"
	"yuanfa-official/backend/internal/coordination"
	"yuanfa-official/backend/internal/httpapi"
	"yuanfa-official/backend/internal/storage"
)

func setup(t *testing.T) (*auth.Service, *sql.DB, auth.LoginResult) {
	t.Helper()
	ctx := context.Background()
	lock, err := coordination.AcquireDataDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lock.Close() })
	store, err := storage.Open(ctx, lock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	service, err := auth.New(store.DB())
	if err != nil {
		t.Fatal(err)
	}
	// Test-only credential, never a deployment default.
	password := "isolated-test-password"
	if err = service.InitAdmin(ctx, "admin", password); err != nil {
		t.Fatal(err)
	}
	if err = service.InitAdmin(ctx, "second", password); err == nil {
		t.Fatal("second administrator accepted")
	}
	if _, err = service.Login(ctx, contract.LoginInput{Username: "missing", Password: password}); err == nil {
		t.Fatal("unknown username logged in")
	}
	login, err := service.Login(ctx, contract.LoginInput{Username: "admin", Password: password})
	if err != nil {
		t.Fatal(err)
	}
	return service, store.DB(), login
}
func TestSessionExpiryAndRevocation(t *testing.T) {
	service, db, login := setup(t)
	ctx := context.Background()
	expiry, err := time.Parse(time.RFC3339Nano, login.Session.ExpiresAt)
	if err != nil {
		t.Fatal(err)
	}
	if remaining := time.Until(expiry); remaining > 12*time.Hour || remaining < 12*time.Hour-time.Minute {
		t.Fatalf("unexpected session lifetime: %v", remaining)
	}
	if err = login.Session.CheckCSRF("wrong"); err == nil {
		t.Fatal("wrong CSRF accepted")
	}
	if err = login.Session.CheckCSRF(login.Session.CSRFToken); err != nil {
		t.Fatal(err)
	}
	if err = service.Logout(ctx, login.Session.Actor); err != nil {
		t.Fatal(err)
	}
	if _, err = service.AuthenticateSession(ctx, login.Secret); err == nil {
		t.Fatal("revoked session accepted")
	}
	if err = service.Authorize(ctx, login.Session.Actor, ""); err == nil {
		t.Fatal("retained session actor survived logout")
	}
	if _, err = db.Exec(`UPDATE sessions SET revoked_at=NULL,expires_at=?`, time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err = service.AuthenticateSession(ctx, login.Secret); err == nil {
		t.Fatal("expired session accepted")
	}
}
func TestBearerHTTPScopesExpiryRevocationAndOrigins(t *testing.T) {
	service, db, login := setup(t)
	ctx := context.Background()
	issued, err := service.IssueToken(ctx, login.Session.Actor, contract.TokenInput{Name: "consumer", ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano), Scopes: []contract.TokenScope{"products:read"}})
	if err != nil {
		t.Fatal(err)
	}
	actor, err := service.AuthenticateToken(ctx, issued.Secret)
	if err != nil {
		t.Fatal(err)
	}
	if err = actor.RequireBrowser(); err == nil {
		t.Fatal("token became browser administrator")
	}
	if err = actor.Require("products:write"); err == nil {
		t.Fatal("read-only token obtained write scope")
	}
	if _, err = service.IssueToken(ctx, actor, contract.TokenInput{}); err == nil {
		t.Fatal("token issued another token")
	}
	var hash string
	if err = db.QueryRow(`SELECT secret_hash FROM mcp_tokens WHERE id=?`, issued.Token.ID).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if len(hash) != 64 || hash == issued.Secret {
		t.Fatal("token secret stored instead of digest")
	}
	list, err := service.ListTokens(ctx, login.Session.Actor)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(list)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), issued.Secret) || strings.Contains(string(encoded), hash) {
		t.Fatal("token list leaked secret/digest")
	}
	middleware := httpapi.AuthMiddleware{Auth: service, Origin: "https://admin.example", SecureCookies: true}
	server := httptest.NewServer(middleware.Bearer(func(w http.ResponseWriter, r *http.Request, a auth.Actor) {
		if err := service.Authorize(r.Context(), a, "products:read"); err != nil {
			httpapi.WriteError(w, err)
			return
		}
		w.WriteHeader(204)
	}))
	defer server.Close()
	request := func(secret, origin string, want int) {
		t.Helper()
		req, err := http.NewRequest("POST", server.URL, nil)
		if err != nil {
			t.Fatal(err)
		}
		if secret != "" {
			req.Header.Set("Authorization", "Bearer "+secret)
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		response, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != want {
			t.Fatalf("bearer HTTP status=%d want=%d", response.StatusCode, want)
		}
	}
	request("", "", 401)
	request("t_invalid", "", 401)
	request(issued.Secret, "https://admin.example.evil", 403)
	request(issued.Secret, "https://admin.example", 204)
	request(issued.Secret, "", 204)
	if err = service.RevokeToken(ctx, login.Session.Actor, issued.Token.ID); err != nil {
		t.Fatal(err)
	}
	request(issued.Secret, "", 401)
	if err = service.Authorize(ctx, actor, "products:read"); err == nil {
		t.Fatal("retained actor survived immediate revocation")
	}
	if _, err = db.Exec(`UPDATE mcp_tokens SET revoked_at=NULL,expires_at=? WHERE id=?`, time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano), issued.Token.ID); err != nil {
		t.Fatal(err)
	}
	request(issued.Secret, "", 401)
	t.Log("real TCP HTTP + SQLite: missing/bad/foreign-origin/revoked/expired Bearer rejected; retained actor rechecked")
}
