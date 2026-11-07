package catalog

import (
	"context"
	"net/url"
	"slices"
	"strings"

	"yuanfa-official/backend/internal/contract"
	"yuanfa-official/backend/internal/release"
)

// purchaseURL repeats the publish-time destination boundary at the moment of
// use, including after a deployment allowlist change. It never fetches a URL.
func (s *Service) purchaseURL(raw string) (*url.URL, error) {
	if err := contract.ValidatePurchaseURL(raw, s.hosts); err != nil {
		return nil, err
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, invalid("purchase_options.url")
	}
	path := strings.ToLower(strings.Trim(u.Path, "/"))
	if path == "" || u.Opaque != "" || strings.Contains(u.Path, "\\") || u.Port() != "" && u.Port() != "443" {
		return nil, invalid("purchase_options.url")
	}
	for _, segment := range strings.Split(path, "/") {
		switch segment {
		case ".", "..", "search", "search.htm", "search.html", "redirect", "redirect.htm", "redirect.html", "shop", "store", "view_shop.htm":
			return nil, invalid("purchase_options.url")
		}
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return nil, invalid("purchase_options.url")
	}
	for key := range query {
		switch strings.ToLower(key) {
		case "url", "target", "redirect", "redirect_url", "redirect_uri", "return_url":
			return nil, invalid("purchase_options.url")
		}
	}
	return u, nil
}
func (s *Service) options(releaseID string, p contract.SnapshotProduct) []contract.PublicPurchaseOption {
	out := []contract.PublicPurchaseOption{}
	for _, option := range p.PurchaseOptions {
		if !option.Enabled {
			continue
		}
		target, err := s.purchaseURL(option.URL)
		if err != nil {
			continue
		}
		out = append(out, contract.PublicPurchaseOption{PurchaseOptionID: option.ID, Platform: option.Platform, StoreName: option.StoreName, MatchScope: option.MatchScope, SkuHint: option.SkuHint, DestinationHost: strings.ToLower(target.Hostname()), HandoffURL: "/go/products/" + p.ID + "/" + option.ID + "?release_id=" + url.QueryEscape(releaseID)})
	}
	return out
}

// PurchaseOptions preserves configured channel order. Missing mappings are a
// normal available product with purchase_status=unavailable and an empty array.
func (s *Service) PurchaseOptions(ctx context.Context, h *release.Handle, id string) (contract.PurchaseOptionsResult, error) {
	out := contract.PurchaseOptionsResult{PurchaseOptions: []contract.PublicPurchaseOption{}, PurchaseStatus: "unavailable"}
	p, err := s.get(ctx, h, id)
	if err != nil {
		return out, err
	}
	out.ReleaseID = h.ID()
	out.ProductID = id
	out.PurchaseOptions = slices.Clone(p.PurchaseOptions)
	if len(out.PurchaseOptions) > 0 {
		out.PurchaseStatus = "available"
	}
	return out, nil
}

// Destination resolves one owned enabled channel from the current verified
// release. It intentionally returns the private destination only to the HTTP
// redirect adapter, never to public DTOs, guide tools or a model.
func (s *Service) Destination(ctx context.Context, h *release.Handle, id, optionID string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if _, err := s.view(h); err != nil {
		return "", err
	}
	for _, p := range h.Catalog().Products {
		if p.ID != id || p.Visibility != "listed" {
			continue
		}
		for _, option := range p.PurchaseOptions {
			if option.ID != optionID || !option.Enabled {
				continue
			}
			if _, err := s.purchaseURL(option.URL); err != nil {
				return "", notFound()
			}
			return option.URL, nil
		}
	}
	return "", notFound()
}
