package llm

import (
	"bufio"
	"context"
	"errors"
	"io"
	"strings"
)

var errProviderSSEEventLimit = errors.New("provider SSE event exceeds its limit")

// readProviderSSE frames the Responses and Anthropic data streams. The adapter
// owns completion markers, tool accumulation and replay. Returning false from
// consume stops reading; plain EOF is returned so the adapter can reject an
// incomplete protocol. Complete frames take precedence over a buffered read
// error, while an unterminated tail retains the read error.
func readProviderSSE(ctx context.Context, body io.Reader, consume func(string) bool) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), maxOpenAIStreamLineBytes)
	lines := providerStreamLines{}
	scanner.Split(lines.split)
	dataLines := make([]string, 0, 1)
	eventSize := providerSSEEventSize{}
	flush := func() bool {
		if len(dataLines) == 0 {
			return true
		}
		payload := strings.Join(dataLines, "\n")
		clear(dataLines)
		dataLines = dataLines[:0]
		eventSize = providerSSEEventSize{}
		return consume(payload)
	}
	for scanner.Scan() {
		if err := scanner.Err(); err != nil && !lines.terminated {
			return err
		}
		line := scanner.Text()
		if line == "" {
			if !flush() {
				return nil
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			part := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if !eventSize.append(len(part)) {
				return errProviderSSEEventLimit
			}
			dataLines = append(dataLines, part)
		}
	}
	if ctx.Err() != nil {
		return nil
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if !flush() {
		return nil
	}
	return io.EOF
}
