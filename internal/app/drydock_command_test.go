package app

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/drydock"
)

func TestDrydockCLIRequiresPinnedTrustAndEmitsLifecycleReceipts(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}
	home := t.TempDir()
	t.Setenv("CYBERAGENT_HOME", home)
	if _, stderr, code := executeTestCommand(t, "workspace", "init", "drydock-cli"); code != 0 {
		t.Fatalf("workspace init failed: %s", stderr)
	}
	root := filepath.Join(home, "workspaces", "drydock-cli")
	runDrydockCLIGit(t, root, "init", "-q", "-b", "main")
	runDrydockCLIGit(t, root, "config", "user.email", "drydock@example.invalid")
	runDrydockCLIGit(t, root, "config", "user.name", "Drydock CLI Test")
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runDrydockCLIGit(t, root, "add", "tracked.txt")
	runDrydockCLIGit(t, root, "commit", "-q", "-m", "base")

	createdRun, stderr, code := executeTestCommand(t, "run", "create",
		"Drydock CLI lifecycle", "--workspace", "drydock-cli", "--profile", "code",
		"--surface", "code", "--phase", "deliver", "--max-turns", "3")
	if code != 0 || stderr != "" {
		t.Fatalf("run create output=%q stderr=%q code=%d", createdRun, stderr, code)
	}
	runID := runIDPattern.FindString(createdRun)
	if runID == "" {
		t.Fatalf("missing Run identity: %s", createdRun)
	}

	previewJSON, stderr, code := executeTestCommand(t, "drydock", "create",
		"--run", runID, "--operation-key", "drydock-cli-preview-0001", "--json")
	var preview application.DrydockCreateResult
	if code != 0 || stderr != "" || json.Unmarshal([]byte(previewJSON), &preview) != nil ||
		!preview.TrustRequired || preview.TrustDigest == "" ||
		preview.Source.WorkspaceID == "" || preview.Source.BaseCommit == "" {
		t.Fatalf("trust preview=%s stderr=%q code=%d parsed=%+v", previewJSON,
			stderr, code, preview)
	}
	confirmedJSON, stderr, code := executeTestCommand(t, "drydock", "create",
		"--run", runID, "--operation-key", "drydock-cli-create-0001",
		"--confirm-workspace-trust", "--expected-trust-digest", preview.TrustDigest, "--json")
	var confirmed application.DrydockCreateResult
	if code != 0 || stderr != "" || json.Unmarshal([]byte(confirmedJSON), &confirmed) != nil ||
		confirmed.Workspace == nil || confirmed.Receipt == nil || confirmed.Checkpoint == nil ||
		confirmed.Workspace.State != drydock.StateReady ||
		confirmed.Receipt.GrantsProcessAuthority {
		t.Fatalf("confirmed create=%s stderr=%q code=%d parsed=%+v", confirmedJSON,
			stderr, code, confirmed)
	}

	statusJSON, stderr, code := executeTestCommand(t, "drydock", "status", "--run",
		runID, "--json")
	var projection application.DrydockProjection
	if code != 0 || stderr != "" || json.Unmarshal([]byte(statusJSON), &projection) != nil ||
		projection.Workspace == nil || projection.Trust == nil ||
		projection.Trust.GrantsProcessAuthority || len(projection.Receipts) != 1 ||
		projection.Receipts[0].Operation != drydock.OperationCreate {
		t.Fatalf("status=%s stderr=%q code=%d parsed=%+v", statusJSON, stderr,
			code, projection)
	}

	cleanupJSON, stderr, code := executeTestCommand(t, "drydock", "cleanup",
		"--run", runID, "--generation", "2", "--operation-key",
		"drydock-cli-cleanup-0001", "--confirm", "--json")
	var cleaned application.DrydockCleanupResult
	if code != 0 || stderr != "" || json.Unmarshal([]byte(cleanupJSON), &cleaned) != nil ||
		cleaned.Workspace.State != drydock.StateCleaned ||
		cleaned.Receipt.Operation != drydock.OperationCleanup || cleaned.Preserved {
		t.Fatalf("cleanup=%s stderr=%q code=%d parsed=%+v", cleanupJSON, stderr,
			code, cleaned)
	}
}

func runDrydockCLIGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, args...)...)
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v: %s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}
