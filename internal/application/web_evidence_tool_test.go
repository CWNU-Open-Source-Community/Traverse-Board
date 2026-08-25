package application_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/store"
	"cyberagent-workbench/internal/toolgateway"
	"cyberagent-workbench/internal/webevidence"
)

type applicationWebFetchBackend struct{ calls int }

func (f *applicationWebFetchBackend) Fetch(_ context.Context, rawURL string,
	_ webevidence.NetworkAuthority,
) (webevidence.FetchedContent, error) {
	f.calls++
	return webevidence.FetchedContent{RequestedURL: rawURL, FinalURL: rawURL,
		RawDigest: webevidence.DigestBytes([]byte("raw application evidence")),
		Robots:    "allowed", Parsed: webevidence.ParsedDocument{Title: "Application evidence",
			Body: "private bounded evidence body", MIME: "text/html", Charset: "utf-8"}}, nil
}

func TestWebEvidenceExecutorRechecksPersistedRunAuthorityAndHasNoSearchFallback(t *testing.T) {
	ctx := context.Background()
	state, err := store.Open(filepath.Join(t.TempDir(), "web-evidence-application.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	mission, created, err := application.NewRunService(state).Create(ctx,
		application.CreateRunRequest{Goal: "Review a public source", Profile: "review",
			NetworkMode: "allowlist", AllowedTargets: []string{"docs.example.com"},
			Budget: domain.Budget{MaxTurns: 4, MaxToolCalls: 8}})
	if err != nil {
		t.Fatal(err)
	}
	run, err := application.NewRunService(state).Start(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	mode, err := state.GetRunMode(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	permission, err := state.GetRunExecutionPermission(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	root, found, err := state.GetRootAgent(ctx, run.ID)
	if err != nil || !found {
		t.Fatalf("root found=%t err=%v", found, err)
	}
	lease, err := state.AcquireRunExecutionLease(ctx,
		domain.AcquireRunExecutionLeaseRequest{RunID: run.ID,
			OwnerID: "web-evidence-application-test", TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	backend := &applicationWebFetchBackend{}
	oldFetchTime := time.Now().UTC().Add(-48 * time.Hour)
	service := webevidence.NewService(state, nil, backend).WithClock(func() time.Time {
		return oldFetchTime
	})
	executor, err := application.NewWebEvidenceToolExecutor(state, service)
	if err != nil {
		t.Fatal(err)
	}
	capabilityContext := toolgateway.WebEvidenceCapabilityContext{RunID: run.ID,
		MissionID: mission.ID, SessionID: run.SessionID, RootAgentID: root.ID,
		WorkspaceID: mission.WorkspaceID, Surface: mode.Surface, Phase: mode.Phase,
		Role: root.Role, Profile: mode.Profile, PermissionMode: permission.Mode,
		PermissionRevision: permission.Revision, ModeRevision: mode.Revision,
		NetworkMode:    mode.Scope.NetworkMode,
		AllowedTargets: append([]string(nil), mode.Scope.AllowedTargets...)}
	scope := toolgateway.WebEvidenceExecutionScope{InvocationID: "web-invocation-1",
		OperationKey: "application-fetch-operation", RunID: run.ID,
		MissionID: mission.ID, SessionID: run.SessionID, RootAgentID: root.ID,
		WorkspaceID: mission.WorkspaceID, Surface: mode.Surface, Phase: mode.Phase,
		Role: root.Role, Profile: mode.Profile, PermissionMode: permission.Mode,
		PermissionRevision: permission.Revision, ModeRevision: mode.Revision,
		CapabilityGeneration: toolgateway.WebEvidenceCapabilitySnapshot(capabilityContext).Generation,
		LeaseID:              lease.Lease.LeaseID, LeaseGeneration: lease.Lease.Generation,
		RequestedBy: "run_supervisor", PolicyDecision: toolgateway.Decision{Allowed: true,
			Approval: toolgateway.ApprovalAutomatic, Risk: "medium", Reason: "test policy"}}

	result, err := executor.ExecuteWebEvidence(ctx, scope, toolgateway.WebFetchTool,
		json.RawMessage(`{"version":"web_fetch.v1","url":"https://docs.example.com/report"}`))
	if err != nil || backend.calls != 1 || result.Metadata["untrusted"] != "true" ||
		result.Metadata["instruction_authorized"] != "false" ||
		result.Metadata["stale"] != "true" || result.Metadata["state"] != "stale" ||
		!strings.Contains(result.Content, "private bounded evidence body") {
		t.Fatalf("result=%#v calls=%d err=%v", result, backend.calls, err)
	}
	replayScope := scope
	replayScope.InvocationID = "web-invocation-replay"
	replay, err := executor.ExecuteWebEvidence(ctx, replayScope, toolgateway.WebFetchTool,
		json.RawMessage(`{"version":"web_fetch.v1","url":"https://docs.example.com/report"}`))
	if err != nil || backend.calls != 1 || replay.Content != result.Content ||
		replay.Metadata["replayed"] != "true" || result.Metadata["replayed"] != "false" {
		t.Fatalf("replay=%#v original=%#v calls=%d err=%v", replay, result, backend.calls, err)
	}

	stale := scope
	stale.InvocationID = "web-invocation-2"
	stale.OperationKey = "application-stale-operation"
	stale.ModeRevision++
	if _, err := executor.ExecuteWebEvidence(ctx, stale, toolgateway.WebFetchTool,
		json.RawMessage(`{"version":"web_fetch.v1","url":"https://docs.example.com/report"}`)); apperror.CodeOf(err) != apperror.CodeFailedPrecondition || backend.calls != 1 {
		t.Fatalf("stale code=%s calls=%d err=%v", apperror.CodeOf(err), backend.calls, err)
	}

	searchScope := scope
	searchScope.InvocationID = "web-invocation-3"
	searchScope.OperationKey = "application-search-operation"
	_, err = executor.ExecuteWebEvidence(ctx, searchScope, toolgateway.WebSearchTool,
		json.RawMessage(`{"version":"web_search.v1","query":"public spec","limit":3}`))
	if apperror.CodeOf(err) != apperror.CodeFailedPrecondition ||
		!strings.Contains(err.Error(), "no fallback provider") || backend.calls != 1 {
		t.Fatalf("search code=%s calls=%d err=%v", apperror.CodeOf(err), backend.calls, err)
	}
}
