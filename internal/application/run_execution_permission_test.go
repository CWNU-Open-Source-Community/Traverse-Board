package application

import (
	"strings"
	"testing"

	"cyberagent-workbench/internal/domain"
)

func TestWorkspaceAccessPermissionRequiresExactOperatorConfirmation(t *testing.T) {
	base := ChangeRunExecutionPermissionRequest{RunID: "run-workspace-access",
		Mode:         string(domain.RunExecutionPermissionWorkspaceAccess),
		OperationKey: "workspace-access-confirmation-0001", RequestedBy: "operator",
		Reason: "select the bounded Workspace permission"}
	if _, _, _, err := normalizeChangeRunExecutionPermissionRequest(base); err == nil ||
		!strings.Contains(err.Error(), "exact sandbox-boundary confirmation") {
		t.Fatalf("missing Workspace confirmation error=%v", err)
	}
	base.ConfirmWorkspaceAccess = true
	normalized, mode, confirmed, err := normalizeChangeRunExecutionPermissionRequest(base)
	if err != nil || mode != domain.RunExecutionPermissionWorkspaceAccess ||
		!confirmed || normalized.Mode != string(mode) {
		t.Fatalf("normalized=%+v mode=%s confirmed=%t err=%v",
			normalized, mode, confirmed, err)
	}
	base.ConfirmUserApproval = true
	if _, _, _, err := normalizeChangeRunExecutionPermissionRequest(base); err == nil {
		t.Fatal("Workspace confirmation accepted an unrelated approval flag")
	}
}

func TestExecutionPermissionRejectsNonOperatorAuthoritySources(t *testing.T) {
	for _, requester := range []string{"model", "agent", "skill", "repository",
		"project_config", "recovery_data", "mcp", "plugin", "hook"} {
		request := ChangeRunExecutionPermissionRequest{RunID: "run-authority-source",
			Mode:         string(domain.RunExecutionPermissionWorkspaceAccess),
			OperationKey: "permission-source-" + requester + "-0001",
			RequestedBy:  requester, Reason: "attempt unauthorized selection",
			ConfirmWorkspaceAccess: true}
		if _, _, _, err := normalizeChangeRunExecutionPermissionRequest(request); err == nil {
			t.Fatalf("requester %q selected a permission mode", requester)
		}
	}
}
