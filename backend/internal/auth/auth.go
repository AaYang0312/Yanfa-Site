// Package auth is the only constructor of authenticated Actor values. Every
// HTTP/MCP request must authenticate again; a connection is not authorization.
package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"yuanfa-official/backend/internal/contract"
)

const SessionLifetime = 12 * time.Hour
const SessionCookie = "yuanfa_session"
const PreviewCookie = "yuanfa_preview"

// Actor's private fields prevent callers from creating administrator privileges.
// Zero Actors fail all checks. Use RequireBrowser for review/confirmation/token
// operations and Require for scope checks; Authorize also rechecks revocation.
type Actor struct {
	adminID, username, kind, credential string
	scopes                              []contract.TokenScope
}

func (a Actor) AdminID() string  { return a.adminID }
func (a Actor) Username() string { return a.username }
func (a Actor) IsBrowser() bool  { return a.kind == "session" && a.adminID != "" && a.credential != "" }

// IdentityKey distinguishes credentials for idempotency ownership, not secrets.
func (a Actor) IdentityKey() string { return a.kind + ":" + a.credential }
func (a Actor) RequireBrowser() error {
	if !a.IsBrowser() {
		return forbidden()
	}
	return nil
}
func (a Actor) Require(scope contract.TokenScope) error {
	if a.adminID == "" || a.credential == "" {
		return unauthorized()
	}
	if a.IsBrowser() {
		return nil
	}
	if a.kind != "token" {
		return unauthorized()
	}
	if scope == "" {
		return nil
	}
	for _, s := range a.scopes {
		if s == scope {
			return nil
		}
	}
	return forbidden()
}
func unauthorized() error {
	return contract.NewBusinessError(401, "UNAUTHENTICATED", "请重新认证。")
}
func forbidden() error {
	return contract.NewBusinessError(403, "FORBIDDEN", "当前身份无权执行此操作。")
}
func invalid(field string) error {
	return contract.NewBusinessError(422, "VALIDATION_FAILED", "字段不合法。", field)
}
func digest(s string) string       { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func timestamp(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }
func validExpiry(value string, now time.Time) bool {
	t, err := time.Parse(time.RFC3339Nano, value)
	return err == nil && t.After(now)
}

// Service shares the application's SQLite connection; it never owns its lifetime.
type Service struct {
	db        *sql.DB
	now       func() time.Time
	dummyHash string
}

func New(db *sql.DB) (*Service, error) {
	dummy, err := randomSecret("")
	if err != nil {
		return nil, err
	}
	hash, err := passwordHash(dummy)
	if err != nil {
		return nil, err
	}
	return &Service{db: db, now: time.Now, dummyHash: hash}, nil
}

// InitAdmin creates exactly one administrator. It cannot reset an existing
// password. Production callers obtain the password only from a no-echo TTY.
func (s *Service) InitAdmin(ctx context.Context, username, password string) error {
	if strings.TrimSpace(username) == "" || utf8.RuneCountInString(username) > 120 {
		return invalid("username")
	}
	if utf8.RuneCountInString(password) < 12 || utf8.RuneCountInString(password) > 1024 {
		return invalid("password")
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM admins`).Scan(&count); err != nil {
		return err
	}
	if count != 0 {
		return contract.NewBusinessError(409, "ADMIN_EXISTS", "管理员已初始化。")
	}
	hash, err := passwordHash(password)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO admins(id,username,password_hash,created_at) VALUES('admin',?,?,?)`, username, hash, timestamp(s.now()))
	return err
}

type Session struct {
	Actor     Actor  `json:"-"`
	CSRFToken string `json:"-"`
	ExpiresAt string `json:"-"`
}
type LoginResult struct {
	Session Session `json:"-"`
	Secret  string  `json:"-"`
}

func (s *Service) Login(ctx context.Context, input contract.LoginInput) (LoginResult, error) {
	var out LoginResult
	var id, username, hash string
	err := s.db.QueryRowContext(ctx, `SELECT id,username,password_hash FROM admins WHERE username=?`, input.Username).Scan(&id, &username, &hash)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	if errors.Is(err, sql.ErrNoRows) {
		hash = s.dummyHash
	}
	matches := passwordMatches(hash, input.Password)
	if id == "" || !matches {
		return out, unauthorized()
	}
	secret, err := randomSecret("s_")
	if err != nil {
		return out, err
	}
	now := s.now()
	expiry := timestamp(now.Add(SessionLifetime))
	credential := digest(secret)
	_, err = s.db.ExecContext(ctx, `INSERT INTO sessions(secret_hash,admin_id,created_at,expires_at) VALUES(?,?,?,?)`, credential, id, timestamp(now), expiry)
	if err != nil {
		return out, err
	}
	out.Secret = secret
	out.Session = Session{Actor: Actor{adminID: id, username: username, kind: "session", credential: credential}, CSRFToken: digest("csrf:" + secret), ExpiresAt: expiry}
	return out, nil
}

func (s *Service) AuthenticateSession(ctx context.Context, secret string) (Session, error) {
	var result Session
	if len(secret) != 45 || !strings.HasPrefix(secret, "s_") {
		return result, unauthorized()
	}
	hash := digest(secret)
	var id, username, expiry string
	err := s.db.QueryRowContext(ctx, `SELECT a.id,a.username,s.expires_at FROM sessions s JOIN admins a ON a.id=s.admin_id WHERE s.secret_hash=? AND s.revoked_at IS NULL`, hash).Scan(&id, &username, &expiry)
	if errors.Is(err, sql.ErrNoRows) {
		return result, unauthorized()
	}
	if err != nil {
		return result, err
	}
	if !validExpiry(expiry, s.now()) {
		return result, unauthorized()
	}
	return Session{Actor: Actor{adminID: id, username: username, kind: "session", credential: hash}, CSRFToken: digest("csrf:" + secret), ExpiresAt: expiry}, nil
}
func (s Session) CheckCSRF(value string) error {
	if value == "" || subtle.ConstantTimeCompare([]byte(value), []byte(s.CSRFToken)) != 1 {
		return contract.NewBusinessError(403, "CSRF_FAILED", "CSRF 校验失败。")
	}
	return nil
}

// Authorize revalidates credential expiry/revocation, including Actors retained
// by an MCP connection. Call it at each business operation, not just initialize.
func (s *Service) Authorize(ctx context.Context, a Actor, scope contract.TokenScope) error {
	if err := a.Require(scope); err != nil {
		return err
	}
	var expiry string
	var err error
	if a.IsBrowser() {
		err = s.db.QueryRowContext(ctx, `SELECT expires_at FROM sessions WHERE secret_hash=? AND admin_id=? AND revoked_at IS NULL`, a.credential, a.adminID).Scan(&expiry)
	} else {
		err = s.db.QueryRowContext(ctx, `SELECT expires_at FROM mcp_tokens WHERE id=? AND admin_id=? AND revoked_at IS NULL`, a.credential, a.adminID).Scan(&expiry)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return unauthorized()
	}
	if err != nil {
		return err
	}
	if !validExpiry(expiry, s.now()) {
		return unauthorized()
	}
	return nil
}
func (s *Service) Logout(ctx context.Context, a Actor) error {
	if err := a.RequireBrowser(); err != nil {
		return err
	}
	if err := s.Authorize(ctx, a, ""); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET revoked_at=?,preview_candidate_id=NULL,preview_expires_at=NULL WHERE secret_hash=?`, timestamp(s.now()), a.credential)
	return err
}

func (s *Service) AuthenticateToken(ctx context.Context, secret string) (Actor, error) {
	if len(secret) != 45 || !strings.HasPrefix(secret, "t_") {
		return Actor{}, unauthorized()
	}
	var a Actor
	var expiry, scopes string
	err := s.db.QueryRowContext(ctx, `SELECT t.id,a.id,a.username,t.expires_at,t.scopes_json FROM mcp_tokens t JOIN admins a ON a.id=t.admin_id WHERE t.secret_hash=? AND t.revoked_at IS NULL`, digest(secret)).Scan(&a.credential, &a.adminID, &a.username, &expiry, &scopes)
	if errors.Is(err, sql.ErrNoRows) {
		return Actor{}, unauthorized()
	}
	if err != nil {
		return Actor{}, err
	}
	if !validExpiry(expiry, s.now()) {
		return Actor{}, unauthorized()
	}
	if err = json.Unmarshal([]byte(scopes), &a.scopes); err != nil {
		return Actor{}, err
	}
	a.kind = "token"
	return a, nil
}
func (s *Service) IssueToken(ctx context.Context, a Actor, input contract.TokenInput) (contract.TokenIssued, error) {
	var result contract.TokenIssued
	if err := a.RequireBrowser(); err != nil {
		return result, err
	}
	if err := s.Authorize(ctx, a, ""); err != nil {
		return result, err
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return result, err
	}
	if err = contract.ValidateJSON("admin.schema.json#/$defs/TokenInput", encoded); err != nil {
		return result, err
	}
	now := s.now()
	if !validExpiry(input.ExpiresAt, now) {
		return result, invalid("expires_at")
	}
	secret, err := randomSecret("t_")
	if err != nil {
		return result, err
	}
	id, err := randomSecret("tok_")
	if err != nil {
		return result, err
	}
	expiry, _ := time.Parse(time.RFC3339Nano, input.ExpiresAt)
	info := contract.TokenInfo{ID: id, Name: input.Name, CreatedAt: timestamp(now), ExpiresAt: timestamp(expiry), Scopes: input.Scopes}
	scopes, err := json.Marshal(input.Scopes)
	if err != nil {
		return result, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM mcp_tokens WHERE admin_id=?`, a.adminID).Scan(&count); err != nil {
		return result, err
	}
	if count >= 1000 {
		return result, contract.NewBusinessError(409, "TOKEN_LIMIT", "凭证数量已达上限。")
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO mcp_tokens(id,admin_id,name,secret_hash,scopes_json,created_at,expires_at) VALUES(?,?,?,?,?,?,?)`, id, a.adminID, input.Name, digest(secret), string(scopes), info.CreatedAt, info.ExpiresAt)
	if err != nil {
		return result, err
	}
	if err = tx.Commit(); err != nil {
		return result, err
	}
	return contract.TokenIssued{Token: info, Secret: secret}, nil
}
func (s *Service) ListTokens(ctx context.Context, a Actor) (contract.TokenList, error) {
	out := contract.TokenList{Items: []contract.TokenInfo{}}
	if err := a.RequireBrowser(); err != nil {
		return out, err
	}
	if err := s.Authorize(ctx, a, ""); err != nil {
		return out, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,created_at,expires_at,revoked_at,scopes_json FROM mcp_tokens WHERE admin_id=? ORDER BY created_at,id`, a.adminID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var info contract.TokenInfo
		var scopes string
		if err = rows.Scan(&info.ID, &info.Name, &info.CreatedAt, &info.ExpiresAt, &info.RevokedAt, &scopes); err != nil {
			return out, err
		}
		if err = json.Unmarshal([]byte(scopes), &info.Scopes); err != nil {
			return out, err
		}
		out.Items = append(out.Items, info)
	}
	return out, rows.Err()
}
func (s *Service) RevokeToken(ctx context.Context, a Actor, id string) error {
	if err := a.RequireBrowser(); err != nil {
		return err
	}
	if err := s.Authorize(ctx, a, ""); err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE mcp_tokens SET revoked_at=COALESCE(revoked_at,?) WHERE id=? AND admin_id=?`, timestamp(s.now()), id, a.adminID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return contract.NewBusinessError(404, "NOT_FOUND", "凭证不存在。")
	}
	return nil
}
