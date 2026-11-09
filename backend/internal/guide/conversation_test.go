package guide_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"yuanfa-official/backend/internal/contract"
	"yuanfa-official/backend/internal/guide"
)

type conversationalModel struct {
	*consultModel
	direct bool
}

func (m *conversationalModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}
func (m *conversationalModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	if m.direct {
		m.calls.Add(1)
		return schema.AssistantMessage(m.proposal, nil), nil
	}
	return m.consultModel.Generate(ctx, input, opts...)
}

func conversationProposal(t *testing.T, req contract.GuideRequirements, ids []string, intent, reply string) string {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal([]byte(proposal(t, req, ids, nil)), &value); err != nil {
		t.Fatal(err)
	}
	value["intent"], value["reply"] = intent, reply
	b, _ := json.Marshal(value)
	return string(b)
}

func TestConversationClarifiesWithoutForcingCatalogLookup(t *testing.T) {
	f := selectionSetup(t)
	req := contract.UnknownRequirements()
	reply := "你准备做什么活儿？比如家装木工、家具制作，还是保温板固定？"
	m := &conversationalModel{consultModel: &consultModel{proposal: conversationProposal(t, req, []string{}, "clarify", reply)}, direct: true}
	out, err := guide.NewAgent(f.service, m).Consult(context.Background(), f.h, contract.ConsultRequest{ReleaseID: f.h.ID(), Message: "帮我选工具", Requirements: &req})
	if err != nil || out.State != "clarifying" || len(out.Recommendations) != 0 || len(out.MissingFields) != 0 || out.Question != nil || m.calls.Load() != 1 || guide.ResultText(out) != reply {
		t.Fatalf("conversation should ask naturally with no product dump: %+v err=%v", out, err)
	}
	b, _ := json.Marshal(out)
	if err = contract.ValidateJSON("guide.schema.json#/$defs/GuideResult", b); err != nil {
		t.Fatal(err)
	}
}

func TestConversationIntroductionUsesNaturalReplyOnce(t *testing.T) {
	f := selectionSetup(t)
	f.requirements = contract.UnknownRequirements()
	reply := "这款主要用于木工作业。你是想了解它的使用方式，还是比较一下其他型号？"
	out := consult(t, f, &consultModel{proposal: conversationProposal(t, f.requirements, []string{"p_selection"}, "consult", reply)}, "介绍一下SELECTION")
	if out.State != "recommendations" || len(out.Recommendations) != 1 || guide.ResultText(out) != reply {
		t.Fatalf("natural reply was replaced by repeated audit notes: %+v", out)
	}
	if len(out.MissingFields) == 0 || out.Recommendations[0].MatchLevel != "needs_confirmation" {
		t.Fatal("presentation changes must not fabricate verified product facts")
	}
}

func TestConversationCannotBypassProductReadsOrClarificationShape(t *testing.T) {
	f := selectionSetup(t)
	req := contract.UnknownRequirements()
	for _, value := range []string{
		conversationProposal(t, req, []string{"p_selection"}, "consult", "直接推荐这个型号"),
		conversationProposal(t, req, []string{"p_selection"}, "clarify", "你准备做什么？"),
		conversationProposal(t, req, []string{}, "clarify", " "),
		strings.Replace(conversationProposal(t, req, []string{}, "clarify", "问用途"), `"reply":"问用途",`, "", 1),
	} {
		m := &conversationalModel{consultModel: &consultModel{proposal: value}, direct: true}
		out, err := guide.NewAgent(f.service, m).Consult(context.Background(), f.h, contract.ConsultRequest{ReleaseID: f.h.ID(), Message: "介绍产品", Requirements: &req})
		if err != nil || out.State != "unavailable" {
			t.Fatalf("invalid conversation escaped the boundary: %+v err=%v", out, err)
		}
	}
}

func TestConversationCannotOverrideExplicitConflict(t *testing.T) {
	f := selectionSetup(t)
	f.snapshot.Products = f.snapshot.Products[:1]
	f.h = f.load(t, "conversation_conflict")
	f.activate(t, f.h, "conversation_conflict")
	f.requirements.AirSupply = new(false)
	reply := "完全适用，可以放心购买。"
	out := consult(t, f, &consultModel{proposal: conversationProposal(t, f.requirements, []string{"p_selection"}, "consult", reply)}, "没有气源能用这款吗？")
	if out.State != "no_match" || len(out.Conflicts) == 0 || strings.Contains(guide.ResultText(out), reply) {
		t.Fatalf("model prose overrode a verified conflict: %+v", out)
	}
}

func TestConversationBrowsingFallbackAvoidsAuditPreamble(t *testing.T) {
	f := selectionSetup(t)
	f.requirements = contract.UnknownRequirements()
	out := consult(t, f, &consultModel{proposal: proposal(t, f.requirements, []string{"p_selection"}, nil)}, "介绍一下SELECTION")
	for _, repeated := range []string{"仅供介绍", "适配需另行确认", "目前无法确认全部适配条件", "产品动力来源尚无法确认"} {
		if strings.Contains(guide.ResultText(out), repeated) {
			t.Fatalf("ordinary browsing still prints audit boilerplate: %s", guide.ResultText(out))
		}
	}
}

func TestConversationFollowupKeepsPreviouslyStatedConditions(t *testing.T) {
	f := selectionSetup(t)
	req := contract.UnknownRequirements()
	req.Material, req.AirSupply = new("木材"), new(false)
	reply := "是拼装家具，还是固定装饰木线？"
	// A partial model reset must not discard previously stated restrictions.
	m := &conversationalModel{consultModel: &consultModel{proposal: conversationProposal(t, contract.UnknownRequirements(), []string{}, "clarify", reply)}, direct: true}
	out, err := guide.NewAgent(f.service, m).Consult(context.Background(), f.h, contract.ConsultRequest{ReleaseID: f.h.ID(), Message: "家里用的", Requirements: &req, Messages: []contract.GuideMessage{{Role: "user", Content: "木材，没有气源"}, {Role: "assistant", Content: "准备做什么用途？"}}})
	if err != nil || out.State != "clarifying" || out.ConfirmedRequirements.Material == nil || *out.ConfirmedRequirements.Material != "木材" || out.ConfirmedRequirements.AirSupply == nil || *out.ConfirmedRequirements.AirSupply || guide.ResultText(out) != reply {
		t.Fatalf("followup lost earlier conditions: %+v %v", out, err)
	}
}

func TestConversationReplyFollowsVisibleCandidateOrder(t *testing.T) {
	f := selectionSetup(t)
	f.requirements = contract.UnknownRequirements()
	reply := "先看 UNKNOWN，再比较 SELECTION。"
	out := consult(t, f, &consultModel{proposal: conversationProposal(t, f.requirements, []string{"p_unknown", "p_selection"}, "consult", reply)}, "比较两款")
	if len(out.Recommendations) != 2 || out.Recommendations[0].ProductID != "p_unknown" || guide.ResultText(out) != reply {
		t.Fatalf("card ordering disagrees with natural reply: %+v", out)
	}
}

func TestConversationExplicitClearRequiresCurrentReference(t *testing.T) {
	for _, validReference := range []bool{true, false} {
		f := selectionSetup(t)
		req := contract.UnknownRequirements()
		req.Material, req.AirSupply = new("木材"), new(false)
		var value map[string]any
		_ = json.Unmarshal([]byte(conversationProposal(t, contract.UnknownRequirements(), []string{}, "clarify", "好的，你打算做家具还是装饰木作？")), &value)
		reference := "气源不限制了"
		if !validReference {
			reference = "旧消息里撤销气源要求"
		}
		value["cleared_requirements"] = []any{map[string]any{"field": "air_supply", "reference": reference}}
		b, _ := json.Marshal(value)
		m := &conversationalModel{consultModel: &consultModel{proposal: string(b)}, direct: true}
		out, err := guide.NewAgent(f.service, m).Consult(context.Background(), f.h, contract.ConsultRequest{ReleaseID: f.h.ID(), Message: "气源不限制了", Requirements: &req})
		if err != nil {
			t.Fatal(err)
		}
		if validReference && (out.State != "clarifying" || out.ConfirmedRequirements.AirSupply != nil || out.ConfirmedRequirements.Material == nil || *out.ConfirmedRequirements.Material != "木材") {
			t.Fatalf("explicit clearance was lost or cleared an unrelated condition: %+v", out)
		}
		if !validReference && (out.State != "unavailable" || out.ConfirmedRequirements.AirSupply == nil || *out.ConfirmedRequirements.AirSupply) {
			t.Fatal("a fabricated reference cleared a condition")
		}
	}
}

func TestConversationTruncatedCandidatesCannotKeepTheirReply(t *testing.T) {
	f := selectionSetup(t)
	p := f.snapshot.Products[0]
	f.snapshot.Products = nil
	ids := []string{}
	for i := range 4 {
		product := p
		product.ID, product.Slug, product.Model = fmt.Sprintf("p_order%d", i), fmt.Sprintf("order%d", i), fmt.Sprintf("ORDER%d", i)
		product.PurchaseOptions = []contract.PurchaseOption{}
		f.snapshot.Products = append(f.snapshot.Products, product)
		ids = append(ids, product.ID)
	}
	f.h = f.load(t, "conversation_truncation")
	f.activate(t, f.h, "conversation_truncation")
	f.requirements = contract.UnknownRequirements()
	reply := "我最推荐第四款 ORDER3。"
	out := consult(t, f, &consultModel{proposal: conversationProposal(t, f.requirements, ids, "consult", reply)}, "看看几款")
	if len(out.Recommendations) != 3 || strings.Contains(guide.ResultText(out), reply) {
		t.Fatalf("reply still refers to a card omitted from the final result: %+v", out)
	}
}

func TestConversationSearchIncludesPublishedProductsWithIncompleteProfiles(t *testing.T) {
	f := selectionSetup(t)
	f.snapshot.Products = f.snapshot.Products[:1]
	f.snapshot.Products[0].GuideProfile.Reviews = []contract.GuideReview{}
	f.h = f.load(t, "conversation_discovery")
	f.activate(t, f.h, "conversation_discovery")
	f.requirements = contract.UnknownRequirements()
	f.requirements.Material = new("木材")
	m := &consultModel{query: `{"q":"SELECTION","material":"木材"}`, proposal: conversationProposal(t, f.requirements, []string{"p_selection"}, "consult", "可以先看看这款的用途介绍。")}
	consult(t, f, m, "木材用途有哪些工具？")
	var result struct { Items []contract.PublicProduct `json:"items"` }
	if err := json.Unmarshal([]byte(m.toolText), &result); err != nil || len(result.Items) != 1 || result.Items[0].ProductID != "p_selection" {
		t.Fatalf("discovery dropped a published description only because of an incomplete profile: %s %v", m.toolText, err)
	}
}
