package app

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"cyberagent-workbench/internal/domain"
)

var threadIDPattern = regexp.MustCompile(`thread-run-[0-9]{14}-[a-f0-9]{12}`)

func TestThreadCLIUsesStableIdentityAcrossRunSuccessionAndLifecycle(t *testing.T) {
	t.Setenv("CYBERAGENT_HOME", t.TempDir())
	created, stderr, code := executeTestCommand(t, "thread", "create",
		"stable CLI task", "--profile", "review", "--max-turns", "4")
	if code != 0 || stderr != "" {
		t.Fatalf("create stdout=%s stderr=%s code=%d", created, stderr, code)
	}
	threadID := threadIDPattern.FindString(created)
	runID := runIDPattern.FindString(created)
	if threadID == "" || runID == "" || !strings.Contains(created, "status: active") {
		t.Fatalf("stable identities missing from %s", created)
	}
	listed, stderr, code := executeTestCommand(t, "thread", "list")
	if code != 0 || stderr != "" || !strings.Contains(listed, threadID) ||
		!strings.Contains(listed, "stable CLI task") {
		t.Fatalf("list stdout=%s stderr=%s code=%d", listed, stderr, code)
	}
	if _, stderr, code = executeTestCommand(t, "run", "cancel", runID); code != 0 {
		t.Fatalf("cancel stderr=%s code=%d", stderr, code)
	}
	continued, stderr, code := executeTestCommand(t, "thread", "send", threadID,
		"continue after cancellation", "--operation-key", "thread-cli-send-operation-0001")
	if code != 0 || stderr != "" || !strings.Contains(continued, "successor_created: true") ||
		!strings.Contains(continued, "predecessor_run: "+runID) {
		t.Fatalf("continuation stdout=%s stderr=%s code=%d", continued, stderr, code)
	}
	successorMatch := regexp.MustCompile(`(?m)^run: (run-[0-9]{14}-[a-f0-9]{12})$`).
		FindStringSubmatch(continued)
	successorID := ""
	if len(successorMatch) == 2 {
		successorID = successorMatch[1]
	}
	if successorID == "" || successorID == runID {
		t.Fatalf("successor identity missing: %s", continued)
	}
	threadVersion := func() string {
		t.Helper()
		shown, shownStderr, shownCode := executeTestCommand(t, "thread", "show",
			threadID, "--json")
		if shownCode != 0 || shownStderr != "" {
			t.Fatalf("show stdout=%s stderr=%s code=%d", shown, shownStderr, shownCode)
		}
		var projection struct {
			Thread domain.Thread `json:"thread"`
		}
		if err := json.Unmarshal([]byte(shown), &projection); err != nil {
			t.Fatal(err)
		}
		return strconv.FormatInt(projection.Thread.Version, 10)
	}
	archiveVersion := threadVersion()

	archived, stderr, code := executeTestCommand(t, "thread", "archive", threadID,
		"--expected-version", archiveVersion,
		"--operation-key", "thread-cli-archive-operation-0001")
	if code != 0 || stderr != "" || !strings.Contains(archived, "status: archived") {
		t.Fatalf("archive stdout=%s stderr=%s code=%d", archived, stderr, code)
	}
	replayed, stderr, code := executeTestCommand(t, "thread", "archive", threadID,
		"--expected-version", archiveVersion,
		"--operation-key", "thread-cli-archive-operation-0001")
	if code != 0 || stderr != "" || replayed != archived {
		t.Fatalf("archive replay stdout=%s first=%s stderr=%s code=%d",
			replayed, archived, stderr, code)
	}
	restored, stderr, code := executeTestCommand(t, "thread", "restore", threadID,
		"--expected-version", threadVersion(),
		"--operation-key", "thread-cli-restore-operation-0001")
	if code != 0 || stderr != "" || !strings.Contains(restored, "status: active") {
		t.Fatalf("restore stdout=%s stderr=%s code=%d", restored, stderr, code)
	}

	exported, stderr, code := executeTestCommand(t, "thread", "export", threadID)
	if code != 0 || stderr != "" {
		t.Fatalf("export stderr=%s code=%d", stderr, code)
	}
	var bundle domain.ThreadExport
	if err := json.Unmarshal([]byte(exported), &bundle); err != nil {
		t.Fatal(err)
	}
	if bundle.Thread.ID != threadID || len(bundle.Runs) != 2 || len(bundle.Sessions) != 2 ||
		len(bundle.Bindings) != 2 ||
		len(bundle.Messages) != 1 || len(bundle.Events) < 5 || len(bundle.AuditEvents) == 0 {
		t.Fatalf("lossless CLI export=%#v", bundle)
	}
}
