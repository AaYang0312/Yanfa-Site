// Package guide builds public guide results from the captured release and the
// shared matching decision. Explicit selections have no model dependency.
package guide

import (
	"context"
	"encoding/json"
	"strconv"

	"yuanfa-official/backend/internal/catalog"
	"yuanfa-official/backend/internal/contract"
	"yuanfa-official/backend/internal/matching"
	"yuanfa-official/backend/internal/release"
)

// Service can read only the public catalog. It has no workspace, management,
// model client or inference semaphore. Returned DTOs belong to the caller.
type Service struct {
	catalog     *catalog.Service
	contactHref string
}

func New(catalog *catalog.Service, contactHref string) *Service {
	return &Service{catalog: catalog, contactHref: contactHref}
}

// Select is the common entry for card selections and a parsed explicit choice.
// Callers provide a stable product ID, the captured active handle and the entire
// merged nine-field requirements. JSON callers must validate before typed decode
// so omitted nullable fields cannot become implicit nulls. Select validates typed
// callers too, normalizes materials and checks scene membership in this release.
// Evaluate alone decides suitability; purchase mappings never affect that decision.
// Conflicts retain Evaluate's original order and duplicates for this product;
// non-conflicting results carry an explicit empty array.
// The HTTP/SSE owner must call BeginResponse after computing the result.
func (s *Service) Select(ctx context.Context, h *release.Handle, input contract.SelectionRequest) (contract.GuideResult, error) {
	var zero contract.GuideResult
	b, err := json.Marshal(input)
	if err != nil {
		return zero, contract.NewBusinessError(422, "VALIDATION_FAILED", "选择条件不合法。", "requirements")
	}
	req, err := contract.Decode[contract.SelectionRequest]("guide.schema.json#/$defs/SelectionRequest", b)
	if err != nil {
		return zero, err
	}
	if h == nil {
		return zero, contract.NewBusinessError(503, "SITE_UNAVAILABLE", "官网尚无可用的已发布版本。")
	}
	if req.ReleaseID != h.ID() {
		return zero, release.Changed()
	}
	if err = validateScenes(h, req.Requirements); err != nil {
		return zero, err
	}
	p, err := s.catalog.Get(ctx, h, req.SelectedProductID)
	if err != nil {
		return zero, err
	}
	decision := matching.Evaluate(req.Requirements, p.GuideProfile)
	out := s.result(h, req.Requirements)
	out.MissingFields = decision.MissingFields
	if decision.MatchLevel == "no_match" {
		out.State = "no_match"
		out.Conflicts = decision.Conflicts
		return out, nil
	}
	options, err := s.catalog.PurchaseOptions(ctx, h, p.ProductID)
	if err != nil {
		return zero, err
	}
	out.State = "selected"
	out.Question = suitabilityQuestion(req.Requirements, decision.MissingFields)
	out.Selection = &contract.GuideSelection{
		ProductID: p.ProductID, Model: p.Model, Name: p.Name, DetailURL: p.DetailURL,
		MatchLevel: decision.MatchLevel, MatchedFacts: decision.MatchedFacts,
		MissingFields: decision.MissingFields, ConfirmationNotes: introduction(p),
		PurchaseOptions: options.PurchaseOptions, PurchaseStatus: options.PurchaseStatus,
	}
	if len(p.Images) > 0 {
		out.Selection.Image = &p.Images[0]
	}
	return out, nil
}

func validateScenes(h *release.Handle, req contract.GuideRequirements) error {
	for i, id := range req.SceneIDs {
		found := false
		for _, scene := range h.Catalog().Scenes {
			if scene.ID == id {
				found = true
				break
			}
		}
		if !found {
			return contract.NewBusinessError(422, "VALIDATION_FAILED", "场景不属于当前发布目录。", "requirements.scene_ids."+strconv.Itoa(i))
		}
	}
	return nil
}
