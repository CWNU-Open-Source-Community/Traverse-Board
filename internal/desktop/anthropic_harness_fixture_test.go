package desktop

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
	"sync"
)

type anthropicHarnessFixture struct {
	mu     sync.Mutex
	system json.RawMessage
	first  json.RawMessage
	nonce  string
}
type anthropicHarnessFixtureMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

func (f *anthropicHarnessFixture) phase(raw []byte) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var body struct {
		System json.RawMessage `json:"system"`
		Tools  []struct {
			Name string `json:"name"`
		} `json:"tools"`
		Messages []json.RawMessage `json:"messages"`
	}
	if json.Unmarshal(raw, &body) != nil {
		return "", "", errors.New("invalid fixture request")
	}
	probe := false
	for _, tool := range body.Tools {
		if tool.Name == "prayu_harness_echo" {
			probe = true
		}
	}
	if !probe {
		return "", "", nil
	}
	if len(body.Tools) != 1 || len(body.Messages) == 0 {
		return "", "", errors.New("probe lost its sole synthetic tool")
	}
	var first anthropicHarnessFixtureMessage
	var prompt string
	if json.Unmarshal(body.Messages[0], &first) != nil || first.Role != "user" || json.Unmarshal(first.Content, &prompt) != nil {
		return "", "", errors.New("probe lost its original user prompt")
	}
	match := regexp.MustCompile(`^Call prayu_harness_echo exactly once with nonce ([0-9a-f]{32})\.$`).FindStringSubmatch(prompt)
	var system string
	if len(match) != 2 || json.Unmarshal(body.System, &system) != nil ||
		!strings.Contains(system, "After receiving its result, return exactly one JSON object with version model_harness_probe.v1") {
		return "", "", errors.New("probe did not carry final acknowledgement instructions from the first request")
	}
	if len(body.Messages) == 1 {
		if f.nonce != "" && (f.nonce != match[1] || !bytes.Equal(f.system, body.System) || !bytes.Equal(f.first, body.Messages[0])) {
			return "", "", errors.New("probe retry changed its original turn")
		}
		f.system = append(json.RawMessage(nil), body.System...)
		f.first = append(json.RawMessage(nil), body.Messages[0]...)
		f.nonce = match[1]
		return "tool_call", f.nonce, nil
	}
	if len(body.Messages) != 3 || f.nonce == "" || match[1] != f.nonce || !bytes.Equal(f.system, body.System) || !bytes.Equal(f.first, body.Messages[0]) {
		return "", "", errors.New("probe continuation changed the original turn")
	}
	var assistant, last anthropicHarnessFixtureMessage
	if json.Unmarshal(body.Messages[1], &assistant) != nil || assistant.Role != "assistant" || json.Unmarshal(body.Messages[2], &last) != nil || last.Role != "user" {
		return "", "", errors.New("probe lost assistant/result roles")
	}
	var calls []struct {
		Type  string          `json:"type"`
		ID    string          `json:"id"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	}
	var arguments struct {
		Nonce string `json:"nonce"`
	}
	if json.Unmarshal(assistant.Content, &calls) != nil || len(calls) != 1 || calls[0].Type != "tool_use" || calls[0].ID != "qualification-tool-1" || calls[0].Name != "prayu_harness_echo" || decodeHarnessFixtureJSON(calls[0].Input, &arguments) != nil || arguments.Nonce != f.nonce {
		return "", "", errors.New("probe continuation lost the accepted synthetic call")
	}
	var results []struct {
		Type    string `json:"type"`
		ID      string `json:"tool_use_id"`
		Content string `json:"content"`
		IsError bool   `json:"is_error"`
	}
	var result struct {
		Version string `json:"version"`
		Status  string `json:"status"`
		Nonce   string `json:"nonce"`
	}
	if json.Unmarshal(last.Content, &results) != nil || len(results) != 1 || results[0].Type != "tool_result" || results[0].ID != calls[0].ID || results[0].IsError || decodeHarnessFixtureJSON([]byte(results[0].Content), &result) != nil || result.Version != "model_harness_probe.v1" || result.Status != "tool_result" || result.Nonce != f.nonce {
		return "", "", errors.New("probe continuation is not exactly one paired tool result without fresh user text")
	}
	return "tool_result", f.nonce, nil
}
func decodeHarnessFixtureJSON(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("fixture JSON contains trailing data")
	}
	return nil
}
