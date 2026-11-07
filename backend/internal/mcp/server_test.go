package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"yuanfa-official/backend/internal/app"
	"yuanfa-official/backend/internal/auth"
	"yuanfa-official/backend/internal/config"
	"yuanfa-official/backend/internal/contract"
)

type bearerTransport struct {
	secret        string
	origin        []string
	status        int
	sessionHeader string
}

func (b *bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header = r.Header.Clone()
	if b.secret != "" {
		r.Header.Set("Authorization", "Bearer "+b.secret)
	}
	for _, o := range b.origin {
		r.Header.Add("Origin", o)
	}
	response, err := http.DefaultTransport.RoundTrip(r)
	if err == nil {
		b.status = response.StatusCode
		b.sessionHeader = response.Header.Get("Mcp-Session-Id")
	}
	return response, err
}

type fixture struct {
	a       *app.App
	server  *httptest.Server
	browser auth.LoginResult
	ctx     context.Context
}

func setup(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	server := httptest.NewUnstartedServer(nil)
	a, err := app.Open(ctx, config.Config{DataDir: t.TempDir(), SiteURL: "http://" + server.Listener.Addr().String()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close(); a.Close() })
	if err = a.Auth.InitAdmin(ctx, "admin", "mcp-isolated-test-password"); err != nil {
		t.Fatal(err)
	}
	browser, err := a.Auth.Login(ctx, contract.LoginInput{Username: "admin", Password: "mcp-isolated-test-password"})
	if err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = a.Mux
	server.Start()
	return &fixture{a, server, browser, ctx}
}
func (f *fixture) token(t *testing.T, scopes ...contract.TokenScope) contract.TokenIssued {
	t.Helper()
	out, err := f.a.Auth.IssueToken(f.ctx, f.browser.Session.Actor, contract.TokenInput{Name: "SDK test", ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano), Scopes: scopes})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func (f *fixture) connect(secret string, origin ...string) (*sdk.ClientSession, *bearerTransport, error) {
	tr := &bearerTransport{secret: secret, origin: origin}
	client := sdk.NewClient(&sdk.Implementation{Name: "boundary-test", Version: "1"}, nil)
	session, err := client.Connect(f.ctx, &sdk.StreamableClientTransport{Endpoint: f.server.URL + "/mcp", HTTPClient: &http.Client{Transport: tr}, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	return session, tr, err
}
func call[T any](t *testing.T, s *sdk.ClientSession, name string, in any) T {
	t.Helper()
	result, err := s.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: in})
	if err != nil {
		t.Fatalf("%s protocol failure: %v", name, err)
	}
	if result.IsError {
		t.Fatalf("%s unexpected business failure", name)
	}
	b, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var out T
	if err = json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}
func business(t *testing.T, s *sdk.ClientSession, name string, in any, code string) contract.ErrorDetail {
	t.Helper()
	result, err := s.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: in})
	if err != nil {
		t.Fatalf("expected business result, got protocol error: %v", err)
	}
	if !result.IsError {
		t.Fatal("business failure reported success")
	}
	b, _ := json.Marshal(result.StructuredContent)
	response, err := contract.Decode[contract.ErrorResponse]("admin.schema.json#/$defs/ErrorResponse", b)
	if err != nil {
		t.Fatal(err)
	}
	if response.Error.Code != code {
		t.Fatalf("want %s got %s", code, response.Error.Code)
	}
	return response.Error
}
func draft(slug string) contract.ProductCreate {
	return contract.ProductCreate{Slug: slug, Kind: "tool", CategoryID: "straight", Visibility: "unlisted", LegacySeriesIDs: []string{}, Applications: []string{}, Features: []string{}, Notes: []string{}, SceneIDs: []contract.SceneID{}, Images: []contract.ProductImage{}, PurchaseOptions: []contract.PurchaseOptionEdit{}, GuideProfile: contract.GuideProfileEdit{SceneIDs: []contract.SceneID{}, Materials: []string{}, Substrates: []string{}, ExcludedMaterials: []string{}, ExcludedSubstrates: []string{}, Limitations: []string{}, ConsultationNotes: []string{}, RequiredRequirements: []contract.RequirementField{}, Sources: []contract.GuideSource{}}}
}
func editing(input contract.ProductCreate) contract.ProductEdit {
	return contract.ProductEdit{Kind: input.Kind, Name: input.Name, Model: input.Model, CategoryID: input.CategoryID, Series: input.Series, LegacySeriesIDs: input.LegacySeriesIDs, Summary: input.Summary, Applications: input.Applications, Features: input.Features, Notes: input.Notes, SceneIDs: input.SceneIDs, NailType: input.NailType, FastenerText: input.FastenerText, LongNose: input.LongNose, Blower: input.Blower, Images: input.Images, CoverMediaID: input.CoverMediaID, Visibility: input.Visibility, SortOrder: input.SortOrder, GuideProfile: input.GuideProfile, PurchaseOptions: input.PurchaseOptions}
}

func TestCredentialsRecheckedOnInitializedClient(t *testing.T) {
	f := setup(t)
	for _, secret := range []string{"", "wrong"} {
		s, tr, err := f.connect(secret)
		if s != nil {
			s.Close()
		}
		if err == nil || tr.status != 401 {
			t.Fatal("missing/incorrect credential accepted")
		}
	}
	for _, kind := range []string{"revoked", "expired"} {
		t.Run(kind, func(t *testing.T) {
			token := f.token(t, "products:read")
			s, tr, err := f.connect(token.Secret)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			call[contract.AdminProductList](t, s, "search_products", map[string]any{})
			if tr.sessionHeader != "" {
				t.Fatal("stateless server issued a session dependency")
			}
			if kind == "revoked" {
				err = f.a.Auth.RevokeToken(f.ctx, f.browser.Session.Actor, token.Token.ID)
			} else {
				_, err = f.a.Store.DB().Exec(`UPDATE mcp_tokens SET expires_at='2000-01-01T00:00:00Z' WHERE id=?`, token.Token.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.CallTool(f.ctx, &sdk.CallToolParams{Name: "search_products", Arguments: map[string]any{}}); err == nil || tr.status != 401 {
				t.Fatal("initialized client retained authority")
			}
			fresh, next, err := f.connect(token.Secret)
			if fresh != nil {
				fresh.Close()
			}
			if err == nil || next.status != 401 {
				t.Fatal("invalid credential initialized a new client")
			}
		})
	}
	token := f.token(t, "products:read")
	for _, origins := range [][]string{{"http://evil.invalid"}, {f.server.URL, f.server.URL}, {"null"}} {
		s, tr, err := f.connect(token.Secret, origins...)
		if s != nil {
			s.Close()
		}
		if err == nil || tr.status != 403 {
			t.Fatal("invalid Origin authorized")
		}
	}
	s, _, err := f.connect(token.Secret, f.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
}

func TestHTTPAndMCPConcurrentEditsPreserveWinningDocument(t *testing.T) {
	f := setup(t)
	token := f.token(t, "products:read", "products:write")
	s, _, err := f.connect(token.Secret)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	input := draft("concurrent")
	p := call[contract.ProductEnvelope](t, s, "create_product", input)
	browserEdit, mcpEdit := editing(input), editing(input)
	browserEdit.Summary = "browser edit"
	mcpEdit.Summary = "MCP edit"
	body, _ := json.Marshal(browserEdit)
	req, _ := http.NewRequest("PUT", f.server.URL+"/api/admin/products/"+p.Product.ID, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", f.server.URL)
	req.Header.Set("X-CSRF-Token", f.browser.Session.CSRFToken)
	req.Header.Set("If-Match", `"`+strconv.FormatInt(p.Version, 10)+`"`)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: f.browser.Secret})
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	var response *http.Response
	var httpErr, errorMCP error
	var result *sdk.CallToolResult
	go func() { defer wg.Done(); <-start; response, httpErr = http.DefaultClient.Do(req) }()
	go func() {
		defer wg.Done()
		<-start
		result, errorMCP = s.CallTool(f.ctx, &sdk.CallToolParams{Name: "update_product", Arguments: contract.MCPUpdateProductInput{ProductID: p.Product.ID, ExpectedVersion: p.Version, Edit: mcpEdit}})
	}()
	close(start)
	wg.Wait()
	if httpErr != nil || errorMCP != nil {
		t.Fatal("network edit failed")
	}
	defer response.Body.Close()
	current := call[contract.ProductEnvelope](t, s, "get_product", contract.MCPGetProductInput{ProductID: p.Product.ID})
	if response.StatusCode == 200 {
		if !result.IsError || current.Product.Summary != browserEdit.Summary {
			t.Fatal("stale MCP overwrote browser")
		}
		b, _ := json.Marshal(result.StructuredContent)
		var failure contract.ErrorResponse
		json.Unmarshal(b, &failure)
		if failure.Error.Code != "VERSION_CONFLICT" || failure.Error.CurrentVersion == nil || *failure.Error.CurrentVersion != current.Version {
			t.Fatal("missing MCP conflict version")
		}
	} else if response.StatusCode == 412 {
		if result.IsError || current.Product.Summary != mcpEdit.Summary {
			t.Fatal("stale browser overwrote MCP")
		}
		var failure contract.ErrorResponse
		if err = json.NewDecoder(response.Body).Decode(&failure); err != nil {
			t.Fatal(err)
		}
		if failure.Error.Code != "VERSION_CONFLICT" || failure.Error.CurrentVersion == nil || *failure.Error.CurrentVersion != current.Version {
			t.Fatal("missing HTTP conflict version")
		}
	} else {
		t.Fatalf("unexpected HTTP status %d", response.StatusCode)
	}
	if current.Version != p.Version+1 || current.WorkspaceRevision != p.WorkspaceRevision+1 {
		t.Fatal("both concurrent edits committed")
	}
}

func TestSchemaAndScopeCannotBypassBrowserAuthority(t *testing.T) {
	f := setup(t)
	token := f.token(t, "products:read")
	s, _, err := f.connect(token.Secret)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	business(t, s, "create_product", draft("forbidden"), "FORBIDDEN")
	business(t, s, "search_products", map[string]any{"path": "/Users/client/image.png"}, "VALIDATION_FAILED")
	business(t, s, "get_product", map[string]any{"product_id": nil}, "VALIDATION_FAILED")
	for _, name := range []string{"confirm_publication", "guide_review", "issue_token"} {
		if _, err = s.CallTool(f.ctx, &sdk.CallToolParams{Name: name, Arguments: map[string]any{}}); err == nil {
			t.Fatal("browser-only tool callable")
		}
	}
	for _, path := range []string{"/api/admin/publication-requests/unknown/confirm", "/api/admin/products/unknown/guide-review", "/api/admin/mcp-tokens"} {
		req, _ := http.NewRequest("POST", f.server.URL+path, bytes.NewBufferString(`{}`))
		req.Header.Set("Authorization", "Bearer "+token.Secret)
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 403 {
			t.Fatal("Bearer bypassed browser boundary")
		}
	}
	full := f.token(t, "products:read", "products:write")
	writer, _, err := f.connect(full.Secret)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	p := call[contract.ProductEnvelope](t, writer, "create_product", draft("server-fields"))
	business(t, writer, "update_product", map[string]any{"product_id": p.Product.ID, "edit": editing(draft("ignored"))}, "VALIDATION_FAILED")
	b, _ := json.Marshal(editing(draft("ignored")))
	var edit map[string]any
	json.Unmarshal(b, &edit)
	edit["guide_profile"].(map[string]any)["reviews"] = []any{}
	business(t, writer, "update_product", map[string]any{"product_id": p.Product.ID, "expected_version": p.Version, "edit": edit}, "VALIDATION_FAILED")
	current := call[contract.ProductEnvelope](t, writer, "get_product", contract.MCPGetProductInput{ProductID: p.Product.ID})
	if current.Version != p.Version {
		t.Fatal("invalid update changed version")
	}
}
