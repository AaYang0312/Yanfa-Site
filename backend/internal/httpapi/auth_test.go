package httpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"yuanfa-official/backend/internal/auth"
	"yuanfa-official/backend/internal/contract"
	"yuanfa-official/backend/internal/coordination"
	"yuanfa-official/backend/internal/httpapi"
	"yuanfa-official/backend/internal/storage"
)

func TestBrowserWriteBoundaryAndSecureCookie(t *testing.T) {
	ctx := context.Background()
	lock, err := coordination.AcquireDataDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	store, err := storage.Open(ctx, lock)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	identity, err := auth.New(store.DB())
	if err != nil {
		t.Fatal(err)
	}
	if err = identity.InitAdmin(ctx, "admin", "isolated-http-password"); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	httpapi.RegisterAuth(mux, httpapi.AuthMiddleware{Auth: identity, Origin: "https://admin.example", SecureCookies: true}, func(*http.Request) (int64, *string, error) { return 0, nil, nil })
	request := func(path, method, body string, origin []string, cookie *http.Cookie, csrf string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		for _, value := range origin {
			req.Header.Add("Origin", value)
		}
		if cookie != nil {
			req.AddCookie(cookie)
		}
		if csrf != "" {
			req.Header.Set("X-CSRF-Token", csrf)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	loginBody := `{"username":"admin","password":"isolated-http-password"}`
	if got := request("/api/admin/login", "POST", loginBody, []string{"https://admin.example", "https://admin.example"}, nil, "").Code; got != 403 {
		t.Fatalf("duplicate Origin accepted: %d", got)
	}
	login := request("/api/admin/login", "POST", loginBody, []string{"https://admin.example"}, nil, "")
	if login.Code != 200 {
		t.Fatalf("login status=%d", login.Code)
	}
	cookies := login.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal("missing session cookie")
	}
	cookie := cookies[0]
	if !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.MaxAge != 43200 {
		t.Fatal("unsafe deployed cookie")
	}
	response, err := contract.Decode[contract.SessionResponse]("admin.schema.json#/$defs/SessionResponse", login.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		origin []string
		csrf   string
	}{
		{nil, response.CSRFToken}, {[]string{"https://admin.example.evil"}, response.CSRFToken}, {[]string{"https://admin.example"}, "wrong"},
	} {
		if got := request("/api/admin/logout", "POST", "", tc.origin, cookie, tc.csrf).Code; got != 403 {
			t.Fatalf("invalid browser write accepted: %d", got)
		}
	}
	if got := request("/api/admin/logout", "POST", "", []string{"https://admin.example"}, cookie, response.CSRFToken).Code; got != 204 {
		t.Fatalf("valid logout failed: %d", got)
	}
	if got := request("/api/admin/session", "GET", "", nil, cookie, "").Code; got != 401 {
		t.Fatalf("revoked cookie accepted: %d", got)
	}
}
