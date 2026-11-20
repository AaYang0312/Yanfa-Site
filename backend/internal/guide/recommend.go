package guide

import (
	"cmp"
	"context"
	"slices"
	"strings"

	"yuanfa-official/backend/internal/contract"
	"yuanfa-official/backend/internal/matching"
	"yuanfa-official/backend/internal/release"
)

const (
	featuredRecommendations = 3
	maxRecommendations      = 12
)

type rankedProduct struct {
	index     int
	relevance int
	decision  contract.MatchDecision
}

func (a *Agent) recommend(ctx context.Context, h *release.Handle, p contract.GuideModelProposal, catalog contract.PublicExport) (contract.GuideResult, error) {
	out := a.result(h, p.Requirements)
	terms := discoveryTerms(p.DiscoveryTerms, p.Requirements, catalog.Scenes)
	categories := make(map[contract.CategoryID]string, len(catalog.Categories))
	for _, category := range catalog.Categories {
		categories[category.ID] = category.Name
	}
	ranked := make([]rankedProduct, 0, len(catalog.Products))
	conflicting := 0
	for index, product := range catalog.Products {
		if err := ctx.Err(); err != nil {
			return contract.GuideResult{}, err
		}
		d := matching.Evaluate(p.Requirements, product.GuideProfile)
		if d.MatchLevel == "no_match" {
			conflicting++
			continue
		}
		// Explicit public warnings can disqualify a recommendation, but public
		// prose can never establish that a product is compatible.
		if hasPublishedMaterialProhibition(product.Notes, p.Requirements) {
			continue
		}
		score := productRelevance(p.Requirements, terms, product, categories[product.CategoryID], d.MatchedFacts)
		if score > 0 {
			ranked = append(ranked, rankedProduct{index, score, d})
		}
	}
	if len(ranked) == 0 {
		if conflicting == len(catalog.Products) {
			return a.noCandidates(ctx, h, p.Requirements, catalog.Products)
		}
		out.State = "clarifying"
		out.Reply = new("暂时没有找到与这些条件和用途都相关的型号。可以补充主要作业场景或换个用途描述，也可以联系咨询。")
		return out, nil
	}
	slices.SortStableFunc(ranked, func(x, y rankedProduct) int {
		if x.decision.MatchLevel != y.decision.MatchLevel {
			return cmp.Compare(x.decision.MatchLevel, y.decision.MatchLevel)
		}
		if d := cmp.Compare(y.relevance, x.relevance); d != 0 {
			return d
		}
		return cmp.Compare(len(y.decision.MatchedFacts), len(x.decision.MatchedFacts))
	})
	out.State = "recommendations"
	out.Reply = new("根据你说的用途和条件，先看下面这几款。")
	if ranked[0].decision.MatchLevel == "needs_confirmation" {
		out.Reply = new("下面几款的公开用途与你的需求相关，卡片上列出了还需要核实的条件。")
		out.Question = suitabilityQuestion(p.Requirements, ranked[0].decision.MissingFields)
	}
	for i, item := range ranked[:min(len(ranked), maxRecommendations)] {
		product := catalog.Products[item.index]
		card := recommendationCard(product, item.decision)
		card.RecommendationReason = new(recommendationReason(product, item.decision))
		if i < featuredRecommendations {
			out.Recommendations = append(out.Recommendations, card)
			out.MissingFields = append(out.MissingFields, item.decision.MissingFields...)
		} else {
			out.AdditionalRecommendations = append(out.AdditionalRecommendations, card)
		}
	}
	out.MissingFields = orderedMissing(out.MissingFields)
	return out, nil
}

func recommendationCard(product contract.PublicProduct, d contract.MatchDecision) contract.GuideRecommendation {
	card := contract.GuideRecommendation{
		ProductID: product.ProductID, Model: product.Model, Name: product.Name, DetailURL: product.DetailURL,
		MatchLevel: d.MatchLevel, MatchedFacts: d.MatchedFacts, MissingFields: d.MissingFields,
		ConfirmationNotes: introduction(product), PurchaseOptions: append([]contract.PublicPurchaseOption{}, product.PurchaseOptions...),
	}
	if len(product.Images) > 0 {
		card.Image = &product.Images[0]
	}
	return card
}

func recommendationReason(product contract.PublicProduct, decision contract.MatchDecision) string {
	for _, fact := range decision.MatchedFacts {
		if fact.Field != "power_source" {
			return fact.Text
		}
	}
	for _, note := range introduction(product) {
		if strings.HasPrefix(note, "用途说明：") || strings.HasPrefix(note, "简介：") {
			return note
		}
	}
	if len(decision.MatchedFacts) != 0 {
		return decision.MatchedFacts[0].Text
	}
	return "请查看商品详情并核实适用条件。"
}

func discoveryTerms(input []string, req contract.GuideRequirements, scenes []contract.Scene) []string {
	var terms []string
	add := func(term string) {
		if term = strings.ToLower(strings.TrimSpace(term)); term != "" && !slices.Contains(terms, term) {
			terms = append(terms, term)
		}
	}
	for _, term := range input {
		add(term)
	}
	for _, scene := range scenes {
		if slices.Contains(req.SceneIDs, scene.ID) {
			add(scene.Name)
		}
	}
	return terms
}

func productRelevance(req contract.GuideRequirements, terms []string, p contract.PublicProduct, category string, facts []contract.MatchedFact) int {
	score := 0
	useContext := len(terms) > 0 || len(req.SceneIDs) > 0
	for _, scene := range req.SceneIDs {
		if slices.Contains(p.GuideProfile.SceneIDs, scene) {
			score += 8
		} else if slices.Contains(p.SceneIDs, scene) {
			score += 4
		}
	}
	if len(terms) > 0 {
		title := strings.ToLower(strings.Join([]string{p.Model, p.Name, p.Series, category}, " "))
		prose := strings.ToLower(strings.Join(append([]string{p.Summary}, p.Applications...), " "))
		features := strings.ToLower(strings.Join(p.Features, " "))
		for _, term := range terms {
			switch {
			case strings.Contains(title, term):
				score += 3
			case strings.Contains(prose, term):
				score += 2
			case strings.Contains(features, term):
				score++
			}
		}
	}
	// A shared power source or material alone doesn't make an item relevant to
	// a specified use. For parameter-only requests, matching evidence can lead.
	if (score > 0 || len(req.SceneIDs) == 0) && req.NailType != nil && p.NailType != nil && *req.NailType == *p.NailType {
		score += 4
	}
	if !useContext {
		for _, fact := range facts {
			if fact.Field != "power_source" || req.AirSupply != nil {
				score++
			}
		}
	}
	return score
}
