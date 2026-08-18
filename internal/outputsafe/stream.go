package outputsafe

import (
	"regexp"
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

const maxRedactingStreamLineBytes = 64 * 1024

var (
	privateKeyBegin        = regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)
	privateKeyEnd          = regexp.MustCompile(`-----END [A-Z ]*PRIVATE KEY-----`)
	presentationWhitespace = strings.NewReplacer("\r", "", "\t", "    ")
)

// RedactingStream combines terminal-control parsing with bounded logical-line
// buffering and secret redaction. It prevents a token or PEM block split
// across pipe reads from being persisted or exposed before it can be
// recognized. An unterminated oversized line is replaced instead of retained.
type RedactingStream struct {
	controls        Stream
	pending         string
	privateKey      bool
	discardOversize bool
	discardTail     string
}

func (s *RedactingStream) Feed(data []byte) string {
	return s.consume(s.controls.Feed(data), false)
}

func (s *RedactingStream) Flush() string {
	return s.consume(s.controls.Flush(), true)
}

func (s *RedactingStream) consume(value string, force bool) string {
	// Preserve only LF as structural terminal whitespace. Carriage returns can
	// rewrite already rendered text, while tabs can create misleading visual
	// alignment; normalize them before any content is persisted.
	value = presentationWhitespace.Replace(value)
	s.pending += value
	var output strings.Builder
	for {
		if s.discardOversize {
			newline := strings.IndexByte(s.pending, '\n')
			if newline < 0 {
				s.observeDiscarded(s.pending)
				s.pending = ""
				if force {
					s.discardOversize = false
					s.discardTail = ""
				}
				break
			}
			s.observeDiscarded(s.pending[:newline+1])
			s.pending = s.pending[newline+1:]
			s.discardOversize = false
			s.discardTail = ""
			output.WriteByte('\n')
			continue
		}

		newline := strings.IndexByte(s.pending, '\n')
		if newline >= 0 {
			line := s.pending[:newline+1]
			s.pending = s.pending[newline+1:]
			if len([]byte(line)) > maxRedactingStreamLineBytes {
				s.observeDiscarded(line)
				output.WriteString("[REDACTED:oversized-line]\n")
				continue
			}
			output.WriteString(s.redactLine(line))
			continue
		}
		if !force {
			if len([]byte(s.pending)) > maxRedactingStreamLineBytes {
				s.observeDiscarded(s.pending)
				s.pending = ""
				s.discardOversize = true
				output.WriteString("[REDACTED:oversized-line]")
			}
			break
		}
		if s.pending != "" {
			if len([]byte(s.pending)) > maxRedactingStreamLineBytes {
				s.observeDiscarded(s.pending)
				output.WriteString("[REDACTED:oversized-line]")
			} else {
				output.WriteString(s.redactLine(s.pending))
			}
			s.pending = ""
		}
		break
	}
	return output.String()
}

func (s *RedactingStream) redactLine(value string) string {
	if s.privateKey {
		if location := privateKeyEnd.FindStringIndex(value); location != nil {
			s.privateKey = false
			return redact.String(value[location[1]:])
		}
		return ""
	}
	location := privateKeyBegin.FindStringIndex(value)
	if location == nil {
		return redact.String(value)
	}
	if privateKeyEnd.FindStringIndex(value[location[1]:]) != nil {
		return redact.String(value)
	}
	s.privateKey = true
	suffix := ""
	if strings.HasSuffix(value, "\n") {
		suffix = "\n"
	}
	return redact.String(value[:location[0]]) + "[REDACTED:private-key]" + suffix
}

func (s *RedactingStream) observeDiscarded(value string) {
	const markerCarryRunes = 96
	combined := s.discardTail + value
	for combined != "" {
		if s.privateKey {
			location := privateKeyEnd.FindStringIndex(combined)
			if location == nil {
				break
			}
			s.privateKey = false
			combined = combined[location[1]:]
			continue
		}
		location := privateKeyBegin.FindStringIndex(combined)
		if location == nil {
			break
		}
		s.privateKey = true
		combined = combined[location[1]:]
	}
	runes := []rune(s.discardTail + value)
	if len(runes) > markerCarryRunes {
		runes = runes[len(runes)-markerCarryRunes:]
	}
	s.discardTail = string(runes)
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
