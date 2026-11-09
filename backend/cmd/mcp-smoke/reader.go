package main

import (
	"reflect"
	"yuanfa-official/backend/internal/contract"
)

// A least-privilege reader observes the same draft, but cannot prepare changes.
func (s *smoke) checkReaderDraft(label string, product contract.ProductEnvelope) {
	read := callUsing[contract.ProductEnvelope](s, s.reader, "products:read", "get_product", contract.MCPGetProductInput{ProductID: product.Product.ID})
	require(reflect.DeepEqual(read, product), "reader returned a different product draft")
	before := s.state()
	attempt := edit(product.Product)
	attempt.Summary += " forbidden reader change"
	writes := []struct {
		name  string
		input any
	}{
		{"create_product", newDraft("least-reader-must-not-create")},
		{"update_product", contract.MCPUpdateProductInput{ProductID: product.Product.ID, ExpectedVersion: product.Version, Edit: attempt}},
		{"delete_draft", contract.MCPDeleteDraftInput{ProductID: product.Product.ID, ExpectedVersion: product.Version}},
		{"open_upload", contract.MCPOpenUploadInput{ProductID: &product.Product.ID}},
	}
	for _, operation := range writes {
		failureUsing(s, s.reader, "products:read", operation.name, operation.input, "FORBIDDEN")
	}
	unchanged := callUsing[contract.ProductEnvelope](s, s.reader, "products:read", "get_product", contract.MCPGetProductInput{ProductID: product.Product.ID})
	require(reflect.DeepEqual(unchanged, product), "reader denial changed the product")
	require(s.state().WorkspaceRevision == before.WorkspaceRevision, "reader denial changed the workspace")
	s.save(label+"-reader-draft.json", map[string]any{"scopes": []string{"products:read"}, "product_id": product.Product.ID, "version": product.Version, "write_denials": len(writes), "unchanged": true})
	s.pass(label + " read-only client sees draft; writes and image access remain forbidden")
}
func (s *smoke) checkReaderConfirmed(label, productID string, version int64) {
	product := callUsing[contract.ProductEnvelope](s, s.reader, "products:read", "get_product", contract.MCPGetProductInput{ProductID: productID})
	require(product.Version == version, "reader missed the browser-confirmed product version")
	s.save(label+"-reader-confirmed.json", map[string]any{"product_id": productID, "version": version, "matches_confirmed_version": true})
}
