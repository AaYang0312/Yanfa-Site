package catalog

import (
	"bytes"
	"encoding/json"
	"net/url"
	"slices"
	"strings"
	"unicode"

	"yuanfa-official/backend/internal/contract"
	"yuanfa-official/backend/internal/matching"
	"yuanfa-official/backend/internal/release"
)

// reviewedProfile constructs a separate allowlisted projection. A review must
// still bind the exact fact digest and existing source hashes. content owns
// source-content validation/invalidation before freezing; this reader never
// imports content, media, auth or storage.
func reviewedProfile(g contract.GuideProfile) contract.ReviewedGuideProfile {
	out := contract.ReviewedGuideProfile{SceneIDs: []contract.SceneID{}, Materials: []string{}, Substrates: []string{}, ExcludedMaterials: []string{}, ExcludedSubstrates: []string{}, Limitations: []string{}, ConsultationNotes: []string{}, RequiredRequirements: []contract.RequirementField{}, Sources: []contract.ReviewedSource{}, UnknownFields: []contract.GuideField{}}
	keep := func(field string, value any) bool {
		var buffer bytes.Buffer
		encoder := json.NewEncoder(&buffer)
		encoder.SetEscapeHTML(false)
		if encoder.Encode(value) != nil || bytes.Equal(buffer.Bytes(), []byte("null\n")) {
			out.UnknownFields = append(out.UnknownFields, contract.GuideField(field))
			return false
		}
		hash := release.Hash(factJSON(buffer.Bytes()))
		for _, review := range g.Reviews {
			if string(review.Field) != field || review.FactSHA256 != hash || len(review.SourceSha256s) == 0 {
				continue
			}
			valid := true
			for _, sha := range review.SourceSha256s {
				if !slices.ContainsFunc(g.Sources, func(s contract.GuideSource) bool { return s.SHA256 == sha }) {
					valid = false
					break
				}
			}
			if !valid {
				continue
			}
			for _, source := range g.Sources {
				if !slices.Contains(review.SourceSha256s, source.SHA256) {
					continue
				}
				publicURL := copyPointer(source.PublicURL)
				if publicURL != nil {
					u, err := url.Parse(*publicURL)
					if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
						publicURL = nil
					}
				}
				item := contract.ReviewedSource{Field: review.Field, PublicLabel: source.PublicLabel, PublicURL: publicURL}
				if len(out.Sources) < 100 && !slices.ContainsFunc(out.Sources, func(s contract.ReviewedSource) bool {
					return s.Field == item.Field && s.PublicLabel == item.PublicLabel && samePointer(s.PublicURL, item.PublicURL)
				}) {
					out.Sources = append(out.Sources, item)
				}
			}
			return true
		}
		out.UnknownFields = append(out.UnknownFields, contract.GuideField(field))
		return false
	}
	if keep("scene_ids", g.SceneIDs) {
		out.SceneIDs = slices.Clone(g.SceneIDs)
	}
	if keep("materials", g.Materials) {
		out.Materials = slices.Clone(g.Materials)
	}
	if keep("substrates", g.Substrates) {
		out.Substrates = slices.Clone(g.Substrates)
	}
	if keep("excluded_materials", g.ExcludedMaterials) {
		out.ExcludedMaterials = slices.Clone(g.ExcludedMaterials)
	}
	if keep("excluded_substrates", g.ExcludedSubstrates) {
		out.ExcludedSubstrates = slices.Clone(g.ExcludedSubstrates)
	}
	if keep("power_source", g.PowerSource) {
		out.PowerSource = copyPointer(g.PowerSource)
	}
	if keep("nail_type", g.NailType) {
		out.NailType = copyPointer(g.NailType)
	}
	if keep("fastener_length_mm", g.FastenerLengthMM) {
		out.FastenerLengthMM = copyPointer(g.FastenerLengthMM)
	}
	if keep("load_bearing", g.LoadBearing) {
		out.LoadBearing = copyPointer(g.LoadBearing)
	}
	if keep("long_nose", g.LongNose) {
		out.LongNose = copyPointer(g.LongNose)
	}
	if keep("blower", g.Blower) {
		out.Blower = copyPointer(g.Blower)
	}
	if keep("limitations", g.Limitations) {
		out.Limitations = slices.Clone(g.Limitations)
	}
	if keep("consultation_notes", g.ConsultationNotes) {
		out.ConsultationNotes = slices.Clone(g.ConsultationNotes)
	}
	if keep("required_requirements", g.RequiredRequirements) {
		out.RequiredRequirements = slices.Clone(g.RequiredRequirements)
	}
	slices.Sort(out.UnknownFields)
	return out
}
func copyPointer[T any](v *T) *T {
	if v == nil {
		return nil
	}
	return new(*v)
}
func samePointer[T comparable](a, b *T) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

// Match the integrated content.FactSHA256 canonical UTF-8 convention, including
// literal U+2028/U+2029 while preserving escaped backslash text.
func factJSON(encoded []byte) []byte {
	data := bytes.TrimSuffix(encoded, []byte{'\n'})
	if !bytes.Contains(data, []byte(`\u202`)) {
		return data
	}
	out := make([]byte, 0, len(data))
	for i := 0; i < len(data); {
		if data[i] == '\\' && i+1 < len(data) {
			if i+6 <= len(data) && (string(data[i:i+6]) == `\u2028` || string(data[i:i+6]) == `\u2029`) {
				if data[i+5] == '8' {
					out = append(out, "\u2028"...)
				} else {
					out = append(out, "\u2029"...)
				}
				i += 6
				continue
			}
			out = append(out, data[i:i+2]...)
			i += 2
			continue
		}
		out = append(out, data[i])
		i++
	}
	return out
}

func (s *Service) project(h *release.Handle, p contract.SnapshotProduct) contract.PublicProduct {
	profile := reviewedProfile(p.GuideProfile)
	decision := matching.Evaluate(contract.UnknownRequirements(), profile)
	out := contract.PublicProduct{SchemaVersion: contract.SchemaVersion, ReleaseID: h.ID(), ProductID: p.ID, ProductVersion: p.Version, Slug: p.Slug, Name: p.Name, Model: p.Model, Kind: p.Kind, CategoryID: p.CategoryID, Series: p.Series, LegacySeriesIDs: slices.Clone(p.LegacySeriesIDs), Summary: p.Summary, Applications: slices.Clone(p.Applications), Features: slices.Clone(p.Features), Notes: slices.Clone(p.Notes), SceneIDs: slices.Clone(p.SceneIDs), NailType: copyPointer(p.NailType), FastenerText: p.FastenerText, LongNose: p.LongNose, Blower: p.Blower, DetailURL: "/products/" + p.Slug + "/", Images: make([]contract.PublicImage, 0, len(p.Images)), GuideProfile: profile, MatchLevel: decision.MatchLevel, MatchedFacts: decision.MatchedFacts, MissingFields: decision.MissingFields, PurchaseOptions: s.options(h.ID(), p)}
	image := func(i contract.SnapshotProductImage) {
		ext := i.Format
		if ext == "jpeg" {
			ext = "jpg"
		}
		out.Images = append(out.Images, contract.PublicImage{URL: "/media/" + i.SHA256 + "." + ext, Label: i.Label, Description: i.Description, Width: i.Width, Height: i.Height})
	}
	for _, i := range p.Images {
		if p.CoverMediaID != nil && i.MediaID == *p.CoverMediaID {
			image(i)
		}
	}
	for _, i := range p.Images {
		if p.CoverMediaID == nil || i.MediaID != *p.CoverMediaID {
			image(i)
		}
	}
	return out
}

func normalizeSearch(value string) string {
	return strings.Map(func(r rune) rune {
		if r == '_' || r == '-' || unicode.IsSpace(r) {
			return -1
		}
		return unicode.ToLower(r)
	}, value)
}
