package content

import "yuanfa-official/backend/internal/contract"

// DTO conversion keeps shared field types and slice storage; it does not encode
// and decode a full product merely to add or remove snapshot image metadata.
func documentFromSnapshot(p contract.SnapshotProduct) contract.ProductDocument {
	images := make([]contract.ProductImage, len(p.Images))
	for i, img := range p.Images {
		images[i] = contract.ProductImage{MediaID: img.MediaID, Label: img.Label, Description: img.Description}
	}
	return contract.ProductDocument{ID: p.ID, Slug: p.Slug, Kind: p.Kind, Name: p.Name, Model: p.Model, CategoryID: p.CategoryID, Series: p.Series, LegacySeriesIDs: p.LegacySeriesIDs, Summary: p.Summary, Applications: p.Applications, Features: p.Features, Notes: p.Notes, SceneIDs: p.SceneIDs, NailType: p.NailType, FastenerText: p.FastenerText, LongNose: p.LongNose, Blower: p.Blower, Images: images, CoverMediaID: p.CoverMediaID, Visibility: p.Visibility, SortOrder: p.SortOrder, GuideProfile: p.GuideProfile, PurchaseOptions: p.PurchaseOptions, Version: p.Version, UpdatedAt: p.UpdatedAt}
}
func snapshotFromDocument(p contract.ProductDocument) contract.SnapshotProduct {
	return contract.SnapshotProduct{ID: p.ID, Slug: p.Slug, Kind: p.Kind, Name: p.Name, Model: p.Model, CategoryID: p.CategoryID, Series: p.Series, LegacySeriesIDs: p.LegacySeriesIDs, Summary: p.Summary, Applications: p.Applications, Features: p.Features, Notes: p.Notes, SceneIDs: p.SceneIDs, NailType: p.NailType, FastenerText: p.FastenerText, LongNose: p.LongNose, Blower: p.Blower, Images: make([]contract.SnapshotProductImage, len(p.Images)), CoverMediaID: p.CoverMediaID, Visibility: p.Visibility, SortOrder: p.SortOrder, GuideProfile: p.GuideProfile, PurchaseOptions: p.PurchaseOptions, Version: p.Version, UpdatedAt: p.UpdatedAt}
}
