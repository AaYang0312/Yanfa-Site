package guide

import (
	"strings"

	"yuanfa-official/backend/internal/contract"
)

// Published prose is useful for discovery without being promoted to a verified
// matching fact. Keep ordinary descriptions separate from structured evidence.
func introduction(p contract.PublicProduct) []string {
	out := make([]string, 0, 4)
	add := func(label, value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		prefix := label + "："
		runes := []rune(value)
		if len([]rune(prefix))+len(runes) > 1000 {
			suffix := "…（完整内容见产品详情）"
			runes = runes[:1000-len([]rune(prefix))-len([]rune(suffix))]
			value = string(runes) + suffix
		}
		out = append(out, prefix+value)
	}
	add("简介", p.Summary)
	add("用途说明", strings.Join(p.Applications, "；"))
	add("特点", strings.Join(p.Features, "；"))
	add("注意事项", strings.Join(p.Notes, "；"))
	return out
}

// No stated conditions means browsing, not a request to complete a technical
// questionnaire. Keep Evaluate's conservative missing fields and match level;
// only suppress proactive visitor questions until a condition is supplied.
func suitabilityQuestion(req contract.GuideRequirements, missing []contract.MissingField) *contract.GuideQuestion {
	if !hasStatedRequirements(req) {
		return nil
	}
	return question(missing)
}

func hasStatedRequirements(req contract.GuideRequirements) bool {
	return len(req.SceneIDs) != 0 || req.Material != nil || req.Substrate != nil || req.NailType != nil || req.FastenerLengthMM != nil || req.AirSupply != nil || req.LoadBearing != nil || req.LongNose != nil || req.Blower != nil
}
