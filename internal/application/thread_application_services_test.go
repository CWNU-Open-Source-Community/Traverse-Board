package application

import (
	"context"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/session"
)

type threadApplicationServiceMetadataStore struct {
	jobs                      []runner.CommandRuntimeServiceMetadata
	requestedLimit, fullReads int
}

func (s *threadApplicationServiceMetadataStore) GetThread(context.Context, string) (domain.Thread, error) {
	return domain.Thread{ID: "thread-services", ActiveRunID: "successor-run"}, nil
}
func (s *threadApplicationServiceMetadataStore) ListThreadCommandRuntimeServiceMetadata(_ context.Context, _ string, limit int) ([]runner.CommandRuntimeServiceMetadata, error) {
	s.requestedLimit = limit
	return s.jobs, nil
}
func (s *threadApplicationServiceMetadataStore) GetThreadCommandRuntimeJob(context.Context, string, string) (runner.CommandRuntimeJob, error) {
	s.fullReads++
	return runner.CommandRuntimeJob{}, apperror.New(apperror.CodeNotFound, "unavailable")
}
func (s *threadApplicationServiceMetadataStore) FindThreadCommandRuntimeStartCall(_ context.Context, _ string, id runner.CommandRuntimeJobIdentity) (domain.SupervisorToolCall, bool, error) {
	if id.ID == "job-from-message" {
		return domain.SupervisorToolCall{CallID: "start-call", RunID: id.RunID, AttemptID: "start-attempt", Turn: 4}, true, nil
	}
	return domain.SupervisorToolCall{}, false, nil
}
func (s *threadApplicationServiceMetadataStore) SupervisorOperatorMessageID(context.Context, string, string, int) (string, error) {
	return "explicit-message", nil
}
func (s *threadApplicationServiceMetadataStore) GetWorkspaceInfo(context.Context, string) (session.WorkspaceInfo, error) {
	return session.WorkspaceInfo{}, nil
}

func TestThreadApplicationServicesMetadataPreservesOriginalRunAndUnknownSource(t *testing.T) {
	now := time.Now().UTC()
	st := &threadApplicationServiceMetadataStore{jobs: []runner.CommandRuntimeServiceMetadata{
		{Identity: runner.CommandRuntimeJobIdentity{ID: "job-from-message", RunID: "predecessor-run"}, State: runner.CommandRuntimeJobRunning, CreatedAt: now},
		{Identity: runner.CommandRuntimeJobIdentity{ID: "job-without-source", RunID: "successor-run"}, State: runner.CommandRuntimeJobFailed, CreatedAt: now},
		{Identity: runner.CommandRuntimeJobIdentity{ID: "job-not-in-page", RunID: "successor-run"}, State: runner.CommandRuntimeJobCompleted, CreatedAt: now},
	}}
	service := NewThreadApplicationService(st)
	view, err := service.List(t.Context(), "thread-services", 2)
	if err != nil || !view.HasMore || len(view.Services) != 2 || st.requestedLimit != 3 || st.fullReads != 0 {
		t.Fatalf("metadata list=%+v err=%v reads=%d limit=%d", view, err, st.fullReads, st.requestedLimit)
	}
	first, second := view.Services[0], view.Services[1]
	if first.RunID != "predecessor-run" || first.SourceMessageID != "explicit-message" || first.SourceCallID != "start-call" || first.SourceTurn != 4 || first.CanStop {
		t.Fatalf("source binding=%+v", first)
	}
	if second.SourceMessageID != "" || second.SourceCallID != "" || second.SourceTurn != 0 || second.State != "failed" {
		t.Fatalf("unknown source was guessed: %+v", second)
	}
	for _, limit := range []int{-1, 51} {
		if _, err := service.List(t.Context(), "thread-services", limit); apperror.CodeOf(err) != apperror.CodeInvalidArgument {
			t.Fatalf("limit %d: %v", limit, err)
		}
	}
}

func TestThreadApplicationServiceOutputKeepsLocalURLsAndScrubsPrivateInputsAcrossFrames(t *testing.T) {
	root := `D:\private\service`
	page := runner.CommandRuntimeOutputPage{BaseCursor: 0, NextCursor: 200, EndCursor: 200, Frames: []runner.CommandRuntimeFrame{
		{Stream: runner.CommandRuntimeStdout, Text: "Local: http://localhost:5173/\nprivate-environ"},
		{Stream: runner.CommandRuntimeStderr, Text: "secondary https://[::1]:3443/\n"},
		{Stream: runner.CommandRuntimeStdout, Text: "ment-value " + root + "\\entry.js\n"},
	}}
	output := projectThreadServiceOutput(page, root, []string{"private-environment-value"})
	if !strings.Contains(output.Stdout, "http://localhost:5173/") || !strings.Contains(output.Stderr, "https://[::1]:3443/") || strings.Contains(output.Stdout, "private-environment-value") || strings.Contains(output.Stdout, root) || output.NextCursor != 200 {
		t.Fatalf("public output=%+v", output)
	}
	urls := threadServiceCandidateURLs([]runner.CommandRuntimeFrame{{Text: output.Stdout}, {Text: output.Stderr}})
	if len(urls) != 2 || urls[0].URL != "http://127.0.0.1:5173/" || urls[1].URL != "https://[::1]:3443/" || urls[0].Verified || urls[0].Source != "command_output" {
		t.Fatalf("candidates=%+v", urls)
	}
}

func TestThreadApplicationServiceURLCandidatesFailClosed(t *testing.T) {
	for _, raw := range []string{
		"http://example.com:5173/", "http://0.0.0.0:5173/", "http://127.0.0.1.example.com/",
		"http://127.0.0.1:5173/?token=private", "http://127.0.0.1:5173/?",
		"http://user:password@127.0.0.1:5173/", "http://127.0.0.1:5173/#secret",
		"http://127.0.0.1:99999/", "http://127.0.0.1:0/", "file:///tmp/preview.html",
		"http://127.0.0.1:5173/\\secret", "http://localhost:5173/" + strings.Repeat("x", 2048),
	} {
		if urls := threadServiceCandidateURLs([]runner.CommandRuntimeFrame{{Text: raw}}); len(urls) != 0 {
			t.Fatalf("unsafe candidate %q: %+v", raw, urls)
		}
	}
	for _, private := range []string{"http://localhost:5173/", "5173", "private-route"} {
		text := scrubThreadServiceText("http://localhost:5173/private-route", "", []string{private})
		if urls := threadServiceCandidateURLs([]runner.CommandRuntimeFrame{{Text: text}}); len(urls) != 0 {
			t.Fatalf("URL shielding bypassed exact env scrub %q: %q", private, text)
		}
	}
	for _, raw := range []string{`http://localhost:5173/D:/private/service/config`, `http://localhost:5173/%2Fhome%2Fprivate%2Fservice/config`} {
		text := scrubThreadServiceText(raw, "/home/private/service", nil)
		if urls := threadServiceCandidateURLs([]runner.CommandRuntimeFrame{{Text: text}}); len(urls) != 0 {
			t.Fatalf("URL shielding bypassed path scrub: %q", text)
		}
	}
	text := scrubThreadServiceText("http://localhost:5173/%70%72ivate-route", "", []string{"private-route"})
	if urls := threadServiceCandidateURLs([]runner.CommandRuntimeFrame{{Text: text}}); len(urls) != 0 {
		t.Fatalf("URL shielding bypassed decoded env scrub: %q", text)
	}
}

func TestThreadApplicationServiceOutputBoundsExpandedRedaction(t *testing.T) {
	page := runner.CommandRuntimeOutputPage{NextCursor: 8000, EndCursor: 8000, Frames: []runner.CommandRuntimeFrame{{Stream: runner.CommandRuntimeStdout, Text: strings.Repeat("x", 8000)}}}
	output := projectThreadServiceOutput(page, "", []string{"x"})
	if len(output.Stdout) > runner.MaxCommandRuntimeOutputRead/2 || !output.Dropped || output.TruncationReason != "public_output_limit" {
		t.Fatalf("expanded redaction is unbounded: bytes=%d output=%+v", len(output.Stdout), output)
	}
}
