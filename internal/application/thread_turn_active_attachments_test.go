package application_test

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/session"
	"cyberagent-workbench/internal/store"
)

type gatedAttachmentProvider struct {
	mu        sync.Mutex
	first     sync.Once
	started   chan struct{}
	release   chan struct{}
	requests  []llm.ChatRequest
	responses []string
}

func (*gatedAttachmentProvider) Name() string { return "gated-active-attachments" }
func (*gatedAttachmentProvider) ListModels(context.Context) ([]llm.ModelInfo, error) {
	return []llm.ModelInfo{{ID: "model", Provider: "gated-active-attachments",
		Capabilities: []string{"chat", "vision"}}}, nil
}
func (*gatedAttachmentProvider) SupportsTools(string) bool    { return false }
func (*gatedAttachmentProvider) SupportsVision(string) bool   { return true }
func (*gatedAttachmentProvider) SupportsJSONMode(string) bool { return true }
func (*gatedAttachmentProvider) DescribeVision(string) llm.VisionCapability {
	return llm.VisionCapability{State: llm.VisionSupported, Source: "test_exact_model"}
}
func (p *gatedAttachmentProvider) Chat(ctx context.Context, request llm.ChatRequest) (*llm.ChatResponse, error) {
	block := false
	p.first.Do(func() { block = true })
	if block {
		close(p.started)
		select {
		case <-p.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	p.mu.Lock()
	index := len(p.requests)
	p.requests = append(p.requests, request)
	response := p.responses[index]
	p.mu.Unlock()
	return &llm.ChatResponse{Text: response, Provider: p.Name(), Model: "model",
		Usage: llm.Usage{InputTokens: 2, OutputTokens: 2, TotalTokens: 4}}, nil
}
func (p *gatedAttachmentProvider) StreamChat(ctx context.Context, request llm.ChatRequest) (<-chan llm.ChatChunk, error) {
	response, err := p.Chat(ctx, request)
	if err != nil {
		return nil, err
	}
	chunks := make(chan llm.ChatChunk, 2)
	chunks <- llm.ChatChunk{Text: response.Text}
	chunks <- llm.FinalChatChunk(response)
	close(chunks)
	return chunks, nil
}
func (p *gatedAttachmentProvider) Requests() []llm.ChatRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]llm.ChatRequest(nil), p.requests...)
}

type preparingAttachmentStore struct {
	*store.SQLiteStore
	once    sync.Once
	started chan struct{}
	release chan struct{}
}

func (s *preparingAttachmentStore) GetThread(ctx context.Context, id string) (domain.Thread, error) {
	block := false
	s.once.Do(func() { block = true })
	if block {
		close(s.started)
		select {
		case <-s.release:
		case <-ctx.Done():
			return domain.Thread{}, ctx.Err()
		}
	}
	return s.SQLiteStore.GetThread(ctx, id)
}

func TestSameAttachmentOperationRetryCannotRejectPreparingOwner(t *testing.T) {
	provider := &lifecycleProvider{responses: []string{
		rootActionResponse(domain.RootActionFinish, "attachment complete", "done", ""),
	}}
	st, _, request, _, _ := threadFilesFixture(t, provider)
	request.Files = nil
	file, err := st.SaveWorkspaceFileAttachment(t.Context(), "ws-thread-files",
		"same-owner-preparing-upload", "text/plain", "same-owner.txt", []byte("same owner"))
	if err != nil {
		t.Fatal(err)
	}
	request.Content = "Submit this attachment exactly once"
	request.OperationKey = "same-owner-preparing-attachment-0001"
	request.Attachments = []domain.FileAttachmentReference{{ID: file.ID,
		WorkspaceID: file.WorkspaceID, SHA256: file.SHA256, ByteSize: file.ByteSize}}
	gated := &preparingAttachmentStore{SQLiteStore: st, started: make(chan struct{}), release: make(chan struct{})}
	router := llm.NewRouter(llm.ModelRef{Provider: provider.Name(), Model: "model"})
	router.RegisterProvider(provider)
	turns := application.NewThreadTurnService(gated,
		application.NewRunLifecycleControlService(st),
		application.NewRunExecutionHandoffService(st, router, policy.NewDefaultChecker()))
	type outcome struct {
		result application.ExecuteThreadTurnResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := turns.Execute(t.Context(), request)
		done <- outcome{result: result, err: err}
	}()
	awaitTurnSignal(t, gated.started)
	_, retryErr := turns.Execute(t.Context(), request)
	close(gated.release)
	first := <-done
	if apperror.CodeOf(retryErr) != apperror.CodeUnavailable {
		t.Fatalf("same-owner retry was not reported as unconfirmed preparation: %v", retryErr)
	}
	if first.err != nil || first.result.Submission.Message.Status != domain.OperatorSteeringCommitted {
		t.Fatalf("same-owner retry rejected the preparing owner: result=%#v err=%v",
			first.result, first.err)
	}
	replayed, err := turns.Execute(t.Context(), request)
	if err != nil || !replayed.Replayed || replayed.Submission.Message.ID != first.result.Submission.Message.ID {
		t.Fatalf("sealed attachment submission did not replay: %#v err=%v", replayed, err)
	}
}

func TestRunningThreadQueuesExactAttachmentsWithoutEarlyEvidenceOrReplay(t *testing.T) {
	provider := &gatedAttachmentProvider{started: make(chan struct{}), release: make(chan struct{}),
		responses: []string{
			rootActionResponse(domain.RootActionFinish, "first complete", "done", ""),
			rootActionResponse(domain.RootActionFinish, "queued file-only complete", "done", ""),
			rootActionResponse(domain.RootActionFinish, "queued attachment complete", "done", ""),
		}}
	st, turns, first, _, path := threadFilesFixture(t, provider)
	first.Files = nil
	first.Content = "Keep working while I prepare another input"
	done := make(chan error, 1)
	go func() {
		_, err := turns.Execute(t.Context(), first)
		done <- err
	}()
	awaitTurnSignal(t, provider.started)

	fileOnly, err := st.SaveWorkspaceFileAttachment(t.Context(), "ws-thread-files",
		"active-queue-file-only-upload", "text/plain", "file-only.txt", []byte("ACTIVE_QUEUE_FILE_ONLY_EVIDENCE"))
	if err != nil {
		t.Fatal(err)
	}
	fileOnlyRequest := first
	fileOnlyRequest.Content = "Use this queued file before the image input"
	fileOnlyRequest.OperationKey = "active-queue-file-only-0002"
	fileOnlyRequest.Attachments = []domain.FileAttachmentReference{{ID: fileOnly.ID,
		WorkspaceID: fileOnly.WorkspaceID, SHA256: fileOnly.SHA256, ByteSize: fileOnly.ByteSize}}
	fileOnlyAccepted, err := turns.Execute(t.Context(), fileOnlyRequest)
	if err != nil || fileOnlyAccepted.ExecutionStarted ||
		fileOnlyAccepted.Submission.Message.Status != domain.OperatorSteeringPending {
		t.Fatalf("active file-only queue admission=%#v err=%v", fileOnlyAccepted, err)
	}

	pixels := threadImagePNG(t, 121)
	image, err := st.SaveWorkspaceImage(t.Context(), "ws-thread-files",
		"active-queue-image-upload", "image/png", "queued.png", pixels)
	if err != nil {
		t.Fatal(err)
	}
	file, err := st.SaveWorkspaceFileAttachment(t.Context(), "ws-thread-files",
		"active-queue-file-upload", "text/plain", "queued.txt", []byte("ACTIVE_QUEUE_FILE_EVIDENCE"))
	if err != nil {
		t.Fatal(err)
	}
	queued := first
	queued.Content = "Use only these queued attachments"
	queued.OperationKey = "active-queue-attachments-0003"
	queued.Images = []domain.ImageReference{{ID: image.ID, SHA256: image.SHA256}}
	queued.Attachments = []domain.FileAttachmentReference{{ID: file.ID, WorkspaceID: file.WorkspaceID,
		SHA256: file.SHA256, ByteSize: file.ByteSize}}
	accepted, err := turns.Execute(t.Context(), queued)
	if err != nil || accepted.ExecutionStarted || accepted.Submission.Message.Status != domain.OperatorSteeringPending {
		t.Fatalf("active attachment queue admission=%#v err=%v", accepted, err)
	}
	revisedContent := "Use the revised instruction with only these queued attachments"
	revised, err := st.ReviseOperatorSteering(t.Context(), domain.ReviseOperatorSteeringRequest{
		SessionID: accepted.Submission.Run.SessionID, MessageID: accepted.Submission.Message.ID,
		ExpectedRevision: 0, Content: revisedContent,
		OperationKey: "active-queue-revision-0001", RequestedBy: "test_operator"})
	if err != nil || revised.Message.Revision != 1 || revised.Message.Content != revisedContent {
		t.Fatalf("active queued message revision=%#v err=%v", revised, err)
	}

	withdrawnFile, err := st.SaveWorkspaceFileAttachment(t.Context(), "ws-thread-files",
		"withdrawn-queue-file-upload", "text/plain", "withdrawn.txt", []byte("WITHDRAWN_FILE_MUST_NOT_REACH_MODEL"))
	if err != nil {
		t.Fatal(err)
	}
	withdrawn := first
	withdrawn.Content = "Withdraw this queued file"
	withdrawn.OperationKey = "active-queue-withdrawn-0004"
	withdrawn.Attachments = []domain.FileAttachmentReference{{ID: withdrawnFile.ID,
		WorkspaceID: withdrawnFile.WorkspaceID, SHA256: withdrawnFile.SHA256,
		ByteSize: withdrawnFile.ByteSize}}
	cancelledCandidate, err := turns.Execute(t.Context(), withdrawn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CancelOperatorSteering(t.Context(), domain.CancelOperatorSteeringRequest{
		MessageID:    cancelledCandidate.Submission.Message.ID,
		OperationKey: "active-queue-withdrawn-cancel-0001", RequestedBy: "test_operator",
		Reason: "operator withdrew the pending attachment"}); err != nil {
		t.Fatal(err)
	}
	history, err := st.ListSessionMessages(t.Context(), accepted.Submission.Run.SessionID, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range history {
		if message.Provenance.SourceKind == session.SourceWorkspaceImage ||
			message.Provenance.SourceKind == session.SourceUploadedFile {
			t.Fatalf("pending attachment leaked into Session evidence: %#v", message)
		}
	}

	close(provider.release)
	if err := awaitTurnResult(t, done); err != nil {
		t.Fatal(err)
	}
	requests := provider.Requests()
	if len(requests) != 3 {
		t.Fatalf("provider calls=%d", len(requests))
	}
	var fileOnlyWire strings.Builder
	for _, message := range requests[1].Messages {
		fileOnlyWire.WriteString(message.Content)
		if len(message.Images) != 0 {
			t.Fatal("file-only queued request fabricated an image")
		}
	}
	if !strings.Contains(fileOnlyWire.String(), fileOnlyRequest.Content) ||
		!strings.Contains(fileOnlyWire.String(), "ACTIVE_QUEUE_FILE_ONLY_EVIDENCE") ||
		strings.Contains(fileOnlyWire.String(), file.SHA256) {
		t.Fatalf("file-only request leaked a later queue item: %s", fileOnlyWire.String())
	}
	var secondWire strings.Builder
	var secondImages [][]byte
	for _, message := range requests[2].Messages {
		secondWire.WriteString(message.Content)
		for _, part := range message.Images {
			secondImages = append(secondImages, part.Data)
		}
	}
	if !strings.Contains(secondWire.String(), revisedContent) || strings.Contains(secondWire.String(), queued.Content) ||
		!strings.Contains(secondWire.String(), "ACTIVE_QUEUE_FILE_EVIDENCE") ||
		!strings.Contains(secondWire.String(), file.SHA256) || len(secondImages) != 1 ||
		!bytes.Equal(secondImages[0], pixels) {
		t.Fatalf("exact queued attachment input missing: images=%d wire=%s", len(secondImages), secondWire.String())
	}
	for _, request := range requests {
		for _, message := range request.Messages {
			if strings.Contains(message.Content, "WITHDRAWN_FILE_MUST_NOT_REACH_MODEL") ||
				strings.Contains(message.Content, withdrawnFile.SHA256) {
				t.Fatal("withdrawn attachment reached a model request")
			}
		}
	}
	history, err = st.ListSessionMessages(t.Context(), accepted.Submission.Run.SessionID, true)
	if err != nil {
		t.Fatal(err)
	}
	fileEvidence, imageEvidence := 0, 0
	for _, message := range history {
		switch message.Provenance.SourceKind {
		case session.SourceUploadedFile:
			fileEvidence++
			if message.Provenance.SourceRef != file.ID && message.Provenance.SourceRef != fileOnly.ID {
				t.Fatalf("withdrawn file persisted as evidence: %#v", message)
			}
		case session.SourceWorkspaceImage:
			imageEvidence++
			if message.Provenance.SourceRef != image.ID {
				t.Fatalf("unexpected image evidence: %#v", message)
			}
		}
	}
	if fileEvidence != 2 || imageEvidence != 1 {
		t.Fatalf("commit evidence counts file=%d image=%d", fileEvidence, imageEvidence)
	}

	reopened, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	replayTurns := newThreadFilesService(reopened, provider)
	replay, err := replayTurns.Execute(t.Context(), queued)
	if err != nil || !replay.Replayed || len(provider.Requests()) != 3 {
		t.Fatalf("restart replay called provider again: %#v calls=%d err=%v",
			replay, len(provider.Requests()), err)
	}
}
