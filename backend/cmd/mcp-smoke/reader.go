package main

import (
	"reflect"

	"yuanfa-official/backend/internal/contract"
)

// checkReaderPending runs inside each default publication flow, against the
// same resources created by the management client. The independent SDK client
// has exactly products:read and never borrows a browser Actor or write scopes.
func (s *smoke) checkReaderPending(label, productID string, candidate contract.Candidate, pending contract.PublicationRequest) {
	readCandidate := callUsing[contract.Candidate](s, s.reader, "products:read", "get_candidate", contract.MCPGetCandidateInput{CandidateID: candidate.ID})
	require(readCandidate.ID == candidate.ID && reflect.DeepEqual(readCandidate, candidate), "least reader changed candidate identity or envelope")
	readPending := callUsing[contract.PublicationRequest](s, s.reader, "products:read", "get_publication", contract.MCPGetPublicationInput{RequestID: pending.ID})
	require(readPending.ID == pending.ID && readPending.State == "awaiting_confirmation" && readPending.ResultReleaseID == nil && reflect.DeepEqual(readPending, pending), "least reader changed pending publication identity or envelope")
	require(readPending.CandidateID != nil && *readPending.CandidateID == candidate.ID && readPending.SnapshotSHA256 == candidate.SnapshotSHA256, "least reader returned another publication target")
	product := callUsing[contract.ProductEnvelope](s, s.reader, "products:read", "get_product", contract.MCPGetProductInput{ProductID: productID})
	before := s.state()
	attempt := edit(product.Product)
	attempt.Summary += " · forbidden least-reader edit"
	writes := []struct {
		name  string
		input any
	}{
		{"create_product", newDraft("least-reader-must-not-create")},
		{"update_product", contract.MCPUpdateProductInput{ProductID: productID, ExpectedVersion: product.Version, Edit: attempt}},
		{"delete_draft", contract.MCPDeleteDraftInput{ProductID: productID, ExpectedVersion: product.Version}},
		{"create_candidate", contract.MCPCreateCandidateInput{ExpectedWorkspaceRevision: candidate.WorkspaceRevision, RequestKey: label + "-least-reader-candidate"}},
		{"request_publication", contract.MCPRequestPublicationInput{Kind: pending.Kind, CandidateID: pending.CandidateID, ReleaseID: pending.ReleaseID, SnapshotSHA256: pending.SnapshotSHA256, ExpectedActiveReleaseID: pending.ExpectedActiveReleaseID, ExpectedWorkspaceRevision: pending.ExpectedWorkspaceRevision, RequestKey: label + "-least-reader-publication"}},
	}
	var denied []map[string]any
	for _, write := range writes {
		failure := failureUsing(s, s.reader, "products:read", write.name, write.input, "FORBIDDEN")
		denied = append(denied, map[string]any{"tool": write.name, "isError": true, "error": failure})
	}
	after := s.state()
	require(after.WorkspaceRevision == before.WorkspaceRevision && equalID(after.ActiveReleaseID, before.ActiveReleaseID), "least reader write denial changed workspace or active release")
	unchangedProduct := callUsing[contract.ProductEnvelope](s, s.reader, "products:read", "get_product", contract.MCPGetProductInput{ProductID: productID})
	unchangedCandidate := callUsing[contract.Candidate](s, s.reader, "products:read", "get_candidate", contract.MCPGetCandidateInput{CandidateID: candidate.ID})
	unchangedPending := callUsing[contract.PublicationRequest](s, s.reader, "products:read", "get_publication", contract.MCPGetPublicationInput{RequestID: pending.ID})
	require(reflect.DeepEqual(unchangedProduct, product) && reflect.DeepEqual(unchangedCandidate, readCandidate) && reflect.DeepEqual(unchangedPending, readPending), "least reader write denial changed a resource")
	s.save(label+"-reader-pending.json", map[string]any{
		"scopes": []string{"products:read"}, "candidate": readCandidate, "publication": readPending,
		"forbidden_writes": denied, "resources_unchanged_after_denials": true,
		"workspace_revision": after.WorkspaceRevision, "active_release_id": after.ActiveReleaseID,
	})
	s.pass(label + " products:read candidate/pending envelopes match; all write tools return structured FORBIDDEN without mutation")
}

func (s *smoke) checkReaderSucceeded(label string, candidate contract.Candidate, publication contract.PublicationRequest) {
	readCandidate := callUsing[contract.Candidate](s, s.reader, "products:read", "get_candidate", contract.MCPGetCandidateInput{CandidateID: candidate.ID})
	readPublication := callUsing[contract.PublicationRequest](s, s.reader, "products:read", "get_publication", contract.MCPGetPublicationInput{RequestID: publication.ID})
	require(readCandidate.ID == candidate.ID && reflect.DeepEqual(readCandidate, candidate), "least reader changed the completed candidate envelope")
	require(readPublication.ID == publication.ID && readPublication.State == "succeeded" && reflect.DeepEqual(readPublication, publication), "least reader changed succeeded publication identity or envelope")
	require(readPublication.ResultReleaseID != nil && *readPublication.ResultReleaseID == candidate.ID && readPublication.CandidateID != nil && *readPublication.CandidateID == candidate.ID, "least reader lost the original result release")
	require(readPublication.SnapshotSHA256 == candidate.SnapshotSHA256 && readPublication.ExpectedWorkspaceRevision == candidate.WorkspaceRevision && equalID(readPublication.ExpectedActiveReleaseID, candidate.ExpectedActiveReleaseID), "least reader lost the reviewed publication envelope")
	s.save(label+"-reader-succeeded.json", map[string]any{
		"scopes": []string{"products:read"}, "candidate": readCandidate, "publication": readPublication,
		"matches_management_result": true,
	})
	s.pass(label + " same products:read SDK client reads succeeded original request, envelope and release")
}
