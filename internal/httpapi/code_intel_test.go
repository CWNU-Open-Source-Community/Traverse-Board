package httpapi

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/codeintel"
)

type codeIntelSourceFake struct {
	servers        []codeintel.CapabilitySnapshot
	qualifications []codeintel.Qualification
}

func (f codeIntelSourceFake) Inventory() []codeintel.CapabilitySnapshot {
	return append([]codeintel.CapabilitySnapshot(nil), f.servers...)
}

func (f codeIntelSourceFake) Qualify(context.Context, string, string) []codeintel.Qualification {
	return append([]codeintel.Qualification(nil), f.qualifications...)
}

func TestCodeIntelInventoryProjectsBoundedMetadataOnlyState(t *testing.T) {
	fixture := newAPIFixture(t)
	digest := strings.Repeat("a", 64)
	generation := strings.Repeat("b", 64)
	capabilityFingerprint := strings.Repeat("c", 64)
	qualifiedAt := time.Date(2026, 8, 20, 1, 2, 3, 0, time.UTC)
	capabilities := codeintel.Capabilities{WorkspaceSymbols: true, DocumentSymbols: true,
		Definition: true, References: true, Implementation: true, Hover: true,
		SignatureHelp: true, Diagnostics: true, CallHierarchy: true, TypeHierarchy: true}
	source := codeIntelSourceFake{
		servers: []codeintel.CapabilitySnapshot{{
			ProtocolVersion: codeintel.ProtocolVersion, ServerID: "gopls", ServerName: "gopls",
			WorkspaceID: fixture.workspace.ID, Languages: []string{"go"},
			Source: codeintel.Source{Kind: "operator_config", Label: "code-intel.json",
				SHA256: digest},
			DescriptorFingerprint: digest, CapabilityFingerprint: capabilityFingerprint,
			Generation: generation, Health: codeintel.HealthHealthy,
			Capabilities: capabilities, ModelVisibleTools: capabilities.ToolNames(),
			ServerVersion: "v0.20.0", ProcessOwned: true, ReadOnly: true,
			QualifiedAt: &qualifiedAt,
		}},
		qualifications: []codeintel.Qualification{{
			ProtocolVersion: codeintel.ProtocolVersion, ServerID: "gopls",
			WorkspaceID: fixture.workspace.ID, Eligible: true, Health: codeintel.HealthConfigured,
			DescriptorFingerprint: digest, ExecutableHashMatched: true, Reviewed: true,
			ProcessOwned: true, MinimalEnvironment: true,
		}},
	}
	otherWorkspaceServer := source.servers[0]
	otherWorkspaceServer.ServerID = "gopls-other"
	otherWorkspaceServer.WorkspaceID = "workspace-other"
	source.servers = append(source.servers, otherWorkspaceServer)
	api, err := New(fixture.store, Config{AccessToken: testAccessToken,
		CodeIntelSource: source, AppVersion: "code-intel-http-test"})
	if err != nil {
		t.Fatal(err)
	}
	response := performSessionMessageRequest(t, api, http.MethodGet,
		CodeIntelInventoryPath+"?workspace_id="+fixture.workspace.ID,
		testAccessToken, "", "", nil)
	var view CodeIntelInventoryView
	decodeDataStatus(t, response, http.StatusOK, &view)
	if view.ProtocolVersion != codeintel.ProtocolVersion || !view.Enabled ||
		len(view.Servers) != 1 || len(view.Qualifications) != 1 ||
		view.Servers[0].Generation != generation ||
		view.Servers[0].CapabilityFingerprint != capabilityFingerprint ||
		view.Servers[0].Health != string(codeintel.HealthHealthy) ||
		len(view.Servers[0].ModelVisibleTools) != 10 ||
		!view.Qualifications[0].Eligible {
		t.Fatalf("unexpected Code Intel inventory: %#v", view)
	}
	raw := strings.ToLower(response.Body.String())
	for _, forbidden := range []string{`"executable"`, `"arguments"`, `"argv"`,
		`"environment"`, `"credential"`, `"stdout"`, `"stderr"`} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("Code Intel projection exposed %s: %s", forbidden, raw)
		}
	}
	capabilityResponse := performSessionMessageRequest(t, api, http.MethodGet,
		"/api/v1/capabilities", testAccessToken, "", "", nil)
	var runtime RuntimeCapabilitiesView
	decodeDataStatus(t, capabilityResponse, http.StatusOK, &runtime)
	if !runtime.CodeIntelEnabled {
		t.Fatalf("Code Intel runtime capability was not projected: %#v", runtime)
	}
}

func TestCodeIntelInventoryRejectsUnknownOrRepeatedQueries(t *testing.T) {
	fixture := newAPIFixture(t)
	api, err := New(fixture.store, Config{AccessToken: testAccessToken,
		CodeIntelSource: codeIntelSourceFake{}, AppVersion: "code-intel-query-test"})
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{
		CodeIntelInventoryPath + "?unexpected=value",
		CodeIntelInventoryPath + "?workspace_id=a&workspace_id=b",
		CodeIntelInventoryPath + "?workspace_id=",
	} {
		response := performSessionMessageRequest(t, api, http.MethodGet, target,
			testAccessToken, "", "", nil)
		assertAPIError(t, response, http.StatusBadRequest, "INVALID_ARGUMENT")
	}
}

func TestCodeIntelInventoryRejectsInvalidRuntimeMetadata(t *testing.T) {
	fixture := newAPIFixture(t)
	digest := strings.Repeat("a", 64)
	invalidServer := codeintel.CapabilitySnapshot{
		ProtocolVersion: codeintel.ProtocolVersion, ServerID: "gopls", ServerName: "gopls",
		WorkspaceID: fixture.workspace.ID, Languages: []string{"typescript", "go"},
		Source: codeintel.Source{Kind: "operator_config", Label: "code-intel.json",
			SHA256: digest},
		DescriptorFingerprint: digest, Health: codeintel.HealthConfigured,
		ProcessOwned: true, ReadOnly: true,
	}
	api, err := New(fixture.store, Config{AccessToken: testAccessToken,
		CodeIntelSource: codeIntelSourceFake{servers: []codeintel.CapabilitySnapshot{
			invalidServer}}, AppVersion: "code-intel-invalid-server-test"})
	if err != nil {
		t.Fatal(err)
	}
	response := performSessionMessageRequest(t, api, http.MethodGet,
		CodeIntelInventoryPath, testAccessToken, "", "", nil)
	assertAPIError(t, response, http.StatusServiceUnavailable, "UNAVAILABLE")

	invalidQualification := codeintel.Qualification{
		ProtocolVersion: codeintel.ProtocolVersion, ServerID: "gopls",
		WorkspaceID: fixture.workspace.ID, Eligible: true, Health: codeintel.HealthConfigured,
		DescriptorFingerprint: digest, ExecutableHashMatched: true, Reviewed: true,
		ProcessOwned: true, MinimalEnvironment: true, Reason: "contradictory reason",
	}
	api, err = New(fixture.store, Config{AccessToken: testAccessToken,
		CodeIntelSource: codeIntelSourceFake{qualifications: []codeintel.Qualification{
			invalidQualification}}, AppVersion: "code-intel-invalid-qualification-test"})
	if err != nil {
		t.Fatal(err)
	}
	response = performSessionMessageRequest(t, api, http.MethodGet,
		CodeIntelInventoryPath+"?workspace_id="+fixture.workspace.ID,
		testAccessToken, "", "", nil)
	assertAPIError(t, response, http.StatusServiceUnavailable, "UNAVAILABLE")
}
