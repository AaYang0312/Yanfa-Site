package matching

import (
	"strings"

	"yuanfa-official/backend/internal/contract"
)

// MissingLabel presents the evidence gap without exposing contract field names.
// Structured missing_fields remain unchanged for existing consumers.
func MissingLabel(field contract.MissingField) string {
	name := strings.TrimPrefix(string(field), "guide_profile.")
	label := map[string]string{
		"scene_ids": "使用场景", "material": "材料", "materials": "适用材料",
		"substrate": "基材", "substrates": "适用基材", "excluded_materials": "不适用材料",
		"excluded_substrates": "不适用基材", "nail_type": "紧固件类型",
		"fastener_length_mm": "紧固件长度", "power_source": "动力来源", "air_supply": "现场气源",
		"load_bearing": "承重条件", "long_nose": "长嘴功能", "blower": "吹尘功能",
		"required_requirements": "适配所需条件", "limitations": "使用限制", "consultation_notes": "使用说明",
	}[name]
	if label == "" {
		label = "适配资料"
	}
	if strings.HasPrefix(string(field), "guide_profile.") {
		return "产品" + label
	}
	return label
}
