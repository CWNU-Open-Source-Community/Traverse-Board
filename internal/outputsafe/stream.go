package outputsafe

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"cyberagent-workbench/internal/redact"
)

// Stream incrementally removes terminal control traffic while preserving
// printable UTF-8. Keeping parser and UTF-8 state across writes prevents an
// escape sequence or multibyte rune split across pipe reads from leaking into
// model-visible output.
type Stream struct {
	state   parserState
	pending []byte
}

type parserState uint8

const (
	stateText parserState = iota
	stateEscape
	stateCSI
	stateString
	stateStringEscape
)

// Feed sanitizes one output chunk. Secret redaction is intentionally applied
// by the caller after assembling a bounded logical page or complete stream so
// secret-shaped values split across chunks are still recognized.
func (s *Stream) Feed(data []byte) string {
	if len(data) == 0 && len(s.pending) == 0 {
		return ""
	}
	input := make([]byte, 0, len(s.pending)+len(data))
	input = append(input, s.pending...)
	input = append(input, data...)
	s.pending = s.pending[:0]

	var output strings.Builder
	output.Grow(len(input))
	for len(input) > 0 {
		value, size := utf8.DecodeRune(input)
		if value == utf8.RuneError && size == 1 && !utf8.FullRune(input) {
			s.pending = append(s.pending, input...)
			break
		}
		input = input[size:]
		if value == utf8.RuneError && size == 1 {
			value = '\uFFFD'
		}
		s.consume(value, &output)
	}
	return output.String()
}

// Flush completes the stream, replacing an incomplete final UTF-8 sequence
// and discarding an incomplete terminal control sequence.
func (s *Stream) Flush() string {
	if len(s.pending) == 0 {
		return ""
	}
	s.pending = s.pending[:0]
	if s.state != stateText {
		return ""
	}
	return "\uFFFD"
}

func (s *Stream) consume(value rune, output *strings.Builder) {
	switch s.state {
	case stateEscape:
		switch value {
		case '[':
			s.state = stateCSI
		case ']', 'P', 'X', '^', '_':
			s.state = stateString
		default:
			s.state = stateText
		}
		return
	case stateCSI:
		if value >= 0x40 && value <= 0x7e {
			s.state = stateText
		}
		return
	case stateString:
		switch value {
		case '\a', '\u009c':
			s.state = stateText
		case '\x1b':
			s.state = stateStringEscape
		}
		return
	case stateStringEscape:
		if value == '\\' {
			s.state = stateText
		} else if value != '\x1b' {
			s.state = stateString
		}
		return
	}

	switch value {
	case '\x1b':
		s.state = stateEscape
		return
	case '\u0090', '\u0098', '\u009d', '\u009e', '\u009f':
		s.state = stateString
		return
	case '\u009b':
		s.state = stateCSI
		return
	case '\n', '\r', '\t':
		output.WriteRune(value)
		return
	}
	if unicode.IsControl(value) || unicode.In(value, unicode.Cf) {
		return
	}
	output.WriteRune(value)
}

// Sanitize is the one-shot form used for already bounded output.
func Sanitize(data []byte) string {
	stream := &Stream{}
	return redact.String(stream.Feed(data) + stream.Flush())
}
