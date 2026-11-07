package publication_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"yuanfa-official/backend/internal/auth"
	"yuanfa-official/backend/internal/contract"
)

func TestPublicationReadScopeAndCredentialBoundaries(t *testing.T) {
	ctx := context.Background()
	a, admin := fixture(t)
	if err := a.Publication.Start(ctx); err != nil {
		t.Fatal(err)
	}
	candidate := readyCandidate(t, a, admin, "read-scope-candidate")
	input := publicationInput(t, a, candidate, "publish")
	pending, err := a.Publication.Request(ctx, admin, input, "read-scope-request")
	if err != nil {
		t.Fatal(err)
	}
	issue := func(t *testing.T, scopes ...contract.TokenScope) (auth.Actor, contract.TokenIssued) {
		t.Helper()
		issued, err := a.Auth.IssueToken(ctx, admin, contract.TokenInput{
			Name: t.Name(), ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano), Scopes: scopes,
		})
		if err != nil {
			t.Fatal(err)
		}
		actor, err := a.Auth.AuthenticateToken(ctx, issued.Secret)
		if err != nil {
			t.Fatal(err)
		}
		return actor, issued
	}
	denyReads := func(t *testing.T, actor auth.Actor, code string) {
		t.Helper()
		_, err := a.Publication.GetCandidate(ctx, actor, candidate.ID)
		requireCode(t, err, code)
		_, err = a.Publication.GetRequest(ctx, actor, pending.ID)
		requireCode(t, err, code)
	}
	t.Run("products read returns frozen candidate and pending intent", func(t *testing.T) {
		reader, _ := issue(t, "products:read")
		gotCandidate, err := a.Publication.GetCandidate(ctx, reader, candidate.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(gotCandidate, candidate) {
			t.Fatalf("reader received a different candidate: %+v", gotCandidate)
		}
		gotRequest, err := a.Publication.GetRequest(ctx, reader, pending.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(gotRequest, pending) {
			t.Fatalf("reader received a different publication intent: %+v", gotRequest)
		}
		_, err = a.Publication.CreateCandidate(ctx, reader, candidate.WorkspaceRevision, "reader-create")
		requireCode(t, err, "FORBIDDEN")
		_, err = a.Publication.Request(ctx, reader, input, "reader-request")
		requireCode(t, err, "FORBIDDEN")
		_, err = a.Publication.Confirm(ctx, reader, pending.ID, confirmation(pending), "reader-confirm")
		requireCode(t, err, "FORBIDDEN")
		var candidates, requests int
		if err = a.Store.DB().QueryRow(`SELECT (SELECT count(*) FROM candidates),(SELECT count(*) FROM publication_requests)`).Scan(&candidates, &requests); err != nil {
			t.Fatal(err)
		}
		if candidates != 1 || requests != 1 || a.Releases.ActiveID() != nil {
			t.Fatal("read-only consumer changed publication resources or active release")
		}
		unchanged, err := a.Publication.GetRequest(ctx, reader, pending.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(unchanged, pending) {
			t.Fatal("rejected writes changed the pending intent")
		}
	})
	for _, scope := range []contract.TokenScope{"candidates:write", "publications:write", "media:read"} {
		t.Run("missing products read with "+string(scope), func(t *testing.T) {
			actor, _ := issue(t, scope)
			denyReads(t, actor, "FORBIDDEN")
		})
	}
	t.Run("unauthenticated", func(t *testing.T) {
		denyReads(t, auth.Actor{}, "UNAUTHENTICATED")
	})
	t.Run("retained actor after expiry", func(t *testing.T) {
		actor, issued := issue(t, "products:read")
		if _, err := a.Store.DB().Exec(`UPDATE mcp_tokens SET expires_at=? WHERE id=?`, time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano), issued.Token.ID); err != nil {
			t.Fatal(err)
		}
		denyReads(t, actor, "UNAUTHENTICATED")
	})
	t.Run("retained actor after revocation", func(t *testing.T) {
		actor, issued := issue(t, "products:read")
		if err := a.Auth.RevokeToken(ctx, admin, issued.Token.ID); err != nil {
			t.Fatal(err)
		}
		denyReads(t, actor, "UNAUTHENTICATED")
	})
}
