package catalog

import (
	"slices"

	"yuanfa-official/backend/internal/contract"
)

// cloneProductContent transfers the cached product's mutable content to a
// caller. Decision fields are handled separately: Search already owns its new
// Evaluate result, while Get and Export must copy the cached decision as well.
func cloneProductContent(p contract.PublicProduct) contract.PublicProduct {
	p.LegacySeriesIDs = slices.Clone(p.LegacySeriesIDs)
	p.Applications = slices.Clone(p.Applications)
	p.Features = slices.Clone(p.Features)
	p.Notes = slices.Clone(p.Notes)
	p.SceneIDs = slices.Clone(p.SceneIDs)
	p.NailType = copyPointer(p.NailType)
	p.Images = slices.Clone(p.Images)
	p.GuideProfile = cloneProfile(p.GuideProfile)
	p.PurchaseOptions = slices.Clone(p.PurchaseOptions)
	return p
}

func cloneProduct(p contract.PublicProduct) contract.PublicProduct {
	p = cloneProductContent(p)
	p.MatchedFacts = slices.Clone(p.MatchedFacts)
	for i := range p.MatchedFacts {
		fact := &p.MatchedFacts[i]
		fact.Sources = slices.Clone(fact.Sources)
		for j := range fact.Sources {
			fact.Sources[j].PublicURL = copyPointer(fact.Sources[j].PublicURL)
		}
	}
	p.MissingFields = slices.Clone(p.MissingFields)
	return p
}

func cloneProfile(p contract.ReviewedGuideProfile) contract.ReviewedGuideProfile {
	p.SceneIDs = slices.Clone(p.SceneIDs)
	p.Materials = slices.Clone(p.Materials)
	p.Substrates = slices.Clone(p.Substrates)
	p.ExcludedMaterials = slices.Clone(p.ExcludedMaterials)
	p.ExcludedSubstrates = slices.Clone(p.ExcludedSubstrates)
	p.PowerSource = copyPointer(p.PowerSource)
	p.NailType = copyPointer(p.NailType)
	p.FastenerLengthMM = copyPointer(p.FastenerLengthMM)
	p.LoadBearing = copyPointer(p.LoadBearing)
	p.LongNose = copyPointer(p.LongNose)
	p.Blower = copyPointer(p.Blower)
	p.Limitations = slices.Clone(p.Limitations)
	p.ConsultationNotes = slices.Clone(p.ConsultationNotes)
	p.RequiredRequirements = slices.Clone(p.RequiredRequirements)
	p.Sources = slices.Clone(p.Sources)
	for i := range p.Sources {
		p.Sources[i].PublicURL = copyPointer(p.Sources[i].PublicURL)
	}
	p.UnknownFields = slices.Clone(p.UnknownFields)
	return p
}
