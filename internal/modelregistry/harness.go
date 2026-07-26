package modelregistry

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"cyberagent-workbench/internal/llm"
)

const (
	HarnessQualificationProtocolVersion = "model_harness_qualification.v1"
	HarnessProbeProtocolVersion         = "model_harness_probe.v1"
	HarnessQualificationTimeout         = 30 * time.Second
	HarnessQualificationTTL             = 7 * 24 * time.Hour
)

const (
	HarnessDiagnosticQualified    = "qualified"
	HarnessDiagnosticIncompatible = "incompatible"
	HarnessDiagnosticUnreachable  = "unreachable"
)

const (
	maxHarnessProbeChunks = 256
	maxHarnessProbeBytes  = 8 * 1024
	maxHarnessRecordBytes = 4 * 1024
)

type HarnessAvailability struct {
	ProtocolVersion        string
	Model                  string
	TransportProtocol      string
	ToolStrategy           string
	JSONStrategy           string
	QualificationStatus    string
	ToolCallsQualified     bool
	ToolResultsQualified   bool
	StrictJSONQualified    bool
	StreamingQualified     bool
	RootEligible           bool
	StructuredJSONEligible bool
	QualifiedAt            string
	ExpiresAt              string
}

type HarnessQualificationResult struct {
	ProtocolVersion         string
	Provider                string
	Model                   string
	Status                  string
	Outcome                 string
	Retryable               bool
	NetworkRequestAttempted bool
	ModelCalls              int
	SyntheticToolCalls      int
	ToolExecuted            bool
	ResponseContentReturned bool
	DurationMillis          int64
	Harness                 HarnessAvailability
}

type persistedHarnessQualification struct {
	Version              string `json:"version"`
	Provider             string `json:"provider"`
	Model                string `json:"model"`
	BindingDigest        string `json:"binding_digest"`
	ToolCallsQualified   bool   `json:"tool_calls_qualified"`
	ToolResultsQualified bool   `json:"tool_results_qualified"`
	StrictJSONQualified  bool   `json:"strict_json_qualified"`
	StreamingQualified   bool   `json:"streaming_qualified"`
	QualifiedAt          string `json:"qualified_at"`
	ExpiresAt            string `json:"expires_at"`
}

type harnessProbeResponse struct {
	Version string `json:"version"`
	Status  string `json:"status"`
	Nonce   string `json:"nonce"`
}

func (r *Registry) QualifyHarness(ctx context.Context, writer RouteSettingWriter,
	provider string, model string,
) (HarnessQualificationResult, error) {
	if r == nil || r.router == nil {
		return HarnessQualificationResult{}, errors.New("model registry is unavailable")
	}
	if ctx == nil || writer == nil {
		return HarnessQualificationResult{},
			errors.New("model Harness qualification dependencies are required")
	}
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	if !validAvailabilityIdentifier(provider, maxPublicProviderNameBytes) ||
		!validAvailabilityIdentifier(model, maxPublicModelNameBytes) ||
		!r.providerModelAvailable(provider, model) {
		return HarnessQualificationResult{},
			errors.New("model Harness qualification Provider model is unavailable")
	}
	r.qualificationMu.Lock()
	defer r.qualificationMu.Unlock()
	ref := llm.ModelRef{Provider: provider, Model: model}
	base, err := r.router.HarnessProfile(ref)
	if err != nil {
		return HarnessQualificationResult{}, err
	}
	result := HarnessQualificationResult{
		ProtocolVersion: HarnessQualificationProtocolVersion,
		Provider:        provider, Model: model,
		Status:                  HarnessDiagnosticIncompatible,
		NetworkRequestAttempted: r.providerNetworkRequired(provider),
		ToolExecuted:            false, ResponseContentReturned: false,
		Harness: harnessAvailability(model, base),
	}
	if rootHarnessReady(base) {
		result.Status = HarnessDiagnosticQualified
		result.Outcome = string(llm.OutcomeSuccess)
		return result, nil
	}
	if base.ToolStrategy != llm.HarnessToolStrategyNative ||
		base.JSONStrategy == llm.HarnessJSONStrategyNone {
		result.Outcome = string(llm.OutcomeInvalidResponse)
		return result, nil
	}
	qualificationCtx, cancel := context.WithTimeout(ctx, HarnessQualificationTimeout)
	defer cancel()
	started := time.Now()
	qualification, modelCalls, syntheticCalls, probeErr := r.probeHarness(
		qualificationCtx, ref, base)
	result.DurationMillis = time.Since(started).Milliseconds()
	if result.DurationMillis < 0 {
		result.DurationMillis = 0
	}
	result.ModelCalls = modelCalls
	result.SyntheticToolCalls = syntheticCalls
	if probeErr != nil {
		providerErr := llm.NormalizeProviderError(provider, probeErr)
		result.Outcome = string(providerErr.Kind)
		result.Retryable = providerErr.Kind.Retryable()
		if providerErr.Kind != llm.OutcomeInvalidResponse {
			result.Status = HarnessDiagnosticUnreachable
		}
		return result, nil
	}

	record := persistedHarnessRecord(provider, model, qualification)
	encoded, err := json.Marshal(record)
	if err != nil {
		return HarnessQualificationResult{}, err
	}
	if len(encoded) > maxHarnessRecordBytes {
		return HarnessQualificationResult{}, errors.New("model Harness qualification record is too large")
	}
	r.routeMu.Lock()
	defer r.routeMu.Unlock()
	current, err := r.router.HarnessProfile(ref)
	if err != nil || current.BindingDigest != base.BindingDigest {
		return HarnessQualificationResult{}, errors.New(
			"model Harness binding changed during qualification")
	}
	if err := writer.SetProviderSetting(ctx, harnessQualificationSettingKey(provider, model),
		string(encoded)); err != nil {
		return HarnessQualificationResult{}, fmt.Errorf(
			"persist model Harness qualification: %w", err)
	}
	if err := r.router.SetHarnessQualification(ref, qualification); err != nil {
		return HarnessQualificationResult{}, err
	}
	r.mu.Lock()
	r.generation++
	r.mu.Unlock()
	verified, err := r.router.HarnessProfile(ref)
	if err != nil {
		return HarnessQualificationResult{}, err
	}
	result.Status = HarnessDiagnosticQualified
	result.Outcome = string(llm.OutcomeSuccess)
	result.Retryable = false
	result.Harness = harnessAvailability(model, verified)
	return result, nil
}

func (r *Registry) probeHarness(ctx context.Context, ref llm.ModelRef,
	base llm.ModelHarness,
) (llm.HarnessQualification, int, int, error) {
	nonceBytes := make([]byte, 16)
	if _, err := rand.Read(nonceBytes); err != nil {
		return llm.HarnessQualification{}, 0, 0, err
	}
	nonce := hex.EncodeToString(nonceBytes)
	schema := json.RawMessage(`{"type":"object","additionalProperties":false,"required":["nonce"],"properties":{"nonce":{"type":"string"}}}`)
	tool := llm.ToolSpec{Name: "prayu_harness_echo",
		Description: "Return the supplied qualification nonce without side effects.",
		Parameters:  schema}
	first := llm.ChatRequest{
		Messages: []llm.Message{
			{Role: "system", Content: "Prayu model Harness qualification. Use only the supplied synthetic tool. Do not answer with text."},
			{Role: "user", Content: "Call prayu_harness_echo exactly once with nonce " + nonce + "."},
		},
		Tools: []llm.ToolSpec{tool}, MaxTokens: 96,
		Metadata: map[string]string{
			"purpose": "model_harness_qualification",
			"phase":   "tool_call",
		},
	}
	firstResponse, err := collectHarnessProbeStream(ctx, r.router, ref, first)
	if err != nil {
		return llm.HarnessQualification{}, 1, 0, err
	}
	if strings.TrimSpace(firstResponse.Text) != "" || len(firstResponse.ToolCalls) != 1 ||
		firstResponse.ToolCalls[0].Name != tool.Name {
		return llm.HarnessQualification{}, 1, len(firstResponse.ToolCalls),
			llm.NewProviderError(llm.OutcomeInvalidResponse, ref.Provider,
				"model Harness probe did not return exactly one synthetic tool call", nil)
	}
	var arguments struct {
		Nonce string `json:"nonce"`
	}
	if err := decodeExactJSON(firstResponse.ToolCalls[0].Arguments, &arguments); err != nil ||
		arguments.Nonce != nonce {
		return llm.HarnessQualification{}, 1, 1,
			llm.NewProviderError(llm.OutcomeInvalidResponse, ref.Provider,
				"model Harness probe returned invalid synthetic tool arguments", err)
	}
	resultJSON, err := json.Marshal(harnessProbeResponse{
		Version: HarnessProbeProtocolVersion, Status: "tool_result", Nonce: nonce,
	})
	if err != nil {
		return llm.HarnessQualification{}, 1, 1, err
	}
	second := llm.ChatRequest{
		Messages: append(append([]llm.Message(nil), first.Messages...),
			llm.Message{Role: "assistant", ToolCalls: firstResponse.ToolCalls},
			llm.Message{Role: "user",
				Content: "Return exactly one JSON object with version model_harness_probe.v1, status ok, and the same nonce. Do not call a tool.",
				ToolResults: []llm.ToolResult{{
					ToolCallID: firstResponse.ToolCalls[0].ID, Content: string(resultJSON),
				}}}),
		Tools: []llm.ToolSpec{tool}, MaxTokens: 96,
		JSONMode: base.JSONStrategy == llm.HarnessJSONStrategyNative,
		Metadata: map[string]string{
			"purpose": "model_harness_qualification",
			"phase":   "tool_result_and_json",
		},
	}
	secondResponse, err := collectHarnessProbeStream(ctx, r.router, ref, second)
	if err != nil {
		return llm.HarnessQualification{}, 2, 1, err
	}
	if len(secondResponse.ToolCalls) != 0 {
		return llm.HarnessQualification{}, 2, 1 + len(secondResponse.ToolCalls),
			llm.NewProviderError(llm.OutcomeInvalidResponse, ref.Provider,
				"model Harness probe called a tool after receiving its synthetic result", nil)
	}
	var final harnessProbeResponse
	if err := decodeExactJSON([]byte(secondResponse.Text), &final); err != nil ||
		final.Version != HarnessProbeProtocolVersion || final.Status != "ok" ||
		final.Nonce != nonce {
		return llm.HarnessQualification{}, 2, 1,
			llm.NewProviderError(llm.OutcomeInvalidResponse, ref.Provider,
				"model Harness probe did not return the exact strict JSON acknowledgement", err)
	}
	now := time.Now().UTC()
	return llm.HarnessQualification{
		ProtocolVersion:    llm.ModelHarnessProtocolVersion,
		BindingDigest:      base.BindingDigest,
		ToolCallsQualified: true, ToolResultsQualified: true,
		StrictJSONQualified: true, StreamingQualified: true,
		QualifiedAt: now, ExpiresAt: now.Add(HarnessQualificationTTL),
	}, 2, 1, nil
}

func collectHarnessProbeStream(ctx context.Context, router *llm.Router, ref llm.ModelRef,
	request llm.ChatRequest,
) (*llm.ChatResponse, error) {
	chunks, err := router.StreamChatModelRef(ctx, ref, request)
	if err != nil {
		return nil, err
	}
	var text strings.Builder
	for count := 1; count <= maxHarnessProbeChunks; count++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case chunk, ok := <-chunks:
			if !ok {
				return nil, llm.NewProviderError(llm.OutcomeInvalidResponse,
					ref.Provider, "model Harness probe stream ended without a final chunk", nil)
			}
			if chunk.Err != nil {
				return nil, chunk.Err
			}
			if len(chunk.Text) > maxHarnessProbeBytes-text.Len() {
				return nil, llm.NewProviderError(llm.OutcomeInvalidResponse,
					ref.Provider, "model Harness probe response exceeded its byte limit", nil)
			}
			text.WriteString(chunk.Text)
			if !chunk.Done {
				if len(chunk.ToolCalls) != 0 {
					return nil, llm.NewProviderError(llm.OutcomeInvalidResponse,
						ref.Provider, "model Harness probe streamed a non-final tool call", nil)
				}
				continue
			}
			if chunk.Usage == nil || chunk.Usage.Validate() != nil {
				return nil, llm.NewProviderError(llm.OutcomeInvalidResponse,
					ref.Provider, "model Harness probe final chunk omitted valid usage", nil)
			}
			if (chunk.Provider != "" && chunk.Provider != ref.Provider) ||
				(chunk.Model != "" && chunk.Model != ref.Model) {
				return nil, llm.NewProviderError(llm.OutcomeInvalidResponse,
					ref.Provider, "model Harness probe response identity changed", nil)
			}
			calls, err := llm.NormalizeToolCalls(chunk.ToolCalls)
			if err != nil {
				return nil, llm.NewProviderError(llm.OutcomeInvalidResponse,
					ref.Provider, "model Harness probe returned invalid tool calls", err)
			}
			return &llm.ChatResponse{Text: text.String(), ToolCalls: calls,
				Usage: *chunk.Usage, Provider: chunk.Provider, Model: chunk.Model}, nil
		}
	}
	return nil, llm.NewProviderError(llm.OutcomeInvalidResponse, ref.Provider,
		"model Harness probe exceeded its stream chunk limit", nil)
}

func decodeExactJSON(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(bytes.TrimSpace(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("JSON contains trailing data")
		}
		return err
	}
	return nil
}

func persistedHarnessRecord(provider string, model string,
	qualification llm.HarnessQualification,
) persistedHarnessQualification {
	return persistedHarnessQualification{
		Version:  HarnessQualificationProtocolVersion,
		Provider: provider, Model: model,
		BindingDigest:        qualification.BindingDigest,
		ToolCallsQualified:   qualification.ToolCallsQualified,
		ToolResultsQualified: qualification.ToolResultsQualified,
		StrictJSONQualified:  qualification.StrictJSONQualified,
		StreamingQualified:   qualification.StreamingQualified,
		QualifiedAt:          qualification.QualifiedAt.UTC().Format(time.RFC3339Nano),
		ExpiresAt:            qualification.ExpiresAt.UTC().Format(time.RFC3339Nano),
	}
}

func (r *Registry) loadHarnessQualifications(ctx context.Context,
	reader RouteSettingReader,
) error {
	for _, provider := range r.providers {
		for _, model := range provider.Models {
			value, found, err := reader.GetProviderSetting(ctx,
				harnessQualificationSettingKey(provider.Name, model))
			if err != nil {
				return err
			}
			if !found || len(value) == 0 || len(value) > maxHarnessRecordBytes {
				continue
			}
			var record persistedHarnessQualification
			if decodeExactJSON([]byte(value), &record) != nil ||
				record.Version != HarnessQualificationProtocolVersion ||
				record.Provider != provider.Name || record.Model != model {
				continue
			}
			qualifiedAt, qualifiedErr := time.Parse(time.RFC3339Nano, record.QualifiedAt)
			expiresAt, expiresErr := time.Parse(time.RFC3339Nano, record.ExpiresAt)
			if qualifiedErr != nil || expiresErr != nil {
				continue
			}
			_ = r.router.SetHarnessQualification(llm.ModelRef{
				Provider: provider.Name, Model: model,
			}, llm.HarnessQualification{
				ProtocolVersion:      llm.ModelHarnessProtocolVersion,
				BindingDigest:        record.BindingDigest,
				ToolCallsQualified:   record.ToolCallsQualified,
				ToolResultsQualified: record.ToolResultsQualified,
				StrictJSONQualified:  record.StrictJSONQualified,
				StreamingQualified:   record.StreamingQualified,
				QualifiedAt:          qualifiedAt, ExpiresAt: expiresAt,
			})
		}
	}
	return nil
}

func harnessQualificationSettingKey(provider string, model string) string {
	sum := sha256.Sum256([]byte(provider + "\x00" + model))
	return "harness_qualification." + hex.EncodeToString(sum[:])
}

func harnessAvailability(model string, profile llm.ModelHarness) HarnessAvailability {
	qualifiedAt := ""
	expiresAt := ""
	if !profile.QualifiedAt.IsZero() {
		qualifiedAt = profile.QualifiedAt.UTC().Format(time.RFC3339Nano)
	}
	if !profile.QualificationExpires.IsZero() {
		expiresAt = profile.QualificationExpires.UTC().Format(time.RFC3339Nano)
	}
	return HarnessAvailability{
		ProtocolVersion: profile.ProtocolVersion, Model: model,
		TransportProtocol: profile.TransportProtocol,
		ToolStrategy:      profile.ToolStrategy, JSONStrategy: profile.JSONStrategy,
		QualificationStatus:    profile.QualificationStatus,
		ToolCallsQualified:     profile.ToolCallsQualified,
		ToolResultsQualified:   profile.ToolResultsQualified,
		StrictJSONQualified:    profile.StrictJSONQualified,
		StreamingQualified:     profile.StreamingQualified,
		RootEligible:           rootHarnessReady(profile),
		StructuredJSONEligible: profile.StrictJSONQualified,
		QualifiedAt:            qualifiedAt, ExpiresAt: expiresAt,
	}
}

func rootHarnessReady(profile llm.ModelHarness) bool {
	return profile.ToolStrategy == llm.HarnessToolStrategyNative &&
		profile.ToolCallsQualified && profile.ToolResultsQualified &&
		profile.StrictJSONQualified && profile.StreamingQualified
}
