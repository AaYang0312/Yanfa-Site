package guide

import (
	"context"
	"encoding/json"
	"errors"

	"yuanfa-official/backend/internal/contract"
	"yuanfa-official/backend/internal/release"
)

func (s *Service) result(h *release.Handle, req contract.GuideRequirements) contract.GuideResult {
	return contract.GuideResult{
		ReleaseID: h.ID(), ConfirmedRequirements: req,
		Recommendations: []contract.GuideRecommendation{}, MissingFields: []contract.MissingField{},
		Conflicts:   []string{},
		ContactHref: s.contactHref,
	}
}

// Unavailable builds the complete failure result when consultation cannot run.
// It still validates the request, release dictionary and public page context;
// existing conditions remain intact, and only an absent first-turn requirements
// object initializes unknowns. It neither infers conditions nor selects a product.
func (s *Service) Unavailable(ctx context.Context, h *release.Handle, input contract.ConsultRequest) (contract.GuideResult, error) {
	var zero contract.GuideResult
	b, err := json.Marshal(input)
	if err != nil {
		return zero, contract.NewBusinessError(422, "VALIDATION_FAILED", "咨询条件不合法。", "requirements")
	}
	req, err := contract.Decode[contract.ConsultRequest]("guide.schema.json#/$defs/ConsultRequest", b)
	if err != nil {
		return zero, err
	}
	if err = ctx.Err(); err != nil {
		return zero, err
	}
	if h == nil {
		return zero, contract.NewBusinessError(503, "SITE_UNAVAILABLE", "官网尚无可用的已发布版本。")
	}
	if req.ReleaseID != h.ID() {
		return zero, release.Changed()
	}
	requirements := contract.UnknownRequirements()
	if req.Requirements != nil {
		requirements = *req.Requirements
	}
	if err = validateScenes(h, requirements); err != nil {
		return zero, err
	}
	if req.ContextProductID != nil {
		if _, err = s.catalog.Get(ctx, h, *req.ContextProductID); err != nil {
			var business *contract.BusinessError
			if errors.As(err, &business) && business.HTTPStatus == 404 {
				return zero, contract.NewBusinessError(422, "VALIDATION_FAILED", "页面商品不属于当前公开目录。", "context_product_id")
			}
			return zero, err
		}
	}
	out := s.result(h, requirements)
	out.State = "unavailable"
	return out, nil
}
