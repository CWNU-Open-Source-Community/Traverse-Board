package application

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/toolgateway"
)

func TestOpenAIResponsesAcceptsConservativeCodeSupervisorRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, "expected bounded fixture refusal", http.StatusBadRequest)
	}))
	defer server.Close()

	provider, err := llm.NewOpenAIResponsesProvider(llm.OpenAIResponsesConfig{
		Name: "responses-supervisor-test", BaseURL: server.URL,
		APIKey: "test-secret", DefaultModel: "model",
	})
	if err != nil {
		t.Fatal(err)
	}
	capabilities := toolgateway.AgentCodeCapabilities(toolgateway.AgentCodeCapabilityContext{
		RunID: "run-1", MissionID: "mission-1", RootAgentID: "agent-1",
		WorkspaceID: "workspace-1", RootFingerprint: strings.Repeat("a", 64),
		Surface: domain.ExecutionSurfaceCode, Phase: domain.ExecutionPhaseDeliver,
		Role: domain.AgentRoleRoot, Profile: domain.ProfileCode,
		PermissionMode: domain.RunExecutionPermissionConservative,
		ModeRevision:   1, PermissionRevision: 1,
	})
	request := llm.ChatRequest{
		Messages: []llm.Message{{Role: "system", Content: "Return JSON."},
			{Role: "user", Content: "Inspect the workspace."}},
		Tools: supervisorStructuredToolSpecs(domain.ExecutionSurfaceCode,
			domain.ExecutionPhaseDeliver, domain.RunExecutionPermissionConservative,
			false, false, supervisorToolOptions{AgentCode: supervisorAgentCodeTools{
				Capabilities: capabilities,
			}, WebEvidence: supervisorWebEvidenceTools{Capabilities: toolgateway.WebEvidenceCapabilities{Available: true, FetchAvailable: true, SearchAvailable: true}},
			}),
		JSONMode: true,
	}

	_, err = provider.StreamChat(context.Background(), request)
	if err == nil {
		t.Fatal("fixture provider unexpectedly accepted the request")
	}
	if strings.Contains(err.Error(), "could not prepare streaming Responses request") {
		t.Fatalf("Supervisor request was rejected before reaching the provider: %v", err)
	}
}
