package store

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/toolgateway"
)

func progressBrowserAuthority(t *testing.T, f *progressToolFixture, session, boot string) toolgateway.AgentBrowserCallAuthority {
	t.Helper()
	a := toolgateway.AgentBrowserCallAuthority{ProtocolVersion: toolgateway.AgentBrowserAuthorityVersion,
		RunID: f.run.ID, MissionID: f.scope.MissionID, SessionID: f.run.SessionID,
		RootAgentID: f.scope.RootAgentID, WorkspaceID: f.scope.WorkspaceID,
		Surface: f.scope.Surface, Phase: f.scope.Phase, Role: f.scope.Role, Profile: f.scope.Profile,
		PermissionMode: f.scope.PermissionMode, ModeRevision: f.scope.ModeRevision,
		PermissionSnapshotID: f.scope.PermissionSnapshotID, PermissionRevision: f.scope.PermissionRevision,
		PermissionActivation: 1, RunAuthorizationFence: 1, ManagerBootID: boot,
		BrowserSessionID: session, SessionGeneration: 1}
	a.Generation = a.Fingerprint()
	if err := a.Validate(); err != nil {
		t.Fatal(err)
	}
	return a
}

func completeProgressBrowserState(t *testing.T, f *progressToolFixture, names []string, salt int,
	mutate func(map[string]any, map[string]any),
) completedProgressTurn {
	t.Helper()
	a := progressBrowserAuthority(t, f, fmt.Sprintf("browser-session-%d", salt), fmt.Sprintf("browser-boot-%d", salt))
	f.authority = progressJSON(t, a)
	var elements []map[string]any
	for i, name := range names {
		elements = append(elements, map[string]any{"ref": fmt.Sprintf("ref-%d-%d", salt, i),
			"role": "button", "name": name, "tag": "button", "type": "button", "disabled": false})
	}
	if elements == nil {
		elements = []map[string]any{}
	}
	snapshot := map[string]any{"version": "agent-browser-runtime.v1", "session_id": a.BrowserSessionID,
		"snapshot_id": fmt.Sprintf("snapshot-%d", salt), "canonical_url": fmt.Sprintf("https://example.test/panel?nonce=%d#%d", salt, salt),
		"document_epoch": salt + 1, "elements": elements, "truncated": false, "untrusted_evidence": true,
		"frames_supported": false, "completed_at": time.Now().UTC().Format(time.RFC3339Nano)}
	envelope := map[string]any{"version": "supervisor_tool_result.v1", "tool": "browser_snapshot", "status": "completed",
		"metadata": map[string]string{"agent_browser_session_id": a.BrowserSessionID, "manager_boot_id": a.ManagerBootID}}
	if mutate != nil {
		mutate(snapshot, envelope)
	}
	envelope["stdout"] = string(progressJSON(t, snapshot))
	cp, callID := f.beginCall(t, "browser_snapshot", json.RawMessage(`{"version":"browser_snapshot.v2"}`))
	f.recordResult(t, cp, callID, domain.SupervisorToolCompleted, "", string(progressJSON(t, envelope)))
	return f.finishCall(t, cp, "continue accepted browser checks")
}

func TestRunProgressGuardNewBrowserControlStatesAreProgress(t *testing.T) {
	f := newProgressToolFixture(t)
	states := [][]string{{"展开全部", "加入队列"}, {"收起", "全文", "删除"}, {"收起", "收起全文", "删除"}, {"删除", "全文", "收起"}, {"收起", "全文"}, {}}
	seen := map[string]bool{}
	var previous string
	for index, names := range states {
		completed := completeProgressBrowserState(t, f, names, index, nil)
		guard := f.guard(t)
		isOld := index == 3 // same whole state as index 1, with different order/refs/session/URL
		if completed.run.Status != domain.RunRunning || (!isOld && guard.StateFingerprint == previous) ||
			(isOld && guard.StateFingerprint != previous) {
			t.Fatalf("state %d progress is wrong: run=%s guard=%+v", index, completed.run.Status, guard)
		}
		if isOld && guard.StagnantTurnCount != 2 || !isOld && guard.StagnantTurnCount != 1 {
			t.Fatalf("state %d changed the original guard counters: %+v", index, guard)
		}
		seen[guard.StateFingerprint] = true
		previous = guard.StateFingerprint
	}
	if len(seen) != 5 {
		t.Fatalf("whole-state novelty, including deletions, should be five: %d", len(seen))
	}
}

func TestRunProgressGuardBrowserRefSessionURLChurnStillPauses(t *testing.T) {
	f := newProgressToolFixture(t)
	var fingerprint string
	for index := 0; index < domain.RunProgressRepeatThreshold; index++ {
		names := []string{"same", "same", "submit"}
		if index%2 == 1 {
			names = []string{"submit", "same", "same"}
		}
		completed := completeProgressBrowserState(t, f, names, index, nil)
		guard := f.guard(t)
		if index == 0 {
			fingerprint = guard.StateFingerprint
		}
		if guard.StateFingerprint != fingerprint || guard.RepeatedActionCount != index+1 ||
			(index == domain.RunProgressRepeatThreshold-1 && completed.run.Status != domain.RunPaused) {
			t.Fatalf("opaque identity or URL churn invented progress: %+v run=%s", guard, completed.run.Status)
		}
	}
}

func TestRunProgressGuardInvalidBrowserSnapshotsDoNotInventProgress(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any, map[string]any)
	}{
		{"inner truncated", func(s, _ map[string]any) { s["truncated"] = true }},
		{"missing truncated", func(s, _ map[string]any) { delete(s, "truncated") }},
		{"outer truncated", func(_, e map[string]any) { e["truncated"] = true }},
		{"unknown outcome", func(_, e map[string]any) { e["status"] = "outcome_unknown" }},
		{"missing metadata", func(_, e map[string]any) { delete(e, "metadata") }},
		{"wrong boot", func(_, e map[string]any) { e["metadata"].(map[string]string)["manager_boot_id"] = "foreign-boot" }},
		{"wrong session", func(s, _ map[string]any) { s["session_id"] = "foreign-session" }},
		{"wrong runtime version", func(s, _ map[string]any) { s["version"] = "foreign.v1" }},
		{"redacted label", func(s, _ map[string]any) { s["elements"].([]map[string]any)[0]["name"] = "[REDACTED_SECRET]" }},
		{"label needs redaction", func(s, _ map[string]any) {
			s["elements"].([]map[string]any)[0]["name"] = "api_key=" + strings.Repeat("x", 40)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newProgressToolFixture(t)
			completeProgressBrowserState(t, f, []string{"initial"}, 0, nil)
			before := f.guard(t).StateFingerprint
			completed := completeProgressBrowserState(t, f, []string{"different"}, 1, tc.mutate)
			if guard := f.guard(t); guard.StateFingerprint != before || guard.StagnantTurnCount != 2 || completed.run.Status != domain.RunRunning {
				t.Fatalf("invalid sealed observation counted: run=%s guard=%+v", completed.run.Status, guard)
			}
		})
	}
}

func TestRunProgressGuardBrowserControlBoundAndReopen(t *testing.T) {
	f := newProgressToolFixture(t)
	var fingerprint string
	for index, size := range []int{65, 128, 129} {
		completed := completeProgressBrowserState(t, f, strings.Split(strings.Repeat("same|", size-1)+"same", "|"), index, nil)
		guard := f.guard(t)
		if completed.run.Status != domain.RunRunning || (size <= 128 && guard.StateFingerprint == fingerprint) ||
			(size > 128 && guard.StateFingerprint != fingerprint) {
			t.Fatalf("runtime control bound %d mismatched: %+v", size, guard)
		}
		fingerprint = guard.StateFingerprint
	}
	before := f.guard(t)
	if err := f.st.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(f.path)
	if err != nil {
		t.Fatal(err)
	}
	f.st = reopened
	t.Cleanup(func() { _ = reopened.Close() })
	if f.fingerprint(t) != fingerprint || f.guard(t) != before {
		t.Fatal("reopen removed completed historical browser evidence")
	}
}
