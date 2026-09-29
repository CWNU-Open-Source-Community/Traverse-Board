package application

import (
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"testing"
)

func TestSupervisorBudgetPreservesExplicitOutputLimit(t *testing.T) {
	for _, budget := range []domain.Budget{{}, {MaxTokens: 100000}, {MaxTokens: 200}} {
		request, err := supervisorRequestWithinBudget(llm.ChatRequest{MaxTokens: 128}, budget, domain.SupervisorCheckpoint{TotalTokens: 50})
		if err != nil {
			t.Fatal(err)
		}
		if request.MaxTokens != 128 {
			t.Errorf("budget %+v overwrote explicit output with %d", budget, request.MaxTokens)
		}
	}
}

func TestModelOutputReserveDoesNotBecomeOptionalWireLimit(t *testing.T) {
	p, err := llm.NewOpenAICompatibleProvider(llm.OpenAICompatibleConfig{Name: "test", BaseURL: "https://example.invalid/v1", APIKey: "synthetic", DefaultModel: "model"})
	if err != nil {
		t.Fatal(err)
	}
	ref := llm.ModelRef{Provider: "test", Model: "model"}
	router := llm.NewRouter(ref)
	router.RegisterProvider(p)
	req, err := router.PrepareModelRequest(ref, llm.ChatRequest{Messages: []llm.Message{{Role: "user", Content: "hello"}}, Tools: []llm.ToolSpec{{Name: "echo", Parameters: []byte(`{"type":"object"}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	w, _ := req.PreparedContextWindow()
	got, plan, err := constrainRequestToModelWindow(req, w, modelContextLayout{})
	if err != nil {
		t.Fatal(err)
	}
	if got.MaxTokens != 0 || plan.OutputReserveTokens != 4096 || got.Metadata["context_output_mode"] != "provider_default" {
		t.Fatalf("local reserve escaped to wire: %+v %+v", got, plan)
	}
	req.MaxTokens = 128
	got, plan, err = constrainRequestToModelWindow(req, w, modelContextLayout{})
	if err != nil || got.MaxTokens != 128 || plan.OutputReserveTokens != 128 {
		t.Fatalf("explicit cap lost: %+v %+v %v", got, plan, err)
	}
}

func TestOutputMetadataPreservesHistoryBudget(t *testing.T) {
	base := llm.DefaultContextWindow()
	w := base
	w.WindowTokens = 1_000_000
	w.DefaultOutputTokens = 8192
	w.MaxOutputTokens = 393216
	for _, source := range []string{"provider_model_metadata", "operator_model_policy"} {
		w.Source = source
		if supervisorMemoryBudget(w) != supervisorMemoryBudget(base) {
			t.Fatalf("new metadata inflated history: %d vs %d", supervisorMemoryBudget(w), supervisorMemoryBudget(base))
		}
	}
}
