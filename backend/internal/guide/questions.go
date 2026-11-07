package guide

import "yuanfa-official/backend/internal/contract"

// question asks only the first visitor condition reported by Evaluate. Product
// evidence gaps remain in confirmation_notes; they cannot be answered by guessing
// visitor requirements or by turning every selection into a full questionnaire.
func question(missing []contract.MissingField) *contract.GuideQuestion {
	for _, field := range missing {
		var text string
		options := []string{}
		switch field {
		case "scene_ids":
			text = "主要用于什么施工场景？"
		case "material":
			text = "需要固定或处理什么材料？"
		case "substrate":
			text = "要固定到什么基材上？"
		case "nail_type":
			text = "需要使用哪种紧固件？"
		case "fastener_length_mm":
			text = "所需紧固件长度是多少毫米？"
		case "air_supply":
			text = "现场有可用的压缩空气气源吗？"
			options = []string{"有", "没有", "不确定"}
		case "load_bearing":
			text = "这次固定是否用于承重？"
			options = []string{"是", "否", "不确定"}
		case "long_nose":
			text = "是否必须使用长嘴功能？"
			options = []string{"需要", "不要求", "不确定"}
		case "blower":
			text = "是否必须带吹尘功能？"
			options = []string{"需要", "不要求", "不确定"}
		default:
			continue
		}
		f := contract.RequirementField(field)
		return &contract.GuideQuestion{Field: &f, Text: text, Options: options}
	}
	return nil
}
