package catalog_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"yuanfa-official/backend/internal/content"
	"yuanfa-official/backend/internal/contract"
	"yuanfa-official/backend/internal/httpapi"
)

func TestReviewedUnicodeAndMissingSourceRemainPublicBoundaries(t *testing.T) {
	f := setup(t)
	g := &f.snapshot.Products[2].GuideProfile
	g.Materials = []string{"木材\u2028复合", "literal \\u2028"}
	sha, err := content.FactSHA256(*g, "materials")
	if err != nil {
		t.Fatal(err)
	}
	for i := range g.Reviews {
		if g.Reviews[i].Field == "materials" {
			g.Reviews[i].FactSHA256 = sha
		}
	}
	h := f.load(t, "unicode_facts")
	f.activate(t, h, "unicode_operation")
	p, err := f.service.Get(context.Background(), h, "p_a")
	if err != nil || !slices.Equal(p.GuideProfile.Materials, g.Materials) {
		t.Fatalf("canonical fact lost: %+v %v", p.GuideProfile, err)
	}
	g.Sources = []contract.GuideSource{}
	h = f.load(t, "missing_source")
	f.activate(t, h, "missing_source_operation")
	p, err = f.service.Get(context.Background(), h, "p_a")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.GuideProfile.Materials) != 0 || len(p.GuideProfile.Sources) != 0 || !slices.Contains(p.GuideProfile.UnknownFields, "materials") {
		t.Fatal("review without source retained")
	}
	b, _ := json.Marshal(p)
	if err = contract.ValidateJSON("public-catalog.schema.json#/$defs/PublicProduct", b); err != nil {
		t.Fatal(err)
	}
}

type blockedBody struct {
	header  http.Header
	started chan struct{}
	resume  chan struct{}
}

func (w *blockedBody) Header() http.Header         { return w.header }
func (w *blockedBody) WriteHeader(int)             {}
func (w *blockedBody) Write(b []byte) (int, error) { close(w.started); <-w.resume; return len(b), nil }
func TestCatalogBodyDoesNotHoldPublicationGate(t *testing.T) {
	f := setup(t)
	next := f.load(t, "slow_body_next")
	mux := http.NewServeMux()
	httpapi.RegisterCatalog(mux, f.releases, f.service)
	writer := &blockedBody{header: make(http.Header), started: make(chan struct{}), resume: make(chan struct{})}
	finished := make(chan struct{})
	defer func() { close(writer.resume); <-finished }()
	go func() {
		defer close(finished)
		mux.ServeHTTP(writer, httptest.NewRequest("GET", "/api/catalog/export", nil))
	}()
	<-writer.started
	activated := make(chan struct{})
	go func() { defer close(activated); f.activate(t, next, "slow_body_operation") }()
	select {
	case <-activated:
	case <-time.After(5 * time.Second):
		t.Fatal("publication blocked on response body")
	}
	select {
	case <-finished:
		t.Fatal("body finished before consumer released it")
	default:
	}
}
