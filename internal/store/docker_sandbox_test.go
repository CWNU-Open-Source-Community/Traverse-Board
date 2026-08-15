package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/idgen"
	"cyberagent-workbench/internal/runmutation"
	"cyberagent-workbench/internal/sandbox"
)

type dockerSandboxStoreFixture struct {
	Run       domain.Run
	Manifest  sandbox.Manifest
	Plan      sandbox.DockerContainerPlan
	Admission domain.DockerSandboxAdmission
	Start     domain.DockerSandboxStartIntent
	Intent    sandbox.DockerContainerLaunchIntent
	Request   sandbox.DockerContainerWriteRequest
}

func TestDockerSandboxDenialAuditIsStableAndBlocksLaterAuthorization(t *testing.T) {
	ctx := context.Background()
	st, run, root := openSandboxManifestStoreAt(t, ctx,
		filepath.Join(t.TempDir(), "docker-sandbox-denial.db"))
	defer st.Close()
	fixture := newDockerSandboxStoreFixture(t, ctx, st, run, root,
		"docker-sandbox-denial")
	denial := domain.DockerSandboxDenial{
		ProtocolVersion:    domain.DockerSandboxDenialProtocolVersion,
		OperationKeyDigest: fixture.Admission.OperationKeyDigest,
		RunID:              fixture.Admission.RunID, MissionID: fixture.Admission.MissionID,
		WorkspaceID: fixture.Admission.WorkspaceID, PlanID: fixture.Admission.PlanID,
		RequestedBy:     fixture.Admission.RequestedBy,
		ReasonCode:      domain.DockerSandboxReasonBudgetExhausted,
		RemediationCode: domain.DockerSandboxRemediationRestoreBudget,
		NetworkMode:     "disabled", CreatedAt: fixture.Admission.CreatedAt,
	}
	denial.DenialFingerprint = domain.DockerSandboxDenialFingerprint(denial)
	inserted, err := st.RecordDockerSandboxDenial(ctx, denial)
	if err != nil || !inserted {
		t.Fatalf("record denial inserted=%t err=%v", inserted, err)
	}
	retry := denial
	retry.CreatedAt = retry.CreatedAt.Add(time.Second)
	retry.DenialFingerprint = domain.DockerSandboxDenialFingerprint(retry)
	inserted, err = st.RecordDockerSandboxDenial(ctx, retry)
	if err != nil || inserted {
		t.Fatalf("replay denial inserted=%t err=%v", inserted, err)
	}
	reason, remediation, found, err := st.GetDockerSandboxDenialByOperation(ctx,
		denial.OperationKeyDigest)
	if err != nil || !found || reason != denial.ReasonCode ||
		remediation != denial.RemediationCode {
		t.Fatalf("denial lookup reason=%q remediation=%q found=%t err=%v",
			reason, remediation, found, err)
	}
	changed := denial
	changed.ReasonCode = domain.DockerSandboxReasonPolicyDenied
	changed.RemediationCode = domain.DockerSandboxRemediationReviewPolicy
	changed.DenialFingerprint = domain.DockerSandboxDenialFingerprint(changed)
	if _, err := st.RecordDockerSandboxDenial(ctx, changed); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("changed denial error=%v", err)
	}
	if _, _, err := st.CreateDockerSandboxAdmission(ctx, fixture.Admission); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("denied operation became authorized: %v", err)
	}
	eventList, err := st.ListRunEvents(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, event := range eventList {
		if event.Type != events.SandboxDockerProductAdmissionDeniedEvent {
			continue
		}
		count++
		if strings.Contains(event.PayloadJSON, denial.OperationKeyDigest) ||
			strings.Contains(event.PayloadJSON, fixture.Admission.RequestFingerprint) {
			t.Fatalf("denial event leaked operation/request fingerprint: %s",
				event.PayloadJSON)
		}
	}
	if count != 1 {
		t.Fatalf("denial events=%d, want 1", count)
	}
}

func TestDockerSandboxProductAdmissionReplayRecoveryAndPrivateEvents(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "docker-sandbox-product.db")
	st, run, root := openSandboxManifestStoreAt(t, ctx, path)
	fixture := newDockerSandboxStoreFixture(t, ctx, st, run, root,
		"docker-sandbox-product")

	record, replayed, err := st.CreateDockerSandboxAdmission(ctx, fixture.Admission)
	if err != nil || replayed || record.Admission.AdmissionFingerprint !=
		fixture.Admission.AdmissionFingerprint || record.Launch != nil || record.Receipt != nil {
		t.Fatalf("create product admission: record=%#v replayed=%t err=%v",
			record, replayed, err)
	}
	replayedRecord, wasReplay, err := st.CreateDockerSandboxAdmission(ctx,
		fixture.Admission)
	if err != nil || !wasReplay || !replayedRecord.Replayed {
		t.Fatalf("exact admission replay: record=%#v replayed=%t err=%v",
			replayedRecord, wasReplay, err)
	}
	beginDockerSandboxStoreStart(t, ctx, st, fixture)
	conflicting := fixture.Admission
	conflicting.RuntimeEpochFingerprint = storeTestDigest("different-runtime-epoch")
	conflicting.AdmissionFingerprint = domain.DockerSandboxAdmissionFingerprint(conflicting)
	if _, _, err := st.CreateDockerSandboxAdmission(ctx,
		conflicting); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("operation replay accepted different authority: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `UPDATE sandbox_docker_product_admissions
		SET execution_authorized = 0 WHERE id = ?`, fixture.Admission.ID); err == nil {
		t.Fatal("product admission was mutable")
	}
	if _, err := st.db.ExecContext(ctx, `DELETE FROM sandbox_docker_product_admissions
		WHERE id = ?`, fixture.Admission.ID); err == nil {
		t.Fatal("product admission was deletable")
	}
	byOperation, found, err := st.GetDockerSandboxAdmissionByOperation(ctx,
		fixture.Admission.OperationKeyDigest)
	if err != nil || !found || byOperation.ID != fixture.Admission.ID {
		t.Fatalf("admission operation lookup: value=%#v found=%t err=%v",
			byOperation, found, err)
	}
	byLifecycle, found, err := st.GetDockerSandboxAdmissionByLifecycleOperation(ctx,
		fixture.Admission.LifecycleOperationDigest)
	if err != nil || !found || byLifecycle.ID != fixture.Admission.ID {
		t.Fatalf("lifecycle operation lookup: value=%#v found=%t err=%v",
			byLifecycle, found, err)
	}

	if _, _, err := st.BeginDockerContainerLifecycle(ctx, fixture.Intent,
		"docker_product_owner", time.Minute); err != nil {
		t.Fatal(err)
	}
	launch := newDockerSandboxStoreLaunch(t, fixture)
	bound, replayed, err := st.BindDockerSandboxLaunch(ctx, launch)
	if err != nil || replayed || bound.Launch == nil ||
		bound.Launch.LaunchFingerprint != launch.LaunchFingerprint {
		t.Fatalf("bind product launch: record=%#v replayed=%t err=%v",
			bound, replayed, err)
	}
	if _, replayed, err = st.BindDockerSandboxLaunch(ctx,
		launch); err != nil || !replayed {
		t.Fatalf("exact launch replay: replayed=%t err=%v", replayed, err)
	}
	var v97Product, v97Execution, v97Artifact int
	if err := st.db.QueryRowContext(ctx, `SELECT product_entry_enabled,
		execution_authorized, artifact_commit_authorized
		FROM sandbox_docker_lifecycle_intents WHERE id = ?`, fixture.Intent.ID).Scan(
		&v97Product, &v97Execution, &v97Artifact); err != nil ||
		v97Product != 0 || v97Execution != 0 || v97Artifact != 0 {
		t.Fatalf("v99 widened v97 historical authority: values=%d/%d/%d err=%v",
			v97Product, v97Execution, v97Artifact, err)
	}

	eventList, err := st.ListRunEvents(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	productEvents := 0
	for _, event := range eventList {
		if event.Type != events.SandboxDockerProductAdmittedEvent &&
			event.Type != events.SandboxDockerProductLaunchBoundEvent {
			continue
		}
		productEvents++
		payload := string(event.PayloadJSON)
		if strings.Contains(payload, fixture.Admission.ManifestJSON) ||
			strings.Contains(payload, fixture.Manifest.Command.Executable) ||
			strings.Contains(payload, fixture.Admission.RuntimeEpochFingerprint) ||
			strings.Contains(payload, fixture.Admission.ApprovalID) {
			t.Fatalf("product event leaked private authority or Manifest: %s", payload)
		}
	}
	if productEvents != 2 {
		t.Fatalf("product events=%d, want admission and launch", productEvents)
	}

	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	recoverable, err := restarted.ListRecoverableDockerSandboxes(ctx, 10)
	if err != nil || len(recoverable) != 1 || recoverable[0].Launch == nil ||
		recoverable[0].Admission.ID != fixture.Admission.ID {
		t.Fatalf("restart recovery list: values=%#v err=%v", recoverable, err)
	}
	loaded, found, err := restarted.GetDockerSandboxRecordByLifecycleIntent(ctx,
		fixture.Intent.ID)
	if err != nil || !found || loaded.Admission.ID != fixture.Admission.ID {
		t.Fatalf("lifecycle record lookup: record=%#v found=%t err=%v",
			loaded, found, err)
	}
}

func TestDockerSandboxAdmissionRejectsExpiredAndStaleAuthority(t *testing.T) {
	ctx := context.Background()

	t.Run("expired readiness", func(t *testing.T) {
		st, run, root := openSandboxManifestStore(t, ctx)
		fixture := newDockerSandboxStoreFixture(t, ctx, st, run, root,
			"docker-sandbox-expired")
		fixture.Admission.CreatedAt = time.Now().UTC().Add(-2 * time.Second)
		fixture.Admission.ReadinessExpiresAt = time.Now().UTC().Add(-time.Second)
		fixture.Admission.AdmissionFingerprint =
			domain.DockerSandboxAdmissionFingerprint(fixture.Admission)
		if _, _, err := st.CreateDockerSandboxAdmission(ctx,
			fixture.Admission); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
			t.Fatalf("expired readiness admission error=%v", err)
		}
	})

	t.Run("latest profile at admission", func(t *testing.T) {
		st, run, root := openSandboxManifestStore(t, ctx)
		fixture := newDockerSandboxStoreFixture(t, ctx, st, run, root,
			"docker-sandbox-stale-profile")
		profiles := application.NewRunExecutionProfileService(st)
		if _, err := profiles.Change(ctx, application.ChangeRunExecutionProfileRequest{
			RunID: run.ID, Profile: "local", OperationKey: "profile-after-admission-build",
			RequestedBy: "docker_product_operator", Reason: "invalidate stale Docker profile",
		}); err != nil {
			t.Fatal(err)
		}
		if _, _, err := st.CreateDockerSandboxAdmission(ctx,
			fixture.Admission); apperror.CodeOf(err) != apperror.CodeConflict {
			t.Fatalf("stale profile admission error=%v", err)
		}
	})

	t.Run("historical binding survives profile change", func(t *testing.T) {
		st, run, root := openSandboxManifestStore(t, ctx)
		fixture := newDockerSandboxStoreFixture(t, ctx, st, run, root,
			"docker-sandbox-stale-launch")
		if _, _, err := st.CreateDockerSandboxAdmission(ctx,
			fixture.Admission); err != nil {
			t.Fatal(err)
		}
		beginDockerSandboxStoreStart(t, ctx, st, fixture)
		if _, _, err := st.BeginDockerContainerLifecycle(ctx, fixture.Intent,
			"stale_launch_owner", time.Minute); err != nil {
			t.Fatal(err)
		}
		profiles := application.NewRunExecutionProfileService(st)
		if _, err := profiles.Change(ctx, application.ChangeRunExecutionProfileRequest{
			RunID: run.ID, Profile: "local", OperationKey: "profile-before-launch",
			RequestedBy: "docker_product_operator", Reason: "revoke Docker launch scope",
		}); err != nil {
			t.Fatal(err)
		}
		launch := newDockerSandboxStoreLaunch(t, fixture)
		if _, replayed, err := st.BindDockerSandboxLaunch(ctx,
			launch); err != nil || replayed {
			t.Fatalf("historical profile binding replayed=%t err=%v", replayed, err)
		}
	})

	t.Run("historical binding survives permission change", func(t *testing.T) {
		st, run, root := openSandboxManifestStore(t, ctx)
		fixture := newDockerSandboxStoreFixture(t, ctx, st, run, root,
			"docker-sandbox-stale-permission")
		if _, _, err := st.CreateDockerSandboxAdmission(ctx,
			fixture.Admission); err != nil {
			t.Fatal(err)
		}
		beginDockerSandboxStoreStart(t, ctx, st, fixture)
		if _, _, err := st.BeginDockerContainerLifecycle(ctx, fixture.Intent,
			"stale_permission_owner", time.Minute); err != nil {
			t.Fatal(err)
		}
		permissions := application.NewRunExecutionPermissionService(st,
			domain.ExecutionPermissionRuntimeCapabilities{
				OperatorApprovalEnabled: true, DangerFullAccessEnabled: true,
			})
		if _, err := permissions.Change(ctx,
			application.ChangeRunExecutionPermissionRequest{
				RunID: run.ID, Mode: string(domain.RunExecutionPermissionFullAccess),
				OperationKey:            "permission-before-launch",
				RequestedBy:             "docker_product_operator",
				Reason:                  "change authority ceiling before Docker launch",
				ConfirmDangerFullAccess: true,
			}); err != nil {
			t.Fatal(err)
		}
		launch := newDockerSandboxStoreLaunch(t, fixture)
		if _, replayed, err := st.BindDockerSandboxLaunch(ctx,
			launch); err != nil || replayed {
			t.Fatalf("historical permission binding replayed=%t err=%v", replayed, err)
		}
	})

	t.Run("historical binding survives approval change", func(t *testing.T) {
		st, run, root := openSandboxManifestStore(t, ctx)
		fixture := newDockerSandboxStoreFixture(t, ctx, st, run, root,
			"docker-sandbox-stale-approval")
		if _, _, err := st.CreateDockerSandboxAdmission(ctx,
			fixture.Admission); err != nil {
			t.Fatal(err)
		}
		beginDockerSandboxStoreStart(t, ctx, st, fixture)
		if _, _, err := st.BeginDockerContainerLifecycle(ctx, fixture.Intent,
			"stale_approval_owner", time.Minute); err != nil {
			t.Fatal(err)
		}
		if _, err := st.db.ExecContext(ctx, `UPDATE tool_approvals
			SET status = 'denied', decision_reason = 'revoked before launch',
				version = version + 1, updated_at = ? WHERE id = ?`,
			ts(time.Now().UTC()), fixture.Admission.ApprovalID); err != nil {
			t.Fatal(err)
		}
		launch := newDockerSandboxStoreLaunch(t, fixture)
		if _, replayed, err := st.BindDockerSandboxLaunch(ctx,
			launch); err != nil || replayed {
			t.Fatalf("historical approval binding replayed=%t err=%v", replayed, err)
		}
	})

	t.Run("historical binding survives budget change", func(t *testing.T) {
		st, run, root := openSandboxManifestStore(t, ctx)
		fixture := newDockerSandboxStoreFixture(t, ctx, st, run, root,
			"docker-sandbox-stale-budget")
		if _, _, err := st.CreateDockerSandboxAdmission(ctx,
			fixture.Admission); err != nil {
			t.Fatal(err)
		}
		beginDockerSandboxStoreStart(t, ctx, st, fixture)
		if _, _, err := st.BeginDockerContainerLifecycle(ctx, fixture.Intent,
			"stale_budget_owner", time.Minute); err != nil {
			t.Fatal(err)
		}
		if _, err := st.db.ExecContext(ctx, `INSERT INTO run_tool_usage
			(run_id, consumed, updated_at, exhausted_at) VALUES (?, 1, ?, NULL)
			ON CONFLICT(run_id) DO UPDATE SET consumed = consumed + 1,
				updated_at = excluded.updated_at`, run.ID, ts(time.Now().UTC())); err != nil {
			t.Fatal(err)
		}
		launch := newDockerSandboxStoreLaunch(t, fixture)
		if _, replayed, err := st.BindDockerSandboxLaunch(ctx,
			launch); err != nil || replayed {
			t.Fatalf("historical budget binding replayed=%t err=%v", replayed, err)
		}
	})

	t.Run("historical binding survives readiness expiry", func(t *testing.T) {
		st, run, root := openSandboxManifestStore(t, ctx)
		fixture := newDockerSandboxStoreFixture(t, ctx, st, run, root,
			"docker-sandbox-expired-bind")
		fixture.Admission.ReadinessExpiresAt = time.Now().UTC().Add(time.Second)
		fixture.Admission.AdmissionFingerprint =
			domain.DockerSandboxAdmissionFingerprint(fixture.Admission)
		if _, _, err := st.CreateDockerSandboxAdmission(ctx, fixture.Admission); err != nil {
			t.Fatal(err)
		}
		beginDockerSandboxStoreStart(t, ctx, st, fixture)
		if _, _, err := st.BeginDockerContainerLifecycle(ctx, fixture.Intent,
			"expired_bind_owner", time.Minute); err != nil {
			t.Fatal(err)
		}
		time.Sleep(1100 * time.Millisecond)
		launch := newDockerSandboxStoreLaunch(t, fixture)
		if _, replayed, err := st.BindDockerSandboxLaunch(ctx,
			launch); err != nil || replayed {
			t.Fatalf("expired historical binding replayed=%t err=%v", replayed, err)
		}
	})
}

func TestDockerSandboxCompletionBindsCleanupAndIOReceipts(t *testing.T) {
	ctx := context.Background()
	st, run, root := openSandboxManifestStore(t, ctx)
	fixture := newDockerSandboxStoreFixture(t, ctx, st, run, root,
		"docker-sandbox-completion")
	if _, _, err := st.CreateDockerSandboxAdmission(ctx, fixture.Admission); err != nil {
		t.Fatal(err)
	}
	beginDockerSandboxStoreStart(t, ctx, st, fixture)
	lifecycle, _, err := st.BeginDockerContainerLifecycle(ctx, fixture.Intent,
		"completion_owner", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	launch := newDockerSandboxStoreLaunch(t, fixture)
	if _, _, err := st.BindDockerSandboxLaunch(ctx, launch); err != nil {
		t.Fatal(err)
	}
	lifecycle = completeDockerSandboxStoreLifecycle(t, ctx, st, lifecycle,
		fixture.Request, 0)
	completedAt := time.Now().UTC()
	exitCode := 0
	forged := domain.DockerSandboxReceipt{
		ID: idgen.New("docker-sandbox-receipt"), AdmissionID: fixture.Admission.ID,
		ProtocolVersion:   domain.DockerSandboxReceiptProtocolVersion,
		LifecycleIntentID: fixture.Intent.ID, AttemptID: fixture.Intent.AttemptID,
		RunID: run.ID, WorkspaceID: fixture.Admission.WorkspaceID,
		Outcome:    domain.DockerSandboxOutcomeSucceeded,
		ReasonCode: domain.DockerSandboxReasonCompleted, ExitCode: &exitCode,
		LogReceiptID: "missing-log-receipt", CleanupComplete: true,
		ArtifactCommitAuthorized: true, CompletedAt: completedAt,
	}
	forged.ReceiptFingerprint = domain.DockerSandboxReceiptFingerprint(forged)
	if _, _, err := st.CompleteDockerSandbox(ctx,
		forged); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("missing I/O FK was accepted: %v", err)
	}
	receipt := forged
	receipt.ID = idgen.New("docker-sandbox-receipt")
	receipt.LogReceiptID = ""
	receipt.ReceiptFingerprint = domain.DockerSandboxReceiptFingerprint(receipt)
	completed, replayed, err := st.CompleteDockerSandbox(ctx, receipt)
	if err != nil || replayed || completed.Receipt == nil ||
		completed.Receipt.ReceiptFingerprint != receipt.ReceiptFingerprint {
		t.Fatalf("complete Docker Sandbox: record=%#v replayed=%t err=%v",
			completed, replayed, err)
	}
	if _, replayed, err = st.CompleteDockerSandbox(ctx,
		receipt); err != nil || !replayed {
		t.Fatalf("completion replay: replayed=%t err=%v", replayed, err)
	}
	if recoverable, err := st.ListRecoverableDockerSandboxes(ctx,
		10); err != nil || len(recoverable) != 0 {
		t.Fatalf("completed admission remained recoverable: %#v err=%v",
			recoverable, err)
	}
	if lifecycle.Receipt == nil {
		t.Fatal("lifecycle cleanup receipt missing")
	}
	cancellation := domain.DockerSandboxCancellation{
		ID: idgen.New("docker-sandbox-cancellation"), AdmissionID: fixture.Admission.ID,
		ProtocolVersion: domain.DockerSandboxCancellationProtocolVersion,
		RunID:           run.ID, RequestedBy: fixture.Admission.RequestedBy,
		OperationKeyDigest: storeTestDigest("late-cancellation"),
		ReasonCode:         domain.DockerSandboxReasonCancelled, RequestedAt: time.Now().UTC(),
	}
	cancellation.CancellationFingerprint =
		domain.DockerSandboxCancellationFingerprint(cancellation)
	if _, _, err := st.RequestDockerSandboxCancellation(ctx,
		cancellation); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("completed product accepted late cancellation: %v", err)
	}
	eventList, err := st.ListRunEvents(ctx, run.ID)
	if err != nil || eventList[len(eventList)-1].Type !=
		events.SandboxDockerProductCompletedEvent ||
		strings.Contains(string(eventList[len(eventList)-1].PayloadJSON),
			fixture.Admission.ManifestJSON) {
		t.Fatalf("completion metadata event invalid: events=%#v err=%v", eventList, err)
	}
}

func TestDockerSandboxCancellationIsAppendOnlyAndRestartIdempotent(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "docker-sandbox-cancellation.db")
	st, run, root := openSandboxManifestStoreAt(t, ctx, path)
	fixture := newDockerSandboxStoreFixture(t, ctx, st, run, root,
		"docker-sandbox-cancellation")
	if _, _, err := st.CreateDockerSandboxAdmission(ctx, fixture.Admission); err != nil {
		t.Fatal(err)
	}
	request := domain.DockerSandboxCancellation{
		ID:              idgen.New("docker-sandbox-cancellation"),
		AdmissionID:     fixture.Admission.ID,
		ProtocolVersion: domain.DockerSandboxCancellationProtocolVersion,
		RunID:           run.ID, RequestedBy: fixture.Admission.RequestedBy,
		OperationKeyDigest: storeTestDigest("docker-sandbox-cancellation-operation"),
		ReasonCode:         domain.DockerSandboxReasonCancelled, RequestedAt: time.Now().UTC(),
	}
	request.CancellationFingerprint =
		domain.DockerSandboxCancellationFingerprint(request)
	stored, replayed, err := st.RequestDockerSandboxCancellation(ctx, request)
	if err != nil || replayed ||
		stored.CancellationFingerprint != request.CancellationFingerprint {
		t.Fatalf("request cancellation: value=%#v replayed=%t err=%v",
			stored, replayed, err)
	}
	conflicting := request
	conflicting.ID = idgen.New("docker-sandbox-cancellation")
	conflicting.CancellationFingerprint =
		domain.DockerSandboxCancellationFingerprint(conflicting)
	if _, _, err := st.RequestDockerSandboxCancellation(ctx,
		conflicting); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("cancellation operation conflict error=%v", err)
	}
	for _, statement := range []string{
		`UPDATE sandbox_docker_product_cancellations SET reason_code = 'ready' WHERE admission_id = ?`,
		`DELETE FROM sandbox_docker_product_cancellations WHERE admission_id = ?`,
	} {
		if _, err := st.db.ExecContext(ctx, statement, fixture.Admission.ID); err == nil {
			t.Fatalf("append-only cancellation mutation succeeded: %s", statement)
		}
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	loaded, found, err := restarted.GetDockerSandboxCancellation(ctx,
		fixture.Admission.ID)
	if err != nil || !found ||
		loaded.CancellationFingerprint != request.CancellationFingerprint {
		t.Fatalf("restart cancellation lookup: value=%#v found=%t err=%v",
			loaded, found, err)
	}
	if replay, wasReplay, err := restarted.RequestDockerSandboxCancellation(ctx,
		request); err != nil || !wasReplay ||
		replay.CancellationFingerprint != request.CancellationFingerprint {
		t.Fatalf("restart cancellation replay: value=%#v replayed=%t err=%v",
			replay, wasReplay, err)
	}
	eventList, err := restarted.ListRunEvents(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	last := eventList[len(eventList)-1]
	if last.Type != events.SandboxDockerProductCancelRequestedEvent ||
		strings.Contains(string(last.PayloadJSON), fixture.Admission.ManifestJSON) ||
		strings.Contains(string(last.PayloadJSON), request.OperationKeyDigest) {
		t.Fatalf("cancellation event leaked request content: %#v", last)
	}
}

func TestSchemaV99DoesNotBackfillDockerProductAuthority(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "schema-v98-docker-product.db")
	st, _, _ := openSandboxManifestStoreAt(t, ctx, path)
	for _, statement := range removeSchemaV99ForTestStatements() {
		if _, err := st.db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("remove schema v99 with %q: %v", statement, err)
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
	if version, err := upgraded.SchemaVersion(ctx); err != nil ||
		version != LatestSchemaVersion {
		t.Fatalf("schema v98 upgrade: version=%d err=%v", version, err)
	}
	var admissions, launches, receipts int
	if err := upgraded.db.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM sandbox_docker_product_admissions),
		(SELECT COUNT(*) FROM sandbox_docker_product_launches),
		(SELECT COUNT(*) FROM sandbox_docker_product_receipts)`).Scan(
		&admissions, &launches, &receipts); err != nil ||
		admissions != 0 || launches != 0 || receipts != 0 {
		t.Fatalf("v99 fabricated product authority: %d/%d/%d err=%v",
			admissions, launches, receipts, err)
	}
}

func newDockerSandboxStoreFixture(t *testing.T, ctx context.Context, st *SQLiteStore,
	run domain.Run, root, prefix string,
) dockerSandboxStoreFixture {
	t.Helper()
	_, manifest, observation := createDockerContainerPlanStoreAuthority(t, ctx, st,
		run.ID, root, prefix)
	plan, operation := newDockerContainerPlanStoreRecord(t, ctx, observation, manifest,
		prefix+"-plan")
	if _, _, err := st.CreateDockerContainerPlan(ctx, plan, operation); err != nil {
		t.Fatal(err)
	}
	profileResult, err := application.NewRunExecutionProfileService(st).Change(ctx,
		application.ChangeRunExecutionProfileRequest{
			RunID: run.ID, Profile: "docker", OperationKey: prefix + "-profile",
			RequestedBy: "docker_product_operator", Reason: "select Docker product profile",
		})
	if err != nil {
		t.Fatal(err)
	}
	permissionResult, err := application.NewRunExecutionPermissionService(st,
		domain.ExecutionPermissionRuntimeCapabilities{OperatorApprovalEnabled: true}).Change(ctx,
		application.ChangeRunExecutionPermissionRequest{
			RunID: run.ID, Mode: string(domain.RunExecutionPermissionApproval),
			OperationKey: prefix + "-permission", RequestedBy: "docker_product_operator",
			Reason: "approve exact Docker product execution", ConfirmUserApproval: true,
		})
	if err != nil {
		t.Fatal(err)
	}
	spec, err := sandbox.CompileDockerContainerSpec(ctx, observation, manifest)
	if err != nil {
		t.Fatal(err)
	}
	request, err := sandbox.NewDockerContainerWriteRequest(ctx, root, spec)
	if err != nil {
		t.Fatal(err)
	}
	createdAt := time.Now().UTC()
	lifecycleOperationDigest := runmutation.Fingerprint(
		"docker_sandbox_product_lifecycle_test.v1", prefix)
	intent, err := sandbox.NewDockerContainerLaunchIntent(
		idgen.New("sandbox-docker-lifecycle"), idgen.New("sandbox-docker-attempt"),
		lifecycleOperationDigest, plan, request, mustDockerLifecycleEndpoint(t),
		plan.RequestedBy, 1, createdAt)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := manifest.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	manifestFingerprint, err := manifest.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	var approvalID string
	var approvalVersion, toolCallsUsed int64
	if err := st.db.QueryRowContext(ctx, `SELECT candidate.approval_id,
		approval.version, candidate.tool_calls_used
		FROM sandbox_execution_candidates candidate
		JOIN tool_approvals approval ON approval.id = candidate.approval_id
		WHERE candidate.id = ?`, plan.CandidateID).Scan(
		&approvalID, &approvalVersion, &toolCallsUsed); err != nil {
		t.Fatal(err)
	}
	admission := domain.DockerSandboxAdmission{
		ID:                       idgen.New("docker-sandbox-admission"),
		ProtocolVersion:          domain.DockerSandboxAdmissionProtocolVersion,
		OperationKeyDigest:       storeTestDigest(prefix + "-admission-operation"),
		RequestFingerprint:       storeTestDigest(prefix + "-product-request"),
		LifecycleOperationDigest: lifecycleOperationDigest,
		RunID:                    run.ID, MissionID: plan.MissionID, WorkspaceID: plan.WorkspaceID,
		PlanID: plan.ID, CandidateID: plan.CandidateID, PreparationID: plan.PreparationID,
		ManifestJSON: string(canonical), ManifestFingerprint: manifestFingerprint,
		PlanFingerprint: plan.PlanFingerprint, SpecFingerprint: plan.SpecFingerprint,
		AuthorityFingerprint:    plan.AuthorityFingerprint,
		ReadinessFingerprint:    storeTestDigest(prefix + "-readiness"),
		ReadinessExpiresAt:      createdAt.Add(time.Minute),
		RuntimeEpochFingerprint: storeTestDigest(prefix + "-runtime-epoch"),
		ProfileSnapshotID:       profileResult.Profile.ID,
		ProfileRevision:         profileResult.Profile.Revision,
		PermissionSnapshotID:    permissionResult.Permission.ID,
		PermissionRevision:      permissionResult.Permission.Revision,
		PermissionMode:          permissionResult.Permission.Mode,
		ApprovalID:              approvalID, ApprovalVersion: approvalVersion,
		PolicyFingerprint: plan.PolicyFingerprint,
		NetworkMode:       "disabled", NetworkTargetCount: 0,
		CPUQuotaMillis: manifest.Resources.CPUQuotaMillis,
		MemoryBytes:    manifest.Resources.MemoryBytes, PIDs: manifest.Resources.PIDs,
		DiskBytes:        manifest.Resources.MaxOutputBytes,
		WallClockSeconds: manifest.TimeoutSeconds,
		LogBytes: min(manifest.Resources.MaxOutputBytes,
			domain.MaxDockerSandboxLogBytes),
		LogLines:            domain.MaxDockerSandboxLogLines,
		ToolCallsRemaining:  run.Budget.MaxToolCalls - toolCallsUsed,
		Decision:            domain.DockerSandboxAdmissionAuthorized,
		ReasonCode:          domain.DockerSandboxReasonReady,
		RemediationCode:     domain.DockerSandboxRemediationNone,
		ProductEntryEnabled: true, ExecutionAuthorized: true,
		ArtifactCommitAuthorized: true, RequestedBy: plan.RequestedBy,
		CreatedAt: createdAt,
	}
	admission.AdmissionFingerprint = domain.DockerSandboxAdmissionFingerprint(admission)
	if err := admission.Validate(); err != nil {
		t.Fatal(err)
	}
	start := domain.DockerSandboxStartIntent{
		AdmissionID:             admission.ID,
		ProtocolVersion:         domain.DockerSandboxStartProtocolVersion,
		OperationKeyDigest:      storeTestDigest(prefix + "-start-operation"),
		RequestFingerprint:      storeTestDigest(prefix + "-start-request"),
		RuntimeEpochFingerprint: admission.RuntimeEpochFingerprint,
		RunID:                   admission.RunID, RequestedBy: admission.RequestedBy,
		CreatedAt: createdAt,
	}
	start.StartFingerprint = domain.DockerSandboxStartFingerprint(start)
	return dockerSandboxStoreFixture{Run: run, Manifest: manifest, Plan: plan,
		Admission: admission, Start: start, Intent: intent, Request: request}
}

func newDockerSandboxStoreLaunch(t *testing.T,
	fixture dockerSandboxStoreFixture,
) domain.DockerSandboxLaunch {
	t.Helper()
	launch := domain.DockerSandboxLaunch{
		AdmissionID:                 fixture.Admission.ID,
		ProtocolVersion:             domain.DockerSandboxLaunchProtocolVersion,
		StartOperationKeyDigest:     fixture.Start.OperationKeyDigest,
		LifecycleIntentID:           fixture.Intent.ID,
		AttemptID:                   fixture.Intent.AttemptID,
		RunID:                       fixture.Run.ID,
		LifecycleRequestFingerprint: fixture.Request.RequestFingerprint,
		CreatedAt:                   fixture.Intent.CreatedAt,
	}
	launch.LaunchFingerprint = domain.DockerSandboxLaunchFingerprint(launch)
	if err := launch.Validate(); err != nil {
		t.Fatal(err)
	}
	return launch
}

func beginDockerSandboxStoreStart(t *testing.T, ctx context.Context,
	st *SQLiteStore, fixture dockerSandboxStoreFixture,
) {
	t.Helper()
	if _, replayed, err := st.BeginDockerSandboxStart(ctx,
		fixture.Start); err != nil || replayed {
		t.Fatalf("begin Docker Sandbox start: replayed=%t err=%v", replayed, err)
	}
}

func completeDockerSandboxStoreLifecycle(t *testing.T, ctx context.Context,
	st *SQLiteStore, record sandbox.DockerContainerLifecycleRecord,
	request sandbox.DockerContainerWriteRequest, exitCode int,
) sandbox.DockerContainerLifecycleRecord {
	t.Helper()
	stage, err := sandbox.NewDockerContainerStageResult(mustDockerLifecycleEndpoint(t),
		request, strings.Repeat("d", 64), false)
	if err != nil {
		t.Fatal(err)
	}
	nextAt := time.Now().UTC()
	appendAction := func(verb string) {
		t.Helper()
		action, actionErr := sandbox.NewDockerContainerLifecyclePreparedAction(
			record.Intent.ID, len(record.Actions)+1, record.Lease, verb, nextAt)
		if actionErr != nil {
			t.Fatal(actionErr)
		}
		record, _, actionErr = st.PrepareDockerContainerLifecycleAction(ctx, action,
			record.Lease)
		if actionErr != nil {
			t.Fatal(actionErr)
		}
		nextAt = time.Now().UTC()
	}
	appendTransition := func(state, reason, containerID string, code *int) {
		t.Helper()
		previous := ""
		if len(record.Transitions) != 0 {
			previous = record.Transitions[len(record.Transitions)-1].TransitionFingerprint
		}
		transition, transitionErr := sandbox.NewDockerContainerLifecycleTransition(
			record.Intent.ID, len(record.Transitions)+1, record.Lease, state, reason,
			code, containerID, previous, nextAt)
		if transitionErr != nil {
			t.Fatal(transitionErr)
		}
		record, _, transitionErr = st.AppendDockerContainerLifecycleTransition(ctx,
			transition, record.Lease)
		if transitionErr != nil {
			t.Fatal(transitionErr)
		}
		nextAt = time.Now().UTC()
	}
	appendAction(string(sandbox.DockerContainerLifecycleActionCreate))
	appendTransition(sandbox.DockerContainerLifecycleTransitionCreated,
		sandbox.DockerContainerLifecycleReasonCreated, stage.ContainerIDFingerprint, nil)
	appendAction(string(sandbox.DockerContainerLifecycleActionStart))
	appendTransition(sandbox.DockerContainerLifecycleTransitionStarted,
		sandbox.DockerContainerLifecycleReasonStarted, stage.ContainerIDFingerprint, nil)
	appendTransition(sandbox.DockerContainerLifecycleTransitionExited,
		sandbox.DockerContainerLifecycleReasonNaturalExit, stage.ContainerIDFingerprint,
		&exitCode)
	appendTransition(sandbox.DockerContainerLifecycleTransitionCleaning,
		sandbox.DockerContainerLifecycleReasonNaturalExit, "", nil)
	appendAction(string(sandbox.DockerContainerLifecycleActionDelete))
	appendTransition(sandbox.DockerContainerLifecycleTransitionCleaned,
		sandbox.DockerContainerLifecycleReasonCleanupCompleted, "", nil)
	final := record.Transitions[len(record.Transitions)-1]
	receipt, err := sandbox.NewDockerContainerLifecycleReceipt(record.Intent.ID,
		record.Lease, final, stage.ContainerIDFingerprint,
		sandbox.DockerContainerLifecycleOutcomeNaturalExit, &exitCode, true, false,
		time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	record, _, err = st.CompleteDockerContainerLifecycle(ctx, receipt, record.Lease)
	if err != nil {
		t.Fatal(err)
	}
	return record
}
