package application

import (
	"encoding/json"
	"strings"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
)

func isHistoricalSpecialistContext(content string) bool {
	var header struct {
		Version string `json:"version"`
	}
	return json.Unmarshal([]byte(content), &header) == nil && header.Version == domain.SpecialistContextVersion
}

func verifySpecialistBriefFinalRequest(request llm.ChatRequest, input string, repair int) error {
	if len(request.Messages) == 0 || len(request.Tools) != 0 {
		return apperror.New(apperror.CodeFailedPrecondition, "Specialist task brief requires a bounded no-tool current input")
	}
	last := request.Messages[len(request.Messages)-1]
	expected := input
	if repair == 1 {
		expected = specialistProtocolRepairRequest(llm.ChatRequest{Messages: []llm.Message{{Role: "user", Content: input}}}).Messages[0].Content
	}
	if strings.TrimSpace(last.Role) != "user" || last.Content != expected {
		return apperror.New(apperror.CodeFailedPrecondition, "final Specialist request changed its mandatory task brief")
	}
	return nil
}
