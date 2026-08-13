package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
)

type testWorkspaceDirectoryPicker struct {
	path string
	err  error
}

func (p testWorkspaceDirectoryPicker) OpenWorkspaceDirectory(context.Context) (string, error) {
	return p.path, p.err
}

type testWorkspaceDirectoryRegistrar struct {
	selectedPath string
	result       WorkspaceImportSummary
	err          error
}

func (r *testWorkspaceDirectoryRegistrar) RegisterWorkspaceDirectory(_ context.Context,
	selectedPath string) (WorkspaceImportSummary, error) {
	r.selectedPath = selectedPath
	return r.result, r.err
}

func TestDesktopWorkspaceImportIsPathlessAndNonAuthorizing(t *testing.T) {
	privatePath := `C:\Users\operator\private-project`
	registrar := &testWorkspaceDirectoryRegistrar{result: WorkspaceImportSummary{
		ID: "ws-import-0123456789abcdef", Name: "private-project",
		CreatedAt: time.Date(2026, 8, 13, 1, 2, 3, 0, time.UTC),
	}}
	bridge := newWorkspaceImportBridge(t,
		testWorkspaceDirectoryPicker{path: privatePath}, registrar)

	result, err := bridge.ImportWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	if registrar.selectedPath != privatePath || result.Status != WorkspaceImportRegistered ||
		result.Workspace == nil || result.Workspace.ID != registrar.result.ID ||
		result.RootPathExposed || result.RendererPathInputSupported ||
		result.DirectoryContentModified || result.AgentAuthorityGranted {
		t.Fatalf("unexpected workspace import result: %#v", result)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), privatePath) {
		t.Fatalf("workspace import disclosed a host path: %s", raw)
	}
	assertExactJSONKeys(t, string(raw), []string{
		"agent_authority_granted", "directory_content_modified", "protocol_version",
		"renderer_path_input_supported", "root_path_exposed", "status", "workspace",
	})
}

func TestDesktopWorkspaceImportCancellationCreatesNothing(t *testing.T) {
	registrar := &testWorkspaceDirectoryRegistrar{}
	bridge := newWorkspaceImportBridge(t, testWorkspaceDirectoryPicker{}, registrar)
	result, err := bridge.ImportWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != WorkspaceImportCancelled || result.Workspace != nil ||
		registrar.selectedPath != "" {
		t.Fatalf("unexpected cancelled import: %#v registrar=%#v", result, registrar)
	}
}

func TestDesktopWorkspaceImportRedactsNativeAndRegistrationFailures(t *testing.T) {
	privatePath := `C:\Users\operator\private-project`
	for _, test := range []struct {
		name      string
		pickerErr error
		storeErr  error
		wantCode  apperror.Code
	}{
		{name: "picker", pickerErr: errors.New(privatePath), wantCode: apperror.CodeUnavailable},
		{name: "registration", storeErr: errors.New(privatePath), wantCode: apperror.CodeUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			bridge := newWorkspaceImportBridge(t,
				testWorkspaceDirectoryPicker{path: privatePath, err: test.pickerErr},
				&testWorkspaceDirectoryRegistrar{err: test.storeErr})
			_, err := bridge.ImportWorkspace()
			if apperror.CodeOf(err) != test.wantCode || strings.Contains(err.Error(), privatePath) {
				t.Fatalf("error = %v, want redacted %s", err, test.wantCode)
			}
		})
	}
}

func newWorkspaceImportBridge(t *testing.T, picker WorkspaceDirectoryPicker,
	registrar WorkspaceDirectoryRegistrar) *DesktopBridge {
	t.Helper()
	selector, preview := NewSkillPackagePreviewBoundary()
	bridge, err := NewDesktopBridge(DesktopBridgeConfig{
		ContextProvider: func() context.Context { return context.Background() },
		FilePicker:      &testSkillPackagePicker{}, ReadToken: testDesktopReadToken,
		ControlToken: testDesktopControlToken, RunCreationEnabled: true,
		APIVersion: "api.v1", AppVersion: "test", UIDigest: testDesktopUIDigest,
		Selector: selector, PreviewBridge: preview,
		WorkspaceDirectoryPicker: picker, WorkspaceRegistrar: registrar,
	})
	if err != nil {
		t.Fatal(err)
	}
	return bridge
}
