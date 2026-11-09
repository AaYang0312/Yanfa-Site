package guide

import (
	"encoding/json"
	"strings"

	"yuanfa-official/backend/internal/contract"
)

// Model omission is not a visitor revocation. Preserve known conditions unless
// a clearance carries an explicit reference to the current visitor message.
func mergeConversationRequirements(req contract.ConsultRequest, proposal contract.GuideModelProposal) (contract.GuideRequirements, error) {
	previous := contract.UnknownRequirements()
	if req.Requirements != nil {
		previous = *req.Requirements
	}
	toMap := func(value contract.GuideRequirements) map[string]any {
		b, _ := json.Marshal(value)
		var fields map[string]any
		_ = json.Unmarshal(b, &fields)
		return fields
	}
	prior, next := toMap(previous), toMap(proposal.Requirements)
	cleared := make(map[string]bool)
	for _, item := range proposal.ClearedRequirements {
		field := string(item.Field)
		if cleared[field] || !strings.Contains(req.Message, item.Reference) {
			return previous, contract.NewBusinessError(422, "VALIDATION_FAILED", "条件撤销缺少本轮明确说明。")
		}
		cleared[field] = true
	}
	for field, value := range next {
		empty := value == nil
		if values, ok := value.([]any); ok {
			empty = len(values) == 0
		}
		if cleared[field] && !empty {
			return previous, contract.NewBusinessError(422, "VALIDATION_FAILED", "已撤销条件不能同时设置新值。")
		}
		if empty && !cleared[field] {
			next[field] = prior[field]
		}
	}
	b, _ := json.Marshal(next)
	return contract.Decode[contract.GuideRequirements]("guide.schema.json#/$defs/GuideRequirements", b)
}
