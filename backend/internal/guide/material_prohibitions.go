package guide

import (
	"regexp"
	"strings"

	"yuanfa-official/backend/internal/contract"
)

// Match only direct prohibitions with a plain list of materials. Do not
// interpret conditional warnings such as "打混凝土时避开钢筋".
var (
	prohibitedMaterialPrefix = regexp.MustCompile(`(?:严禁(?:击打|打)?|不能(?:钉|打)|不适合钉|不适用于)[：: ]*([^：:]+)$`)
	prohibitedMaterialSuffix = regexp.MustCompile(`^(.+?)[ ]*(?:一律不用|不能用|禁止使用)$`)
)

func materialKey(value string) string {
	value = strings.TrimSpace(value)
	switch value {
	case "水泥", "水泥墙", "混凝土", "混凝土墙", "普通混凝土":
		return "混凝土"
	case "砖墙", "红砖", "红砖墙", "砖砌墙", "实心红砖":
		return "砖墙"
	default:
		return value
	}
}

func hasPublishedMaterialProhibition(notes []string, req contract.GuideRequirements) bool {
	if req.Material == nil && req.Substrate == nil {
		return false
	}
	for _, note := range notes {
		for _, clause := range strings.FieldsFunc(note, func(r rune) bool { return strings.ContainsRune("。；;！!\n", r) }) {
			clause = strings.TrimSpace(clause)
			match := prohibitedMaterialPrefix.FindStringSubmatch(clause)
			if match == nil {
				match = prohibitedMaterialSuffix.FindStringSubmatch(clause)
				if match != nil && strings.ContainsAny(match[1], "：:") {
					continue
				}
			}
			if match == nil {
				continue
			}
			for _, material := range strings.FieldsFunc(match[1], func(r rune) bool { return strings.ContainsRune("、，,/／", r) }) {
				key := materialKey(strings.SplitN(material, "等", 2)[0])
				if key != "" && ((req.Material != nil && key == materialKey(*req.Material)) || (req.Substrate != nil && key == materialKey(*req.Substrate))) {
					return true
				}
			}
		}
	}
	return false
}
