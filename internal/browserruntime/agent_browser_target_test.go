package browserruntime

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAgentBrowserTargetRejectsAmbiguityAndOldSnapshot(t *testing.T) {
	r := &AgentBrowserRuntime{epoch: 7, refs: map[string]agentBrowserRef{
		"expand": {epoch: 7, snapshot: "current", name: "展开全部", role: "button"},
		"submit": {epoch: 7, snapshot: "current", name: "加入队列", role: "button"},
	}}
	got, err := r.ResolveSnapshotTarget(t.Context(), "current", "加入队列", "button")
	if err != nil || got != "submit" {
		t.Fatalf("wrong control: %s %v", got, err)
	}
	r.refs["private"] = agentBrowserRef{epoch: 7, snapshot: "current", name: "token=synthetic-secret-value", role: "button"}
	r.refs["looks-redacted"] = agentBrowserRef{epoch: 7, snapshot: "current", name: "token=[REDACTED:secret]", role: "button"}
	if _, err = r.ResolveSnapshotTarget(t.Context(), "current", "token=[REDACTED:secret]", "button"); !errors.Is(err, ErrAgentBrowserTargetChanged) {
		t.Fatal("redacted duplicate labels selected a raw node")
	}
	if _, err = r.ResolveSnapshotTarget(t.Context(), "old", "加入队列", "button"); !errors.Is(err, ErrAgentBrowserStaleReference) {
		t.Fatal("old snapshot reauthorized")
	}
	if _, err = r.ResolveSnapshotTarget(t.Context(), "current", "加入队列", "link"); !errors.Is(err, ErrAgentBrowserTargetChanged) {
		t.Fatal("role mismatch selected")
	}
	r.refs["duplicate"] = agentBrowserRef{epoch: 7, snapshot: "current", name: "加入队列", role: "button"}
	if _, err = r.ResolveSnapshotTarget(t.Context(), "current", "加入队列", "button"); !errors.Is(err, ErrAgentBrowserTargetChanged) {
		t.Fatal("ambiguous label picked first map entry")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err = r.ResolveSnapshotTarget(ctx, "current", "加入队列", "button"); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled selection continued")
	}
	r.invalidateLocked()
	if _, err = r.ResolveSnapshotTarget(t.Context(), "current", "展开全部", "button"); !errors.Is(err, ErrAgentBrowserStaleReference) {
		t.Fatal("input-invalidated snapshot selected")
	}
}

func TestAgentBrowserRealTargetUsesReturnedNamesAndLiveInputChecks(t *testing.T) {
	r, _ := agentBrowserTestRuntime(t)
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<button aria-label="展开全部">expand</button><button type="submit" aria-label="加入队列" onclick="document.querySelector('#status').textContent='submitted'">add</button><button>同名</button><button>同名</button><p id="status"></p>`)
	}))
	defer site.Close()
	if _, err := r.Navigate(t.Context(), site.URL); err != nil {
		t.Fatal(err)
	}
	first, err := r.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.ResolveSnapshotTarget(t.Context(), first.SnapshotID, "同名", "button"); !errors.Is(err, ErrAgentBrowserTargetChanged) {
		t.Fatal("duplicate live names accepted")
	}
	ref, err := r.ResolveSnapshotTarget(t.Context(), first.SnapshotID, "加入队列", "button")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.Click(t.Context(), first.SnapshotID, ref); err != nil {
		t.Fatal(err)
	}
	next, err := r.Snapshot(t.Context())
	if err != nil || !strings.Contains(next.Text, "submitted") {
		t.Fatalf("named click picked expand or failed: %v", err)
	}
	if _, err = r.Click(t.Context(), first.SnapshotID, ref); !errors.Is(err, ErrAgentBrowserStaleReference) {
		t.Fatal("old ref survived named click")
	}
}
