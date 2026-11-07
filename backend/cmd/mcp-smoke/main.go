// Command mcp-smoke exercises the real stateless SDK endpoint and local browser
// approval/upload flows on an isolated development instance. It mutates drafts
// and publishes twice; it is not customer WorkBuddy acceptance.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"yuanfa-official/backend/internal/contract"
	"yuanfa-official/schemas"
)

//go:embed browser.mjs
var browserScript string

var allScopes = []contract.TokenScope{"products:read", "products:write", "media:read", "candidates:write", "publications:write"}

type event struct {
	Method        string `json:"method"`
	Status        int    `json:"status"`
	SessionHeader bool   `json:"session_header"`
}
type transport struct {
	secret string
	origin string
	mu     sync.Mutex
	events []event
}

func (t *transport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header = r.Header.Clone()
	t.mu.Lock()
	secret := t.secret
	t.mu.Unlock()
	if secret != "" {
		r.Header.Set("Authorization", "Bearer "+secret)
	}
	if t.origin != "" {
		r.Header.Set("Origin", t.origin)
	}
	response, err := http.DefaultTransport.RoundTrip(r)
	if err == nil {
		t.mu.Lock()
		t.events = append(t.events, event{r.Method, response.StatusCode, response.Header.Get("Mcp-Session-Id") != ""})
		t.mu.Unlock()
	}
	return response, err
}
func (t *transport) lastStatus() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.events) == 0 {
		return 0
	}
	return t.events[len(t.events)-1].Status
}

type smoke struct {
	ctx                       context.Context
	base, out, password, csrf string
	admin                     *http.Client
	session                   *sdk.ClientSession
	trace                     *transport
	reader                    *sdk.ClientSession
	readerTrace               *transport
	tools                     map[string]*sdk.Tool
	outputSchemas             map[string]string
	report                    map[string]any
	checks                    []string
	calls                     []map[string]any
	issued                    []contract.TokenIssued
	phase                     string
}

func require(ok bool, label string) {
	if !ok {
		panic(errors.New(label))
	}
}
func must(err error, label string) { require(err == nil, label) }
func privateFile(name string) string {
	file, err := os.Open(name)
	must(err, "protected input unavailable")
	defer file.Close()
	stat, err := file.Stat()
	must(err, "protected input stat failed")
	require(stat.Mode().IsRegular() && stat.Mode().Perm()&0077 == 0, "protected input must be a private regular file")
	b, err := io.ReadAll(io.LimitReader(file, 8193))
	must(err, "protected input unreadable")
	require(len(b) <= 8192, "protected input too large")
	return strings.TrimSuffix(strings.TrimSuffix(string(b), "\n"), "\r")
}
func (s *smoke) pass(label string) { s.checks = append(s.checks, label); fmt.Println("PASS " + label) }
func (s *smoke) save(name string, value any) {
	b, err := json.MarshalIndent(value, "", "  ")
	must(err, "evidence encoding failed")
	must(os.WriteFile(filepath.Join(s.out, name), append(b, '\n'), 0600), "evidence write failed")
}
func (s *smoke) request(method, path string, body any, headers map[string]string) (int, []byte) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		must(err, "HTTP input encoding failed")
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(s.ctx, method, s.base+path, reader)
	must(err, "HTTP request creation failed")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if method != "GET" {
		req.Header.Set("Origin", s.base)
		if s.csrf != "" {
			req.Header.Set("X-CSRF-Token", s.csrf)
		}
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	response, err := s.admin.Do(req)
	must(err, "HTTP request failed")
	defer response.Body.Close()
	b, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	must(err, "HTTP response read failed")
	return response.StatusCode, b
}
func decode[T any](b []byte) T {
	var out T
	must(json.Unmarshal(b, &out), "response decoding failed")
	return out
}
func (s *smoke) issue(scopes []contract.TokenScope, expiry time.Time) contract.TokenIssued {
	status, b := s.request("POST", "/api/admin/mcp-tokens", contract.TokenInput{Name: "local SDK smoke " + rand.Text(), ExpiresAt: expiry.UTC().Format(time.RFC3339Nano), Scopes: scopes}, nil)
	require(status == 201, "browser token issue failed")
	issued := decode[contract.TokenIssued](b)
	s.issued = append(s.issued, issued)
	return issued
}
func (s *smoke) connect(secret, origin string) (*sdk.ClientSession, *transport, error) {
	tr := &transport{secret: secret, origin: origin}
	client := sdk.NewClient(&sdk.Implementation{Name: "yuanfa-local-smoke", Version: "1"}, nil)
	session, err := client.Connect(s.ctx, &sdk.StreamableClientTransport{Endpoint: s.base + "/mcp", HTTPClient: &http.Client{Transport: tr, Timeout: 30 * time.Second}, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	return session, tr, err
}
func call[T any](s *smoke, name string, in any) T {
	return callUsing[T](s, s.session, "management", name, in)
}
func callUsing[T any](s *smoke, session *sdk.ClientSession, principal, name string, in any) T {
	result, err := session.CallTool(s.ctx, &sdk.CallToolParams{Name: name, Arguments: in})
	must(err, name+" protocol failure")
	if result.IsError {
		b, _ := json.Marshal(result.StructuredContent)
		failure := decode[contract.ErrorResponse](b)
		panic(errors.New(name + ": " + failure.Error.Code))
	}
	encoded, err := json.Marshal(result.StructuredContent)
	must(err, "tool response encoding failed")
	must(contract.ValidateJSON(s.outputSchemas[name], encoded), name+" output violates shared schema")
	s.calls = append(s.calls, map[string]any{"tool": name, "principal": principal, "isError": false})
	return decode[T](encoded)
}
func (s *smoke) failure(name string, in any, code string) contract.ErrorDetail {
	return failureUsing(s, s.session, "management", name, in, code)
}
func failureUsing(s *smoke, session *sdk.ClientSession, principal, name string, in any, code string) contract.ErrorDetail {
	result, err := session.CallTool(s.ctx, &sdk.CallToolParams{Name: name, Arguments: in})
	must(err, "expected structured business failure")
	require(result.IsError, "failure disguised as success")
	b, _ := json.Marshal(result.StructuredContent)
	must(contract.ValidateJSON("admin.schema.json#/$defs/ErrorResponse", b), "unstructured tool error")
	failure := decode[contract.ErrorResponse](b).Error
	require(failure.Code == code, "unexpected structured error code: "+failure.Code)
	s.calls = append(s.calls, map[string]any{"tool": name, "principal": principal, "isError": true, "code": failure.Code, "current_version": failure.CurrentVersion})
	return failure
}
func (s *smoke) browser(input map[string]any) map[string]any {
	input["password"] = s.password
	input["base"] = s.base
	b, err := json.Marshal(input)
	must(err, "browser input encoding failed")
	command := exec.CommandContext(s.ctx, "node", "--input-type=module", "-e", browserScript)
	command.Stdin = bytes.NewReader(b)
	// Go uses absolute private TMPDIR; only Chromium uses this short Linux path.
	command.Env = append(os.Environ(), "TMPDIR=/proc/self/cwd/.work/browser-tmp")
	var out, diagnostic bytes.Buffer
	command.Stdout = &out
	command.Stderr = &diagnostic
	err = command.Run()
	must(err, "native browser action failed")
	return decode[map[string]any](out.Bytes())
}
func edit(p contract.ProductDocument) contract.ProductEdit {
	profile := p.GuideProfile
	options := make([]contract.PurchaseOptionEdit, len(p.PurchaseOptions))
	for i, o := range p.PurchaseOptions {
		options[i] = contract.PurchaseOptionEdit{ID: new(o.ID), Platform: o.Platform, StoreName: o.StoreName, URL: o.URL, MatchScope: o.MatchScope, SkuHint: o.SkuHint, Enabled: o.Enabled}
	}
	return contract.ProductEdit{Kind: p.Kind, Name: p.Name, Model: p.Model, CategoryID: p.CategoryID, Series: p.Series, LegacySeriesIDs: p.LegacySeriesIDs, Summary: p.Summary, Applications: p.Applications, Features: p.Features, Notes: p.Notes, SceneIDs: p.SceneIDs, NailType: p.NailType, FastenerText: p.FastenerText, LongNose: p.LongNose, Blower: p.Blower, Images: p.Images, CoverMediaID: p.CoverMediaID, Visibility: p.Visibility, SortOrder: p.SortOrder, PurchaseOptions: options, GuideProfile: contract.GuideProfileEdit{SceneIDs: profile.SceneIDs, Materials: profile.Materials, Substrates: profile.Substrates, ExcludedMaterials: profile.ExcludedMaterials, ExcludedSubstrates: profile.ExcludedSubstrates, PowerSource: profile.PowerSource, NailType: profile.NailType, FastenerLengthMM: profile.FastenerLengthMM, LoadBearing: profile.LoadBearing, LongNose: profile.LongNose, Blower: profile.Blower, Limitations: profile.Limitations, ConsultationNotes: profile.ConsultationNotes, RequiredRequirements: profile.RequiredRequirements, Sources: profile.Sources}}
}
func newDraft(slug string) contract.ProductCreate {
	return contract.ProductCreate{Slug: slug, Kind: "tool", Name: "SDK smoke disposable draft", CategoryID: "straight", Visibility: "unlisted", LegacySeriesIDs: []string{}, Applications: []string{}, Features: []string{}, Notes: []string{}, SceneIDs: []contract.SceneID{}, Images: []contract.ProductImage{}, PurchaseOptions: []contract.PurchaseOptionEdit{}, GuideProfile: contract.GuideProfileEdit{SceneIDs: []contract.SceneID{}, Materials: []string{}, Substrates: []string{}, ExcludedMaterials: []string{}, ExcludedSubstrates: []string{}, Limitations: []string{}, ConsultationNotes: []string{}, RequiredRequirements: []contract.RequirementField{}, Sources: []contract.GuideSource{}}}
}
func (s *smoke) state() contract.SessionResponse {
	status, b := s.request("GET", "/api/admin/session", nil, nil)
	require(status == 200, "session state unavailable")
	return decode[contract.SessionResponse](b)
}
func (s *smoke) publish(label, productID string) contract.PublicationRequest {
	state := s.state()
	in := contract.MCPCreateCandidateInput{ExpectedWorkspaceRevision: state.WorkspaceRevision, RequestKey: rand.Text()}
	c := call[contract.Candidate](s, "create_candidate", in)
	repeated := call[contract.Candidate](s, "create_candidate", in)
	require(repeated.ID == c.ID, "candidate idempotency lost")
	conflict := in
	conflict.ExpectedWorkspaceRevision++
	s.failure("create_candidate", conflict, "IDEMPOTENCY_CONFLICT")
	deadline := time.Now().Add(330 * time.Second)
	for c.State != "ready" && c.State != "failed" {
		require(time.Now().Before(deadline), "candidate polling timeout")
		time.Sleep(200 * time.Millisecond)
		c = call[contract.Candidate](s, "get_candidate", contract.MCPGetCandidateInput{CandidateID: c.ID})
	}
	require(c.State == "ready", "real candidate build failed")
	s.save(label+"-candidate.json", c)
	request := contract.MCPRequestPublicationInput{Kind: "publish", CandidateID: &c.ID, SnapshotSHA256: c.SnapshotSHA256, ExpectedActiveReleaseID: c.ExpectedActiveReleaseID, ExpectedWorkspaceRevision: c.WorkspaceRevision, RequestKey: rand.Text()}
	p := call[contract.PublicationRequest](s, "request_publication", request)
	repeatedP := call[contract.PublicationRequest](s, "request_publication", request)
	require(repeatedP.ID == p.ID, "publication idempotency lost")
	require(p.State == "awaiting_confirmation", "intent prematurely activated")
	before := s.state()
	require(equalID(before.ActiveReleaseID, state.ActiveReleaseID), "intent changed active release")
	s.checkReaderPending(label, productID, c, p)
	// Navigate the exact service-returned link; no reconstructed dynamic route.
	proof := s.browser(map[string]any{"action": "confirm", "url": p.ReviewURL, "out": filepath.Join(s.out, label), "request_id": p.ID, "sha256": p.SnapshotSHA256, "expected_active": p.ExpectedActiveReleaseID, "revision": p.ExpectedWorkspaceRevision, "release_id": c.ID})
	s.save(label+"-browser.json", proof)
	p = call[contract.PublicationRequest](s, "get_publication", contract.MCPGetPublicationInput{RequestID: p.ID})
	require(p.State == "succeeded" && p.ResultReleaseID != nil && *p.ResultReleaseID == c.ID && p.CompletedAt != nil && p.SiteURL != nil, "confirmed publication has no real success result")
	require(equalID(s.state().ActiveReleaseID, p.ResultReleaseID), "published authority mismatch")
	s.checkReaderSucceeded(label, c, p)
	s.save(label+"-publication.json", p)
	s.pass(label + " MCP candidate, original browser review/confirm and SDK succeeded result")
	return p
}
func equalID(a, b *string) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }

func main() {
	base := flag.String("base", "", "isolated development origin; this smoke modifies and publishes content")
	out := flag.String("out", "", "redacted evidence directory")
	flag.Parse()
	if *base == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "--base and --out required")
		os.Exit(2)
	}
	code := run(strings.TrimRight(*base, "/"), *out)
	os.Exit(code)
}
func run(base, out string) (exit int) {
	parsed, err := url.Parse(base)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" || (parsed.Scheme != "https" && (parsed.Scheme != "http" || (parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "localhost" && parsed.Hostname() != "::1"))) {
		fmt.Fprintln(os.Stderr, "HTTPS or loopback HTTP origin required")
		return 2
	}
	out, err = filepath.Abs(out)
	if err != nil {
		return 2
	}
	if err = os.MkdirAll(out, 0700); err != nil {
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	jar, _ := cookiejar.New(nil)
	s := &smoke{ctx: ctx, base: base, out: out, admin: &http.Client{Jar: jar, Timeout: 30 * time.Second}, report: map[string]any{"success": false, "sdk": "github.com/modelcontextprotocol/go-sdk v1.8.0", "client": "local SDK smoke; not customer WorkBuddy"}, phase: "protected login"}
	defer func() {
		if recovered := recover(); recovered != nil {
			exit = 1
			s.report["failed_phase"] = s.phase
			if e, ok := recovered.(error); ok {
				s.report["failure"] = e.Error()
			} else {
				s.report["failure"] = "smoke invariant failed"
			}
			fmt.Fprintln(os.Stderr, "FAIL "+s.phase)
		}
		if s.session != nil {
			s.session.Close()
		}
		if s.reader != nil {
			s.reader.Close()
		}
		// Every issued credential is revoked with the browser session even on error.
		revoked := 0
		for _, token := range s.issued {
			status, _ := s.request("DELETE", "/api/admin/mcp-tokens/"+token.Token.ID, nil, nil)
			if status == 204 {
				revoked++
			}
		}
		s.report["issued_tokens"] = len(s.issued)
		s.report["revoked_tokens"] = revoked
		s.report["checks"] = s.checks
		s.report["tool_calls"] = s.calls
		if s.trace != nil {
			s.trace.mu.Lock()
			s.report["mcp_http"] = append([]event(nil), s.trace.events...)
			s.trace.mu.Unlock()
		}
		if s.readerTrace != nil {
			s.readerTrace.mu.Lock()
			s.report["reader_mcp_http"] = append([]event(nil), s.readerTrace.events...)
			s.readerTrace.mu.Unlock()
		}
		s.save("mcp-smoke.json", s.report)
	}()
	must(os.MkdirAll(filepath.Join(".work", "browser-tmp"), 0700), "private browser temp creation failed")
	s.password = privateFile(os.Getenv("SMOKE_ADMIN_PASSWORD_FILE"))
	status, b := s.request("POST", "/api/admin/login", contract.LoginInput{Username: "admin", Password: s.password}, nil)
	require(status == 200, "browser login failed")
	s.csrf = decode[contract.SessionResponse](b).CSRFToken
	token := s.issue(allScopes, time.Now().Add(time.Hour))
	s.phase = "SDK initialize/list"
	s.session, s.trace, err = s.connect(token.Secret, "")
	must(err, "SDK initialization failed")
	s.report["initialize"] = s.session.InitializeResult()
	listed, err := s.session.ListTools(ctx, nil)
	must(err, "SDK tools/list failed")
	s.tools = map[string]*sdk.Tool{}
	for _, tool := range listed.Tools {
		s.tools[tool.Name] = tool
		require(tool.InputSchema != nil && tool.OutputSchema != nil, "tool schema missing")
	}
	s.outputSchemas = map[string]string{}
	sharedManifest, err := schemas.Files.ReadFile("mcp-tools.schema.json")
	must(err, "tool manifest unavailable")
	var manifest struct {
		Tools []struct {
			Name   string `json:"name"`
			Output struct {
				Ref string `json:"$ref"`
			} `json:"outputSchema"`
		} `json:"x-tools"`
	}
	must(json.Unmarshal(sharedManifest, &manifest), "tool manifest invalid")
	for _, tool := range manifest.Tools {
		s.outputSchemas[tool.Name] = "mcp-tools.schema.json" + tool.Output.Ref
		require(s.tools[tool.Name] != nil, "shared management tool absent")
	}
	require(len(s.tools) == 11, "unexpected management tools")
	for _, forbidden := range []string{"confirm_publication", "guide_review", "issue_token"} {
		require(s.tools[forbidden] == nil, "browser-only tool exposed")
	}
	s.save("tools.json", listed)
	s.pass("official SDK initialize and tools/list with self-contained shared schemas")
	readOnly := s.issue([]contract.TokenScope{"products:read"}, time.Now().Add(time.Hour))
	require(len(readOnly.Token.Scopes) == 1 && readOnly.Token.Scopes[0] == "products:read", "least reader Token scopes changed")
	s.reader, s.readerTrace, err = s.connect(readOnly.Secret, "")
	must(err, "products:read SDK initialization failed")
	s.report["reader_initialize"] = s.reader.InitializeResult()
	s.report["reader_scopes"] = readOnly.Token.Scopes
	s.phase = "credential and Origin boundaries"
	for _, secret := range []string{"", "wrong"} {
		client, tr, err := s.connect(secret, "")
		if client != nil {
			client.Close()
		}
		require(err != nil && tr.lastStatus() == 401, "missing/incorrect token accepted")
	}
	var headerDenials []map[string]any
	for _, secret := range []string{"", "wrong"} {
		initialized, trace, err := s.connect(token.Secret, "")
		must(err, "existing-client credential probe initialization failed")
		_, err = initialized.ListTools(ctx, nil)
		must(err, "existing-client credential probe list failed")
		trace.mu.Lock()
		trace.secret = secret
		trace.mu.Unlock()
		_, err = initialized.CallTool(ctx, &sdk.CallToolParams{Name: "search_products", Arguments: map[string]any{}})
		require(err != nil && trace.lastStatus() == 401, "existing missing/incorrect client remained authorized")
		initialized.Close()
		kind := "wrong"
		if secret == "" {
			kind = "missing"
		}
		headerDenials = append(headerDenials, map[string]any{"credential": kind, "status": trace.lastStatus(), "call_denied": true})
	}
	s.report["existing_client_header_denials"] = headerDenials
	client, tr, err := s.connect(token.Secret, "https://untrusted.invalid")
	if client != nil {
		client.Close()
	}
	require(err != nil && tr.lastStatus() == 403, "wrong Origin accepted")
	client, _, err = s.connect(token.Secret, s.base)
	must(err, "exact Origin rejected")
	client.Close()
	expiring := s.issue(allScopes, time.Now().Add(3*time.Second))
	client, tr, err = s.connect(expiring.Secret, "")
	must(err, "future token could not initialize")
	_, err = client.ListTools(ctx, nil)
	must(err, "future token could not list")
	expiry, _ := time.Parse(time.RFC3339Nano, expiring.Token.ExpiresAt)
	time.Sleep(time.Until(expiry) + 100*time.Millisecond)
	_, err = client.CallTool(ctx, &sdk.CallToolParams{Name: "search_products", Arguments: map[string]any{}})
	require(err != nil && tr.lastStatus() == 401, "existing expired client remained authorized")
	s.report["existing_client_expired_status"] = tr.lastStatus()
	client.Close()
	client, tr, err = s.connect(expiring.Secret, "")
	if client != nil {
		client.Close()
	}
	require(err != nil && tr.lastStatus() == 401, "expired token initialized")
	s.pass("missing/wrong/expired tokens and forbidden Origin rejected, including initialized client")
	s.phase = "workspace CRUD and version conflicts"
	listing := call[contract.AdminProductList](s, "search_products", contract.MCPSearchProductsInput{Limit: new(int64(1)), Visibility: new("listed")})
	require(len(listing.Items) > 0, "seeded listed product required")
	source := call[contract.ProductEnvelope](s, "get_product", contract.MCPGetProductInput{ProductID: listing.Items[0].ID})
	temporary := call[contract.ProductEnvelope](s, "create_product", newDraft("sdk-smoke-"+strings.ToLower(rand.Text())))
	deleted := call[contract.DeletionResult](s, "delete_draft", contract.MCPDeleteDraftInput{ProductID: temporary.Product.ID, ExpectedVersion: temporary.Version})
	require(deleted.DeletedID == temporary.Product.ID, "draft deletion failed")
	s.failure("open_upload", map[string]any{"file_path": "C:\\client\\photo.png"}, "VALIDATION_FAILED")
	s.failure("get_product", map[string]any{"product_id": nil}, "VALIDATION_FAILED")
	original := edit(source.Product)
	browserChange := original
	browserChange.Summary += " · browser smoke"
	status, b = s.request("PUT", "/api/admin/products/"+source.Product.ID, browserChange, map[string]string{"If-Match": fmt.Sprintf(`"%d"`, source.Version)})
	require(status == 200, "browser edit failed")
	current := decode[contract.ProductEnvelope](b)
	stale := s.failure("update_product", contract.MCPUpdateProductInput{ProductID: source.Product.ID, ExpectedVersion: source.Version, Edit: original}, "VERSION_CONFLICT")
	require(stale.CurrentVersion != nil && *stale.CurrentVersion == current.Version, "MCP conflict current_version missing")
	textChange := edit(current.Product)
	textChange.Summary += " · MCP SDK 文字修改"
	changed := call[contract.ProductEnvelope](s, "update_product", contract.MCPUpdateProductInput{ProductID: source.Product.ID, ExpectedVersion: current.Version, Edit: textChange})
	status, b = s.request("PUT", "/api/admin/products/"+source.Product.ID, browserChange, map[string]string{"If-Match": fmt.Sprintf(`"%d"`, current.Version)})
	require(status == 412, "stale browser edit did not conflict")
	httpFailure := decode[contract.ErrorResponse](b)
	require(httpFailure.Error.Code == stale.Code && httpFailure.Error.CurrentVersion != nil && *httpFailure.Error.CurrentVersion == changed.Version, "HTTP/MCP conflict not equivalent")
	for _, path := range []string{"/api/admin/publication-requests/unknown/confirm", "/api/admin/products/" + source.Product.ID + "/guide-review", "/api/admin/mcp-tokens"} {
		status, _ = s.request("POST", path, map[string]any{}, map[string]string{"Authorization": "Bearer " + token.Secret})
		require(status == 403, "Bearer bypassed browser-only API")
	}
	for _, name := range []string{"confirm_publication", "guide_review", "issue_token"} {
		_, err = s.session.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: map[string]any{}})
		require(err != nil, "browser-only MCP call accepted")
	}
	s.pass("workspace CRUD, strict client path/server-field boundary, HTTP/MCP equivalent VERSION_CONFLICT and browser-only denials")
	s.phase = "text publication chain"
	textPublication := s.publish("text", source.Product.ID)
	s.phase = "image upload and association"
	upload := call[contract.OpenUploadResult](s, "open_upload", contract.OpenUploadInput{ProductID: &source.Product.ID})
	img := image.NewNRGBA(image.Rect(0, 0, 48, 36))
	seed := byte(time.Now().UnixNano())
	for y := range 36 {
		for x := range 48 {
			img.SetNRGBA(x, y, color.NRGBA{uint8(x * 5), uint8(y * 7), seed, 255})
		}
	}
	var pngBytes bytes.Buffer
	must(png.Encode(&pngBytes, img), "PNG encoding failed")
	proof := s.browser(map[string]any{"action": "upload", "url": upload.UploadURL, "out": filepath.Join(s.out, "image"), "png": base64.StdEncoding.EncodeToString(pngBytes.Bytes())})
	s.save("image-upload-browser.json", proof)
	mediaID, ok := proof["media_id"].(string)
	require(ok && mediaID != "", "browser did not return selected media")
	found := false
	var cursor *string
	var uploaded contract.MediaRecord
	for {
		page := call[contract.MediaList](s, "list_media", contract.MediaQuery{After: &upload.After, Cursor: cursor, Limit: new(int64(1))})
		for _, item := range page.Items {
			if item.MediaID == mediaID {
				found = true
				uploaded = item
			}
		}
		cursor = page.NextCursor
		if cursor == nil {
			break
		}
	}
	require(found, "new browser upload not discoverable after original cursor")
	current = call[contract.ProductEnvelope](s, "get_product", contract.MCPGetProductInput{ProductID: source.Product.ID})
	imageChange := edit(current.Product)
	imageChange.Images = append([]contract.ProductImage{{MediaID: uploaded.MediaID, Label: "SDK browser upload", Description: "本地烟测上传图片"}}, imageChange.Images...)
	imageChange.CoverMediaID = &uploaded.MediaID
	call[contract.ProductEnvelope](s, "update_product", contract.MCPUpdateProductInput{ProductID: source.Product.ID, ExpectedVersion: current.Version, Edit: imageChange})
	s.phase = "image publication chain"
	imagePublication := s.publish("image", source.Product.ID)
	status, b = s.request("GET", "/api/catalog/products/"+source.Product.ID+"?release_id="+*imagePublication.ResultReleaseID, nil, nil)
	require(status == 200, "published product unavailable")
	detail := decode[contract.ProductDetail](b)
	require(detail.Product.Summary == textChange.Summary && len(detail.Product.Images) > 0 && strings.Contains(detail.Product.Images[0].URL, uploaded.SHA256), "published text/image differ from MCP changes")
	s.report["text_release_id"] = textPublication.ResultReleaseID
	s.report["image_release_id"] = imagePublication.ResultReleaseID
	s.report["uploaded_media_id"] = uploaded.MediaID
	s.pass("browser upload login return, original after cursor, image/cover association and published image/text")
	s.phase = "revocation on existing client"
	status, _ = s.request("DELETE", "/api/admin/mcp-tokens/"+token.Token.ID, nil, nil)
	require(status == 204, "token revoke failed")
	_, err = s.session.CallTool(ctx, &sdk.CallToolParams{Name: "search_products", Arguments: map[string]any{}})
	require(err != nil && s.trace.lastStatus() == 401, "initialized revoked client remained authorized")
	s.report["existing_client_revoked_status"] = s.trace.lastStatus()
	client, tr, err = s.connect(token.Secret, "")
	if client != nil {
		client.Close()
	}
	require(err != nil && tr.lastStatus() == 401, "revoked token initialized")
	s.pass("revocation denies next call on existing official SDK client and new initialize")
	s.report["success"] = true
	return 0
}
