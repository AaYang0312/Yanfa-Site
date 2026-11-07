// Package matching owns the deterministic suitability decision shared by public
// catalog queries and guide consumers. Inputs must be validated requirements and
// a reviewed public profile; display tags and purchase links are never evidence.
package matching

import (
	"fmt"
	"slices"
	"strings"

	"yuanfa-official/backend/internal/contract"
)

var requirementOrder = [...]string{"scene_ids", "material", "substrate", "nail_type", "fastener_length_mm", "air_supply", "load_bearing", "long_nose", "blower"}

// Evaluate checks every declared hard condition, even when required_requirements
// is empty or unreviewed. Unknown power and an unreviewed required-field policy
// always prevent matched. Conflicts take precedence over all missing evidence.
// Boolean false means no feature/load requirement, except air_supply=false,
// which conflicts with a reviewed pneumatic power source.
// The result owns all mutable data, including source URL pointers; consumers
// may edit it without changing the input profile or another evaluation.
func Evaluate(req contract.GuideRequirements, p contract.ReviewedGuideProfile) contract.MatchDecision {
	out := contract.MatchDecision{MatchLevel: "matched", MatchedFacts: []contract.MatchedFact{}, MissingFields: []contract.MissingField{}, ConfirmationNotes: []string{}, Conflicts: []string{}}
	missing := func(field string) { out.MissingFields = append(out.MissingFields, contract.MissingField(field)) }
	known := func(field string) bool { return !slices.Contains(p.UnknownFields, contract.GuideField(field)) }
	fact := func(field, text string) {
		sources := []contract.PublicSource{}
		for _, s := range p.Sources {
			if string(s.Field) == field {
				item := contract.PublicSource{PublicLabel: s.PublicLabel, PublicURL: s.PublicURL}
				if !slices.ContainsFunc(sources, func(v contract.PublicSource) bool {
					return v.PublicLabel == item.PublicLabel && sameURL(v.PublicURL, item.PublicURL)
				}) {
					if item.PublicURL != nil {
						item.PublicURL = new(*item.PublicURL)
					}
					sources = append(sources, item)
				}
			}
		}
		out.MatchedFacts = append(out.MatchedFacts, contract.MatchedFact{Field: contract.GuideField(field), Text: text, Sources: sources})
	}
	required := func(field string) bool {
		return known("required_requirements") && slices.Contains(p.RequiredRequirements, contract.RequirementField(field))
	}
	if !known("required_requirements") {
		missing("guide_profile.required_requirements")
	}
	if len(req.SceneIDs) > 0 || required("scene_ids") {
		if len(req.SceneIDs) == 0 {
			missing("scene_ids")
		}
		if !known("scene_ids") {
			missing("guide_profile.scene_ids")
		} else if len(req.SceneIDs) > 0 {
			for _, scene := range req.SceneIDs {
				if !slices.Contains(p.SceneIDs, scene) {
					out.Conflicts = append(out.Conflicts, "已审核适用场景不包含："+string(scene))
				}
			}
			if len(out.Conflicts) == 0 {
				fact("scene_ids", "已审核场景符合声明条件。")
			}
		}
	}
	material := func(value *string, field, includedField, excludedField string, included, excluded []string) {
		if value == nil && !required(field) {
			return
		}
		if value == nil {
			missing(field)
		}
		if !known(includedField) {
			missing("guide_profile." + includedField)
		}
		if !known(excludedField) {
			missing("guide_profile." + excludedField)
		}
		if value == nil {
			return
		}
		v := strings.TrimSpace(*value)
		if known(excludedField) && slices.Contains(excluded, v) {
			out.Conflicts = append(out.Conflicts, "已审核资料明确不适用："+v)
			return
		}
		if known(includedField) && slices.Contains(included, v) {
			fact(includedField, "已审核适用材料/基材："+v)
		} else {
			missing("guide_profile." + includedField)
		}
	}
	material(req.Material, "material", "materials", "excluded_materials", p.Materials, p.ExcludedMaterials)
	material(req.Substrate, "substrate", "substrates", "excluded_substrates", p.Substrates, p.ExcludedSubstrates)
	if req.NailType != nil || required("nail_type") {
		if req.NailType == nil {
			missing("nail_type")
		}
		if !known("nail_type") || p.NailType == nil {
			missing("guide_profile.nail_type")
		} else if req.NailType != nil {
			if *req.NailType != *p.NailType {
				out.Conflicts = append(out.Conflicts, "紧固件类型与已审核资料冲突。")
			} else {
				fact("nail_type", "已审核紧固件类型："+string(*p.NailType))
			}
		}
	}
	if req.FastenerLengthMM != nil || required("fastener_length_mm") {
		if req.FastenerLengthMM == nil {
			missing("fastener_length_mm")
		}
		if !known("fastener_length_mm") || p.FastenerLengthMM == nil {
			missing("guide_profile.fastener_length_mm")
		} else if req.FastenerLengthMM != nil {
			length := *req.FastenerLengthMM
			if length < p.FastenerLengthMM.Min || length > p.FastenerLengthMM.Max {
				out.Conflicts = append(out.Conflicts, "钉长超出已审核的闭区间范围。")
			} else {
				fact("fastener_length_mm", fmt.Sprintf("钉长 %g mm 在已审核范围 %g–%g mm 内。", length, p.FastenerLengthMM.Min, p.FastenerLengthMM.Max))
			}
		}
	}
	if !known("power_source") || p.PowerSource == nil {
		missing("guide_profile.power_source")
	} else if *p.PowerSource == "pneumatic" {
		if req.AirSupply == nil {
			missing("air_supply")
		} else if !*req.AirSupply {
			out.Conflicts = append(out.Conflicts, "该工具需要压缩空气，现场明确没有可用气源。")
		} else {
			fact("power_source", "已审核气动工具，访客声明有可用压缩空气。")
		}
	} else {
		fact("power_source", "已审核动力："+string(*p.PowerSource))
	}
	if required("air_supply") && req.AirSupply == nil {
		missing("air_supply")
	}
	boolean := func(field string, value, supported *bool) {
		if value == nil {
			if required(field) {
				missing(field)
				if !known(field) || supported == nil {
					missing("guide_profile." + field)
				}
			}
			return
		}
		if !*value {
			return
		}
		if !known(field) || supported == nil {
			missing("guide_profile." + field)
		} else if !*supported {
			out.Conflicts = append(out.Conflicts, "已审核资料不满足必需条件："+field)
		} else {
			fact(field, "已审核满足必需条件："+field)
		}
	}
	boolean("load_bearing", req.LoadBearing, p.LoadBearing)
	boolean("long_nose", req.LongNose, p.LongNose)
	boolean("blower", req.Blower, p.Blower)
	slices.SortFunc(out.MissingFields, func(a, b contract.MissingField) int {
		rank := func(v contract.MissingField) int {
			for i, f := range requirementOrder {
				if string(v) == f {
					return i
				}
			}
			return len(requirementOrder)
		}
		if d := rank(a) - rank(b); d != 0 {
			return d
		}
		return strings.Compare(string(a), string(b))
	})
	out.MissingFields = slices.Compact(out.MissingFields)
	for _, field := range out.MissingFields {
		note := "请补充或核实条件：" + string(field)
		if field == "air_supply" {
			note = "请确认现场有可用压缩空气。"
		} else if strings.HasPrefix(string(field), "guide_profile.") {
			note = "产品选型资料待核实：" + strings.TrimPrefix(string(field), "guide_profile.")
		}
		out.ConfirmationNotes = append(out.ConfirmationNotes, note)
	}
	if len(out.Conflicts) > 0 {
		out.MatchLevel = "no_match"
	} else if len(out.MissingFields) > 0 {
		out.MatchLevel = "needs_confirmation"
	}
	return out
}
func sameURL(a, b *string) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }
