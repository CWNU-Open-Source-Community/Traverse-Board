package skills

import (
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/domain"
)

func TestBuiltinModeAndInvocationPoliciesAreExplicit(t *testing.T) {
	registry, err := BuiltinRegistry()
	if err != nil {
		t.Fatal(err)
	}
	code, _ := registry.Get("code")
	if !code.SupportsContext(ExecutionContext{
		Surface: domain.ExecutionSurfaceCode, Phase: domain.ExecutionPhasePlan,
		Profile: domain.ProfileCode, Role: domain.AgentRoleSpecialist,
	}) || code.SupportsContext(ExecutionContext{
		Surface: domain.ExecutionSurfaceCyber, Phase: domain.ExecutionPhasePlan,
		Profile: domain.ProfileCode, Role: domain.AgentRoleRoot,
	}) {
		t.Fatal("code Skill surface or role policy drifted")
	}
	if !code.AllowsInvocation(InvocationSourceUser, true) ||
		!code.AllowsInvocation(InvocationSourceModel, false) {
		t.Fatal("code Skill invocation policy drifted")
	}

	plan, _ := registry.Get("plan-delivery")
	if !plan.SupportsContext(ExecutionContext{
		Surface: domain.ExecutionSurfaceCyber, Phase: domain.ExecutionPhaseDeliver,
		Profile: domain.ProfileCode, Role: domain.AgentRoleRoot,
	}) || plan.SupportsContext(ExecutionContext{
		Surface: domain.ExecutionSurfaceCode, Phase: domain.ExecutionPhaseDeliver,
		Profile: domain.ProfileCode, Role: domain.AgentRoleSpecialist,
	}) {
		t.Fatal("plan-delivery root-only policy drifted")
	}
	if !plan.AllowsInvocation(InvocationSourceUser, true) ||
		plan.AllowsInvocation(InvocationSourceUser, false) ||
		plan.AllowsInvocation(InvocationSourceModel, false) {
		t.Fatal("plan-delivery explicit-only policy drifted")
	}
	doctor, _ := registry.Get("doctor")
	if !doctor.SupportsContext(ExecutionContext{
		Surface: domain.ExecutionSurfaceCyber, Phase: domain.ExecutionPhasePlan,
		Profile: domain.ProfileLearn, Role: domain.AgentRoleRoot,
	}) || doctor.SupportsContext(ExecutionContext{
		Surface: domain.ExecutionSurfaceCyber, Phase: domain.ExecutionPhaseDeliver,
		Profile: domain.ProfileLearn, Role: domain.AgentRoleRoot,
	}) {
		t.Fatal("doctor Plan-only policy drifted")
	}
	loopMonitor, _ := registry.Get("loop-monitor")
	if !loopMonitor.SupportsContext(ExecutionContext{
		Surface: domain.ExecutionSurfaceCyber, Phase: domain.ExecutionPhasePlan,
		Profile: domain.ProfileLearn, Role: domain.AgentRoleRoot,
	}) || !loopMonitor.SupportsContext(ExecutionContext{
		Surface: domain.ExecutionSurfaceCode, Phase: domain.ExecutionPhaseDeliver,
		Profile: domain.ProfileCode, Role: domain.AgentRoleRoot,
	}) || loopMonitor.SupportsContext(ExecutionContext{
		Surface: domain.ExecutionSurfaceCode, Phase: domain.ExecutionPhasePlan,
		Profile: domain.ProfileCode, Role: domain.AgentRoleSpecialist,
	}) || !loopMonitor.AllowsInvocation(InvocationSourceUser, true) ||
		loopMonitor.AllowsInvocation(InvocationSourceUser, false) ||
		loopMonitor.AllowsInvocation(InvocationSourceModel, true) {
		t.Fatal("loop-monitor root/explicit-only policy drifted")
	}
	runVerify, _ := registry.Get("run-verify")
	if !runVerify.SupportsContext(ExecutionContext{
		Surface: domain.ExecutionSurfaceCyber, Phase: domain.ExecutionPhaseDeliver,
		Profile: domain.ProfileScript, Role: domain.AgentRoleRoot,
	}) || runVerify.SupportsContext(ExecutionContext{
		Surface: domain.ExecutionSurfaceCode, Phase: domain.ExecutionPhasePlan,
		Profile: domain.ProfileScript, Role: domain.AgentRoleRoot,
	}) {
		t.Fatal("run-verify Deliver-only policy drifted")
	}
}

func TestResolveSelectionEnforcesSurfaceAndInvocationPolicy(t *testing.T) {
	registry, err := BuiltinRegistry()
	if err != nil {
		t.Fatal(err)
	}
	base := ResolveSelectionRequest{
		SelectionID: "selection-mode-policy", RunID: "run-mode-policy",
		MissionID: "mission-mode-policy", Surface: domain.ExecutionSurfaceCyber,
		Phase: domain.ExecutionPhasePlan, Profile: domain.ProfileCode,
		Names: []string{"code"}, TokenBudget: DefaultSelectionTokenBudget,
		Invocation: InvocationSourceUser, Explicit: true,
		RequestedBy: "operator", CreatedAt: time.Now().UTC(),
	}
	if _, err := registry.ResolveSelection(base); err == nil ||
		!strings.Contains(err.Error(), "incompatible with surface") {
		t.Fatalf("Cyber selected the Code-only guide: %v", err)
	}
	base.Names = []string{"plan-delivery"}
	if _, err := registry.ResolveSelection(base); err != nil {
		t.Fatalf("Cyber root could not select plan-delivery: %v", err)
	}
	base.Surface = domain.ExecutionSurfaceCode
	base.Invocation = InvocationSourceModel
	base.Explicit = false
	if _, err := registry.ResolveSelection(base); err == nil ||
		!strings.Contains(err.Error(), "does not allow model invocation") {
		t.Fatalf("model activated explicit-only plan-delivery: %v", err)
	}
	base.Names = []string{"code"}
	if _, err := registry.ResolveSelection(base); err != nil {
		t.Fatalf("model-invocable code Skill was rejected: %v", err)
	}
}

func TestListForContextFiltersModeAndInvocationTogether(t *testing.T) {
	registry, err := BuiltinRegistry()
	if err != nil {
		t.Fatal(err)
	}
	context := ExecutionContext{
		Surface: domain.ExecutionSurfaceCyber, Phase: domain.ExecutionPhasePlan,
		Profile: domain.ProfileCode, Role: domain.AgentRoleRoot,
	}
	listed, err := registry.ListForContext(context, InvocationSourceUser, true)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"debug", "doctor", "loop-monitor", "plan-delivery", "security-review"}; !equalManifestNames(listed, want) {
		t.Fatalf("Cyber explicit-user list = %#v", listed)
	}
	listed, err = registry.ListForContext(context, InvocationSourceModel, false)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"debug", "doctor", "security-review"}; !equalManifestNames(listed, want) {
		t.Fatalf("Cyber model list = %#v", listed)
	}
}

func TestPhaseSpecificRootSelectionDeliversOnlyTheActiveSubset(t *testing.T) {
	registry, err := BuiltinRegistry()
	if err != nil {
		t.Fatal(err)
	}
	selection, err := registry.ResolveSelection(ResolveSelectionRequest{
		SelectionID: "selection-phase-subset", RunID: "run-phase-subset",
		MissionID: "mission-phase-subset", Surface: domain.ExecutionSurfaceCode,
		Phase: domain.ExecutionPhasePlan, Profile: domain.ProfileCode,
		Names: []string{"doctor", "run-verify"}, TokenBudget: DefaultSelectionTokenBudget,
		Invocation: InvocationSourceUser, Explicit: true,
		RequestedBy: "operator", CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := registry.AssembleContextFor(selection, ExecutionContext{
		Surface: domain.ExecutionSurfaceCode, Phase: domain.ExecutionPhasePlan,
		Profile: domain.ProfileCode, Role: domain.AgentRoleRoot,
	})
	if err != nil {
		t.Fatal(err)
	}
	deliver, err := registry.AssembleContextFor(selection, ExecutionContext{
		Surface: domain.ExecutionSurfaceCode, Phase: domain.ExecutionPhaseDeliver,
		Profile: domain.ProfileCode, Role: domain.AgentRoleRoot,
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.ItemCount != 1 || plan.Items[0].Name != "doctor" ||
		deliver.ItemCount != 1 || deliver.Items[0].Name != "run-verify" ||
		plan.SelectionItemCount != 2 || deliver.SelectionItemCount != 2 ||
		plan.Fingerprint == deliver.Fingerprint {
		t.Fatalf("phase subsets drifted: plan=%#v deliver=%#v", plan, deliver)
	}

	doctorOnly := CloneSelection(selection)
	doctorOnly.Items = doctorOnly.Items[:1]
	doctorOnly.ItemCount = 1
	doctorOnly.TokenUpperBound = doctorOnly.Items[0].TokenUpperBound
	doctorOnly.Fingerprint = SelectionFingerprint(doctorOnly)
	empty, err := registry.AssembleContextFor(doctorOnly, ExecutionContext{
		Surface: domain.ExecutionSurfaceCode, Phase: domain.ExecutionPhaseDeliver,
		Profile: domain.ProfileCode, Role: domain.AgentRoleRoot,
	})
	if err != nil {
		t.Fatal(err)
	}
	if empty.ItemCount != 0 || empty.TokenUpperBound != 0 ||
		empty.SelectionItemCount != 1 || len(empty.Items) != 0 {
		t.Fatalf("empty Deliver subset drifted: %#v", empty)
	}
}

func equalManifestNames(manifests []Manifest, want []string) bool {
	if len(manifests) != len(want) {
		return false
	}
	for index, manifest := range manifests {
		if manifest.Name != want[index] {
			return false
		}
	}
	return true
}

func TestSpecialistSelectionUsesManifestMetadataRatherThanSkillName(t *testing.T) {
	content := []byte("# Named by metadata\n")
	manifest := fixtureModeManifest(content)
	manifest.Name = "implementation-guide"
	entry := registryEntry{manifest: manifest, content: append([]byte(nil), content...)}
	registry := &Registry{
		entries: map[string]registryEntry{manifest.Name: cloneRegistryEntry(entry)},
		versions: map[string]map[string]registryEntry{
			manifest.Name: {manifest.Version: cloneRegistryEntry(entry)},
		},
	}
	selection, err := registry.ResolveSelection(ResolveSelectionRequest{
		SelectionID: "selection-metadata-name", RunID: "run-metadata-name",
		MissionID: "mission-metadata-name", Surface: domain.ExecutionSurfaceCode,
		Phase: domain.ExecutionPhaseDeliver, Profile: domain.ProfileCode,
		Names: []string{manifest.Name}, TokenBudget: DefaultSelectionTokenBudget,
		Invocation: InvocationSourceUser, Explicit: true,
		RequestedBy: "operator", CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	selected, found, err := registry.SpecialistSelectionItem(selection, ExecutionContext{
		Surface: domain.ExecutionSurfaceCode, Phase: domain.ExecutionPhaseDeliver,
		Profile: domain.ProfileCode, Role: domain.AgentRoleSpecialist,
	})
	if err != nil || !found || selected.Name != manifest.Name || selected.Ordinal != 1 {
		t.Fatalf("metadata-selected Specialist guide = %#v found=%t err=%v", selected, found, err)
	}
}

func TestCommonCapabilitySkillBodiesPreserveEvidenceAndAuthorityBoundaries(t *testing.T) {
	registry, err := BuiltinRegistry()
	if err != nil {
		t.Fatal(err)
	}
	contracts := map[string][]string{
		"doctor":          {"doctor snapshot", "not_configured", "Never turn a diagnosis into an automatic repair"},
		"debug":           {"model, tool, policy, application, or infrastructure", "next_after_sequence", "In Deliver"},
		"loop-monitor":    {"explicit target Run", "unchanged round without calling a model or tool", "approved_repair"},
		"run-verify":      {"Extension: ui-evidence", "fixed commit/worktree fingerprint", "admitted local sandbox"},
		"review":          {"merge-base", "concurrent or durable code", "confirmed, inferred, or unverified"},
		"focused-checks":  {"smallest credible set", "must never be reported as passed"},
		"simplify":        {"call-site evidence", "generated code, reflection, registration, build tags"},
		"security-review": {"read-only by default", "authentication and authorization", "sensitive persistent or logged data"},
	}
	for name, fragments := range contracts {
		entry, found := registry.entries[name]
		if !found {
			t.Fatalf("common capability %q is missing", name)
		}
		body := string(entry.content)
		for _, fragment := range fragments {
			if !strings.Contains(body, fragment) {
				t.Fatalf("common capability %q lost contract %q", name, fragment)
			}
		}
		if !strings.Contains(body, "grants no") {
			t.Fatalf("common capability %q lost its non-authorizing boundary", name)
		}
	}
}
