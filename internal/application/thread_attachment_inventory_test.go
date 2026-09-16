package application_test

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/store"
)

func TestThreadAttachmentInventoryBoundsHistoryWithoutBlockingTextAndSurvivesSuccessor(t *testing.T) {
	responses := make([]string, 20)
	for i := range responses {
		responses[i] = rootActionResponse(domain.RootActionFinish, "Acknowledged original-file evidence", "done", "")
	}
	provider := &lifecycleProvider{responses: responses}
	dbpath := filepath.Join(t.TempDir(), "bounded-inputs.db")
	st, err := store.Open(dbpath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err = st.SaveWorkspace(t.Context(), store.WorkspaceRecord{ID: "workspace-bounded-inputs", Name: "inputs", RootPath: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	_, run, err := application.NewRunService(st).Create(t.Context(), application.CreateRunRequest{Goal: "Keep original attachment identity across replies", WorkspaceID: "workspace-bounded-inputs", Profile: "review", ModelRoute: provider.Name() + "/model", Interactive: true, Budget: domain.Budget{MaxTurns: 32}})
	if err != nil {
		t.Fatal(err)
	}
	turns := newThreadFilesService(st, provider)
	var latest domain.WorkspaceFileAttachment
	for batch := 0; batch < 17; batch++ {
		input := application.ExecuteThreadTurnRequest{Version: domain.ThreadMessageProtocolVersion, ThreadID: domain.InitialThreadID(run.ID), OperationKey: fmt.Sprintf("bounded-input-message-%d", batch), RequestedBy: "test_operator"}
		for ordinal := 0; ordinal < 4 && batch*4+ordinal < 65; ordinal++ {
			index := batch*4 + ordinal
			file, err := st.SaveWorkspaceFileAttachment(t.Context(), "workspace-bounded-inputs", fmt.Sprintf("bounded-input-upload-%d", index), "text/plain", fmt.Sprintf("file-%d.txt", index), []byte(fmt.Sprintf("FILE_VALUE_%d", index)))
			if err != nil {
				t.Fatal(err)
			}
			latest = file
			input.Attachments = append(input.Attachments, domain.FileAttachmentReference{ID: file.ID, WorkspaceID: file.WorkspaceID, SHA256: file.SHA256, ByteSize: file.ByteSize})
		}
		if _, err := turns.Execute(t.Context(), input); err != nil {
			t.Fatalf("batch %d: %v", batch, err)
		}
	}
	follow := application.ExecuteThreadTurnRequest{Version: domain.ThreadMessageProtocolVersion, ThreadID: domain.InitialThreadID(run.ID), OperationKey: "bounded-input-text-followup", RequestedBy: "test_operator", Content: "Discuss the files without running commands."}
	if _, err := turns.Execute(t.Context(), follow); err != nil {
		t.Fatal("65 earlier files blocked text", err)
	}
	check := func(index int) {
		var text strings.Builder
		for _, m := range provider.requests[index].Messages {
			text.WriteString(m.Content)
		}
		wire := text.String()
		if !strings.Contains(wire, "Coverage: 1 earlier original files") || !strings.Contains(wire, latest.ID+"/content.txt") || !strings.Contains(wire, "No currently offered command runtime supports original attachment access") {
			t.Fatalf("bounded raw input coverage or real capability missing in request %d", index)
		}
		if count := strings.Count(wire, "/content.txt"); count != 64 {
			t.Fatalf("request %d original paths=%d, want 64", index, count)
		}
	}
	check(17)
	if _, err := application.NewRunService(st).Fail(t.Context(), run.ID, "isolated predecessor ended"); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = store.Open(dbpath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	turns = newThreadFilesService(st, provider)
	follow.OperationKey = "bounded-input-successor-followup"
	result, err := turns.Execute(t.Context(), follow)
	if err != nil || result.Submission.Run.ID == run.ID {
		t.Fatalf("successor failed %#v %v", result, err)
	}
	check(18)
	_, raw, err := st.GetWorkspaceFileAttachment(t.Context(), latest.WorkspaceID, latest.ID)
	if err != nil || string(raw) != "FILE_VALUE_64" {
		t.Fatal("original lost across successor", err)
	}
}
