package guide_test

import (
	"testing"
	"time"

	"yuanfa-official/backend/internal/contract"
	"yuanfa-official/backend/internal/guide"
)

// The real Eino agent must allow a multi-step catalog consultation to finish
// beyond the old 20-second cutoff. Only the external model is controlled.
func TestConsultStreamCompletesAfterTwentySeconds(t *testing.T) {
	f := selectionSetup(t)
	first, rest := "Here is the product.", " Its uses and features are ready."
	head, tail := streamEnvelope(t, f.requirements, []string{"p_selection"}, "consult", first, rest)
	m := newPipeConsultModel(head, tail, true)
	req := contract.ConsultRequest{
		ReleaseID: f.h.ID(), Message: "Introduce SELECTION uses and features", Requirements: &f.requirements,
	}
	deltas := make(chan string, 128)
	started := time.Now()
	release := time.NewTimer(21 * time.Second)
	defer release.Stop()
	_, done := startConsultStream(t, f, m, req, func(delta string) error {
		deltas <- delta
		return nil
	})
	preview := awaitStreamPreview(t, deltas, done, first)
	select {
	case got := <-done:
		t.Fatalf("consultation ended before the delayed model completed: elapsed=%v state=%s err=%v", time.Since(started), got.result.State, got.err)
	case <-release.C:
		close(m.script.tailGate)
	}
	got := awaitStreamResult(t, done)
	if got.err != nil || got.result.State != "recommendations" || len(got.result.Recommendations) != 1 || got.result.Recommendations[0].ProductID != "p_selection" {
		t.Fatalf("delayed consultation did not commit its valid result: state=%s recommendations=%v err=%v", got.result.State, got.result.Recommendations, got.err)
	}
	if text := remainingStreamText(deltas, preview); text != first+rest || guide.ResultText(got.result) != text {
		t.Fatalf("delayed reply did not finish intact: preview=%q final=%q", text, guide.ResultText(got.result))
	}
	// One tool round trip: a slow response must not trigger automatic retries.
	assertNativeStream(t, m, 2)
	t.Logf("completed controlled model consultation in %v with exactly two model calls", time.Since(started))
}
