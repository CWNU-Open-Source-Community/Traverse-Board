package llm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

type imageTestRuntime struct{ state VisionSupport }

func (imageTestRuntime) ResolveCredential(context.Context) (string, error) { return "test-only", nil }
func (imageTestRuntime) MapModel(model string) (string, error)             { return model, nil }
func (imageTestRuntime) Apply(string, http.Header, map[string]any) error   { return nil }
func (imageTestRuntime) BindingDigest() string                             { return strings.Repeat("a", 64) }
func (r imageTestRuntime) DescribeVision(model string) VisionCapability {
	if model != "image-model" {
		return VisionCapability{State: VisionUnknown, Source: "unknown"}
	}
	return VisionCapability{State: r.state, Source: "operator_declared"}
}

func testImagePart(t *testing.T, marker uint8) ImagePart {
	t.Helper()
	bitmap := image.NewRGBA(image.Rect(0, 0, 3, 2))
	bitmap.Set(1, 1, color.RGBA{R: marker, G: 27, B: 92, A: 255})
	var data bytes.Buffer
	if err := png.Encode(&data, bitmap); err != nil {
		t.Fatal(err)
	}
	raw := data.Bytes()
	sum := sha256.Sum256(raw)
	return ImagePart{MediaType: "image/png", Data: raw, SHA256: hex.EncodeToString(sum[:]), Width: 3, Height: 2}
}

func imageTestProvider(t *testing.T, protocol, endpoint string, state VisionSupport) Provider {
	t.Helper()
	var provider Provider
	var err error
	runtime := imageTestRuntime{state: state}
	switch protocol {
	case "chat":
		provider, err = NewOpenAICompatibleProvider(OpenAICompatibleConfig{Name: "image-provider", BaseURL: endpoint, DefaultModel: "image-model", Runtime: runtime})
	case "responses":
		provider, err = NewOpenAIResponsesProvider(OpenAIResponsesConfig{Name: "image-provider", BaseURL: endpoint, DefaultModel: "image-model", Runtime: runtime})
	case "anthropic":
		provider, err = NewAnthropicCompatibleProvider(AnthropicCompatibleConfig{Name: "image-provider", BaseURL: endpoint, DefaultModel: "image-model", Runtime: runtime})
	case "ollama":
		local, localErr := NewOllamaProvider(OllamaConfig{Name: "image-provider", BaseURL: endpoint})
		err = localErr
		if local != nil {
			local.models["image-model"] = ollamaModelState{known: state != VisionUnknown, vision: state == VisionSupported}
			provider = local
		}
	default:
		t.Fatal("unknown test protocol")
	}
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

func TestImageNativeWirePayloadsPreserveBytesAndToolPairs(t *testing.T) {
	for _, protocol := range []string{"chat", "responses", "anthropic", "ollama"} {
		for _, stream := range []bool{false, true} {
			name := protocol + "/chat"
			if stream {
				name = protocol + "/stream"
			}
			t.Run(name, func(t *testing.T) {
				images := []ImagePart{testImagePart(t, 12), testImagePart(t, 98)}
				captured := make(chan map[string]any, 1)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
						w.WriteHeader(400)
						return
					}
					captured <- body
					if stream {
						// Capture the actual streaming request; response parsing is
						// separately covered by the existing stream protocol tests.
						w.WriteHeader(http.StatusBadRequest)
						_, _ = w.Write([]byte(`{"error":{"message":"test response ends after capture"}}`))
						return
					}
					w.Header().Set("Content-Type", "application/json")
					switch protocol {
					case "chat":
						_, _ = w.Write([]byte(`{"model":"image-model","choices":[{"index":0,"message":{"role":"assistant","content":"captured"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`))
					case "responses":
						_, _ = w.Write([]byte(`{"id":"resp-captured","object":"response","status":"completed","model":"image-model","output":[{"id":"msg-captured","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"captured"}]}],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}`))
					case "anthropic":
						_, _ = w.Write([]byte(`{"id":"msg-captured","type":"message","role":"assistant","model":"image-model","content":[{"type":"text","text":"captured"}],"stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":1}}`))
					case "ollama":
						_, _ = w.Write([]byte(`{"model":"image-model","message":{"role":"assistant","content":"captured"},"done":true,"prompt_eval_count":2,"eval_count":1}`))
					}
				}))
				defer server.Close()
				provider := imageTestProvider(t, protocol, server.URL, VisionSupported)
				router := NewRouter(ModelRef{Provider: provider.Name(), Model: "image-model"})
				router.RegisterProvider(provider)
				request := ChatRequest{Messages: []Message{
					{Role: "assistant", ToolCalls: []ToolCall{{ID: "call-read", Name: "workspace_read", Arguments: json.RawMessage(`{"path":"README.md"}`)}}},
					{Role: "user", Content: "查看这两张截图", Images: images, ToolResults: []ToolResult{{ToolCallID: "call-read", Content: "recorded tool evidence"}}},
				}}
				if stream {
					if _, err := router.StreamChat(t.Context(), "default", request); err == nil {
						t.Fatal("fixture must reject after capturing stream request")
					}
				} else {
					response, err := router.Chat(t.Context(), "default", request)
					if err != nil || response.Text != "captured" {
						t.Fatalf("response=%+v err=%v", response, err)
					}
				}
				body := <-captured
				encoded, _ := json.Marshal(body)
				if !bytes.Contains(encoded, []byte("recorded tool evidence")) || !bytes.Contains(encoded, []byte("workspace_read")) {
					t.Fatal("tool pair disappeared from image request")
				}
				var got []string
				switch protocol {
				case "chat":
					messages := body["messages"].([]any)
					if messages[1].(map[string]any)["tool_call_id"] != "call-read" {
						t.Fatal("tool result binding changed")
					}
					parts := messages[2].(map[string]any)["content"].([]any)
					if parts[0].(map[string]any)["text"] != "查看这两张截图" {
						t.Fatal("text missing")
					}
					for _, raw := range parts[1:] {
						part := raw.(map[string]any)
						if part["type"] != "image_url" {
							t.Fatal("not native image_url")
						}
						got = append(got, strings.TrimPrefix(part["image_url"].(map[string]any)["url"].(string), "data:image/png;base64,"))
					}
				case "responses":
					items := body["input"].([]any)
					if items[0].(map[string]any)["call_id"] != "call-read" || items[1].(map[string]any)["call_id"] != "call-read" {
						t.Fatal("native call/result binding changed")
					}
					parts := items[2].(map[string]any)["content"].([]any)
					for _, raw := range parts[1:] {
						part := raw.(map[string]any)
						if part["type"] != "input_image" {
							t.Fatal("not native input_image")
						}
						got = append(got, strings.TrimPrefix(part["image_url"].(string), "data:image/png;base64,"))
					}
				case "anthropic":
					parts := body["messages"].([]any)[1].(map[string]any)["content"].([]any)
					if parts[0].(map[string]any)["type"] != "tool_result" || parts[0].(map[string]any)["tool_use_id"] != "call-read" {
						t.Fatal("tool result must precede images/text")
					}
					for _, raw := range parts[1:3] {
						part := raw.(map[string]any)
						source := part["source"].(map[string]any)
						if part["type"] != "image" || source["type"] != "base64" || source["media_type"] != "image/png" {
							t.Fatal("not native image block")
						}
						got = append(got, source["data"].(string))
					}
				case "ollama":
					messages := body["messages"].([]any)
					if messages[1].(map[string]any)["role"] != "tool" {
						t.Fatal("tool result lost")
					}
					for _, raw := range messages[2].(map[string]any)["images"].([]any) {
						got = append(got, raw.(string))
					}
				}
				if len(got) != len(images) {
					t.Fatal("images lost")
				}
				for i, raw := range got {
					data, err := base64.StdEncoding.DecodeString(raw)
					if err != nil || !bytes.Equal(data, images[i].Data) {
						t.Fatalf("image %d bytes/order changed", i)
					}
				}
			})
		}
	}
}

func TestImageUnsupportedUnknownAndInvalidNeverCallProvider(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	defer server.Close()
	for _, protocol := range []string{"chat", "responses", "anthropic", "ollama"} {
		for _, state := range []VisionSupport{VisionUnknown, VisionUnsupported} {
			provider := imageTestProvider(t, protocol, server.URL, state)
			request := ChatRequest{Model: "image-model", Messages: []Message{{Role: "user", Images: []ImagePart{testImagePart(t, 4)}}}}
			if _, err := provider.Chat(t.Context(), request); err == nil {
				t.Fatalf("%s admitted %s vision", protocol, state)
			}
			if _, err := provider.StreamChat(t.Context(), request); err == nil {
				t.Fatalf("%s streaming admitted %s vision", protocol, state)
			}
		}
		provider := imageTestProvider(t, protocol, server.URL, VisionSupported)
		for _, mutate := range []func(*Message){
			func(m *Message) { m.Role = "system" }, func(m *Message) { m.Role = "assistant" },
			func(m *Message) { m.Images[0].SHA256 = strings.Repeat("0", 64) },
			func(m *Message) { m.Images[0].Width++ }, func(m *Message) { m.Images[0].MediaType = "image/jpeg" },
			func(m *Message) { m.Images[0].Data = []byte("not an image") },
			func(m *Message) { m.Images = append(m.Images, m.Images[0], m.Images[0], m.Images[0], m.Images[0]) },
		} {
			message := Message{Role: "user", Images: []ImagePart{testImagePart(t, 6)}}
			mutate(&message)
			if _, err := provider.Chat(t.Context(), ChatRequest{Model: "image-model", Messages: []Message{message}}); err == nil {
				t.Fatalf("%s admitted invalid image", protocol)
			}
		}
	}
	if calls.Load() != 0 {
		t.Fatal("rejected image made an HTTP request")
	}
}

func TestImageDiagnosticOmissionCopyAndBudget(t *testing.T) {
	part := testImagePart(t, 44)
	request := ChatRequest{Model: "image-model", Messages: []Message{{Role: "user", Content: "test", Images: []ImagePart{part}}}}
	safe, err := redactRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	safe.Messages[0].Images[0].Data[0] ^= 0xff
	if bytes.Equal(safe.Messages[0].Images[0].Data, request.Messages[0].Images[0].Data) {
		t.Fatal("provider request aliases original admitted image bytes")
	}
	raw, _ := json.Marshal(request)
	if bytes.Contains(raw, []byte(part.SHA256)) || bytes.Contains(raw, []byte(base64.StdEncoding.EncodeToString(part.Data))) || bytes.Contains(raw, []byte("images")) {
		t.Fatal("image entered ordinary diagnostic JSON")
	}
	if EstimateImageTokens(part) < 1024 || EstimateImageTokens(ImagePart{Width: 3840, Height: 2160}) <= EstimateImageTokens(part) {
		t.Fatal("image budget is missing or independent of dimensions")
	}
	inputLimit, err := DefaultContextWindow().InputLimit(4096)
	if err != nil || EstimateImageTokens(ImagePart{Width: 1920, Height: 1080})+12000 > inputLimit {
		t.Fatal("ordinary 1080p screenshot plus a 12K tool/text allowance cannot fit the default window")
	}
	if EstimateImageTokens(ImagePart{Width: 4096, Height: 4096}) < inputLimit {
		t.Fatal("large image cost is capped to force it into the window")
	}
	local := imageTestProvider(t, "ollama", "http://127.0.0.1:11434", VisionSupported)
	_, _, estimatedBytes, err := local.(*OllamaProvider).prepareRequest(request)
	if err != nil || ollamaUsage(0, 0, "ok", estimatedBytes).InputTokens < EstimateImageTokens(part) {
		t.Fatal("Ollama fallback accounting omitted image")
	}
}
