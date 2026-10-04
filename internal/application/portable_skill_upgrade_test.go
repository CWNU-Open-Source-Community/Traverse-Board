package application_test

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/plugins"
)

func TestPortableSkillSameSourceUpgradeRequiresExplicitSwitch(t *testing.T) {
	st := openHistoryRecallStore(t, filepath.Join(t.TempDir(), "upgrade.db"))
	defer st.Close()
	directory := nativeTinySkill(t)
	path := filepath.Join(directory, "SKILL.md")
	oldBody, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	old := importPortableFixture(t, st, directory, "upgrade-v1")
	oldPin := installedReadPin(old)
	oldArchive, err := st.LoadPluginObject(t.Context(), old.ID)
	if err != nil {
		t.Fatal(err)
	}
	newBody := append(append([]byte(nil), oldBody...), []byte("Revision two adds bounded guidance.\n")...)
	writeBody := func(raw []byte) {
		t.Helper()
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeBody(newBody)
	service := portableCatalogService(t, st)
	request := application.ImportSkillFromDirectoryRequest{Directory: directory, Surface: domain.ExecutionSurfaceCode,
		OperationKey: "upgrade-v2", InstalledBy: "operator", ConfirmUntrusted: true}
	_, err = service.ImportFromDirectory(t.Context(), request)
	t.Logf("same-source new revision import: code=%s error=%v", apperror.CodeOf(err), err)
	if apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("enabled predecessor was replaced without an explicit switch: %v", err)
	}
	values, err := st.ListPluginInstallations(t.Context(), old.PackageID(), 100)
	if err != nil || len(values) != 2 {
		t.Fatalf("upgrade records=%d error=%v", len(values), err)
	}
	var next plugins.Installation
	for _, value := range values {
		if value.ID != old.ID {
			next = value
		}
	}
	if next.State != plugins.StateApproved || next.Generation != 2 || len(next.EnabledCapabilities) != 0 ||
		next.PackageID() != old.PackageID() || next.Revision() == old.Revision() || next.Source.URI != old.Source.URI {
		t.Fatalf("conflicted upgrade did not retain a separate approved revision: %+v", next)
	}
	assertPersisted := func(want plugins.Installation) {
		t.Helper()
		got, err := st.GetPluginInstallation(t.Context(), want.ID)
		if err != nil || got.State != want.State || got.Generation != want.Generation || got.Revision() != want.Revision() ||
			!slices.Equal(got.EnabledCapabilities, want.EnabledCapabilities) {
			t.Fatalf("installation changed unexpectedly: got=%+v want=%+v error=%v", got, want, err)
		}
	}
	assertPersisted(old)
	t.Logf("after conflict: old=%s generation=%d new=%s generation=%d", old.State, old.Generation, next.State, next.Generation)
	if _, err := service.ImportFromDirectory(t.Context(), request); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("retry bypassed the still-enabled predecessor: %v", err)
	}
	assertPersisted(old)
	assertPersisted(next)
	old = reviewInstalledFixture(t, st, old, plugins.ReviewDisable)
	assertPersisted(old)
	assertPersisted(next)
	t.Logf("after explicit disable: old=%s generation=%d new=%s generation=%d", old.State, old.Generation, next.State, next.Generation)
	next = reviewInstalledFixture(t, st, next, plugins.ReviewEnable)
	assertPersisted(old)
	assertPersisted(next)
	t.Logf("after explicit enable: old=%s generation=%d new=%s generation=%d", old.State, old.Generation, next.State, next.Generation)
	newPin := installedReadPin(next)
	provider := &scriptedToolProvider{responses: []*llm.ChatResponse{
		{ToolCalls: []llm.ToolCall{installedReadCall(newPin, "read-upgraded-skill", "")}},
		textResponse(rootActionResponse(domain.RootActionContinue, "Read the explicitly enabled revision", "", "")),
	}}
	run := startedSkillRun(t, st)
	if _, err := newToolLoopSupervisor(st, provider).Step(t.Context(), run.ID); err != nil {
		t.Fatal(err)
	}
	requests := provider.Requests()
	if len(requests) != 2 || !installedPinInCatalog(t, requests[0], newPin) || installedPinInCatalog(t, requests[0], oldPin) {
		t.Fatalf("Supervisor did not select only the new revision: requests=%d", len(requests))
	}
	assertInstalledReadResult(t, requests[1], newPin, "", newBody)

	// A retry is bound to its original bytes and current installation state. It
	// must neither re-enable the predecessor nor mint a new enablement generation.
	assertReplay := func(key string, want plugins.Installation) {
		t.Helper()
		retry := request
		retry.OperationKey = key
		result, err := service.ImportFromDirectory(t.Context(), retry)
		if err != nil || result.Portable == nil || result.Portable.ID != want.ID ||
			result.Portable.State != want.State || result.Portable.Generation != want.Generation {
			t.Fatalf("operation replay renewed authority: result=%+v want=%+v error=%v", result.Portable, want, err)
		}
		assertPersisted(want)
		t.Logf("operation %s retry: state=%s generation=%d unchanged", key, want.State, want.Generation)
	}
	assertReplay("upgrade-v2", next)
	request.OperationKey = "upgrade-v1"
	if _, err := service.ImportFromDirectory(t.Context(), request); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("old operation accepted changed source bytes: %v", err)
	}
	writeBody(oldBody)
	assertReplay("upgrade-v1", old)
	assertPersisted(next)
	after, err := st.LoadPluginObject(t.Context(), old.ID)
	if err != nil || !bytes.Equal(oldArchive, after) {
		t.Fatalf("upgrade or retry changed retained predecessor bytes: %v", err)
	}
	writeBody(newBody)
	next = reviewInstalledFixture(t, st, next, plugins.ReviewDisable)
	assertReplay("upgrade-v2", next)
	assertPersisted(old)
}
