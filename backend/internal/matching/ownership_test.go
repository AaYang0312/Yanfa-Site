package matching_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"yuanfa-official/backend/internal/contract"
	"yuanfa-official/backend/internal/matching"
)

func TestEvaluateResultOwnsSourceURLs(t *testing.T) {
	p := profile()
	sourceURL := new("https://docs.example.test/reviewed-fact")
	p.Sources = []contract.ReviewedSource{
		{Field: "materials", PublicLabel: "共享输入URL的材料来源", PublicURL: sourceURL},
		{Field: "materials", PublicLabel: "共享输入URL的材料来源", PublicURL: sourceURL},
		{Field: "power_source", PublicLabel: "共享输入URL的动力来源", PublicURL: sourceURL},
		{Field: "nail_type", PublicLabel: "仅标签来源", PublicURL: nil},
	}
	serialize := func(value any) []byte {
		t.Helper()
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	first := matching.Evaluate(requirements(), p)
	if first.MatchLevel != "matched" {
		t.Fatalf("reviewed requirements failed: %+v", first)
	}
	firstBytes := serialize(first)
	*sourceURL = "https://docs.example.test/input-edited"
	if !bytes.Equal(serialize(first), firstBytes) {
		t.Fatal("editing input URL changed an existing result")
	}
	inputBytes := serialize(p)
	second := matching.Evaluate(requirements(), p)
	secondBytes := serialize(second)
	for i := range first.MatchedFacts {
		fact := &first.MatchedFacts[i]
		fact.Text = "consumer fact"
		for j := range fact.Sources {
			source := &fact.Sources[j]
			source.PublicLabel = "consumer source"
			if source.PublicURL != nil {
				*source.PublicURL = "https://unmapped.invalid/consumer-source"
			}
		}
	}
	if !bytes.Equal(serialize(p), inputBytes) {
		t.Fatal("editing result source changed the original profile")
	}
	if !bytes.Equal(serialize(second), secondBytes) {
		t.Fatal("editing one result changed another result")
	}
	for _, fact := range second.MatchedFacts {
		if fact.Field == "nail_type" {
			if fact.Sources[0].PublicURL != nil {
				t.Fatal("null public source URL changed")
			}
		}
	}
}
