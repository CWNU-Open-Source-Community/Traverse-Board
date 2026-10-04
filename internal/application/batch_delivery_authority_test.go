package application

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/session"
)

type batchValidationAuthorityStore struct {
	BatchDeliveryStore
	reads            int
	runReads         int
	beforeRun        func(int)
	beforePermission func(int)
	workspaceRoot    string
}

func (s *batchValidationAuthorityStore) GetRun(ctx context.Context, runID string) (domain.Run, error) {
	s.runReads++
	if s.beforeRun != nil {
		s.beforeRun(s.runReads)
	}
	return s.BatchDeliveryStore.GetRun(ctx, runID)
}

func (s *batchValidationAuthorityStore) GetRunExecutionPermission(ctx context.Context, runID string) (domain.RunExecutionPermissionSnapshot, error) {
	s.reads++
	if s.beforePermission != nil {
		s.beforePermission(s.reads)
	}
	return s.BatchDeliveryStore.GetRunExecutionPermission(ctx, runID)
}

func (s *batchValidationAuthorityStore) GetWorkspaceInfo(ctx context.Context, id string) (session.WorkspaceInfo, error) {
	info, err := s.BatchDeliveryStore.GetWorkspaceInfo(ctx, id)
	if s.workspaceRoot != "" {
		info.RootPath = s.workspaceRoot
	}
	return info, err
}

type batchValidationRecordingStarter struct {
	calls int
	spec  runner.OnceStartSpec
}

func (*batchValidationRecordingStarter) Name() string    { return "batch-recording-test" }
func (*batchValidationRecordingStarter) Available() bool { return true }
func (s *batchValidationRecordingStarter) Start(_ context.Context, spec runner.OnceStartSpec) (runner.OnceStartResult, error) {
	s.calls++
	s.spec = spec
	return runner.OnceStartResult{}, nil
}

func TestBatchValidationDispatchUsesCurrentApprovalModes(t *testing.T) {
	for _, mode := range []domain.RunExecutionPermissionMode{domain.RunExecutionPermissionAsk,
		domain.RunExecutionPermissionAuto, domain.RunExecutionPermissionFull} {
		t.Run(string(mode), func(t *testing.T) {
			fixture := newBatchDeliveryApplicationFixture(t, false, mode)
			underlying := &batchValidationRecordingStarter{}
			starter := &batchValidationStarter{OnceStarter: underlying, service: fixture.service, runID: fixture.run.ID}
			_, err := starter.Start(t.Context(), runner.OnceStartSpec{
				RequestFingerprint: strings.Repeat("a", 64), ExecutablePath: "native-go",
				Argv: []string{"test", "./..."}, WorkingDirectory: fixture.repository})
			if mode == domain.RunExecutionPermissionFull {
				if err != nil || underlying.calls != 1 {
					t.Fatalf("Full dispatch: calls=%d err=%v", underlying.calls, err)
				}
			} else if apperror.CodeOf(err) != apperror.CodePolicyDenied || underlying.calls != 0 {
				t.Fatalf("unsandboxed %s dispatch: calls=%d err=%v", mode, underlying.calls, err)
			}
		})
	}
}

func TestBatchValidationFinalDispatchRejectsAuthorityDrift(t *testing.T) {
	for _, change := range []string{"revoke", "reactivate", "restart", "fence", "workspace", "working-root", "pause-resume", "startup-gate"} {
		t.Run(change, func(t *testing.T) {
			fixture := newBatchDeliveryApplicationFixture(t, false, domain.RunExecutionPermissionFull)
			store := &batchValidationAuthorityStore{BatchDeliveryStore: fixture.store}
			fixture.service.store = store
			workingRoot := filepath.Join(t.TempDir(), "execution")
			if err := os.Mkdir(workingRoot, 0o700); err != nil {
				t.Fatal(err)
			}
			permission, err := fixture.store.GetRunExecutionPermission(t.Context(), fixture.run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.caps.RuntimeAuthority.IssueRunAuthorizationFence(fixture.run.ID); err != nil {
				t.Fatal(err)
			}
			store.beforeRun = func(read int) {
				if change == "pause-resume" && read == 2 {
					if _, err := NewRunService(fixture.store).Pause(t.Context(), fixture.run.ID); err != nil {
						t.Fatal(err)
					}
					if _, err := NewRunService(fixture.store).Start(t.Context(), fixture.run.ID); err != nil {
						t.Fatal(err)
					}
				}
			}
			store.beforePermission = func(read int) {
				if read != 2 {
					return
				}
				switch change {
				case "revoke":
					fixture.caps.RuntimeAuthority.RevokeRun(fixture.run.ID)
				case "reactivate":
					if _, err := fixture.caps.RuntimeAuthority.ActivateRunFullAccess(permission); err != nil {
						t.Fatal(err)
					}
				case "restart":
					fixture.service.executionPermissionCapabilities.RuntimeAuthority = domain.NewExecutionPermissionRuntimeAuthority()
				case "fence":
					if _, err := fixture.caps.RuntimeAuthority.RotateRunAuthorizationFence(fixture.run.ID); err != nil {
						t.Fatal(err)
					}
				case "workspace":
					store.workspaceRoot = t.TempDir()
				case "working-root":
					if err := os.Rename(workingRoot, workingRoot+"-previous"); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(workingRoot, 0o700); err != nil {
						t.Fatal(err)
					}
				case "startup-gate":
					fixture.service.hostValidationExecutionEnabled = false
				}
			}
			underlying := &batchValidationRecordingStarter{}
			starter := &batchValidationStarter{OnceStarter: underlying, service: fixture.service, runID: fixture.run.ID}
			_, err = starter.Start(t.Context(), runner.OnceStartSpec{
				RequestFingerprint: strings.Repeat("a", 64), ExecutablePath: "native-go",
				Argv: []string{"test", "./..."}, WorkingDirectory: workingRoot})
			if err == nil || store.reads != 2 || underlying.calls != 0 {
				t.Fatalf("stale dispatch: reads=%d calls=%d err=%v", store.reads, underlying.calls, err)
			}
			if change == "restart" {
				if _, live := fixture.service.executionPermissionCapabilities.FullAccessGeneration(permission); live {
					t.Fatal("cold recovery recreated persisted Full authority")
				}
			}
		})
	}
}

func TestBatchValidationDispatchFreezesCommandInputs(t *testing.T) {
	fixture := newBatchDeliveryApplicationFixture(t, false, domain.RunExecutionPermissionFull)
	spec := runner.OnceStartSpec{RequestFingerprint: strings.Repeat("a", 64),
		ExecutablePath: "native-go", Argv: []string{"test", "./..."},
		WorkingDirectory: fixture.repository, Environment: []string{"BATCH_VALUE=original"}}
	store := &batchValidationAuthorityStore{BatchDeliveryStore: fixture.store,
		beforePermission: func(int) { spec.Argv[0] = "changed"; spec.Environment[0] = "BATCH_VALUE=changed" }}
	fixture.service.store = store
	underlying := &batchValidationRecordingStarter{}
	starter := &batchValidationStarter{OnceStarter: underlying, service: fixture.service, runID: fixture.run.ID}
	if _, err := starter.Start(t.Context(), spec); err != nil {
		t.Fatal(err)
	}
	if underlying.calls != 1 || underlying.spec.Argv[0] != "test" || underlying.spec.Environment[0] != "BATCH_VALUE=original" {
		t.Fatalf("dispatch did not use the authorized inputs: %#v", underlying)
	}
}

func TestBatchDeliveryValidationRechecksAfterNativeResolution(t *testing.T) {
	fixture := newBatchDeliveryApplicationFixture(t, false, domain.RunExecutionPermissionFull)
	marker := filepath.Join(fixture.repository, "unexpected-dispatch.txt")
	source := "package one\nimport (\"os\"; \"testing\")\nfunc TestDispatch(t *testing.T) { if err := os.WriteFile(" +
		"`" + filepath.ToSlash(marker) + "`" + ", []byte(\"executed\"), 0600); err != nil { t.Fatal(err) } }\n"
	if err := os.WriteFile(filepath.Join(fixture.repository, "internal", "one", "dispatch_test.go"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	store := &batchValidationAuthorityStore{BatchDeliveryStore: fixture.store,
		beforePermission: func(read int) {
			// Admission, resolved-operation authorization, then final dispatch.
			if read == 3 {
				fixture.caps.RuntimeAuthority.RevokeRun(fixture.run.ID)
			}
		}}
	fixture.service.store = store
	receipts, err := fixture.service.runBatchDeliveryValidations(t.Context(), fixture.run.ID,
		fixture.repository, strings.Repeat("a", 40), []domain.BatchDeliveryValidationRequirement{
			{ID: "late-authority", Kind: domain.BatchValidationGoTest, Scope: "."}})
	if apperror.CodeOf(err) != apperror.CodePolicyDenied || store.reads != 3 || len(receipts) != 0 {
		t.Fatalf("final native dispatch: reads=%d receipts=%#v err=%v", store.reads, receipts, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("revoked validation ran: %v", err)
	}
}
