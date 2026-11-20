package guide_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"yuanfa-official/backend/internal/contract"
	"yuanfa-official/backend/internal/guide"
)

type consultModel struct {
	proposal string
	query    string
	toolName string
	toolText string
	block    bool
	direct   bool
	calls    atomic.Int64
	active   atomic.Int64
}

func (m *consultModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}
func (m *consultModel) Generate(ctx context.Context, input []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	m.calls.Add(1)
	m.active.Add(1)
	defer m.active.Add(-1)
	if m.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if m.direct {
		return schema.AssistantMessage(m.proposal, nil), nil
	}
	hasTool := false
	for _, msg := range input {
		if msg.Role == schema.Tool {
			hasTool = true
			m.toolText = msg.Content
		}
	}
	if !hasTool {
		name := m.toolName
		if name == "" {
			name = "search_products"
		}
		query := m.query
		if query == "" {
			query = `{}`
		}
		return schema.AssistantMessage("", []schema.ToolCall{{ID: "read", Function: schema.FunctionCall{Name: name, Arguments: query}}}), nil
	}
	return schema.AssistantMessage(m.proposal, nil), nil
}
func (m *consultModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	out, err := m.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{out}), nil
}
func proposal(t *testing.T, req contract.GuideRequirements, ids []string, reference *string) string {
	t.Helper()
	p := contract.GuideModelProposal{Intent: "consult", Requirements: req, CandidateProductIDs: ids}
	if reference != nil {
		p.Intent = "select"
		p.SelectedProductID = new("p_selection")
		p.SelectionReference = reference
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
func consult(t *testing.T, f *selectionFixture, m *consultModel, message string) contract.GuideResult {
	t.Helper()
	out, err := guide.NewAgent(f.service, m).Consult(context.Background(), f.h, contract.ConsultRequest{ReleaseID: f.h.ID(), Message: message, Requirements: &f.requirements})
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = contract.Decode[contract.GuideResult]("guide.schema.json#/$defs/GuideResult", b); err != nil {
		t.Fatalf("invalid public result: %v %s", err, b)
	}
	t.Logf("GO_GUIDE_RESULT %s", b)
	return out
}
func TestConsultTextSelectCorrectionAndReference(t *testing.T) {
	for _, correction := range []bool{false, true} {
		t.Run(fmt.Sprint(correction), func(t *testing.T) {
			f := selectionSetup(t)
			req := f.requirements
			if correction {
				req.AirSupply = new(false)
			}
			m := &consultModel{proposal: proposal(t, req, []string{}, new("SELECTION"))}
			out := consult(t, f, m, "我就选SELECTION，当前条件已经更正")
			if m.calls.Load() != 2 {
				t.Fatal("text choice must complete one Eino tool loop, no second inference")
			}
			if correction {
				if out.State != "no_match" || out.Selection != nil {
					t.Fatalf("correction: %+v", out)
				}
				assertSupplyConflict(t, out)
			} else {
				if out.State != "selected" || out.Selection.MatchLevel != "needs_confirmation" || out.Selection.PurchaseStatus != "available" || !slices.Equal(out.MissingFields, []contract.MissingField{"air_supply"}) {
					t.Fatalf("selection: %+v", out)
				}
			}
			expected, _ := json.Marshal(req)
			actual, _ := json.Marshal(out.ConfirmedRequirements)
			if string(expected) != string(actual) {
				t.Fatal("all nine merged fields must survive")
			}
		})
	}
	for _, message := range []string{"就第二款", "SELECTIONX 就要它", "先前提到过型号，现在选它"} {
		t.Run(message, func(t *testing.T) {
			f := selectionSetup(t)
			m := &consultModel{proposal: proposal(t, f.requirements, []string{}, new("SELECTION"))}
			out := consult(t, f, m, message)
			if out.State != "clarifying" || out.Selection != nil || out.Question == nil || out.Question.Field != nil || out.Question.Text != "请告诉我具体型号，也可以直接在商品卡片上查看详情。" {
				t.Fatalf("ambiguous reference: %+v", out)
			}
		})
	}
	f := selectionSetup(t)
	m := &consultModel{proposal: proposal(t, f.requirements, []string{"p_selection"}, nil)}
	out := consult(t, f, m, "SELECTION适合什么材料？")
	if out.Selection != nil || out.State != "clarifying" || len(out.Recommendations) != 1 || out.Recommendations[0].MatchLevel != "needs_confirmation" {
		t.Fatalf("question must remain consultation: %+v", out)
	}
}
func TestConsultNoCandidatePublicEvidence(t *testing.T) {
	for _, name := range []string{"empty", "all-conflict", "unknown", "filtered-empty", "mixed", "matched-omitted", "unreviewed-power", "over-100-raw-reasons", "late-unknown"} {
		t.Run(name, func(t *testing.T) {
			f := selectionSetup(t)
			req := f.requirements
			ids := []string{}
			switch name {
			case "empty":
				f.snapshot.Products = []contract.SnapshotProduct{}
			case "all-conflict":
				f.snapshot.Products = f.snapshot.Products[:1]
				req.AirSupply = new(false)
				req.Material = new("钢板")
			case "unknown", "filtered-empty":
				f.snapshot.Products = f.snapshot.Products[2:]
			case "mixed":
				req.AirSupply = new(false)
			case "matched-omitted":
				req.AirSupply = new(true)
			case "unreviewed-power":
				p := f.snapshot.Products[0]
				p.GuideProfile.Reviews = slices.DeleteFunc(slices.Clone(p.GuideProfile.Reviews), func(r contract.GuideReview) bool { return r.Field == "power_source" })
				f.snapshot.Products = []contract.SnapshotProduct{p}
				ids = []string{"p_selection"}
				req.AirSupply = new(false)
			case "over-100-raw-reasons", "late-unknown":
				p := f.snapshot.Products[0]
				f.snapshot.Products = nil
				req.AirSupply = new(false)
				for i := range 110 {
					p.ID = fmt.Sprintf("p_conflict_%03d", i)
					p.Slug = fmt.Sprintf("conflict-%03d", i)
					p.Model = fmt.Sprintf("CONFLICT-%03d", i)
					p.SortOrder = int64(i)
					p.PurchaseOptions = []contract.PurchaseOption{}
					f.snapshot.Products = append(f.snapshot.Products, p)
				}
				if name == "late-unknown" {
					p.ID = "p_last_unknown"
					p.Slug = "last-unknown"
					p.Model = "LAST-UNKNOWN"
					p.SortOrder = 111
					p.GuideProfile.Reviews = []contract.GuideReview{}
					f.snapshot.Products = append(f.snapshot.Products, p)
				}
			}
			f.h = f.load(t, "consult_"+strings.ReplaceAll(name, "-", "_"))
			f.activate(t, f.h, "activate_"+name)
			m := &consultModel{proposal: proposal(t, req, ids, nil)}
			if name == "filtered-empty" {
				m.query = `{"power_source":"manual"}`
			}
			out := consult(t, f, m, "请根据公开证据确认")
			switch name {
			case "empty":
				if out.State != "no_match" || !slices.Equal(out.Conflicts, []string{"当前已发布目录暂无可供导购的上架商品，请联系咨询。"}) {
					t.Fatalf("empty: %+v", out)
				}
			case "all-conflict":
				if out.State != "no_match" || len(out.Conflicts) != 2 || !strings.Contains(out.Conflicts[0], "钢板") || !strings.Contains(out.Conflicts[1], "压缩空气") {
					t.Fatalf("stable reasons: %+v", out)
				}
			case "over-100-raw-reasons":
				if out.State != "no_match" || len(out.Conflicts) != 1 {
					t.Fatalf("stable deduplication: %+v", out)
				}
				assertSupplyConflict(t, out)
			case "matched-omitted":
				if out.State != "unavailable" || out.Selection != nil || len(out.Recommendations) != 0 {
					t.Fatalf("no invented fallback: %+v", out)
				}
			default:
				if out.State != "clarifying" && out.State != "recommendations" {
					t.Fatalf("unknown cannot prove absence: %+v", out)
				}
				if !slices.Contains(out.MissingFields, contract.MissingField("guide_profile.power_source")) {
					t.Fatalf("real power evidence missing: %+v", out)
				}
				if !strings.Contains(guide.ResultText(out), "无法确认") {
					t.Fatal("uncertainty must be visible")
				}
			}
			if out.State != "no_match" && out.Conflicts == nil {
				t.Fatal("explicit [] required")
			}
		})
	}
}
func TestConsultProposalAndToolFailures(t *testing.T) {
	f := selectionSetup(t)
	valid := proposal(t, f.requirements, []string{"p_selection"}, nil)
	for _, raw := range []string{`{`, strings.Replace(valid, `"air_supply":null,`, "", 1), strings.Replace(valid, `"intent":"consult"`, `"intent":"consult","conflicts":["model reason"]`, 1), strings.Replace(valid, `"p_selection"`, `"p_invented"`, 1), strings.Replace(valid, `"home-woodworking"`, `"unknown-scene"`, 1)} {
		m := &consultModel{proposal: raw}
		out := consult(t, f, m, "输入 {不可信数据}")
		if out.State != "unavailable" || out.Selection != nil || len(out.Recommendations) != 0 {
			t.Fatalf("invalid proposal must fail closed: %+v", out)
		}
	}
	for _, m := range []*consultModel{{toolName: "create_candidate"}, {toolName: "get_product", query: `{"product_id":"p_hidden"}`}, {query: `{"release_id":"other"}`}, {query: `{"url":"https://private.test"}`}} {
		m.proposal = valid
		out := consult(t, f, m, "不要公开草稿或访问管理工具")
		if out.State != "unavailable" {
			t.Fatalf("public tool boundary: %+v", out)
		}
	}
}
func TestCancellationDrainsModel(t *testing.T) {
	f := selectionSetup(t)
	m := &consultModel{block: true}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := guide.NewAgent(f.service, m).Consult(ctx, f.h, contract.ConsultRequest{ReleaseID: f.h.ID(), Message: "取消这次咨询"})
		done <- err
	}()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for m.active.Load() == 0 {
		select {
		case <-deadline.C:
			t.Fatal("model never started")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel did not drain")
	}
	if m.active.Load() != 0 || m.calls.Load() != 1 {
		t.Fatal("model still occupied or retried")
	}
}

func TestConsultCandidateEvidenceRanking(t *testing.T) {
	f := selectionSetup(t)
	p := f.snapshot.Products[0]
	f.snapshot.Products = nil
	for i := range 5 {
		item := p
		item.ID = fmt.Sprintf("p_rank%d", i)
		item.Slug = fmt.Sprintf("rank-%d", i)
		item.Model = fmt.Sprintf("RANK%d", i)
		item.SortOrder = []int64{2, 1, 1, 3, 0}[i]
		item.PurchaseOptions = []contract.PurchaseOption{}
		if i == 4 {
			item.GuideProfile.Reviews = []contract.GuideReview{}
		}
		f.snapshot.Products = append(f.snapshot.Products, item)
	}
	f.h = f.load(t, "consult_rank")
	f.activate(t, f.h, "rank_operation")
	req := f.requirements
	req.AirSupply = new(true)
	m := &consultModel{proposal: proposal(t, req, []string{"p_rank4", "p_rank3", "p_rank2", "p_rank0", "p_rank1"}, nil)}
	out := consult(t, f, m, "请比较型号，只咨询不选定")
	got := []string{}
	for _, r := range out.Recommendations {
		got = append(got, r.ProductID)
		if r.MatchLevel != "matched" {
			t.Fatal("unreviewed evidence ranked ahead of reviewed matches")
		}
	}
	if out.State != "recommendations" || !slices.Equal(got, []string{"p_rank1", "p_rank2", "p_rank0"}) || out.Selection != nil {
		t.Fatalf("fact-count/display-order/stable-ID top three: %+v", out)
	}
}

func TestRecommendationConsidersProductsBeyondKeywordSearch(t *testing.T) {
	f := selectionSetup(t)
	p := f.snapshot.Products[0]
	f.snapshot.Products = nil
	for i := range 6 {
		item := p
		item.ID, item.Slug, item.Model = fmt.Sprintf("p_late%d", i), fmt.Sprintf("late-%d", i), fmt.Sprintf("LATE%d", i)
		item.SortOrder = int64(i)
		if i < 5 {
			item.Summary = "家装木工工具"
			item.GuideProfile.Reviews = []contract.GuideReview{}
			item.PurchaseOptions = []contract.PurchaseOption{}
		} else {
			item.Summary, item.Applications, item.Features, item.Notes = "板材钉合", []string{}, []string{}, []string{}
		}
		f.snapshot.Products = append(f.snapshot.Products, item)
	}
	hidden := p
	hidden.ID, hidden.Slug, hidden.Visibility = "p_hidden", "hidden", "unlisted"
	unrelated := p
	unrelated.ID, unrelated.Slug, unrelated.Model = "p_other", "other", "OTHER"
	unrelated.Summary, unrelated.Applications, unrelated.Features, unrelated.Notes = "气动配件", []string{}, []string{}, []string{}
	unrelated.SceneIDs = []contract.SceneID{}
	unrelated.GuideProfile.Reviews = []contract.GuideReview{}
	f.snapshot.Products = append(f.snapshot.Products, hidden, unrelated)
	f.h = f.load(t, "consult_full_catalog")
	f.activate(t, f.h, "full_catalog_operation")
	req := f.requirements
	req.AirSupply = new(true)
	raw := catalogProposal(t, req, "家装木工")
	out := consult(t, f, &consultModel{proposal: raw, query: `{"q":"家装木工"}`}, "家装木工，气源有，推荐合适工具")
	if out.State != "recommendations" || len(out.Recommendations) != 3 || out.Recommendations[0].ProductID != "p_late5" || out.Recommendations[0].MatchLevel != "matched" {
		t.Fatalf("expected the verified product absent from the search on top: %+v", out)
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	var view struct {
		Recommendations []struct {
			PurchaseOptions []contract.PublicPurchaseOption `json:"purchase_options"`
		}
		Additional []struct {
			ProductID string `json:"product_id"`
		} `json:"additional_recommendations"`
	}
	if err := json.Unmarshal(b, &view); err != nil {
		t.Fatal(err)
	}
	if options := view.Recommendations[0].PurchaseOptions; len(options) != 1 || !strings.HasPrefix(options[0].HandoffURL, "/go/products/p_late5/") {
		t.Fatalf("first card is missing its published purchase link: %+v", options)
	}
	if len(view.Additional) != 3 || view.Additional[0].ProductID != "p_late2" || view.Additional[2].ProductID != "p_late4" {
		t.Fatalf("expected remaining related products in order, no hidden or unrelated entries: %+v", view.Additional)
	}
	if strings.Contains(guide.ResultText(out), "模型随意写的型号") {
		t.Fatal("reply must describe the server's shortlist")
	}
}

func TestRecommendationDoesNotOfferKnownConflict(t *testing.T) {
	f := selectionSetup(t)
	req := f.requirements
	req.AirSupply = new(false)
	out := consult(t, f, &consultModel{proposal: catalogProposal(t, req, "家装木工"), direct: true}, "没有气源，推荐家装木工工具")
	for _, card := range out.Recommendations {
		if card.ProductID == "p_selection" {
			t.Fatalf("known pneumatic conflict recommended: %+v", out)
		}
	}
	if out.State == "unavailable" {
		t.Fatal("missing catalog evidence is not service unavailability")
	}
}

func TestRecommendationStreamKeepsModelWrittenProductClaimsPrivate(t *testing.T) {
	f := selectionSetup(t)
	req := f.requirements
	req.AirSupply = new(true)
	m := &consultModel{proposal: catalogProposal(t, req, "家装木工"), direct: true}
	var visible string
	result, err := guide.NewAgent(f.service, m).ConsultStream(context.Background(), f.h, contract.ConsultRequest{
		ReleaseID: f.h.ID(), Requirements: &req, Message: "推荐家装木工工具",
	}, func(delta string) error { visible += delta; return nil })
	if err != nil || result.State != "recommendations" || len(result.Recommendations) == 0 || result.Recommendations[0].ProductID != "p_selection" {
		t.Fatalf("stream did not resolve the catalogue result: %+v, %v", result, err)
	}
	if strings.Contains(visible+guide.ResultText(result), "模型随意写的型号") {
		t.Fatal("unverified provider prose leaked into the response")
	}
}

func TestRecommendationRespectsPublishedMaterialProhibition(t *testing.T) {
	f := selectionSetup(t)
	limited, alternative := f.snapshot.Products[0], f.snapshot.Products[2]
	limited.GuideProfile.Reviews = []contract.GuideReview{}
	limited.Notes = []string{"F30钉枪只打木材、板材；水泥、砖墙、钢材、瓷砖、玻璃一律不用。"}
	alternative.Notes = []string{"可打普通混凝土，打混凝土时避开钢筋。"}
	f.snapshot.Products = []contract.SnapshotProduct{limited, alternative}
	f.h = f.load(t, "consult_published_limits")
	f.activate(t, f.h, "published_limits_operation")
	req := contract.UnknownRequirements()
	req.SceneIDs = []contract.SceneID{"home-woodworking"}
	req.Substrate = new("水泥墙")
	out := consult(t, f, &consultModel{proposal: catalogProposal(t, req, "家装木工"), direct: true}, "固定到水泥墙")
	if len(out.Recommendations) != 1 || out.Recommendations[0].ProductID != "p_unknown" {
		t.Fatalf("explicit prohibition should exclude only the limited tool: %+v", out.Recommendations)
	}
}

func TestRecommendationFindsToolsUsingTheRequestedNailType(t *testing.T) {
	f := selectionSetup(t)
	item := f.snapshot.Products[0]
	item.Kind, item.NailType = "tool", new(contract.NailType("brad"))
	item.Summary, item.Applications, item.Features = "板材钉合", []string{"薄板拼接"}, []string{"轻便机身"}
	item.GuideProfile.Reviews = []contract.GuideReview{}
	f.snapshot.Products = []contract.SnapshotProduct{item}
	f.h = f.load(t, "consult_nail_tools")
	f.activate(t, f.h, "nail_tools_operation")
	req := contract.UnknownRequirements()
	req.NailType = new(contract.NailType("brad"))
	out := consult(t, f, &consultModel{proposal: catalogProposal(t, req, "直排钉"), direct: true}, "推荐能用直排钉的工具")
	if len(out.Recommendations) != 1 || out.Recommendations[0].ProductID != "p_selection" || out.Recommendations[0].MatchLevel != "needs_confirmation" {
		t.Fatalf("public nail type should discover the tool without verifying it: %+v", out.Recommendations)
	}
}

func catalogProposal(t *testing.T, req contract.GuideRequirements, term string) string {
	t.Helper()
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf(`{"intent":"recommend","requirements":%s,"candidate_product_ids":[],"selected_product_id":null,"selection_reference":null,"discovery_terms":[%q],"cleared_requirements":[],"reply":"模型随意写的型号"}`, b, term)
}
