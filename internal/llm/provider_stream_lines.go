package llm

import "bufio"

// Scanner may buffer complete lines and an unterminated tail from a single
// Read returning both bytes and an error. Preserve complete frames in order;
// only the tail must defer to the read failure instead of its JSON parser.
type providerStreamLines struct{ terminated bool }

func (s *providerStreamLines) split(data []byte, atEOF bool) (int, []byte, error) {
	advance, token, err := bufio.ScanLines(data, atEOF)
	if token != nil {
		s.terminated = advance > 0 && data[advance-1] == '\n'
	}
	return advance, token, err
}
