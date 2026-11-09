package guide

import (
	"fmt"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// replyEnvelope contains only the natural-language answer, never nested tool
// arguments or reasoning. Proposal freezes the preceding metadata and replaces
// reply with a nonempty placeholder for the caller's ordinary contract checks.
// Closed describes the reply string, not completion of the enclosing object.
type replyEnvelope struct {
	Proposal []byte
	Text     string
	Closed   bool
}

const (
	replyEnvelopeMaxBytes = 64 * 1024
	replyEnvelopeMaxDepth = 64
)

// inspectReplyEnvelope inspects cumulative model Content. It accepts only a JSON
// root object whose last member is a string named reply. found becomes true as
// soon as that value's opening quote arrives. Incomplete UTF-8, JSON escapes and
// surrogate pairs are withheld until complete; invalid input fails closed.
//
// After reply closes, only whitespace, an optional closing root brace, and more
// whitespace are allowed. Closed therefore does NOT replace final JSON/schema
// validation: a final message missing its closing root brace is still incomplete.
// No state is retained between calls and all allocations are bounded by the
// input byte cap (the synthetic Proposal can add at most three bytes).
func inspectReplyEnvelope(content string) (replyEnvelope, bool, error) {
	if len(content) > replyEnvelopeMaxBytes {
		return replyEnvelope{}, false, fmt.Errorf("reply envelope exceeds %d bytes", replyEnvelopeMaxBytes)
	}
	p := replyEnvelopeReader{content: content}
	p.space()
	if p.pos == len(content) {
		return replyEnvelope{}, false, nil
	}
	if content[p.pos] != '{' {
		return replyEnvelope{}, false, p.invalid("expected root object")
	}
	complete, err := p.object(true, 1)
	if err != nil {
		return replyEnvelope{}, false, err
	}
	if complete {
		p.space()
		if p.pos != len(content) {
			return replyEnvelope{}, false, p.invalid("trailing content")
		}
	}
	return p.envelope, p.found, nil
}

type replyEnvelopeReader struct {
	content  string
	pos      int
	envelope replyEnvelope
	found    bool
}

func (p *replyEnvelopeReader) invalid(reason string) error {
	// Do not copy model content (including metadata/tool text) into error logs.
	return fmt.Errorf("invalid reply envelope at byte %d: %s", p.pos, reason)
}

func (p *replyEnvelopeReader) space() {
	for p.pos < len(p.content) {
		switch p.content[p.pos] {
		case ' ', '\t', '\r', '\n':
			p.pos++
		default:
			return
		}
	}
}

func (p *replyEnvelopeReader) object(root bool, depth int) (bool, error) {
	if depth > replyEnvelopeMaxDepth {
		return false, p.invalid("nesting limit exceeded")
	}
	p.pos++ // opening brace
	p.space()
	if p.pos == len(p.content) {
		return false, nil
	}
	if p.content[p.pos] == '}' {
		p.pos++
		return true, nil
	}
	var keys map[string]struct{}
	if root {
		keys = make(map[string]struct{})
	}
	for {
		p.space()
		if p.pos == len(p.content) {
			return false, nil
		}
		if p.content[p.pos] != '"' {
			return false, p.invalid("expected object key")
		}
		key, complete, err := p.stringValue(root)
		if err != nil || !complete {
			return false, err
		}
		if root {
			if _, duplicate := keys[key]; duplicate {
				return false, p.invalid("duplicate root key")
			}
			keys[key] = struct{}{}
		}
		p.space()
		if p.pos == len(p.content) {
			return false, nil
		}
		if p.content[p.pos] != ':' {
			return false, p.invalid("expected colon")
		}
		p.pos++
		p.space()
		if p.pos == len(p.content) {
			return false, nil
		}
		if root && key == "reply" {
			if p.content[p.pos] != '"' {
				return false, p.invalid("reply must be a string")
			}
			start := p.pos
			text, closed, err := p.stringValue(true)
			if err != nil {
				return false, err
			}
			p.found = true
			p.envelope = replyEnvelope{Proposal: []byte(p.content[:start] + `"_"}`), Text: text, Closed: closed}
			if !closed {
				return false, nil
			}
			p.space()
			if p.pos == len(p.content) {
				return false, nil // reply closed; root close has not arrived yet
			}
			if p.content[p.pos] != '}' {
				return false, p.invalid("reply must be the last root member")
			}
			p.pos++
			return true, nil // the caller checks for non-whitespace after the root
		}
		complete, err = p.value(depth + 1)
		if err != nil || !complete {
			return false, err
		}
		p.space()
		if p.pos == len(p.content) {
			return false, nil
		}
		switch p.content[p.pos] {
		case '}':
			p.pos++
			return true, nil
		case ',':
			p.pos++
		default:
			return false, p.invalid("expected object separator")
		}
	}
}

func (p *replyEnvelopeReader) array(depth int) (bool, error) {
	if depth > replyEnvelopeMaxDepth {
		return false, p.invalid("nesting limit exceeded")
	}
	p.pos++
	p.space()
	if p.pos == len(p.content) {
		return false, nil
	}
	if p.content[p.pos] == ']' {
		p.pos++
		return true, nil
	}
	for {
		complete, err := p.value(depth + 1)
		if err != nil || !complete {
			return false, err
		}
		p.space()
		if p.pos == len(p.content) {
			return false, nil
		}
		switch p.content[p.pos] {
		case ']':
			p.pos++
			return true, nil
		case ',':
			p.pos++
			p.space()
		default:
			return false, p.invalid("expected array separator")
		}
	}
}

func (p *replyEnvelopeReader) value(depth int) (bool, error) {
	if p.pos == len(p.content) {
		return false, nil
	}
	switch p.content[p.pos] {
	case '{':
		return p.object(false, depth)
	case '[':
		return p.array(depth)
	case '"':
		_, complete, err := p.stringValue(false)
		return complete, err
	case 't':
		return p.literal("true")
	case 'f':
		return p.literal("false")
	case 'n':
		return p.literal("null")
	default:
		if p.content[p.pos] == '-' || replyEnvelopeDigit(p.content[p.pos]) {
			return p.number()
		}
		return false, p.invalid("expected JSON value")
	}
}

func (p *replyEnvelopeReader) literal(want string) (bool, error) {
	for i := 0; i < len(want); i++ {
		if p.pos == len(p.content) {
			return false, nil
		}
		if p.content[p.pos] != want[i] {
			return false, p.invalid("invalid literal")
		}
		p.pos++
	}
	return true, nil
}

func replyEnvelopeDigit(c byte) bool { return c >= '0' && c <= '9' }

func (p *replyEnvelopeReader) number() (bool, error) {
	if p.content[p.pos] == '-' {
		p.pos++
		if p.pos == len(p.content) {
			return false, nil
		}
	}
	if p.content[p.pos] == '0' {
		p.pos++
		if p.pos < len(p.content) && replyEnvelopeDigit(p.content[p.pos]) {
			return false, p.invalid("leading zero")
		}
	} else if p.content[p.pos] >= '1' && p.content[p.pos] <= '9' {
		for p.pos < len(p.content) && replyEnvelopeDigit(p.content[p.pos]) {
			p.pos++
		}
	} else {
		return false, p.invalid("invalid number")
	}
	if p.pos < len(p.content) && p.content[p.pos] == '.' {
		p.pos++
		complete, err := p.digits()
		if err != nil || !complete {
			return false, err
		}
	}
	if p.pos < len(p.content) && (p.content[p.pos] == 'e' || p.content[p.pos] == 'E') {
		p.pos++
		if p.pos < len(p.content) && (p.content[p.pos] == '+' || p.content[p.pos] == '-') {
			p.pos++
		}
		return p.digits()
	}
	return true, nil
}

func (p *replyEnvelopeReader) digits() (bool, error) {
	if p.pos == len(p.content) {
		return false, nil
	}
	if !replyEnvelopeDigit(p.content[p.pos]) {
		return false, p.invalid("expected number digit")
	}
	for p.pos < len(p.content) && replyEnvelopeDigit(p.content[p.pos]) {
		p.pos++
	}
	return true, nil
}

// stringValue validates every string, but collects text only for root keys and
// the answer itself. Metadata/tool strings cannot become preview text.
func (p *replyEnvelopeReader) stringValue(collect bool) (string, bool, error) {
	p.pos++ // opening quote
	var text strings.Builder
	for p.pos < len(p.content) {
		c := p.content[p.pos]
		switch {
		case c == '"':
			p.pos++
			return text.String(), true, nil
		case c == '\\':
			p.pos++
			if p.pos == len(p.content) {
				return text.String(), false, nil
			}
			escape := p.content[p.pos]
			p.pos++
			var decoded rune
			switch escape {
			case '"', '\\', '/':
				decoded = rune(escape)
			case 'b':
				decoded = '\b'
			case 'f':
				decoded = '\f'
			case 'n':
				decoded = '\n'
			case 'r':
				decoded = '\r'
			case 't':
				decoded = '\t'
			case 'u':
				first, complete, err := p.hexRune(false)
				if err != nil || !complete {
					return text.String(), false, err
				}
				switch {
				case first >= 0xDC00 && first <= 0xDFFF:
					return "", false, p.invalid("isolated low surrogate")
				case first >= 0xD800 && first <= 0xDBFF:
					if p.pos == len(p.content) {
						return text.String(), false, nil
					}
					if p.content[p.pos] != '\\' {
						return "", false, p.invalid("missing low surrogate escape")
					}
					p.pos++
					if p.pos == len(p.content) {
						return text.String(), false, nil
					}
					if p.content[p.pos] != 'u' {
						return "", false, p.invalid("missing low surrogate escape")
					}
					p.pos++
					second, complete, err := p.hexRune(true)
					if err != nil || !complete {
						return text.String(), false, err
					}
					if second < 0xDC00 || second > 0xDFFF {
						return "", false, p.invalid("invalid low surrogate")
					}
					decoded = utf16.DecodeRune(first, second)
				default:
					decoded = first
				}
			default:
				return "", false, p.invalid("invalid string escape")
			}
			if collect {
				text.WriteRune(decoded)
			}
		case c < 0x20:
			return "", false, p.invalid("unescaped control byte")
		case c < utf8.RuneSelf:
			p.pos++
			if collect {
				text.WriteByte(c)
			}
		default:
			remaining := p.content[p.pos:]
			if !utf8.FullRuneInString(remaining) {
				return text.String(), false, nil
			}
			decoded, size := utf8.DecodeRuneInString(remaining)
			if decoded == utf8.RuneError && size == 1 {
				return "", false, p.invalid("invalid UTF-8")
			}
			p.pos += size
			if collect {
				text.WriteRune(decoded)
			}
		}
	}
	return text.String(), false, nil
}

func (p *replyEnvelopeReader) hexRune(lowSurrogate bool) (rune, bool, error) {
	var value rune
	for i := 0; i < 4; i++ {
		if p.pos == len(p.content) {
			return 0, false, nil
		}
		c := p.content[p.pos]
		var digit rune
		switch {
		case c >= '0' && c <= '9':
			digit = rune(c - '0')
		case c >= 'a' && c <= 'f':
			digit = rune(c-'a') + 10
		case c >= 'A' && c <= 'F':
			digit = rune(c-'A') + 10
		default:
			return 0, false, p.invalid("invalid Unicode escape")
		}
		p.pos++
		value = value*16 + digit
		// Reject impossible scalar prefixes as soon as they are known, rather
		// than waiting for all four hex digits or returning a preview meanwhile.
		if lowSurrogate && (i == 0 && value != 0xD || i == 1 && (value < 0xDC || value > 0xDF)) {
			return 0, false, p.invalid("invalid low surrogate prefix")
		}
		if !lowSurrogate && i == 1 && value >= 0xDC && value <= 0xDF {
			return 0, false, p.invalid("isolated low surrogate prefix")
		}
	}
	return value, true, nil
}
