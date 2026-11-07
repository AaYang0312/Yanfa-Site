package release

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"yuanfa-official/backend/internal/contract"
	"yuanfa-official/backend/internal/coordination"
)

func artifact(t *testing.T, dir, id, resource string) *Handle {
	t.Helper()
	b, err := os.ReadFile("../../../seed/initial-catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var snapshot contract.CatalogSnapshot
	if err = json.Unmarshal(b, &snapshot); err != nil {
		t.Fatal(err)
	}
	snapshot.ReleaseID = id
	snapshot.Products = []contract.SnapshotProduct{}
	snapshot.PreviouslyPublishedRoutes = []contract.PublishedRoute{}
	for i := range snapshot.LegacySeries {
		snapshot.LegacySeries[i].ProductIDs = []string{}
	}
	b, err = json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "artifacts", id)
	if err = os.MkdirAll(filepath.Join(root, "site", "_astro"), 0700); err != nil {
		t.Fatal(err)
	}
	css := []byte("body{color:" + resource + "}")
	name := "_astro/" + Hash(css) + ".css"
	manifest := contract.Manifest{SchemaVersion: 1, CandidateID: id, WorkspaceRevision: snapshot.WorkspaceRevision, CodeVersion: "test-artifact", SnapshotSHA256: Hash(b), Routes: []string{"index.html"}, Resources: []contract.ManifestResource{{Path: name, SHA256: Hash(css), Bytes: int64(len(css))}}}
	for name, data := range map[string][]byte{"catalog.json": b, "manifest.json": []byte(encodeTest(manifest)), "site/index.html": []byte(`<meta name="release-id" content="` + id + `"><h1>catalog</h1>`), "site/" + name: css} {
		if err = os.WriteFile(filepath.Join(root, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	h, err := Load(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	return h
}
func encodeTest(v any) string { b, _ := json.Marshal(v); return string(b) }
func activate(t *testing.T, m *Manager, h *Handle, op string) {
	t.Helper()
	p, err := m.Prepare(h, op)
	if err != nil {
		t.Fatal(err)
	}
	m.gate.Lock()
	switched, err := m.ActivateLocked(context.Background(), p)
	m.gate.Unlock()
	if err != nil || !switched {
		t.Fatalf("activate: switched=%v err=%v", switched, err)
	}
}
func TestCaptureResponseBoundaryAndHistoricalResources(t *testing.T) {
	dir := t.TempDir()
	gate := &coordination.Gate{}
	m := New(dir, gate)
	old := artifact(t, dir, "release_old", "red")
	next := artifact(t, dir, "release_next", "blue")
	activate(t, m, old, "operation_old")
	captured, err := m.Capture(old.ID())
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	finished := make(chan struct{})
	releaseResponse := make(chan struct{})
	defer close(releaseResponse)
	go func() {
		defer close(finished)
		if err := m.BeginResponse(old.ID(), func() error { close(started); return nil }); err != nil {
			t.Error(err)
		}
		<-releaseResponse
	}()
	<-started
	activate(t, m, next, "operation_next")
	select {
	case <-finished:
		t.Fatal("activation waited for entire response")
	default:
	}
	if _, err = m.Capture(old.ID()); err == nil {
		t.Fatal("old expected ID accepted")
	}
	called := false
	if err = m.BeginResponse(old.ID(), func() error { called = true; return nil }); err == nil || called {
		t.Fatal("stale response started")
	}
	if captured.ID() != old.ID() {
		t.Fatal("captured handle mutated")
	}
	for name := range old.resources {
		h, ok := m.Resource(name)
		if !ok {
			t.Fatal("old hash unauthorized")
		}
		f, err := h.Open(name)
		if err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
	restored := New(dir, gate)
	if err = restored.Restore(); err != nil {
		t.Fatal(err)
	}
	if got := restored.Active(); got.ReleaseID != next.ID() || got.OperationID != "operation_next" || !restored.Successful(old.ID()) {
		t.Fatal("durable authority lost")
	}
}
func TestCorruptArtifactRefusesAuthorityAndPrivatePaths(t *testing.T) {
	dir := t.TempDir()
	gate := &coordination.Gate{}
	m := New(dir, gate)
	h := artifact(t, dir, "release_integrity", "red")
	activate(t, m, h, "operation_integrity")
	for _, name := range []string{"../catalog.json", "catalog.json", "manifest.json", "not-listed.css"} {
		if f, err := h.Open(name); err == nil {
			f.Close()
			t.Fatal("private path authorized: " + name)
		}
	}
	for name := range h.resources {
		if err := os.WriteFile(filepath.Join(h.directory, "site", name), []byte("tampered"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	restored := New(dir, gate)
	if err := restored.Restore(); err == nil {
		t.Fatal("corrupt successful resource accepted")
	}
	if restored.Status() != "unavailable" {
		t.Fatal("corrupt pointer reported ready")
	}
	if _, err := restored.Capture(""); err == nil {
		t.Fatal("corrupt snapshot served")
	}
}
func TestRouteDoesNotCleanTraversalIntoAuthorizedPath(t *testing.T) {
	for _, name := range []string{"/../catalog.json", "/products/../index.html", "//index.html", "/x\\y"} {
		if got := Route(name); got != "" {
			t.Fatalf("unsafe path %q became %q", name, got)
		}
	}
}

func TestPreparedActivationRejectsRoundTripAfterRestore(t *testing.T) {
	dir := t.TempDir()
	gate := &coordination.Gate{}
	m := New(dir, gate)
	first := artifact(t, dir, "release_A", "red")
	next := artifact(t, dir, "release_B", "blue")
	activate(t, m, first, "operation_A1")
	prepared, err := m.Prepare(next, "operation_stale")
	if err != nil {
		t.Fatal(err)
	}
	activate(t, m, next, "operation_B2")
	activate(t, m, first, "operation_A3")
	restored := New(dir, gate)
	if err = restored.Restore(); err != nil {
		t.Fatal(err)
	}
	gate.Lock()
	switched, err := restored.ActivateLocked(context.Background(), prepared)
	gate.Unlock()
	if switched || err == nil {
		t.Fatalf("prepared activation revived after A-B-A: switched=%v err=%v", switched, err)
	}
	if restored.OperationID() != "operation_A3" || *restored.ActiveID() != first.ID() {
		t.Fatal("rejected prepared operation changed authority")
	}
}
