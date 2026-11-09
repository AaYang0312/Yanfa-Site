package guide

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/cloudwego/eino/schema"
	"yuanfa-official/backend/internal/contract"
	"yuanfa-official/backend/internal/release"
)

// replyPreview keeps the provider envelope private. Metadata is checked once
// before the first visible token; the complete proposal is checked again by
// Consult before any cards or conversation state can be committed.
type replyPreview struct {
	agent    *Agent
	ctx      context.Context
	handle   *release.Handle
	request  contract.ConsultRequest
	counts   *runCounts
	emit     func(string) error
	content  string
	emitted  string
	metadata string
	checked  bool
	allowed  bool
	invalid  bool
}

func (p *replyPreview) reset() {
	if p.emitted != "" {
		p.invalid = true
	}
	p.content, p.checked, p.allowed = "", false, false
}

func (p *replyPreview) append(chunk *schema.Message) error {
	if chunk == nil {
		return nil
	}
	if len(chunk.ToolCalls) != 0 {
		p.checked, p.allowed = true, false
		if p.emitted != "" {
			return errors.New("tool call after reply preview")
		}
	}
	if chunk.Content == "" {
		return nil
	}
	if len(p.content)+len(chunk.Content) > 64*1024 {
		return errors.New("guide proposal exceeds limit")
	}
	p.content += chunk.Content
	if p.invalid || (p.checked && !p.allowed) {
		return nil
	}
	envelope, ready, err := inspectReplyEnvelope(p.content)
	if err != nil {
		if p.emitted != "" {
			return err
		}
		// An intermediate assistant turn may introduce a tool call in prose.
		// Keep it private and let Eino consume the complete turn normally.
		p.checked, p.allowed = true, false
		return nil
	}
	if !ready {
		return nil
	}
	if !p.checked {
		p.checked = true
		// Clearances are optional in the schema for old JSON clients, but must
		// precede reply for streaming so they cannot change approved metadata.
		var fields map[string]json.RawMessage
		if json.Unmarshal(envelope.Proposal, &fields) != nil {
			return nil
		}
		if _, present := fields["cleared_requirements"]; !present {
			return nil
		}
		proposal, e := contract.Decode[contract.GuideModelProposal]("guide.schema.json#/$defs/GuideModelProposal", envelope.Proposal)
		if e != nil {
			return nil
		}
		if proposal.Intent != "clarify" && p.counts.tools.Load() == 0 {
			return nil
		}
		proposal.Requirements, e = mergeConversationRequirements(p.request, proposal)
		if e != nil || validateScenes(p.handle, proposal.Requirements) != nil {
			return nil
		}
		out, e := p.agent.resolveProposal(p.ctx, p.handle, p.request, proposal)
		if e != nil {
			return e
		}
		if out.Reply == nil || out.State == "unavailable" || out.State == "no_match" || out.State == "selected" {
			return nil
		}
		p.metadata = proposalMetadata(fields)
		p.allowed = true
	}
	if !p.allowed {
		return nil
	}
	// Hold trailing whitespace until another token arrives, matching x-trim
	// at the final contract boundary without ever retracting visible text.
	text := strings.TrimSpace(envelope.Text)
	if !strings.HasPrefix(text, p.emitted) {
		return errors.New("reply preview changed")
	}
	if len(text) > len(p.emitted) {
		if err = p.emit(text[len(p.emitted):]); err != nil {
			return err
		}
		p.emitted = text
	}
	return nil
}

func proposalMetadata(fields map[string]json.RawMessage) string {
	delete(fields, "reply")
	data, _ := json.Marshal(fields)
	return string(data)
}

func (p *replyPreview) matches(content string, result contract.GuideResult) bool {
	if p.invalid {
		return false
	}
	if p.emitted == "" {
		return true
	}
	envelope, ready, err := inspectReplyEnvelope(content)
	if err != nil || !ready || !envelope.Closed {
		return false
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(content), &fields) != nil || proposalMetadata(fields) != p.metadata {
		return false
	}
	return result.Reply != nil && strings.TrimSpace(envelope.Text) == *result.Reply && strings.HasPrefix(ResultText(result), p.emitted)
}
