package guide

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestReplyEnvelopeEveryByteBoundary(t *testing.T) {
	metadata := ` {"intent":"consult","metadata":{"reply":"嵌套内容","args":["工具",true,false,null,-12.5e+2]},"candidate_product_ids":[],"reply":`
	pieces := []struct{ encoded, decoded string }{
		{"中", "中"}, {"文", "文"}, {`\n`, "\n"}, {`\\`, "\\"}, {`\"`, `"`}, {`\/`, "/"},
		{`\b`, "\b"}, {`\f`, "\f"}, {`\r`, "\r"}, {`\t`, "\t"}, {`\u4F60`, "你"},
		{`\u597D`, "好"}, {`\uD83D\uDE00`, "😀"}, {"😀", "😀"}, {"结", "结"}, {"束", "束"},
	}
	content := metadata + `"`
	ends := make([]int, len(pieces))
	for i, piece := range pieces {
		content += piece.encoded
		ends[i] = len(content)
	}
	closing := len(content) + 1
	content += `"} `
	for size := 0; size <= len(content); size++ {
		got, found, err := inspectReplyEnvelope(content[:size])
		if err != nil {
			t.Fatalf("prefix %d: %v", size, err)
		}
		wantFound := size >= len(metadata)+1
		if found != wantFound {
			t.Fatalf("prefix %d: found=%v want %v", size, found, wantFound)
		}
		if !found {
			continue
		}
		wantText := ""
		for i, piece := range pieces {
			if size >= ends[i] {
				wantText += piece.decoded
			}
		}
		if got.Text != wantText || got.Closed != (size >= closing) {
			t.Fatalf("prefix %d: text=%q closed=%v; want %q closed=%v", size, got.Text, got.Closed, wantText, size >= closing)
		}
		if !utf8.ValidString(got.Text) || string(got.Proposal) != metadata+`"_"}` || !json.Valid(got.Proposal) {
			t.Fatalf("prefix %d: unsafe text or invalid proposal: %+v", size, got)
		}
	}
}

func TestReplyEnvelopeRootOnlyAndProposal(t *testing.T) {
	for _, input := range []string{
		``, ` `, `{`, `{"reply`, `{"reply"`, `{"reply":`, `{"nested":{"reply":"secret"}}`,
		`{"tool_calls":[{"arguments":{"reply":"tool output"}}],"copy":"\"reply\":\"quoted\""}`,
		`{"nested":[{"reply":"secret"}],"reply":"`,
	} {
		got, found, err := inspectReplyEnvelope(input)
		if err != nil {
			t.Fatalf("%q: %v", input, err)
		}
		wantFound := strings.HasSuffix(input, `,"reply":"`)
		if found != wantFound || got.Text != "" || got.Closed {
			t.Fatalf("%q: unexpected extraction %+v found=%v", input, got, found)
		}
	}
	got, found, err := inspectReplyEnvelope(`{"intent":"clarify","\u0072eply":"你好"}`)
	if err != nil || !found || !got.Closed || got.Text != "你好" {
		t.Fatalf("escaped root key: %+v %v %v", got, found, err)
	}
	var proposal map[string]json.RawMessage
	if err := json.Unmarshal(got.Proposal, &proposal); err != nil {
		t.Fatal(err)
	}
	if string(proposal["intent"]) != `"clarify"` || string(proposal["reply"]) != `"_"` || len(proposal) != 2 {
		t.Fatalf("proposal must contain only preceding metadata and the placeholder: %s", got.Proposal)
	}
	got, found, err = inspectReplyEnvelope(`{"reply":""`)
	if err != nil || !found || !got.Closed || got.Text != "" || string(got.Proposal) != `{"reply":"_"}` {
		t.Fatalf("closed string is independent of root completion: %+v %v %v", got, found, err)
	}
}

func TestReplyEnvelopeIncompleteAndInvalidUnicode(t *testing.T) {
	for _, suffix := range []string{``, `\`, `\u`, `\uD`, `\uD8`, `\uD83`, `\uD83D`, `\uD83D\`, `\uD83D\u`, `\uD83D\uD`, `\uD83D\uDE`, `\uD83D\uDE0`, "\xe4", "\xe4\xb8", "\xf0", "\xf0\x9f", "\xf0\x9f\x98"} {
		got, found, err := inspectReplyEnvelope(`{"reply":"安全` + suffix)
		if err != nil || !found || got.Text != "安全" || got.Closed {
			t.Fatalf("split suffix %q: %+v %v %v", suffix, got, found, err)
		}
	}
	for _, suffix := range []string{`\uDC00`, `\uDFFF`, `\uDC`, `\uDF`, `\uD83D"`, `\uD83Dx`, `\uD83D\n`, `\uD83D\u0`, `\uD83D\uD8`, `\uD83D\u0041`, `\uD83D\uD800`, `\u0g`, `\q`, "\xff", "\xe4x", "\xf0\x80", "\xed\xa0\x80", "\n"} {
		got, found, err := inspectReplyEnvelope(`{"reply":"安全` + suffix)
		if err == nil || found || got.Text != "" || len(got.Proposal) != 0 {
			t.Fatalf("invalid suffix %q must fail closed: %+v %v %v", suffix, got, found, err)
		}
	}
	got, found, err := inspectReplyEnvelope(`{"reply":"\uD834\uDD1E�"}`)
	if err != nil || !found || got.Text != "𝄞�" || !got.Closed {
		t.Fatalf("valid pair and explicit replacement rune: %+v %v %v", got, found, err)
	}
}

func TestReplyEnvelopeRejectsInvalidAndDuplicateRootKeys(t *testing.T) {
	inputs := []string{
		"```json\n", `thinking {"reply":"no"}`, `[]`, `null`, `"reply"`, `{reply:`,
		`{"a":1,"a"`, `{"a":1,"\u0061":2,"reply":"no"}`, `{"reply":"one","reply":"two"}`,
		`{"reply":"one","\u0072eply":`, `{"reply":null}`, `{"reply":{}`, `{"reply":123`,
		`{"a":01,"reply":"no"}`, `{"a":+1}`, `{"a":.1}`, `{"a":1.e2}`, `{"a":1e+}`, `{"a":NaN}`,
		`{"a":truX`, `{"a":falseX`, `{"a":[1,]}`, `{"a":{"b":1,}}`, `{"a":1,,"reply":"no"}`,
		`{"a" 1}`, `{"a":"\uD800","reply":"no"}`, `{"a":"\uDC00","reply":"no"}`,
		`{"reply":"one" garbage`, `{"reply":"one",}`, `{"reply":"one"} extra`, `{"reply":"one"}{}`,
		`{"reply":"one",`, `{"reply":"one","late":null}`, `{"reply":"one","late":{"reply":"not the answer"}}`,
		`{"reply":"one","late":truX`, `{"reply":"one","late":[0,]}`, `{"reply":"one","a":1,"a":2}`,
	}
	for _, input := range inputs {
		got, found, err := inspectReplyEnvelope(input)
		if err == nil || found || got.Text != "" || len(got.Proposal) != 0 || got.Closed {
			t.Errorf("%q must fail closed: %+v %v %v", input, got, found, err)
		}
	}
}

func TestReplyEnvelopeValidIncompleteGrammar(t *testing.T) {
	for _, input := range []string{
		`{"a":-`, `{"a":0.`, `{"a":1e`, `{"a":1E-`, `{"a":t`, `{"a":tru`, `{"a":fals`, `{"a":nu`,
		`{"a":[`, `{"a":[1,`, `{"a":{"b":`, `{"a":{"b":"text`, `{"a":0`, `{"a":[]`,
		`{"a":{},`, `{"a":true, "repl`, `{"a":"\uD83D\uDE`,
	} {
		_, found, err := inspectReplyEnvelope(input)
		if err != nil || found {
			t.Errorf("valid incomplete prefix %q: found=%v error=%v", input, found, err)
		}
	}
	for _, suffix := range []string{``, ` `, "\n\t\r", `}`, " \n} \t\r\n"} {
		got, found, err := inspectReplyEnvelope(`{"reply":"完整"` + suffix)
		if err != nil || !found || got.Text != "完整" || !got.Closed {
			t.Errorf("valid suffix %q: %+v %v %v", suffix, got, found, err)
		}
	}
}

func TestReplyEnvelopeLimits(t *testing.T) {
	const limit = 64 * 1024
	prefix := `{"reply":"`
	atLimit := prefix + strings.Repeat("x", limit-len(prefix))
	got, found, err := inspectReplyEnvelope(atLimit)
	if err != nil || !found || len(got.Text) != limit-len(prefix) || got.Closed {
		t.Fatalf("at byte limit: found=%v text bytes=%d error=%v", found, len(got.Text), err)
	}
	for _, input := range []string{atLimit + "x", strings.Repeat(" ", limit+1), `{"a":` + strings.Repeat("[", 1000)} {
		if _, found, err := inspectReplyEnvelope(input); err == nil || found {
			t.Fatalf("unbounded input accepted: bytes=%d found=%v error=%v", len(input), found, err)
		}
	}
}

func FuzzReplyEnvelope(f *testing.F) {
	for _, seed := range []string{`{"reply":"你好"}`, `{"nested":{"reply":"secret"},"reply":"\uD83D\uDE00`, `{"a":[true,-1.5e+2],"reply":"x"}`, `{"reply":"x","reply":"y"}`, "{\"reply\":\"\xf0\x9f"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		got, found, err := inspectReplyEnvelope(input)
		if err != nil {
			if found || len(got.Proposal) != 0 || got.Text != "" || got.Closed {
				t.Fatal("invalid envelope exposed a preview")
			}
			return
		}
		if found && (!utf8.ValidString(got.Text) || !json.Valid(got.Proposal) || len(got.Proposal) > 64*1024+4) {
			t.Fatal("unsafe preview")
		}
		if json.Valid([]byte(input)) {
			var root map[string]json.RawMessage
			if json.Unmarshal([]byte(input), &root) != nil {
				t.Fatal("accepted non-object root")
			}
			if raw, exists := root["reply"]; exists {
				var want string
				if json.Unmarshal(raw, &want) != nil || !found || !got.Closed || got.Text != want {
					t.Fatal("completed reply differs from the standard JSON decoder")
				}
			}
		}
	})
}
