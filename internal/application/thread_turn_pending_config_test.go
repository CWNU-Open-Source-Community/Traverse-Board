package application_test

import (
	"context"
	"path/filepath"
	"testing"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/modelregistry"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/store"
)

func TestThreadTurnIneligiblePendingModelPreservesActiveRunAndRetries(t *testing.T) {
	for _, failure := range []string{"provider_disabled", "harness_ineligible", "qualification_failed"} {
		t.Run(failure, func(t *testing.T) {
			ctx := context.Background()
			st, err := store.Open(filepath.Join(t.TempDir(), "pending-model.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			runs := application.NewRunService(st)
			_, created, err := runs.Create(ctx, application.CreateRunRequest{
				Goal:    "preserve a usable Run when its next model becomes unavailable",
				Profile: "review", ModelRoute: "lifecycle-test/model", Interactive: true,
				Budget: domain.Budget{MaxTurns: 4},
			})
			if err != nil {
				t.Fatal(err)
			}
			started, err := runs.Start(ctx, created.ID)
			if err != nil {
				t.Fatal(err)
			}
			provider := &lifecycleProvider{responses: []string{
				rootActionResponse(domain.RootActionWait, "Continue with the usable model.", "", "operator boundary"),
			}}
			router := llm.NewRouter(llm.ModelRef{Provider: provider.Name(), Model: "model"})
			router.RegisterProvider(provider)
			registry := &mutableThreadModelRouteRegistry{router: router,
				snapshot: modelregistry.Snapshot{
					ProtocolVersion: modelregistry.ProtocolVersion, Generation: 1,
					Providers: []modelregistry.ProviderAvailability{{
						Name: provider.Name(), DisplayName: "Lifecycle Provider",
						Kind: modelregistry.ProviderKindOpenAICompatible, Status: modelregistry.ProviderAvailable,
						Models: []string{"model", "model-next"}, CredentialSource: "test", Enabled: true,
						Harnesses: []modelregistry.HarnessAvailability{
							{ProtocolVersion: modelregistry.HarnessQualificationProtocolVersion,
								Model: "model", RootEligible: true, LatestQualificationStatus: modelregistry.QualificationStatusAvailable},
							{ProtocolVersion: modelregistry.HarnessQualificationProtocolVersion,
								Model: "model-next", RootEligible: true, LatestQualificationStatus: modelregistry.QualificationStatusAvailable},
						},
					}},
				}}
			threadID := domain.InitialThreadID(started.ID)
			modelRoutes := application.NewThreadModelRouteService(st, registry)
			selectModel := func(model, key string) {
				t.Helper()
				_, err := modelRoutes.Change(ctx, application.ChangeThreadModelRouteRequest{
					Version: domain.ThreadModelRouteControlProtocolVersion, ThreadID: threadID,
					Action: domain.ThreadModelRouteSelect, Provider: provider.Name(), Model: model,
					OperationKey: key, RequestedBy: "thread_turn_test_operator",
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			selectModel("model-next", "pending-model-select-next-0001")
			registry.mu.Lock()
			switch failure {
			case "provider_disabled":
				registry.snapshot.Providers[0].Enabled = false
			case "harness_ineligible":
				registry.snapshot.Providers[0].Harnesses[1].RootEligible = false
			case "qualification_failed":
				registry.snapshot.Providers[0].Harnesses[1].LatestQualificationStatus = modelregistry.QualificationStatusAuthFailed
			}
			registry.snapshot.Generation++
			registry.mu.Unlock()
			turns := application.NewThreadTurnService(st,
				application.NewRunLifecycleControlService(st),
				application.NewRunExecutionHandoffService(st, router, policy.NewDefaultChecker())).
				WithModelRouteRegistry(registry)
			request := application.ExecuteThreadTurnRequest{
				Version: domain.ThreadMessageProtocolVersion, ThreadID: threadID,
				Content:      "Continue this task after confirming the selected model.",
				OperationKey: "pending-model-original-message-0001", RequestedBy: "thread_turn_test_operator",
			}
			blocked, err := turns.Execute(ctx, request)
			if apperror.CodeOf(err) != apperror.CodeFailedPrecondition || blocked.ExecutionStarted ||
				blocked.ModelCalled || blocked.Submission.Message.ID != "" || provider.calls != 0 {
				t.Fatalf("unavailable model was not rejected before execution: result=%+v calls=%d err=%v", blocked, provider.calls, err)
			}
			storedRun, err := st.GetRun(ctx, started.ID)
			if err != nil || storedRun.Status != domain.RunRunning || storedRun.Config.ModelRoute != started.Config.ModelRoute {
				t.Fatalf("unavailable pending model destroyed the active Run: status=%s route=%s err=%v", storedRun.Status, storedRun.Config.ModelRoute, err)
			}
			thread, err := st.GetThread(ctx, threadID)
			if err != nil || thread.ActiveRunID != started.ID || thread.LastRunID != started.ID {
				t.Fatalf("unavailable pending model changed the Thread: %+v err=%v", thread, err)
			}
			bindings, err := st.ListThreadRuns(ctx, threadID)
			if err != nil || len(bindings) != 1 {
				t.Fatalf("unavailable pending model created a successor: bindings=%+v err=%v", bindings, err)
			}
			messages, err := st.ListOperatorSteering(ctx, started.ID, 100)
			if err != nil || len(messages) != 0 {
				t.Fatalf("unavailable pending model queued the message: messages=%+v err=%v", messages, err)
			}
			registry.mu.Lock()
			registry.snapshot.Providers[0].Enabled = true
			registry.snapshot.Generation++
			registry.mu.Unlock()
			selectModel("model", "pending-model-select-usable-0001")
			continued, err := turns.Execute(ctx, request)
			if err != nil || continued.Submission.Run.ID != started.ID || continued.Submission.SuccessorCreated ||
				continued.Submission.Message.Status != domain.OperatorSteeringCommitted || !continued.ModelCalled || provider.calls != 1 {
				t.Fatalf("reselection did not resume the original Run and request: result=%+v calls=%d err=%v", continued, provider.calls, err)
			}
			messages, err = st.ListOperatorSteering(ctx, started.ID, 100)
			if err != nil || len(messages) != 1 || messages[0].Content != request.Content {
				t.Fatalf("reselection did not enqueue exactly the original message: messages=%+v err=%v", messages, err)
			}
		})
	}
}
