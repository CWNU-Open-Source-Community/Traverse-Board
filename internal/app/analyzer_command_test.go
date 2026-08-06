package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmbeddedAnalyzerCLIExecutesWorkspaceFileAndCommitsArtifact(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CYBERAGENT_HOME", home)
	if _, stderr, code := executeTestCommand(t, "workspace", "init", "demo"); code != 0 {
		t.Fatalf("workspace init failed: %s", stderr)
	}
	inputPath := filepath.Join(home, "workspaces", "demo", "attachments", "sample.txt")
	if err := os.WriteFile(inputPath, []byte("first line\nsecond line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	created, stderr, code := executeTestCommand(t, "run", "create", "analyze sample",
		"--workspace", "demo", "--profile", "code")
	if code != 0 {
		t.Fatalf("run create failed: output=%s stderr=%s", created, stderr)
	}
	runID := runIDPattern.FindString(created)
	if runID == "" {
		t.Fatalf("run id missing: %s", created)
	}
	if _, stderr, code := executeTestCommand(t, "analyzer", "execute", "--run", runID,
		"--file", "attachments/sample.txt", "--confirm", "WRONG"); code == 0 ||
		!strings.Contains(stderr, "confirmation") {
		t.Fatalf("wrong confirmation passed: code=%d stderr=%s", code, stderr)
	}
	output, stderr, code := executeTestCommand(t, "analyzer", "execute", "--run", runID,
		"--file", "attachments/sample.txt", "--confirm", "RUN-EMBEDDED-ANALYZER")
	artifactID := artifactIDPattern.FindString(output)
	if code != 0 || artifactID == "" || !strings.Contains(output, "input_bytes: 23") ||
		!strings.Contains(output, "lines: 2") || !strings.Contains(output, "filesystem: false") {
		t.Fatalf("analyzer execute failed: code=%d output=%s stderr=%s", code, output, stderr)
	}
	content, stderr, code := executeTestCommand(t, "artifact", "read", artifactID)
	if code != 0 || !strings.Contains(content, `"protocol_version":"analyzer_result.v1"`) ||
		!strings.Contains(content, `"input_bytes":23`) || strings.Contains(content, "first line") {
		t.Fatalf("artifact output is invalid: code=%d output=%s stderr=%s", code, content, stderr)
	}
	timeline, stderr, code := executeTestCommand(t, "run", "events", runID)
	for _, eventType := range []string{"analyzer.execution_capability_issued",
		"analyzer.execution_capability_consumed", "analyzer.execution_completed", "artifact.created"} {
		if code != 0 || strings.Count(timeline, eventType) != 1 {
			t.Fatalf("timeline missing %s: code=%d output=%s stderr=%s", eventType, code, timeline, stderr)
		}
	}
	if strings.Contains(timeline, "first line") || strings.Contains(timeline, "content_base64") {
		t.Fatalf("timeline leaked analyzer input: %s", timeline)
	}
}

func TestEmbeddedAnalyzerCLIRejectsWorkspaceEscapeAndAmbiguousInput(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CYBERAGENT_HOME", home)
	if _, stderr, code := executeTestCommand(t, "workspace", "init", "demo"); code != 0 {
		t.Fatalf("workspace init failed: %s", stderr)
	}
	created, stderr, code := executeTestCommand(t, "run", "create", "analyze sample", "--workspace", "demo")
	if code != 0 {
		t.Fatalf("run create failed: %s", stderr)
	}
	runID := runIDPattern.FindString(created)
	if _, stderr, code := executeTestCommand(t, "analyzer", "execute", "--run", runID,
		"--file", "../outside.txt", "--confirm", "RUN-EMBEDDED-ANALYZER"); code == 0 ||
		(!strings.Contains(stderr, "resolve analyzer input") && !strings.Contains(stderr, "escapes")) {
		t.Fatalf("workspace escape passed: code=%d stderr=%s", code, stderr)
	}
	if _, stderr, code := executeTestCommand(t, "analyzer", "execute", "--run", runID,
		"--text", "inline", "--file", "attachments/input.txt",
		"--confirm", "RUN-EMBEDDED-ANALYZER"); code == 0 || !strings.Contains(stderr, "usage") {
		t.Fatalf("ambiguous input passed: code=%d stderr=%s", code, stderr)
	}
}
