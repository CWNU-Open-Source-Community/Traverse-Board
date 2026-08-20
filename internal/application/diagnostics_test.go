package application_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/modelregistry"
	"cyberagent-workbench/internal/store"
)

func TestStructuredDiagnosticsWithholdPayloadsAndProjectReadiness(t *testing.T) {
	ctx := context.Background()
	state, err := store.Open(filepath.Join(t.TempDir(), "diagnostics.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	_, created, err := application.NewRunService(state).Create(ctx,
		application.CreateRunRequest{Goal: "diagnose bounded timeline", Profile: "code",
			Surface: "code", Phase: "plan", Budget: domain.Budget{MaxTurns: 8},
			RequestedBy: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := application.NewRunService(state).Start(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	secret := "-----BEGIN PRIVATE KEY----- diagnostic-secret"
	queued, err := state.EnqueueOperatorSteering(ctx, domain.EnqueueOperatorSteeringRequest{
		RunID: run.ID, SessionID: run.SessionID, Content: secret,
		OperationKey: "diagnostic-secret-steering-operation-0001",
		RequestedBy:  "operator",
	})
	if err != nil {
		t.Fatal(err)
	}
	service := application.NewDiagnosticsService(state, modelregistry.New(nil))
	doctor, err := service.Doctor(ctx, run.ID)
	if err != nil || doctor.ProtocolVersion != application.DoctorSnapshotProtocolVersion ||
		doctor.SchemaVersion != store.LatestSchemaVersion || doctor.Run == nil ||
		doctor.Run.RunID != run.ID || doctor.Run.ProcessCapabilityGranted ||
		doctor.Redaction.Prompts != "withheld" {
		t.Fatalf("doctor=%#v err=%v", doctor, err)
	}
	for _, check := range doctor.Checks {
		if check.Status != "ready" && check.Status != "degraded" &&
			check.Status != "not_configured" && check.Status != "not_probed" {
			t.Fatalf("doctor emitted undocumented readiness %q for %s",
				check.Status, check.Component)
		}
	}
	debug, err := service.Debug(ctx, application.DebugQueryRequest{
		Version: application.DebugQueryProtocolVersion, RunID: run.ID,
		From: run.CreatedAt.Add(-time.Minute), To: time.Now().UTC().Add(time.Minute),
		Limit: 100, CorrelationKind: "request", CorrelationID: queued.Message.ID,
	})
	if err != nil || debug.ProtocolVersion != application.DebugQueryProtocolVersion ||
		len(debug.Items) == 0 || debug.Items[0].PayloadState != "withheld" {
		t.Fatalf("debug=%#v err=%v", debug, err)
	}
	raw, err := json.Marshal(struct {
		Doctor application.DoctorSnapshot   `json:"doctor"`
		Debug  application.DebugQueryResult `json:"debug"`
	}{Doctor: doctor, Debug: debug})
	if err != nil {
		t.Fatal(err)
	}
	serialized := string(raw)
	if strings.Contains(serialized, secret) || strings.Contains(serialized, "payload_json") ||
		strings.Contains(serialized, "terminal_input_issued") {
		t.Fatalf("structured diagnostics exposed withheld content: %s", serialized)
	}
}
