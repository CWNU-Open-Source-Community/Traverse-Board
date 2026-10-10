package httpapi

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
)

type batchWorkbenchControllerStub struct {
	batchDeliveryControllerStub
	prepare application.PrepareBatchDeliveryWorkbenchRequest
	owner   application.BatchDeliveryWorkbenchOwnerRequest
	execute application.ExecuteBatchDeliveryWorkbenchRequest
}

func (s *batchWorkbenchControllerStub) result() application.BatchDeliveryWorkbenchSnapshot {
	return application.BatchDeliveryWorkbenchSnapshot{Snapshot: s.snapshot, WorkerAvailable: true,
		Children: []application.BatchDeliveryWorkbenchChild{{Ordinal: 1, Generation: 1, OwnerAvailable: true}}}
}
func (s *batchWorkbenchControllerStub) PrepareWorkbench(_ context.Context, request application.PrepareBatchDeliveryWorkbenchRequest) (application.BatchDeliveryWorkbenchSnapshot, error) {
	s.prepare = request
	return s.result(), nil
}
func (s *batchWorkbenchControllerStub) WorkbenchSnapshot(context.Context, string) (application.BatchDeliveryWorkbenchSnapshot, error) {
	return s.result(), nil
}
func (s *batchWorkbenchControllerStub) RenewWorkbenchOwner(_ context.Context, request application.BatchDeliveryWorkbenchOwnerRequest) (application.BatchDeliveryWorkbenchSnapshot, error) {
	s.owner = request
	return s.result(), nil
}
func (s *batchWorkbenchControllerStub) ExecuteWorkbench(_ context.Context, request application.ExecuteBatchDeliveryWorkbenchRequest) (application.BatchDeliveryWorkbenchSnapshot, error) {
	s.execute = request
	return s.result(), nil
}

func TestBatchWorkbenchHTTPControlsRetainBindingsAndOmitOwnerAuthority(t *testing.T) {
	fixture := newAPIFixture(t)
	now := time.Now().UTC()
	controller := &batchWorkbenchControllerStub{batchDeliveryControllerStub: batchDeliveryControllerStub{snapshot: application.BatchDeliverySnapshot{
		Plan: domain.BatchDeliveryPlan{ID: "batch-http-workbench-0001", RunID: fixture.run.ID, CreatedAt: now, UpdatedAt: now},
		Workspaces: []domain.BatchDeliveryWorkspace{{PlanID: "batch-http-workbench-0001", Ordinal: 1, Generation: 1,
			WorktreeRoot: "private-worktree", OwnerTokenDigest: "private-digest", ToolProfile: domain.DefaultBatchDeliveryToolProfile(), CreatedAt: now, UpdatedAt: now}},
		Mailbox: map[int][]domain.BatchDeliveryMailboxMessage{1: {}},
	}}}
	fixture.api.batchDeliveryController = controller
	fixture.api.batchDeliveryControlEnabled = true
	base := "/api/v1/runs/" + fixture.run.ID + "/batch-deliveries"
	prepared := performControlMethodPathRequest(t, fixture.api, http.MethodPost, base+"/prepare-workbench", "workbench-http-prepare-001", strings.NewReader(
		`{"version":"batch-delivery-workbench.v1","proposal_id":"proposal-1","confirm":true,"tasks":[{"ordinal":1,"ownership_hints":[{"path":"internal/one/a.go","kind":"file"}],"validations":[{"id":"diff","kind":"git_diff_check","scope":"."}]}]}`))
	if prepared.Code != http.StatusCreated || controller.prepare.RunID != fixture.run.ID || controller.prepare.OperationKey != "workbench-http-prepare-001" || len(controller.prepare.Tasks) != 1 {
		t.Fatalf("preparation status=%d request=%#v body=%s", prepared.Code, controller.prepare, prepared.Body.String())
	}
	path := base + "/" + controller.snapshot.Plan.ID
	read := fixture.get(t, path+"/workbench")
	if read.Code != http.StatusOK {
		t.Fatalf("read status=%d body=%s", read.Code, read.Body.String())
	}
	for _, forbidden := range []string{"owner_token", "owner_token_digest", "worktree_root", "private-digest", "private-worktree"} {
		if strings.Contains(read.Body.String(), forbidden) || strings.Contains(prepared.Body.String(), forbidden) {
			t.Fatalf("projection leaked %s", forbidden)
		}
	}
	for _, action := range []string{"workbench-owner", "workbench-execute"} {
		body := `{"version":"batch-delivery-workbench.v1","expected_generation":1,"confirm":true`
		if action == "workbench-owner" {
			body += `,"retry":true`
		}
		body += `}`
		result := performControlMethodPathRequest(t, fixture.api, http.MethodPost, path+"/children/1/"+action, "workbench-http-original-01", strings.NewReader(body))
		if result.Code != http.StatusOK {
			t.Fatalf("action=%s status=%d body=%s", action, result.Code, result.Body.String())
		}
	}
	if controller.owner.PlanID != controller.snapshot.Plan.ID || controller.owner.ExpectedGeneration != 1 || !controller.owner.Retry ||
		controller.execute.Ordinal != 1 || controller.execute.OperationKey != "workbench-http-original-01" {
		t.Fatalf("owner=%#v execution=%#v", controller.owner, controller.execute)
	}
	controller.execute = application.ExecuteBatchDeliveryWorkbenchRequest{}
	for _, body := range []string{`{"version":"wrong","expected_generation":1,"confirm":true}`,
		`{"version":"batch-delivery-workbench.v1","expected_generation":1,"confirm":false}`,
		`{"version":"batch-delivery-workbench.v1","expected_generation":1,"confirm":true,"owner_token":"forged"}`} {
		result := performControlMethodPathRequest(t, fixture.api, http.MethodPost, path+"/children/1/workbench-execute", "workbench-http-rejected-01", strings.NewReader(body))
		if result.Code != http.StatusBadRequest || controller.execute.PlanID != "" {
			t.Fatalf("invalid control status=%d body=%s", result.Code, result.Body.String())
		}
	}
	controller.snapshot.Plan.RunID = "foreign-run"
	foreign := performControlMethodPathRequest(t, fixture.api, http.MethodPost, path+"/children/1/workbench-execute", "workbench-http-foreign-01", strings.NewReader(`{"version":"batch-delivery-workbench.v1","expected_generation":1,"confirm":true}`))
	if foreign.Code != http.StatusNotFound || controller.execute.PlanID != "" {
		t.Fatalf("foreign plan status=%d body=%s", foreign.Code, foreign.Body.String())
	}
}
