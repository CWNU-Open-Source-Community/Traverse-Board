package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/idgen"
	"cyberagent-workbench/internal/runmutation"
	"cyberagent-workbench/internal/skills"
	"cyberagent-workbench/internal/toolgateway"
)

func TestSkillCandidateRequiresHumanReviewBeforeImport(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "skill-candidates.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	workspace := WorkspaceRecord{
		ID: idgen.New("workspace"), Name: "candidate-workspace",
		RootPath: t.TempDir(), CreatedAt: time.Now().UTC(),
	}
	if err := st.SaveWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	_, run, err := application.NewRunService(st).Create(ctx, application.CreateRunRequest{
		Goal: "generate reusable Skill", Profile: "code", Surface: "code", Phase: "deliver",
		WorkspaceID: workspace.ID, Budget: domain.Budget{MaxTurns: 8, MaxTokens: 8192},
	})
	if err != nil {
		t.Fatal(err)
	}
	root, _, err := st.RegisterRootAgent(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.NewRunService(st).Start(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	candidate := fixtureSkillCandidate(t, run, root, workspace.ID, "reviewed-helper")
	seedSkillCandidateInvocation(t, st, run, workspace.ID, candidate.InvocationID, 1)
	stored, replayed, err := st.CreateSkillCandidate(ctx, candidate)
	if err != nil || replayed || stored.ID != candidate.ID {
		t.Fatalf("create candidate=%#v replayed=%t err=%v", stored, replayed, err)
	}
	stored, replayed, err = st.CreateSkillCandidate(ctx, candidate)
	if err != nil || !replayed || stored.CandidateFingerprint != candidate.CandidateFingerprint {
		t.Fatalf("candidate replay=%#v replayed=%t err=%v", stored, replayed, err)
	}

	registry, err := skills.BuiltinRegistry()
	if err != nil {
		t.Fatal(err)
	}
	objects, err := skills.NewLocalPackageObjectStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := application.NewSkillCandidateService(st,
		application.NewSkillPackageRegistryService(st, objects, registry))
	rejectedCandidate := fixtureSkillCandidate(t, run, root, workspace.ID, "rejected-helper")
	seedSkillCandidateInvocation(t, st, run, workspace.ID, rejectedCandidate.InvocationID, 2)
	if _, _, err := st.CreateSkillCandidate(ctx, rejectedCandidate); err != nil {
		t.Fatal(err)
	}
	rejected, err := service.Review(ctx, application.ReviewSkillCandidateRequest{
		CandidateID:          rejectedCandidate.ID,
		CandidateFingerprint: rejectedCandidate.CandidateFingerprint,
		Decision:             skills.SkillCandidateReviewReject, Reason: "too repository-specific",
		OperationKey: "candidate-review-rejected-0001", Reviewer: "reviewer",
	})
	if err != nil || rejected.Record.Status() != skills.SkillCandidateRejected {
		t.Fatalf("rejected candidate=%#v err=%v", rejected, err)
	}
	if _, err := service.Import(ctx, application.ImportSkillCandidateRequest{
		CandidateID:          rejectedCandidate.ID,
		CandidateFingerprint: rejectedCandidate.CandidateFingerprint,
		OperationKey:         "candidate-import-rejected-0001", ImportedBy: "reviewer",
		ConfirmUntrusted: true,
	}); err == nil {
		t.Fatal("rejected Skill candidate was imported")
	}
	if _, err := service.Import(ctx, application.ImportSkillCandidateRequest{
		CandidateID: candidate.ID, CandidateFingerprint: candidate.CandidateFingerprint,
		OperationKey: "candidate-import-before-review", ImportedBy: "reviewer",
		ConfirmUntrusted: true,
	}); err == nil {
		t.Fatal("unreviewed Skill candidate was imported")
	}
	if _, err := service.Review(ctx, application.ReviewSkillCandidateRequest{
		CandidateID: candidate.ID, CandidateFingerprint: runmutation.Fingerprint("wrong"),
		Decision:     skills.SkillCandidateReviewApprove,
		OperationKey: "candidate-review-wrong-fingerprint", Reviewer: "reviewer",
	}); err == nil {
		t.Fatal("human review accepted a mismatched candidate fingerprint")
	}
	if _, err := service.Review(ctx, application.ReviewSkillCandidateRequest{
		CandidateID: candidate.ID, CandidateFingerprint: candidate.CandidateFingerprint,
		Decision:     skills.SkillCandidateReviewApprove,
		OperationKey: "candidate-review-model-identity", Reviewer: "run_supervisor",
	}); err == nil {
		t.Fatal("model-owned identity was accepted as a human reviewer")
	}
	reviewed, err := service.Review(ctx, application.ReviewSkillCandidateRequest{
		CandidateID: candidate.ID, CandidateFingerprint: candidate.CandidateFingerprint,
		Decision: skills.SkillCandidateReviewApprove, Reason: "bounded and reusable",
		OperationKey: "candidate-review-approved-0001", Reviewer: "reviewer",
	})
	if err != nil || reviewed.Record.Status() != skills.SkillCandidateApproved {
		t.Fatalf("approved candidate=%#v err=%v", reviewed, err)
	}
	if _, err := service.Import(ctx, application.ImportSkillCandidateRequest{
		CandidateID: candidate.ID, CandidateFingerprint: candidate.CandidateFingerprint,
		OperationKey: "candidate-import-without-confirmation", ImportedBy: "reviewer",
	}); err == nil {
		t.Fatal("candidate imported without the separate untrusted confirmation")
	}
	if _, err := service.Import(ctx, application.ImportSkillCandidateRequest{
		CandidateID: candidate.ID, CandidateFingerprint: candidate.CandidateFingerprint,
		OperationKey: "candidate-import-model-identity", ImportedBy: "model",
		ConfirmUntrusted: true,
	}); err == nil {
		t.Fatal("model-owned identity imported an approved candidate")
	}
	imported, err := service.Import(ctx, application.ImportSkillCandidateRequest{
		CandidateID: candidate.ID, CandidateFingerprint: candidate.CandidateFingerprint,
		OperationKey: "candidate-import-approved-0001", ImportedBy: "reviewer",
		ConfirmUntrusted: true,
	})
	if err != nil || imported.Record.Status() != skills.SkillCandidateImported ||
		imported.Record.Import == nil ||
		imported.InstalledPackage.Installation.PackageFingerprint != candidate.PackageFingerprint {
		t.Fatalf("imported candidate=%#v err=%v", imported, err)
	}
	imported, err = service.Import(ctx, application.ImportSkillCandidateRequest{
		CandidateID: candidate.ID, CandidateFingerprint: candidate.CandidateFingerprint,
		OperationKey: "candidate-import-approved-0001", ImportedBy: "reviewer",
		ConfirmUntrusted: true,
	})
	if err != nil || !imported.Replayed {
		t.Fatalf("candidate import replay=%#v err=%v", imported, err)
	}

	for _, query := range []string{
		`UPDATE skill_candidates SET content = 'changed' WHERE id = ?`,
		`DELETE FROM skill_candidate_reviews WHERE candidate_id = ?`,
		`UPDATE skill_candidate_imports SET imported_by = 'changed' WHERE candidate_id = ?`,
	} {
		if _, err := st.db.ExecContext(ctx, query, candidate.ID); err == nil {
			t.Fatalf("immutable candidate ledger mutation succeeded: %s", query)
		}
	}
}

func fixtureSkillCandidate(t *testing.T, run domain.Run, root domain.AgentNode,
	workspaceID, name string,
) skills.SkillCandidate {
	t.Helper()
	content := []byte("# Reviewed helper\n\nFollow the verified bounded workflow.\n")
	manifest := skills.BindManifestContent(skills.Manifest{
		Protocol: skills.ProtocolVersion, Name: name, Version: "1.0.0",
		Description: "A bounded generated workflow for human review.",
		Profiles:    []domain.Profile{domain.ProfileCode},
		Surfaces:    []domain.ExecutionSurface{domain.ExecutionSurfaceCode},
		Phases: []domain.ExecutionPhase{
			domain.ExecutionPhasePlan, domain.ExecutionPhaseDeliver,
		},
		Roles:         []domain.AgentRole{domain.AgentRoleRoot},
		UserInvocable: true, ModelInvocable: true,
		ToolDependencies: []toolgateway.ToolName{
			toolgateway.ListWorkspaceTool, toolgateway.ReadFileTool,
		},
	}, content)
	raw, err := skills.BuildUnsignedPackage(manifest, content)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := skills.ParsePackage(raw)
	if err != nil {
		t.Fatal(err)
	}
	preview := parsed.Preview()
	value := skills.SkillCandidate{
		ID: idgen.New("skill-candidate"), ProtocolVersion: skills.SkillCandidateProtocolVersion,
		OperationKeyDigest: runmutation.Fingerprint("candidate-operation", name),
		InvocationID:       "toolcall-" + name,
		RunID:              run.ID, RootAgentID: root.ID, SessionID: run.SessionID,
		WorkspaceID: workspaceID, Surface: domain.ExecutionSurfaceCode,
		Manifest: preview.Manifest, Content: string(content),
		ArchiveSHA256: preview.ArchiveSHA256, PackageFingerprint: preview.PackageFingerprint,
		ArchiveBytes: preview.ArchiveBytes, RequestedBy: "run_supervisor",
		CreatedAt: time.Now().UTC(),
	}
	value.RequestFingerprint = skills.SkillCandidateRequestFingerprint(value)
	value.CandidateFingerprint = skills.SkillCandidateFingerprint(value)
	if err := value.Validate(); err != nil {
		t.Fatal(err)
	}
	return value
}

func seedSkillCandidateInvocation(t *testing.T, st *SQLiteStore, run domain.Run,
	workspaceID, invocationID string, sequence int,
) {
	t.Helper()
	if _, err := st.db.ExecContext(t.Context(), `INSERT INTO run_tool_calls
		(id, run_id, session_id, workspace_id, tool_name, action_class, sequence, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, invocationID, run.ID, run.SessionID, workspaceID,
		toolgateway.SkillCandidateProposeTool, toolgateway.ClassAgentProposal, sequence,
		ts(time.Now().UTC().Add(-time.Minute))); err != nil {
		t.Fatal(err)
	}
}

func TestSchemaV112AddsEmptySkillCandidateLedger(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v111-skill-candidates.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range removeSchemaV112ForTestStatements() {
		if _, err := st.db.ExecContext(t.Context(), statement); err != nil {
			t.Fatalf("downgrade v112 with %q: %v", statement, err)
		}
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	if version, err := upgraded.SchemaVersion(t.Context()); err != nil ||
		version != LatestSchemaVersion {
		t.Fatalf("schema version=%d err=%v", version, err)
	}
	for _, table := range []string{
		"skill_candidates", "skill_candidate_reviews", "skill_candidate_imports",
	} {
		assertTableCount(t, upgraded, table, 0)
	}
}
