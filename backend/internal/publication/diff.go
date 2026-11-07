package publication

import (
	"reflect"
	"slices"
	"strings"

	"yuanfa-official/backend/internal/contract"
	"yuanfa-official/backend/internal/release"
)

// makeDiff retains field-level values, including channel URL/store/SKU and image
// ordering. It compares the entire frozen workspace, not the current form.
func makeDiff(baseline *release.Handle, next contract.CatalogSnapshot, active *string) contract.CandidateDiff {
	out := contract.CandidateDiff{BaselineReleaseID: active, Changes: []contract.Change{}, AffectedRoutes: []string{}, Warnings: []string{}}
	old := map[string]contract.SnapshotProduct{}
	if baseline != nil {
		for _, p := range baseline.Catalog().Products {
			old[p.ID] = p
		}
	}
	affected := map[string]bool{"index.html": true, "products/index.html": true}
	record := func(id *string, field string, before, after any) {
		var b, a *string
		if before != nil {
			b = new(encode(before))
		}
		if after != nil {
			a = new(encode(after))
		}
		out.Changes = append(out.Changes, contract.Change{ProductID: id, Field: field, BeforeJson: b, AfterJson: a})
	}
	for _, p := range next.Products {
		previous, exists := old[p.ID]
		delete(old, p.ID)
		if !exists {
			record(new(p.ID), "product", nil, p)
			affected["products/"+p.Slug+"/index.html"] = true
		} else {
			value, prior := reflect.ValueOf(p), reflect.ValueOf(previous)
			typ := value.Type()
			for i := range value.NumField() {
				if !reflect.DeepEqual(value.Field(i).Interface(), prior.Field(i).Interface()) {
					record(new(p.ID), strings.Split(typ.Field(i).Tag.Get("json"), ",")[0], prior.Field(i).Interface(), value.Field(i).Interface())
					affected["products/"+p.Slug+"/index.html"] = true
				}
			}
		}
		if p.Visibility == "listed" && p.Kind == "tool" {
			reviewed := map[string]bool{}
			for _, r := range p.GuideProfile.Reviews {
				reviewed[string(r.Field)] = true
			}
			if !reviewed["required_requirements"] {
				out.Warnings = append(out.Warnings, p.ID+": 选型所需条件尚未审核，公开选型只能提示待核实。")
			}
		}
	}
	ids := make([]string, 0, len(old))
	for id := range old {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		p := old[id]
		record(new(id), "product", p, nil)
		affected["products/"+p.Slug+"/index.html"] = true
	}
	if baseline != nil {
		before := baseline.Catalog()
		for _, field := range []struct {
			name          string
			before, after any
		}{{"categories", before.Categories, next.Categories}, {"scenes", before.Scenes, next.Scenes}, {"nail_types", before.NailTypes, next.NailTypes}, {"legacy_series", before.LegacySeries, next.LegacySeries}} {
			if !reflect.DeepEqual(field.before, field.after) {
				record(nil, field.name, field.before, field.after)
			}
		}
	}
	for _, series := range next.LegacySeries {
		affected["products/"+series.Slug+"/index.html"] = true
	}
	for name := range affected {
		out.AffectedRoutes = append(out.AffectedRoutes, name)
	}
	slices.Sort(out.AffectedRoutes)
	return out
}
