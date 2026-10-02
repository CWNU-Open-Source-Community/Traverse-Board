package application_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/plugins"
)

// Installing another instruction package must not prevent an otherwise ordinary
// Run from reaching its provider. This goes through import, persisted enablement,
// and the real Supervisor rather than calling the catalog builder directly.
func TestPortableSkillCatalogCapacityDoesNotBlockRun(t *testing.T) {
	st := openHistoryRecallStore(t, filepath.Join(t.TempDir(), "capacity.db"))
	defer st.Close()
	sources := t.TempDir()
	for count := 1; count <= 33; count++ {
		name := fmt.Sprintf("capacity-%02d", count)
		directory := filepath.Join(sources, name)
		if err := os.Mkdir(directory, 0700); err != nil {
			t.Fatal(err)
		}
		raw := fmt.Sprintf("---\nname: %s\ndescription: Capacity fixture %d.\n---\nInspect fixture %d only.\n", name, count, count)
		if err := os.WriteFile(filepath.Join(directory, "SKILL.md"), []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		importPortableFixture(t, st, directory, name)
		if count < 32 {
			continue
		}
		t.Run(fmt.Sprintf("enabled_%d", count), func(t *testing.T) {
			values, err := st.ListPluginInstallations(t.Context(), "", 1000)
			if err != nil {
				t.Fatal(err)
			}
			enabledSkills := 0
			for _, value := range values {
				if value.State != plugins.StateEnabled || value.Source.Surface != "code" || value.Snapshot == nil ||
					len(value.EnabledCapabilities) != 1 || value.EnabledCapabilities[0] != plugins.CapabilitySkills {
					t.Fatalf("fixture is not an enabled code Skill: %+v", value)
				}
				enabledSkills += len(value.Snapshot.Skills)
			}
			if len(values) != count || enabledSkills != count {
				t.Fatalf("installations=%d enabled Skills=%d want=%d", len(values), enabledSkills, count)
			}
			run := startedSkillRun(t, st)
			provider := &scriptedToolProvider{responses: []*llm.ChatResponse{
				textResponse(rootActionResponse(domain.RootActionContinue, "Continue an ordinary Run", "", "")),
			}}
			result, stepErr := newToolLoopSupervisor(st, provider).Step(t.Context(), run.ID)
			persisted, err := st.GetRun(t.Context(), run.ID)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("enabled Skills=%d provider requests=%d step code=%s error=%v run=%s checkpoint=%s",
				enabledSkills, len(provider.Requests()), apperror.CodeOf(stepErr), stepErr, persisted.Status, result.Checkpoint.Phase)
			if stepErr != nil {
				t.Fatalf("%d enabled Skills blocked an ordinary Run before completion: %v", enabledSkills, stepErr)
			}
			if len(provider.Requests()) != 1 || result.RequestedAction != domain.RootActionContinue || persisted.Status != domain.RunRunning {
				t.Fatalf("ordinary Run did not progress: requests=%d result=%+v persisted=%s", len(provider.Requests()), result, persisted.Status)
			}
		})
	}
}
