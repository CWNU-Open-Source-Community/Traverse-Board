package httpapi

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/contextmgr"
	"cyberagent-workbench/internal/coordinator"
	"cyberagent-workbench/internal/credential"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/fileedit"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/pricing"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/skills"
	"cyberagent-workbench/internal/standardcodedelivery"
	"cyberagent-workbench/internal/toolgateway"
	"cyberagent-workbench/internal/uievidence"
	"cyberagent-workbench/internal/verification"
)

func TestOpenAPIDocumentIsDeterministicCapabilitySeparatedAndSecretFree(t *testing.T) {
	first, err := GenerateOpenAPI()
	if err != nil {
		t.Fatal(err)
	}
	second, err := GenerateOpenAPI()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) || len(first) == 0 || first[len(first)-1] != '\n' || !json.Valid(first) {
		t.Fatal("OpenAPI generation is not deterministic canonical JSON")
	}
	for _, forbidden := range []string{`"lease_id"`, `"pending_input"`, `"fencing_token"`, `"api_key"`} {
		if bytes.Contains(first, []byte(forbidden)) {
			t.Fatalf("OpenAPI document exposed forbidden internal property %s", forbidden)
		}
	}

	var document openAPIDocument
	if err := json.Unmarshal(first, &document); err != nil {
		t.Fatal(err)
	}
	if document.OpenAPI != openAPISpecVersion || document.JSONSchemaDialect != openAPIJSONSchemaDialect ||
		document.Info.Version != Version || document.Info.License.Identifier != "Apache-2.0" ||
		document.ReadOnly || len(document.Security) != 1 {
		t.Fatalf("OpenAPI metadata is incomplete: %#v", document)
	}
	expectedPaths := sortedOpenAPIPaths()
	specsByOperation := make(map[string]openAPIOperationSpec)
	expectedOperationCounts := make(map[string]int)
	for _, spec := range openAPIOperationSpecs() {
		specsByOperation[spec.OperationID] = spec
		expectedOperationCounts[spec.Path]++
	}
	actualPaths := make([]string, 0, len(document.Paths))
	operationIDs := make(map[string]struct{}, len(document.Paths))
	for path, item := range document.Paths {
		actualPaths = append(actualPaths, path)
		type operationEntry struct {
			method    string
			operation *openAPIOperation
		}
		operations := make([]operationEntry, 0, 4)
		if item.Get != nil {
			operations = append(operations, operationEntry{method: http.MethodGet, operation: item.Get})
		}
		if item.Post != nil {
			operations = append(operations, operationEntry{method: http.MethodPost, operation: item.Post})
		}
		if item.Patch != nil {
			operations = append(operations, operationEntry{method: http.MethodPatch, operation: item.Patch})
		}
		if item.Delete != nil {
			operations = append(operations, operationEntry{method: http.MethodDelete, operation: item.Delete})
		}
		expectedOperations := expectedOperationCounts[path]
		if len(operations) != expectedOperations {
			t.Fatalf("path %s exposes %d operations, want %d: %#v",
				path, len(operations), expectedOperations, item)
		}
		for _, entry := range operations {
			operation := entry.operation
			spec, found := specsByOperation[operation.OperationID]
			if !found || spec.Path != path {
				t.Fatalf("path %s exposes undocumented operation %#v", path, operation)
			}
			expectedMethod := spec.Method
			if expectedMethod == "" {
				expectedMethod = http.MethodGet
			}
			status, statusErr := openAPISuccessStatus(spec)
			if statusErr != nil {
				t.Fatal(statusErr)
			}
			if expectedMethod != entry.method || operation.Responses[status] == nil ||
				(operation.RequestBody != nil) != (spec.RequestType != nil) {
				t.Fatalf("path %s operation does not match its Go spec: %#v", path, operation)
			}
			if spec.Control {
				if operation.ReadOnly || len(operation.Security) != 1 ||
					operation.Security[0]["ControlBearerAuth"] == nil || operation.RequestBody == nil {
					t.Fatalf("path %s has an incomplete control operation: %#v", path, operation)
				}
			} else if !operation.ReadOnly || len(operation.Security) != 0 {
				t.Fatalf("path %s has an incomplete read operation: %#v", path, operation)
			}
			if _, duplicate := operationIDs[operation.OperationID]; duplicate {
				t.Fatalf("duplicate OpenAPI operation id %q", operation.OperationID)
			}
			operationIDs[operation.OperationID] = struct{}{}
		}
	}
	sort.Strings(actualPaths)
	if !reflect.DeepEqual(actualPaths, expectedPaths) {
		t.Fatalf("OpenAPI path catalog drifted:\n got %v\nwant %v", actualPaths, expectedPaths)
	}

	var raw struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(first, &raw); err != nil {
		t.Fatal(err)
	}
	allowedMethods := make(map[string]map[string]bool)
	for _, spec := range openAPIOperationSpecs() {
		method := strings.ToLower(spec.Method)
		if method == "" {
			method = "get"
		}
		if allowedMethods[spec.Path] == nil {
			allowedMethods[spec.Path] = make(map[string]bool)
		}
		allowedMethods[spec.Path][method] = true
	}
	for path, item := range raw.Paths {
		for method := range item {
			if !allowedMethods[path][method] {
				t.Fatalf("OpenAPI path %s exposed unexpected operation %q", path, method)
			}
		}
	}
	assertOpenAPISchemaOmits(t, document.Components.Schemas, "RunExecutionLeaseView", "lease_id")
	assertOpenAPISchemaOmits(t, document.Components.Schemas, "SupervisorCheckpointView", "pending_input")
	for _, field := range []string{"content", "content_sha256", "requested_by", "session_id", "session_message_id"} {
		assertOpenAPISchemaOmits(t, document.Components.Schemas,
			"OperatorSteeringMessageView", field)
	}
	assertOpenAPISchemaOmits(t, document.Components.Schemas, "ArtifactView", "content")
	assertOpenAPISchemaOmits(t, document.Components.Schemas,
		"ProviderCredentialStatusView", "secret")
	assertOpenAPIPropertyFlag(t, document.Components.Schemas,
		"ProviderCredentialRequestView", "secret", "writeOnly", true)
	assertOpenAPIPropertyFlag(t, document.Components.Schemas,
		"ProviderCredentialListView", "items", "minItems", float64(4))
	assertOpenAPIPropertyFlag(t, document.Components.Schemas,
		"ProviderCredentialListView", "items", "maxItems", float64(4))
	assertOpenAPIEnum(t, document.Components.Schemas, "ProviderCredentialStatusView", "provider",
		[]string{"anthropic", "deepseek", "mimo", "openai"})
	credentialProviders := []string{"anthropic", "deepseek", "mimo", "openai"}
	credentialPath, credentialPathFound := document.Paths[ProviderCredentialPathTemplate]
	if !credentialPathFound || credentialPath.Post == nil {
		t.Fatal("Provider credential control path is missing")
	}
	providerParameterFound := false
	for _, parameter := range credentialPath.Post.Parameters {
		if parameter.Name != "provider" {
			continue
		}
		providerParameterFound = true
		raw, ok := parameter.Schema["enum"].([]any)
		if !ok {
			t.Fatal("Provider credential path parameter has no enum")
		}
		actual := make([]string, len(raw))
		for index, value := range raw {
			actual[index], ok = value.(string)
			if !ok {
				t.Fatal("Provider credential path parameter enum is not a string list")
			}
		}
		if !reflect.DeepEqual(actual, credentialProviders) {
			t.Fatalf("Provider credential path enum=%v want=%v", actual, credentialProviders)
		}
	}
	if !providerParameterFound {
		t.Fatal("Provider credential path parameter is missing")
	}
	assertOpenAPIEnum(t, document.Components.Schemas, "ProviderAvailabilityView", "kind",
		[]string{"local", "anthropic_compatible", "openai_compatible", "ollama"})
	assertOpenAPIEnum(t, document.Components.Schemas, "ModelHarnessAvailabilityView",
		"transport_protocol", []string{"mock", "anthropic_messages",
			"openai_chat_completions", "ollama_chat", "provider_contract"})
	failureReasons := []string{"none", "not_configured", "authentication", "network",
		"rate_limit", "capacity", "model_not_found", "protocol_incompatible"}
	assertOpenAPIEnum(t, document.Components.Schemas, "ProviderDiagnosticView",
		"failure_reason", failureReasons)
	assertOpenAPIEnum(t, document.Components.Schemas, "ModelHarnessQualificationView",
		"failure_reason", failureReasons)
	qualificationStatuses := []string{"not_configured", "available", "protocol_mismatch",
		"auth_failed", "network_failed", "rate_limit", "capacity", "model_unsupported"}
	assertOpenAPIEnum(t, document.Components.Schemas, "ProviderDiagnosticView",
		"qualification_status", qualificationStatuses)
	assertOpenAPIEnum(t, document.Components.Schemas, "ModelHarnessQualificationView",
		"qualification_status", qualificationStatuses)
	assertOpenAPIEnum(t, document.Components.Schemas, "ModelHarnessAvailabilityView",
		"latest_qualification_status", append([]string{""}, qualificationStatuses...))
	for _, field := range []string{"path", "content", "command", "hook"} {
		assertOpenAPISchemaOmits(t, document.Components.Schemas,
			"SkillPackageInstallRequestView", field)
	}
	for _, field := range []string{"command", "content", "path", "request_fingerprint",
		"decision_reason", "requested_by", "reviewed_by", "grant_id"} {
		assertOpenAPISchemaOmits(t, document.Components.Schemas,
			"ApprovalQueueItemView", field)
	}
	for _, field := range []string{"executable", "argv", "shell", "environment",
		"raw_output"} {
		assertOpenAPISchemaOmits(t, document.Components.Schemas,
			"ControlledCommandProposalView", field)
	}
	for _, field := range []string{"command", "shell", "environment_values", "raw_output"} {
		assertOpenAPISchemaOmits(t, document.Components.Schemas,
			"HostCommandProposalView", field)
	}
	assertOpenAPISchemaOmits(t, document.Components.Schemas, "AgentNodeView", "status_reason")
	assertOpenAPISchemaOmits(t, document.Components.Schemas, "DelegationReviewView", "reason")
	assertOpenAPISchemaOmits(t, document.Components.Schemas, "DelegationApplicationView", "policy_fingerprint")
	assertOpenAPISchemaOmits(t, document.Components.Schemas, "FanoutExecutionShardView", "report_json")
	assertOpenAPISchemaOmits(t, document.Components.Schemas, "FanoutExecutionShardView", "error_reason")
	assertOpenAPISchemaOmits(t, document.Components.Schemas, "FindingArtifactEvidenceView", "note")
	assertOpenAPISchemaOmits(t, document.Components.Schemas, "FindingArtifactEvidenceView", "attached_by")
	assertOpenAPISchemaOmits(t, document.Components.Schemas, "RunExecutionProfileView", "requested_by")
	assertOpenAPISchemaOmits(t, document.Components.Schemas, "RunExecutionProfileView", "reason")
	for _, field := range []string{"selection_id", "mission_id", "mode_snapshot_id", "requested_by",
		"operation_id", "fingerprint", "digest", "content", "path"} {
		assertOpenAPISchemaOmits(t, document.Components.Schemas,
			"ExternalSkillProjectionView", field)
	}
	for _, field := range []string{"selection_id", "installation_id", "fingerprint", "sha256",
		"object_key", "content", "path", "archive_bytes", "content_bytes"} {
		assertOpenAPISchemaOmits(t, document.Components.Schemas,
			"ExternalSkillProjectionItemView", field)
	}
	assertOpenAPISchemaOptional(t, document.Components.Schemas, "AgentGraphView", "root_agent_id")
}

func TestOpenAPIGoldenDocumentMatchesGoDTOs(t *testing.T) {
	generated, err := GenerateOpenAPI()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("..", "..", "docs", "openapi.json")
	committed, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read committed OpenAPI document: %v", err)
	}
	if !bytes.Equal(committed, generated) {
		t.Fatalf("%s is stale; regenerate it with `cyberagent api openapi --output docs/openapi.json`", path)
	}
}

func TestOpenAPIRoutesMatchAuthenticatedLiveHandlers(t *testing.T) {
	fixture := newAPIFixture(t)
	fixture.api.eventStream = testEventStreamConfig(1, 100*time.Millisecond)
	fixture.api.gitAdvancedController = &gitAdvancedControllerStub{}
	fixture.api.gitAdvancedControlEnabled = true
	fixture.api.githubReviewController = &githubReviewControllerStub{}
	fixture.api.githubReviewControlEnabled = true
	fixture.api.standardCodeDeliveryController = &standardCodeDeliveryControllerStub{
		binding: standardcodedelivery.Binding{
			MissionID:         fixture.run.MissionID,
			SessionID:         fixture.run.SessionID,
			SourceWorkspaceID: fixture.workspace.ID,
		},
	}
	childRun, child, childAttempt, childModel :=
		prepareOpenAPISpecialistCancellationTarget(t, fixture)
	_, profileRun, err := application.NewRunService(fixture.store).Create(t.Context(),
		application.CreateRunRequest{Goal: "OpenAPI execution profile target", Profile: "code",
			Budget: domain.Budget{MaxTurns: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.NewRunExecutionPermissionService(fixture.store,
		domain.ExecutionPermissionRuntimeCapabilities{
			OperatorApprovalEnabled: true, DangerFullAccessEnabled: true,
			DebugMaximumAccessEnabled: true,
		}).Change(t.Context(), application.ChangeRunExecutionPermissionRequest{
		RunID: profileRun.ID, Mode: string(domain.RunExecutionPermissionDebug),
		OperationKey: "openapi-browser-cdp-debug-permission-0001",
		RequestedBy:  "openapi_test", Reason: "prepare full CDP debug contract",
		ConfirmDebugAccess: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := application.NewRunBrowserCDPPermissionService(fixture.store,
		domain.BrowserCDPPermissionRuntimeCapabilities{
			ControlEnabled: true, FullDebugEnabled: true,
		}).Change(t.Context(), application.ChangeRunBrowserCDPPermissionRequest{
		RunID: profileRun.ID, Mode: string(domain.RunBrowserCDPPermissionFullDebug),
		OperationKey: "openapi-browser-cdp-permission-0001",
		RequestedBy:  "openapi_test", Reason: "prepare restricted transition target",
		ConfirmFullCDPDebug: true,
	}); err != nil {
		t.Fatal(err)
	}
	_, interactionRun, err := application.NewRunService(fixture.store).Create(t.Context(),
		application.CreateRunRequest{Goal: "OpenAPI execution interaction target", Profile: "code",
			Budget: domain.Budget{MaxTurns: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.NewRunExecutionProfileService(fixture.store).Change(t.Context(),
		application.ChangeRunExecutionProfileRequest{
			RunID: interactionRun.ID, Profile: string(domain.RunExecutionProfileLocal),
			OperationKey: "openapi-interaction-profile-0001",
			RequestedBy:  "openapi_test", Reason: "prepare controlled interaction",
		}); err != nil {
		t.Fatal(err)
	}
	_, lifecycleRun, err := application.NewRunService(fixture.store).Create(t.Context(),
		application.CreateRunRequest{Goal: "OpenAPI lifecycle target", Profile: "code",
			Budget: domain.Budget{MaxTurns: 2}})
	if err != nil {
		t.Fatal(err)
	}
	_, executionCreated, err := application.NewRunService(fixture.store).Create(t.Context(),
		application.CreateRunRequest{Goal: "OpenAPI execution target", Profile: "code",
			ModelRoute: "mock/mock-code", Budget: domain.Budget{MaxTurns: 2}})
	if err != nil {
		t.Fatal(err)
	}
	executionRun, err := application.NewRunService(fixture.store).Start(t.Context(),
		executionCreated.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.EnqueueOperatorSteering(t.Context(),
		domain.EnqueueOperatorSteeringRequest{
			RunID: executionRun.ID, SessionID: executionRun.SessionID,
			Content:      "OpenAPI execution input",
			OperationKey: "openapi-execution-queue-0001", RequestedBy: "openapi_test",
		}); err != nil {
		t.Fatal(err)
	}
	_, planModeRun, err := application.NewRunService(fixture.store).Create(t.Context(),
		application.CreateRunRequest{Goal: "OpenAPI Plan entry target", Profile: "code",
			Phase: "deliver", Budget: domain.Budget{MaxTurns: 2}})
	if err != nil {
		t.Fatal(err)
	}
	planRun, planProposal := prepareOpenAPIPlanControlTarget(t, fixture)
	checker := policy.NewDefaultChecker()
	gateway := toolgateway.New(fixture.store, checker)
	pendingApproval, err := gateway.Invoke(t.Context(), toolgateway.ToolCall{
		Name: toolgateway.ShellTool, Arguments: map[string]string{"command": "echo OpenAPI approval"},
		RunID: fixture.run.ID, SessionID: fixture.run.SessionID,
		WorkspaceID: fixture.workspace.ID, RequestedBy: "openapi_test",
	})
	if err != nil || pendingApproval.Proposal == nil {
		t.Fatalf("prepare OpenAPI approval=%#v err=%v", pendingApproval, err)
	}
	approvalRecord, err := fixture.store.GetApprovalByProposal(t.Context(), pendingApproval.Proposal.ID)
	if err != nil {
		t.Fatal(err)
	}
	fileEditRecord, err := fileedit.NewManager(fixture.store).Propose(t.Context(), fileedit.Proposal{
		SessionID: fixture.run.SessionID, WorkspaceID: fixture.workspace.ID,
		WorkspaceRoot: fixture.workspace.RootPath, Path: "openapi-review.txt",
		ProposedText: "bounded OpenAPI review\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, wakeCreated, err := application.NewRunService(fixture.store).Create(t.Context(),
		application.CreateRunRequest{Goal: "OpenAPI wake target", Profile: "code",
			Budget: domain.Budget{MaxTurns: 2}})
	if err != nil {
		t.Fatal(err)
	}
	wakeRun, err := application.NewRunService(fixture.store).Start(t.Context(), wakeCreated.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.EnqueueOperatorSteering(t.Context(),
		domain.EnqueueOperatorSteeringRequest{RunID: wakeRun.ID, SessionID: wakeRun.SessionID,
			Content: "OpenAPI wake input", OperationKey: "openapi-wake-queue-0001",
			RequestedBy: "openapi_test"}); err != nil {
		t.Fatal(err)
	}
	_, wakeExecutionCreated, err := application.NewRunService(fixture.store).Create(t.Context(),
		application.CreateRunRequest{Goal: "OpenAPI wake execution target", Profile: "code",
			ModelRoute: "mock/mock-code", Budget: domain.Budget{MaxTurns: 2}})
	if err != nil {
		t.Fatal(err)
	}
	wakeExecutionRun, err := application.NewRunService(fixture.store).Start(t.Context(),
		wakeExecutionCreated.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.EnqueueOperatorSteering(t.Context(),
		domain.EnqueueOperatorSteeringRequest{RunID: wakeExecutionRun.ID,
			SessionID: wakeExecutionRun.SessionID, Content: "OpenAPI foreground wake input",
			OperationKey: "openapi-wake-execution-queue-0001",
			RequestedBy:  "openapi_test"}); err != nil {
		t.Fatal(err)
	}
	if _, err := application.NewRunWakeControlService(fixture.store).Schedule(t.Context(),
		application.ScheduleRunWakeRequest{Version: domain.RunWakeControlProtocolVersion,
			RunID: wakeExecutionRun.ID, OperationKey: "openapi-wake-execution-schedule-0001",
			RequestedBy: "openapi_test", MaxAttempts: 2, BaseBackoffSeconds: 5,
			MaxBackoffSeconds: 30, MaxElapsedSeconds: 120}); err != nil {
		t.Fatal(err)
	}
	skillArchive := buildOpenAPISkillPackage(t)
	objects, err := skills.NewLocalPackageObjectStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	builtins, err := skills.BuiltinRegistry()
	if err != nil {
		t.Fatal(err)
	}
	_, scheduledCreated, err := application.NewRunService(fixture.store).Create(t.Context(),
		application.CreateRunRequest{Goal: "OpenAPI scheduled observation target", Profile: "code",
			Surface: "code", Phase: "plan", WorkspaceID: fixture.workspace.ID,
			Budget: domain.Budget{MaxTurns: 2}})
	if err != nil {
		t.Fatal(err)
	}
	scheduledJobs := application.NewScheduledJobService(fixture.store)
	scheduledAnchor := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	scheduledSeed, err := scheduledJobs.Create(t.Context(), application.CreateScheduledJobRequest{
		Version: domain.ScheduledJobProtocolVersion, RunID: scheduledCreated.ID,
		TargetRunID: scheduledCreated.ID,
		Schedule: domain.ScheduledJobSchedule{Kind: domain.ScheduledJobOnce, Timezone: "UTC",
			AnchorAt: scheduledAnchor, MisfirePolicy: domain.ScheduledJobMisfireRunOnce},
		DeadlineAt: scheduledAnchor.Add(time.Hour), StopOnTargetTerminal: true,
		MaxRounds: 1, MaxModelCalls: 0, MaxElapsedSeconds: 3600,
		Retry: domain.ScheduledJobRetryPolicy{MaxAttempts: 2,
			InitialBackoffSeconds: 1, MaxBackoffSeconds: 10},
		Notification: domain.ScheduledJobNotifyAll, ExecutionMode: domain.ScheduledJobReadOnly,
		OperationKey: "openapi-scheduled-seed-0001", RequestedBy: "openapi_test",
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.api.runLifecycleEnabled = true
	fixture.api.runExecutionEnabled = true
	fixture.api.planDeliveryControlEnabled = true
	fixture.api.approvalControlEnabled = true
	fixture.api.controlledCommandProposalControlEnabled = true
	fixture.api.hostCommandProposalControlEnabled = true
	fixture.api.modelControlEnabled = true
	fixture.api.providerCredentialEnabled = true
	fixture.api.fileEditReviewEnabled = true
	fixture.api.fileEditProposalEnabled = true
	fixture.api.runWakeControlEnabled = true
	fixture.api.fileEditApplyEnabled = true
	fixture.api.runWakeExecutionEnabled = true
	fixture.api.scheduledJobControlEnabled = true
	fixture.api.skillInstallationEnabled = true
	fixture.api.evidenceAttachmentEnabled = true
	fixture.api.verificationEvidenceEnabled = true
	fixture.api.embeddedAnalyzerExecutionEnabled = true
	fixture.api.extensionControlEnabled = true
	fixture.api.extensionController = &extensionControllerStub{}
	fixture.api.dockerSandboxControlEnabled = true
	fixture.api.dockerSandboxController = &dockerSandboxControllerStub{}
	fixture.api.runLifecycleController = application.NewRunLifecycleControlService(fixture.store)
	executionController := application.NewRunExecutionHandoffService(
		fixture.store, llm.NewDefaultRouter(), policy.NewDefaultChecker())
	fixture.api.runExecutionController = executionController
	now := time.Now().UTC()
	fixture.api.publicModelStreamSource = staticPublicModelStreamSource{found: true,
		snapshot: application.PublicModelStreamSnapshot{
			Version: application.PublicModelStreamVersion,
			Call: application.ActiveCallInfo{
				RunID: fixture.run.ID, SessionID: fixture.run.SessionID,
				AttemptID: "openapi-public-stream-attempt", ModelAttempt: 1,
				TransportAttempt: 1, MaxAttempts: 1, Provider: "mock", Model: "mock-code",
				StartedAt: now,
			},
			Revision: 1, ContentKind: application.PublicModelStreamRootMessage,
			Text: "OpenAPI public model stream", Provisional: true,
			UpdatedAt: now,
		}}
	fixture.api.planDeliveryController = application.NewPlanDeliveryControlService(fixture.store)
	fixture.api.approvalController = application.NewApprovalControlService(fixture.store,
		gateway, checker)
	fixture.api.controlledCommandProposalController =
		&controlledCommandProposalControllerStub{
			view: application.ControlledCommandProposalView{
				Proposal: runner.ControlledCommandProposal{
					ID:                  "controlled-command-proposal-openapi",
					ProtocolVersion:     runner.ControlledCommandProposalProtocolVersion,
					PolicyVersion:       runner.ControlledCommandProposalPolicyVersion,
					RunID:               fixture.run.ID,
					MissionID:           fixture.run.MissionID,
					SessionID:           fixture.run.SessionID,
					WorkspaceID:         fixture.workspace.ID,
					Kind:                runner.ControlledCommandGitStatus,
					TimeoutMilliseconds: 5000,
					Purpose:             "inspect OpenAPI Git state",
					PermissionMode:      domain.RunExecutionPermissionConservative,
					PermissionRevision:  1,
					Fingerprint:         strings.Repeat("a", 64),
					CreatedAt:           time.Now().UTC(),
				},
			},
		}
	hostView := testHostCommandProposalView(t, fixture.run.ID, fixture.run.MissionID,
		fixture.run.SessionID, fixture.workspace.ID)
	hostView.Proposal.ID = "controlled-command-proposal-openapi"
	fixture.api.hostCommandProposalController = &hostCommandProposalControllerStub{view: hostView}
	fixture.api.modelControlController = application.NewModelControlService(
		fixture.api.modelRegistry, fixture.store)
	fixture.api.priceSnapshotController = fixture.store
	fixture.api.fanoutExecutionController = application.NewReadOnlyFanoutExecutionService(
		fixture.store, llm.NewDefaultRouter(), policy.NewDefaultChecker())
	fixture.api.childTaskControlController = application.NewChildTaskControlService(fixture.store)
	fixture.api.batchDeliveryControlEnabled = true
	fixture.api.batchDeliveryController = application.NewBatchDeliveryService(fixture.store)
	credentialStore := credential.NewMemoryStore()
	fixture.api.providerCredentialController = application.NewProviderCredentialService(
		credentialStore)
	fixture.api.fileEditReviewController = application.NewFileEditReviewService(fixture.store)
	fileEditProposalController := application.NewFileEditProposalService(fixture.store,
		checker)
	fixture.api.fileEditProposalController = fileEditProposalController
	fixture.api.runWakeController = application.NewRunWakeControlService(fixture.store)
	fixture.api.fileEditApplyController = application.NewFileEditApplyService(fixture.store, checker)
	fixture.api.runWakeExecutionController = application.NewForegroundRunWakeConsumer(
		fixture.store, executionController)
	fixture.api.scheduledJobController = scheduledJobs
	fixture.api.embeddedAnalyzerExecutionController =
		application.NewEmbeddedAnalyzerExecutionService(fixture.store)
	fixture.api.workspaceCheckpointControlEnabled = true
	fixture.api.workspaceCheckpointController = &workspaceCheckpointControllerStub{}
	uiAttemptID := "ui-attempt-openapi-0001"
	uiArtifact, err := uievidence.SealArtifact(uievidence.ArtifactMetadata{
		ID: fixture.artifactID, AttemptID: uiAttemptID, RunID: fixture.run.ID,
		StepID: "navigate", Kind: uievidence.ArtifactDOM, MIME: "application/octet-stream",
		Viewport:     uievidence.Viewport{Width: 1440, Height: 900, DPR: 1},
		SourceCommit: "non-git", RetentionPolicy: uievidence.ArtifactRetentionRunHistory,
		Untrusted: true, CreatedAt: time.Now().UTC(),
	}, []byte("evidence"))
	if err != nil {
		t.Fatal(err)
	}
	fixture.api.uiEvidenceControlEnabled = true
	fixture.api.uiEvidenceController = &uiEvidenceControllerStub{
		attempt: uievidence.Attempt{ProtocolVersion: uievidence.AttemptProtocolVersion,
			Manifest: uievidence.Manifest{AttemptID: uiAttemptID, RunID: fixture.run.ID},
			Status:   uievidence.StatusNotRun, FailureStage: uievidence.FailureNone},
		bundle: application.UIEvidenceBundle{Steps: []uievidence.StepReceipt{},
			Artifacts: []uievidence.ArtifactMetadata{}},
		artifact: uiArtifact,
	}
	uiStub := fixture.api.uiEvidenceController.(*uiEvidenceControllerStub)
	uiStub.bundle.Attempt = uiStub.attempt
	fixture.api.skillInstallationController = application.NewSkillPackageRegistryService(
		fixture.store, objects, builtins)
	steering, err := fixture.store.EnqueueOperatorSteering(t.Context(),
		domain.EnqueueOperatorSteeringRequest{
			RunID: fixture.run.ID, SessionID: fixture.run.SessionID,
			Content:      "OpenAPI cancellation target",
			OperationKey: "openapi-cancellation-target-0001",
			RequestedBy:  "openapi_test",
		})
	if err != nil {
		t.Fatal(err)
	}
	evidenceContent := "OpenAPI evidence\n"
	if err := os.WriteFile(filepath.Join(fixture.workspace.RootPath, "README.md"),
		[]byte(evidenceContent), 0o644); err != nil {
		t.Fatal(err)
	}
	evidenceDigest := sha256.Sum256([]byte(evidenceContent))
	proposalSource, err := fileEditProposalController.IssueSource(t.Context(),
		fixture.run.ID, "README.md")
	if err != nil {
		t.Fatal(err)
	}
	recoverySource, err := fileEditProposalController.IssueSource(t.Context(),
		fixture.run.ID, "README.md")
	if err != nil {
		t.Fatal(err)
	}
	recoveryProposal, err := fileEditProposalController.Propose(t.Context(),
		application.CreateFileEditProposalRequest{
			Version: application.FileEditProposalProtocolVersion, RunID: fixture.run.ID,
			SourceHandle: recoverySource.Handle, ProposedText: "OpenAPI recovery proposal\n",
		})
	if err != nil {
		t.Fatal(err)
	}
	verificationPlan, err := application.NewVerificationPlanService(fixture.store).Record(
		t.Context(), application.RecordVerificationPlanRequest{
			Version: verification.PlanProtocolVersion, RunID: fixture.run.ID,
			Title: "OpenAPI association plan", Summary: "Live route metadata",
			Items: []application.VerificationPlanItemRequest{{Title: "Live association",
				ExpectedObservation: "Observe an explicit operator result"}},
			OperationKey: "openapi-association-plan-operation-0001", AuthoredBy: "operator",
		})
	if err != nil {
		t.Fatal(err)
	}
	verificationEvidence, err := application.NewVerificationEvidenceService(fixture.store).Record(
		t.Context(), application.RecordVerificationEvidenceRequest{
			Version: verification.EvidenceProtocolVersion, RunID: fixture.run.ID,
			Outcome: string(verification.OutcomePass), Title: "OpenAPI association evidence",
			Summary:      "Explicit live route observation",
			OperationKey: "openapi-association-evidence-operation-0001", RecordedBy: "operator",
		})
	if err != nil {
		t.Fatal(err)
	}
	openAPISnapshot, err := application.NewVerificationSnapshotExportService(fixture.store).Build(
		t.Context(), fixture.run.ID, verificationPlan.Plan.ID, 1,
		application.VerificationSnapshotExportFormatJSON)
	if err != nil {
		t.Fatal(err)
	}
	openAPIReceipt, err := application.NewVerificationSnapshotReceiptService(fixture.store).Record(
		t.Context(), application.RecordVerificationSnapshotReceiptRequest{
			Version: verification.SnapshotReceiptProtocolVersion, RunID: fixture.run.ID,
			PlanID: verificationPlan.Plan.ID, PlanItemOrdinal: 1,
			Format:                         openAPISnapshot.Format,
			SnapshotHighWaterEventSequence: openAPISnapshot.SnapshotHighWaterEventSequence,
			ContentSHA256:                  openAPISnapshot.ContentSHA256, ConfirmMetadataSnapshot: true,
			OperationKey: "openapi-snapshot-review-receipt-0001", RecordedBy: "operator",
		})
	if err != nil {
		t.Fatal(err)
	}
	openAPIMemory, err := application.NewContextMemoryService(fixture.store).Create(t.Context(),
		contextmgr.CreateMemoryRequest{Scope: contextmgr.MemoryScopeUser,
			ScopeID: contextmgr.LocalUserMemoryScope, Title: "OpenAPI memory fixture",
			Content: "Preserve the public contract.", RequestedBy: "openapi_test"})
	if err != nil {
		t.Fatal(err)
	}
	openAPICheckpoint, err := application.NewContextContinuityService(fixture.store).Checkpoint(
		t.Context(), application.CreateContinuityCheckpointRequest{RunID: fixture.run.ID,
			Title: "OpenAPI checkpoint fixture", RequestedBy: "openapi_test"})
	if err != nil {
		t.Fatal(err)
	}
	_, openAPIThreadRun, err := application.NewRunService(fixture.store).Create(t.Context(),
		application.CreateRunRequest{Goal: "OpenAPI Thread fixture", Profile: "review",
			WorkspaceID: fixture.workspace.ID, Budget: domain.Budget{MaxTurns: 2}})
	if err != nil {
		t.Fatal(err)
	}
	openAPIThread, err := fixture.store.GetThreadByRun(t.Context(), openAPIThreadRun.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, deleteThreadRun, err := application.NewRunService(fixture.store).Create(t.Context(),
		application.CreateRunRequest{Goal: "OpenAPI delete Thread fixture", Profile: "review",
			WorkspaceID: fixture.workspace.ID, Budget: domain.Budget{MaxTurns: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.NewRunService(fixture.store).Cancel(t.Context(),
		deleteThreadRun.ID); err != nil {
		t.Fatal(err)
	}
	deleteThread, err := fixture.store.GetThreadByRun(t.Context(), deleteThreadRun.ID)
	if err != nil {
		t.Fatal(err)
	}
	standardCodeController := &standardCodePresetControllerStub{}
	standardCodeAPI, err := New(fixture.store, Config{
		AccessToken: testAccessToken, ControlToken: testControlToken,
		RunControlEnabled: true, ExecutionPermissionControlEnabled: true,
		ExecutionPermissionCapabilities: domain.ExecutionPermissionRuntimeCapabilities{
			WorkspaceSandboxEnabled: true,
		},
		StandardCodePresetEnabled:    true,
		StandardCodePresetController: standardCodeController,
		AppVersion:                   "openapi-standard-code-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	replacements := map[string]string{
		"{thread_id}":         openAPIThread.ID,
		"{run_id}":            fixture.run.ID,
		"{workspace_id}":      fixture.workspace.ID,
		"{agent_id}":          child.ID,
		"{session_id}":        fixture.run.SessionID,
		"{message_id}":        steering.Message.ID,
		"{work_item_id}":      fixture.workItems[0].ID,
		"{note_id}":           fixture.notes[0].ID,
		"{artifact_id}":       fixture.artifactID,
		"{report_id}":         "report-openapi-missing-0001",
		"{execution_id}":      "fanout-execution-openapi-missing-0001",
		"{approval_id}":       approvalRecord.ID,
		"{proposal_id}":       "controlled-command-proposal-openapi",
		"{batch_delivery_id}": "batch-openapi-missing-0001",
		"{connection_id}":     "github-connection-openapi",
		"{attempt_id}":        uiAttemptID,
		"{edit_id}":           fileEditRecord.ID,
		"{job_id}":            scheduledSeed.Job.ID,
		"{action}":            string(domain.ScheduledJobPause),
		"{object_id}":         strings.Repeat("a", 40),
		"{plan_id}":           verificationPlan.Plan.ID,
		"{ordinal}":           "1",
		"{route}":             "code",
		"{provider}":          "mimo",
		"{server_id}":         "mcp-openapi",
		"{installation_id}":   "plugin-openapi",
		"{memory_id}":         openAPIMemory.ID,
		"{node_id}":           openAPICheckpoint.ID,
	}
	for _, spec := range openAPIOperationSpecs() {
		requestPath := spec.Path
		for placeholder, value := range replacements {
			requestPath = strings.ReplaceAll(requestPath, placeholder, value)
		}
		if spec.Path == SpecialistModelCancellationPathTemplate {
			requestPath = strings.ReplaceAll(spec.Path, "{run_id}", childRun.ID)
			requestPath = strings.ReplaceAll(requestPath, "{agent_id}", child.ID)
		} else if spec.Path == RunExecutionProfileControlPathTemplate {
			requestPath = strings.ReplaceAll(spec.Path, "{run_id}", profileRun.ID)
		} else if spec.Path == RunExecutionPermissionControlPathTemplate {
			requestPath = strings.ReplaceAll(spec.Path, "{run_id}", profileRun.ID)
		} else if spec.Path == RunBrowserCDPPermissionControlPathTemplate {
			requestPath = strings.ReplaceAll(spec.Path, "{run_id}", profileRun.ID)
		} else if spec.Path == RunExecutionInteractionControlPathTemplate {
			requestPath = strings.ReplaceAll(spec.Path, "{run_id}", interactionRun.ID)
		} else if spec.Path == RunLifecycleControlPathTemplate {
			requestPath = strings.ReplaceAll(spec.Path, "{run_id}", lifecycleRun.ID)
		} else if spec.Path == PlanModeControlPathTemplate {
			requestPath = strings.ReplaceAll(spec.Path, "{run_id}", planModeRun.ID)
		} else if spec.Path == PlanDirectionControlPathTemplate ||
			spec.Path == PlanDeliveryControlPathTemplate {
			requestPath = strings.ReplaceAll(spec.Path, "{run_id}", planRun.ID)
		} else if spec.Path == RunExecutionControlPathTemplate {
			requestPath = strings.ReplaceAll(spec.Path, "{run_id}", executionRun.ID)
		} else if spec.Path == RunWakeIntentPathTemplate ||
			spec.Path == RunWakeCancellationPathTemplate {
			requestPath = strings.ReplaceAll(spec.Path, "{run_id}", wakeRun.ID)
		} else if spec.Path == RunWakeExecutionPathTemplate {
			requestPath = strings.ReplaceAll(spec.Path, "{run_id}", wakeExecutionRun.ID)
		} else if spec.Path == RunScheduledJobsPathTemplate ||
			spec.Path == ScheduledJobActionPathTemplate {
			requestPath = strings.ReplaceAll(requestPath, fixture.run.ID, scheduledCreated.ID)
		} else if spec.Path == FileEditProposalRecoveryPathTemplate {
			requestPath = strings.ReplaceAll(spec.Path, "{run_id}", fixture.run.ID)
			requestPath = strings.ReplaceAll(requestPath, "{edit_id}", recoveryProposal.Edit.ID)
		} else if spec.OperationID == "deleteThread" {
			requestPath = strings.ReplaceAll(spec.Path, "{thread_id}", deleteThread.ID)
		}
		if spec.OperationID == "searchWorkspace" {
			requestPath += "?query=README"
		} else if spec.OperationID == "getWorkspaceRepositoryFileHistory" {
			requestPath += "?path=README.md"
		} else if spec.OperationID == "getWorkspaceRepositoryCommitFilePreview" {
			requestPath += "?path=README.md"
		} else if spec.OperationID == "compareWorkspaceRepositoryCommits" {
			requestPath += "?base_object_id=" + strings.Repeat("a", 40) +
				"&head_object_id=" + strings.Repeat("b", 40)
		} else if spec.OperationID == "issueFileEditProposalSource" {
			requestPath += "?path=README.md"
		} else if spec.OperationID == "exportCodeHandoff" {
			requestPath += "?format=markdown"
		} else if spec.OperationID == "exportRunVerificationPlanItemSnapshot" {
			requestPath += "?format=json"
		} else if spec.OperationID == "getDockerSandboxStatus" {
			requestPath += "?admission_id=docker-sandbox-admission-openapi"
		} else if spec.OperationID == "listRunFanoutExecutions" {
			requestPath += "?plan_id=plan-openapi-fanout"
		} else if spec.OperationID == "queryDebugTimeline" ||
			spec.OperationID == "exportDiagnosticBundle" {
			requestPath += "?run_id=" + fixture.run.ID
		} else if spec.OperationID == "getGitHubReviewProjection" {
			requestPath += "?connection_id=github-connection-openapi"
		}
		t.Run(spec.OperationID, func(t *testing.T) {
			requestAPI := fixture.api
			if spec.Path == StandardCodePresetCreatePath ||
				spec.Path == StandardCodePresetRunPathTemplate ||
				spec.Path == StandardCodePauseAndConfigurePathTemplate {
				requestAPI = standardCodeAPI
			}
			var response *httptest.ResponseRecorder
			expectedStatus := http.StatusOK
			if spec.OperationID == "getRunFindingReport" {
				expectedStatus = http.StatusNotFound
			} else if spec.OperationID == "cancelRunFanoutExecution" ||
				spec.OperationID == "reviewRunChildTaskProposal" ||
				spec.OperationID == "admitRunChildTaskProposal" ||
				strings.Contains(spec.OperationID, "RunBatchDelivery") ||
				spec.OperationID == "prepareRunBatchDelivery" {
				expectedStatus = http.StatusNotFound
			} else if spec.OperationID == "getWorkspaceRepositoryCommitFilePreview" {
				expectedStatus = http.StatusPreconditionFailed
			}
			if spec.OperationID == "discoverGitAdvancedHunks" {
				body := `{"spec":{"protocol_version":"git-advanced.v1",` +
					`"operation":"hunk_stage","paths":["README.md"]}}`
				request := httptest.NewRequest(http.MethodPost,
					"http://127.0.0.1"+requestPath, strings.NewReader(body))
				request.Host = "127.0.0.1:8765"
				request.RemoteAddr = "127.0.0.1:45000"
				request.Header.Set("Authorization", "Bearer "+testAccessToken)
				request.Header.Set("Content-Type", "application/json")
				response = httptest.NewRecorder()
				fixture.api.ServeHTTP(response, request)
			} else if spec.Path == DockerSandboxReadinessPath {
				body, marshalErr := json.Marshal(DockerSandboxReadinessRequestView{
					PlanID: "sandbox-docker-plan-openapi", Manifest: dockerSandboxHTTPTestManifest(),
				})
				if marshalErr != nil {
					t.Fatal(marshalErr)
				}
				request := httptest.NewRequest(http.MethodPost,
					"http://127.0.0.1"+requestPath, bytes.NewReader(body))
				request.Host = "127.0.0.1:8765"
				request.RemoteAddr = "127.0.0.1:45000"
				request.Header.Set("Authorization", "Bearer "+testAccessToken)
				request.Header.Set("Content-Type", "application/json")
				response = httptest.NewRecorder()
				fixture.api.ServeHTTP(response, request)
			} else if spec.Control {
				body := `{"profile":"docker"}`
				if spec.OperationID == "createContextMemory" {
					body = `{"scope":"user","scope_id":"local-user",` +
						`"title":"OpenAPI created memory","content":"Explicit test memory."}`
				} else if spec.OperationID == "updateContextMemory" {
					body = `{"expected_version":1,"status":"disabled"}`
				} else if spec.OperationID == "deleteContextMemory" {
					body = `{"expected_version":2}`
				} else if spec.OperationID == "refreshRunProjectInstructions" {
					state, inspectErr := application.NewProjectInstructionService(fixture.store).
						Inspect(t.Context(), fixture.run.ID, "")
					if inspectErr != nil {
						t.Fatal(inspectErr)
					}
					encoded, marshalErr := json.Marshal(projectInstructionRefreshRequestView{
						ExpectedFingerprint:     state.Pinned.Snapshot.Fingerprint,
						ExpectedLiveFingerprint: state.Live.Fingerprint, Confirm: true,
					})
					if marshalErr != nil {
						t.Fatal(marshalErr)
					}
					body = string(encoded)
				} else if spec.OperationID == "reviewGitAdvancedOperation" {
					body = `{"operation_key":"openapi-git-advanced-review",` +
						`"scope":{"capability_generation":"` + strings.Repeat("a", 64) + `",` +
						`"lease_generation":1},"spec":{"protocol_version":"git-advanced.v1",` +
						`"operation":"stash_create","message":"OpenAPI preview"}}`
				} else if spec.OperationID == "executeGitAdvancedOperation" {
					body = `{"operation_id":"git-advanced-operation-openapi",` +
						`"approval_id":"approval-openapi","scope":{` +
						`"capability_generation":"` + strings.Repeat("a", 64) + `",` +
						`"lease_generation":1}}`
				} else if spec.OperationID == "configureGitHubReviewConnection" {
					body = `{"repository":{"host":"github.com","owner":"openai",` +
						`"name":"prayu","full_name":"openai/prayu","private":false},` +
						`"credential":{"name":"github-review-openapi",` +
						`"kind":"fine_grained_pat"},"allowed_log_hosts":[],` +
						`"write_enabled":false,"enabled":true,"expected_generation":0}`
				} else if spec.OperationID == "beginGitHubReviewDeviceFlow" ||
					spec.OperationID == "disconnectGitHubReviewCredential" {
					body = `{}`
				} else if spec.OperationID == "pollGitHubReviewDeviceFlow" {
					body = `{"session_id":"github-device-openapi"}`
				} else if spec.OperationID == "qualifyGitHubReviewConnection" ||
					spec.OperationID == "fetchGitHubReviewSnapshot" {
					body = `{"pull_request":118}`
				} else if spec.OperationID == "buildGitHubReviewEvidence" {
					body = `{"snapshot_id":"github-snapshot-openapi"}`
				} else if spec.OperationID == "reviewGitHubReviewWrite" {
					body = `{"connection_id":"github-connection-openapi",` +
						`"snapshot_id":"github-snapshot-openapi",` +
						`"operation_key":"github-write-openapi",` +
						`"spec":{"protocol_version":"github-review-write.v1",` +
						`"operation":"reply","identity":{"repository":{"host":"github.com",` +
						`"owner":"openai","name":"prayu","full_name":"openai/prayu",` +
						`"private":false},"number":118,"node_id":"PR_openapi",` +
						`"state":"open","draft":false,"merged":false,"fork":false,` +
						`"base_ref":"main","base_sha":"` + strings.Repeat("a", 40) + `",` +
						`"head_ref":"feature","head_sha":"` + strings.Repeat("b", 40) + `",` +
						`"merge_base_sha":"` + strings.Repeat("a", 40) + `",` +
						`"updated_at":"2026-08-20T00:00:00Z"},` +
						`"credential":{"name":"github-review-openapi",` +
						`"kind":"fine_grained_pat"},"capability_generation":"` +
						strings.Repeat("c", 64) + `","body":"review reply"}}`
				} else if spec.OperationID == "executeGitHubReviewWrite" {
					body = `{"operation_id":"github-write-openapi",` +
						`"approval_id":"github-approval-openapi"}`
				} else if spec.OperationID == "createContinuityCheckpoint" {
					body = `{"title":"OpenAPI live checkpoint"}`
				} else if spec.OperationID == "createWorkspaceCheckpoint" {
					body = `{"operation_key":"openapi-workspace-checkpoint-create-0001",` +
						`"title":"OpenAPI Workspace checkpoint"}`
				} else if spec.OperationID == "recordStandardCodeDelivery" {
					body = `{"operation_key":"openapi-standard-code-delivery-0001",` +
						`"declaration":"no_applicable_tests",` +
						`"verification_job_ids":[],"uncovered_items":[]}`
				} else if spec.OperationID == "startRunUIEvidence" {
					body = `{}`
				} else if spec.OperationID == "cancelUIEvidence" {
					body = `{"confirm":true}`
				} else if spec.OperationID == "previewWorkspaceRewind" {
					body = `{"target_checkpoint_id":"checkpoint-target",` +
						`"expected_current_checkpoint_id":"checkpoint-current"}`
				} else if spec.OperationID == "rewindWorkspaceCheckpoint" {
					body = `{"target_checkpoint_id":"checkpoint-target",` +
						`"expected_current_checkpoint_id":"checkpoint-current",` +
						`"operation_key":"openapi-workspace-rewind-0001","confirm":true}`
				} else if spec.OperationID == "undoWorkspaceMutation" ||
					spec.OperationID == "redoWorkspaceMutation" {
					body = `{"expected_current_checkpoint_id":"checkpoint-current",` +
						`"operation_key":"openapi-workspace-cursor-` + spec.OperationID + `",` +
						`"confirm":true}`
				} else if spec.OperationID == "forkWorkspaceCheckpoint" {
					body = `{"target_checkpoint_id":"checkpoint-target",` +
						`"expected_current_checkpoint_id":"checkpoint-current",` +
						`"operation_key":"openapi-workspace-fork-0001",` +
						`"workspace_name":"openapi-fork",` +
						`"branch":"codex/openapi-workspace-fork","confirm":true}`
				} else if spec.OperationID == "forkContinuityNode" ||
					spec.OperationID == "resumeContinuityNode" {
					body = `{"goal":"OpenAPI continuity branch"}`
				} else if spec.Path == DockerSandboxAdmissionPath {
					encoded, marshalErr := json.Marshal(DockerSandboxAdmissionRequestView{
						PlanID: "sandbox-docker-plan-openapi", Manifest: dockerSandboxHTTPTestManifest(),
						RequestedBy: "openapi_test",
					})
					if marshalErr != nil {
						t.Fatal(marshalErr)
					}
					body = string(encoded)
				} else if spec.Path == DockerSandboxStartPath ||
					spec.Path == DockerSandboxCancelPath {
					body = `{"admission_id":"docker-sandbox-admission-openapi",` +
						`"requested_by":"openapi_test"}`
				} else if spec.OperationID == "createThread" {
					body = `{"version":"thread_creation.v1","goal":"OpenAPI live Thread",` +
						`"workspace_id":"` + fixture.workspace.ID + `"}`
				} else if spec.OperationID == "submitThreadMessage" {
					body = `{"version":"thread_message_submission.v1",` +
						`"content":"OpenAPI live Thread message"}`
				} else if spec.OperationID == "archiveThread" ||
					spec.OperationID == "restoreThread" || spec.OperationID == "deleteThread" {
					threadID := openAPIThread.ID
					if spec.OperationID == "deleteThread" {
						threadID = deleteThread.ID
					}
					current, loadErr := fixture.store.GetThread(t.Context(), threadID)
					if loadErr != nil {
						t.Fatal(loadErr)
					}
					body = `{"version":"thread_lifecycle.v1","expected_version":` +
						fmt.Sprint(current.Version) + `}`
				} else if spec.Path == StandardCodePresetCreatePath {
					body = `{"version":"standard_code_preset.v1",` +
						`"workspace_id":"` + fixture.workspace.ID + `",` +
						`"goal":"OpenAPI Standard Code Run",` +
						`"backend_intent":"auto","confirm_workspace_trust":false}`
				} else if spec.Path == StandardCodePresetRunPathTemplate ||
					spec.Path == StandardCodePauseAndConfigurePathTemplate {
					body = `{"version":"standard_code_preset.v1",` +
						`"backend_intent":"auto","confirm_workspace_trust":false}`
				} else if spec.Path == RunCreationControlPath {
					body = `{"version":"run_creation.v1","goal":"OpenAPI live Run",` +
						`"workspace_id":"` + fixture.workspace.ID + `"}`
				} else if spec.Path == SessionMessageControlPathTemplate {
					body = `{"version":"session_message_submission.v1",` +
						`"content":"OpenAPI live Session message"}`
				} else if spec.Path == SessionArchiveControlPathTemplate {
					body = `{"version":"session_archive.v1","confirm":true}`
				} else if spec.Path == SessionSteeringCancellationPathTemplate {
					body = `{"version":"session_steering_cancellation.v1",` +
						`"reason":"OpenAPI live cancellation"}`
				} else if spec.Path == RunLifecycleControlPathTemplate {
					body = `{"version":"run_lifecycle_control.v1","action":"start"}`
				} else if spec.Path == PlanDirectionControlPathTemplate {
					body = `{"version":"plan_delivery_control.v1","proposal_id":"` +
						planProposal.ID + `","direction":1}`
				} else if spec.Path == PlanDeliveryControlPathTemplate {
					body = `{"version":"plan_delivery_control.v1"}`
				} else if spec.Path == ApprovalDecisionControlPathTemplate {
					body = `{"version":"approval_control.v1","action":"approve_once"}`
				} else if spec.Path == ControlledCommandProposalReviewPathTemplate {
					body = `{"version":"controlled_command_proposal_review.v1",` +
						`"decision":"deny"}`
				} else if spec.Path == HostCommandProposalReviewPathTemplate {
					body = `{"version":"host_command_review.v1","decision":"deny"}`
				} else if spec.Path == RunExecutionControlPathTemplate {
					body = `{"version":"run_execution_handoff.v1","max_steps":1}`
				} else if spec.Path == ModelRouteControlPathTemplate {
					body = `{"version":"model_route_control.v1","provider":"mock","model":"mock-code"}`
				} else if spec.Path == ProviderDiagnosticPath {
					body = `{"version":"provider_diagnostic.v1","provider":"mock","model":"mock-code","confirm_diagnostic":true}`
				} else if spec.Path == ModelHarnessQualificationPath {
					body = `{"version":"model_harness_qualification.v1","provider":"mock","model":"mock-code","confirm_qualification":true}`
				} else if spec.Path == FanoutExecutionCancelPathTemplate {
					body = `{"version":"readonly_fanout_cancel.v1","confirm_cancel":true}`
				} else if spec.Path == ChildTaskProposalReviewPathTemplate {
					body = `{"version":"child_task_review.v1","action":"deny","reviewer":"openapi_test","fanout_tier":"2","confirm_review":true}`
				} else if spec.Path == ChildTaskProposalAdmitPathTemplate {
					body = `{"version":"child_task_admit.v1","confirm_admit":true}`
				} else if spec.Path == BatchDeliveriesPathTemplate {
					body = `{"version":"batch_delivery_prepare.v1",` +
						`"proposal_id":"child-task-proposal-openapi-missing",` +
						`"spec":{"version":"batch-delivery.v1","tasks":[{` +
						`"ordinal":1,"ownership_hints":[{"path":"internal/openapi",` +
						`"kind":"directory"}],"dependency_ordinals":[],` +
						`"budget":{"turn_limit":1,"token_limit":1,"timeout_millis":1000},` +
						`"validations":[{"id":"diff","kind":"git_diff_check",` +
						`"scope":"."}],"expected_artifacts":[]}],` +
						`"contract":{"require_clean":true,` +
						`"require_independent_review":true,"require_all_validations":true,` +
						`"max_changed_files":8,"max_diff_bytes":65536}},"confirm":true}`
				} else if spec.Path == BatchDeliveryReviewPathTemplate {
					body = `{"version":"batch_delivery_review_control.v1",` +
						`"generation":1,"reviewer":"openapi-reviewer",` +
						`"verdict":"accepted","summary":"reviewed",` +
						`"full_diff_reviewed":true,"call_chain_reviewed":true,` +
						`"tests_reviewed":true}`
				} else if spec.Path == BatchDeliveryRenewPathTemplate {
					body = `{"version":"batch_delivery_renew_owner.v1",` +
						`"expected_generation":1,"retry":false,"confirm":true}`
				} else if spec.Path == BatchDeliveryMergePathTemplate {
					body = `{"version":"batch_delivery_merge.v1",` +
						`"ordered_ordinals":[1],"confirm_replay":false,"confirm":true}`
				} else if spec.Path == BatchDeliveryCancelPathTemplate {
					body = `{"version":"batch_delivery_cancel.v1",` +
						`"reason":"OpenAPI route probe","confirm":true}`
				} else if spec.Path == BatchDeliveryRecoverPathTemplate {
					body = `{"version":"batch_delivery_reconcile.v1","confirm":true}`
				} else if spec.Path == PriceSnapshotsPath {
					encoded, marshalErr := json.Marshal(PriceSnapshotImportRequestView{
						Version:  pricing.ProtocolVersion,
						Document: openAPIPriceSnapshotDocument(t),
					})
					if marshalErr != nil {
						t.Fatal(marshalErr)
					}
					body = string(encoded)
				} else if spec.Path == EmbeddedAnalyzerExecutionPathTemplate {
					body = `{"version":"` + application.EmbeddedAnalyzerExecutionProtocolVersion +
						`","text":"OpenAPI embedded analyzer fixture\\n","media_type":"text/plain",` +
						`"confirmation":"` + application.EmbeddedAnalyzerExecutionConfirmation + `"}`
				} else if spec.Path == ProviderCredentialPathTemplate {
					body = `{"version":"provider_credential.v1","action":"set",` +
						`"secret":"temporary-openapi-key","confirm":true}`
				} else if spec.Path == ExtensionMCPReviewPath {
					body = `{"version":"extension-control.v1","action":"disable",` +
						`"expected_descriptor_fingerprint":"` + strings.Repeat("a", 64) + `"}`
				} else if spec.Path == ExtensionMCPRefreshPath {
					body = `{"version":"extension-control.v1"}`
				} else if spec.Path == ExtensionPluginReviewPath {
					body = `{"version":"extension-control.v1","action":"disable",` +
						`"expected_package_fingerprint":"` + strings.Repeat("b", 64) + `",` +
						`"expected_generation":1,"confirm_untrusted":false}`
				} else if spec.Path == FileEditProposalPathTemplate {
					body = `{"version":"file_edit_proposal.v1","source_handle":"` +
						proposalSource.Handle + `","proposed_text":"OpenAPI proposal\\n"}`
				} else if spec.Path == FileEditReviewPathTemplate {
					body = `{"version":"file_edit_review.v1","action":"approve_intent"}`
				} else if spec.Path == FileEditApplyPathTemplate {
					body = `{"version":"file_edit_apply.v1"}`
				} else if spec.Path == RunWakeIntentPathTemplate {
					body = `{"version":"run_wake_control.v1","max_attempts":3,` +
						`"initial_delay_seconds":0,"base_backoff_seconds":5,` +
						`"max_backoff_seconds":60,"max_elapsed_seconds":300}`
				} else if spec.Path == RunWakeCancellationPathTemplate {
					body = `{"version":"run_wake_control.v1"}`
				} else if spec.Path == RunWakeExecutionPathTemplate {
					body = `{"version":"run_wake_consumer.v1","max_steps":1}`
				} else if spec.Path == RunScheduledJobsPathTemplate {
					body = `{"version":"scheduled-job.v1","schedule":{"kind":"once",` +
						`"timezone":"UTC","anchor_at":"` +
						scheduledAnchor.Add(time.Minute).Format(time.RFC3339) + `",` +
						`"misfire_policy":"run_once"},"deadline_at":"` +
						scheduledAnchor.Add(2*time.Hour).Format(time.RFC3339) + `",` +
						`"stop_on_target_terminal":true,"max_rounds":1,"max_model_calls":0,` +
						`"max_elapsed_seconds":3600,"retry":{"max_attempts":2,` +
						`"initial_backoff_seconds":1,"max_backoff_seconds":10},` +
						`"notification":"all","execution_mode":"read_only",` +
						`"confirm_repair":false}`
				} else if spec.Path == ScheduledJobActionPathTemplate {
					body = `{"version":"scheduled-job-control.v1","expected_revision":` +
						fmt.Sprint(scheduledSeed.Job.Revision) + `}`
				} else if spec.Path == SkillPackageInstallPath {
					body = `{"version":"skill_package_installation.v1","archive_base64":"` +
						base64.StdEncoding.EncodeToString(skillArchive) +
						`","surface":"code","confirm_untrusted":true}`
				} else if spec.Path == EvidenceAttachmentPathTemplate {
					body = `{"version":"session_evidence_attachment.v1",` +
						`"source_kind":"workspace_file","source_ref":"README.md",` +
						`"content_sha256":"` + hex.EncodeToString(evidenceDigest[:]) + `"}`
				} else if spec.Path == VerificationEvidencePathTemplate {
					body = `{"version":"operator_verification_evidence.v1",` +
						`"outcome":"pass","title":"OpenAPI verification",` +
						`"summary":"Live route verified"}`
				} else if spec.Path == VerificationPlanPathTemplate {
					body = `{"version":"operator_verification_plan.v1",` +
						`"title":"OpenAPI verification plan","summary":"Operator guidance",` +
						`"items":[{"title":"Live route",` +
						`"expected_observation":"Observe a successful response"}]}`
				} else if spec.Path == VerificationAssociationPathTemplate {
					body = `{"version":"operator_verification_plan_evidence_association.v1",` +
						`"plan_id":"` + verificationPlan.Plan.ID + `",` +
						`"plan_item_ordinal":1,"evidence_id":"` +
						verificationEvidence.Evidence.ID + `"}`
				} else if spec.Path == VerificationSnapshotReceiptPathTemplate {
					snapshot, err := application.NewVerificationSnapshotExportService(
						fixture.store).Build(t.Context(), fixture.run.ID,
						verificationPlan.Plan.ID, 1, application.VerificationSnapshotExportFormatJSON)
					if err != nil {
						t.Fatal(err)
					}
					body = `{"version":"operator_verification_plan_item_snapshot_receipt.v1",` +
						`"plan_id":"` + verificationPlan.Plan.ID + `",` +
						`"plan_item_ordinal":1,"format":"json",` +
						`"snapshot_high_water_event_sequence":` +
						fmt.Sprint(snapshot.SnapshotHighWaterEventSequence) + `,` +
						`"content_sha256":"` + snapshot.ContentSHA256 + `",` +
						`"confirm_metadata_snapshot":true}`
				} else if spec.Path == VerificationSnapshotReceiptReviewPathTemplate {
					body = `{"version":"operator_verification_plan_item_snapshot_receipt_review.v1",` +
						`"receipt_id":"` + openAPIReceipt.Receipt.ID + `",` +
						`"receipt_content_sha256":"` + openAPIReceipt.Receipt.ContentSHA256 + `",` +
						`"receipt_event_sequence":` +
						fmt.Sprint(openAPIReceipt.Receipt.EventSequence) + `,` +
						`"decision":"metadata_confirmed",` +
						`"confirm_non_authorizing_review":true}`
				} else if spec.Path == RunExecutionPermissionControlPathTemplate {
					body = `{"mode":"full_access","confirm_danger_full_access":true}`
				} else if spec.Path == RunBrowserCDPPermissionControlPathTemplate {
					body = `{"mode":"restricted"}`
				} else if spec.Path == RunExecutionInteractionControlPathTemplate {
					body = `{"mode":"controlled","trust":"trusted",` +
						`"confirm_workspace_trust":true}`
				} else if spec.Path == PlanModeControlPathTemplate {
					body = `{"version":"plan_delivery_control.v1"}`
				} else if spec.Path != RunExecutionProfileControlPathTemplate {
					attemptID := fixture.checkpoint.AttemptID
					modelAttempt := 1
					if spec.Path == SpecialistModelCancellationPathTemplate {
						attemptID = childAttempt.ID
						modelAttempt = childModel.Number
					}
					body = `{"attempt_id":"` + attemptID + `","model_attempt":` +
						fmt.Sprint(modelAttempt) + `}`
				}
				method := spec.Method
				if method == "" {
					method = http.MethodPost
				}
				response = performControlMethodPathRequest(t, requestAPI, method, requestPath,
					"openapi-live-operation-012345-"+spec.OperationID,
					strings.NewReader(body))
				status, statusErr := openAPISuccessStatus(spec)
				if statusErr != nil {
					t.Fatal(statusErr)
				}
				if spec.OperationID != "cancelRunFanoutExecution" &&
					spec.OperationID != "reviewRunChildTaskProposal" &&
					spec.OperationID != "admitRunChildTaskProposal" &&
					!strings.Contains(spec.OperationID, "RunBatchDelivery") &&
					spec.OperationID != "prepareRunBatchDelivery" {
					expectedStatus, statusErr = strconv.Atoi(status)
					if statusErr != nil {
						t.Fatal(statusErr)
					}
				}
			} else {
				response = fixture.get(t, requestPath)
			}
			if spec.OperationID == "getBrowserSafeWebReadiness" {
				// Browser availability is environment-dependent: a machine with an
				// accepted browser returns 200 with a fail-closed readiness receipt,
				// otherwise the route is live with 404 (no accepted browser).
				if response.Code != http.StatusOK && response.Code != http.StatusNotFound {
					t.Fatalf("documented route is not live: path=%s status=%d body=%s",
						requestPath, response.Code, response.Body.String())
				}
			} else if response.Code != expectedStatus {
				t.Fatalf("documented route is not live: path=%s status=%d body=%s",
					requestPath, response.Code, response.Body.String())
			}
			assertSecurityHeaders(t, response)
			contentType := response.Header().Get("Content-Type")
			if spec.Streaming {
				streamEvents := parseSSEEvents(t, response.Body.Bytes())
				if !strings.HasPrefix(contentType, "text/event-stream") || len(streamEvents) != 1 {
					t.Fatalf("SSE response is invalid: content-type=%q body=%s", contentType, response.Body.String())
				}
			} else if spec.RawDocument {
				if !strings.HasPrefix(contentType, openAPIContentType) ||
					!bytes.Contains(response.Body.Bytes(), []byte(`"openapi": "3.1.0"`)) {
					t.Fatalf("raw OpenAPI response is invalid: content-type=%q body=%s", contentType, response.Body.String())
				}
			} else if spec.RawArtifact {
				if !strings.HasPrefix(contentType, "application/octet-stream") ||
					response.Body.String() != "evidence" {
					t.Fatalf("raw UI evidence artifact is invalid: content-type=%q body=%q",
						contentType, response.Body.String())
				}
			} else if !strings.HasPrefix(contentType, "application/json") || !json.Valid(response.Body.Bytes()) {
				t.Fatalf("API envelope has wrong content type %q", contentType)
			}
		})
	}

	unauthorized := fixture.request(t, http.MethodGet, OpenAPIPath, "",
		"127.0.0.1:8765", "127.0.0.1:45000", nil)
	assertAPIError(t, unauthorized, http.StatusUnauthorized, "POLICY_DENIED")
	assertAPIError(t, fixture.get(t, OpenAPIPath+"?format=yaml"), http.StatusBadRequest, "INVALID_ARGUMENT")
}

func buildOpenAPISkillPackage(t *testing.T) []byte {
	t.Helper()
	content := []byte("# OpenAPI external review\n\nInspect workspace evidence only.\n")
	digest := sha256.Sum256(content)
	manifest := skills.Manifest{
		Protocol: skills.ProtocolVersion, Name: "openapi-external-review", Version: "1.0.0",
		Description: "OpenAPI inert Skill installation fixture.",
		Profiles:    []domain.Profile{domain.ProfileReview},
		ToolDependencies: []toolgateway.ToolName{
			toolgateway.ListWorkspaceTool, toolgateway.ReadFileTool,
		},
		ContentPath: skills.PackageContentPath, ContentSHA256: hex.EncodeToString(digest[:]),
		ContentBytes: len(content), ContentTokenUpperBound: skills.ContentTokenUpperBound(content),
	}
	manifestRaw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, entry := range []struct {
		name string
		data []byte
	}{{skills.PackageManifestPath, manifestRaw}, {skills.PackageContentPath, content}} {
		file, createErr := writer.CreateHeader(&zip.FileHeader{Name: entry.name, Method: zip.Deflate})
		if createErr != nil {
			t.Fatal(createErr)
		}
		if _, writeErr := file.Write(entry.data); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func prepareOpenAPIPlanControlTarget(t *testing.T,
	fixture *apiFixture,
) (domain.Run, domain.PlanDeliveryProposal) {
	t.Helper()
	ctx := t.Context()
	runs := application.NewRunService(fixture.store)
	_, run, err := runs.Create(ctx, application.CreateRunRequest{
		Goal: "OpenAPI Plan control target", Profile: "review", Phase: "plan",
		ModelRoute: "http-plan/model",
		Budget:     domain.Budget{MaxTurns: 4, MaxTokens: 1000, MaxToolCalls: 4},
	})
	if err != nil {
		t.Fatal(err)
	}
	run, err = runs.Start(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	provider := &httpPlanProvider{responses: []*llm.ChatResponse{
		{Provider: "http-plan", Model: "model",
			Usage: llm.Usage{InputTokens: 2, OutputTokens: 2, TotalTokens: 4},
			ToolCalls: []llm.ToolCall{{ID: "openapi-plan-control-call",
				Name: "plan_delivery_propose", Arguments: json.RawMessage(httpPlanDeliveryPayload)}}},
		{Text: httpRootWaitResponse(t), Provider: "http-plan", Model: "model",
			Usage: llm.Usage{InputTokens: 2, OutputTokens: 2, TotalTokens: 4}},
	}}
	router := llm.NewRouter(llm.ModelRef{Provider: provider.Name(), Model: "model"})
	router.RegisterProvider(provider)
	if _, err := application.NewRunSupervisor(fixture.store, router,
		policy.NewDefaultChecker()).Step(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	proposals, err := fixture.store.ListPlanDeliveryProposals(ctx, run.ID, 2)
	if err != nil || len(proposals) != 1 {
		t.Fatalf("OpenAPI Plan proposals=%#v err=%v", proposals, err)
	}
	run, err = fixture.store.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	return run, proposals[0]
}

func prepareOpenAPISpecialistCancellationTarget(t *testing.T,
	fixture *apiFixture,
) (domain.Run, domain.AgentNode, domain.AgentAttempt, llm.ModelAttempt) {
	t.Helper()
	ctx := t.Context()
	runs := application.NewRunService(fixture.store)
	_, run, err := runs.Create(ctx, application.CreateRunRequest{
		Goal: "OpenAPI Specialist cancellation target", Profile: "code",
		Budget: domain.Budget{MaxTurns: 4},
	})
	if err != nil {
		t.Fatal(err)
	}
	run, err = runs.Start(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	root, found, err := fixture.store.GetRootAgent(ctx, run.ID)
	if err != nil || !found {
		t.Fatalf("OpenAPI Specialist root missing: found=%t err=%v", found, err)
	}
	coord, err := coordinator.NewWithSpecialistAdmission(fixture.store,
		coordinator.SpecialistAdmissionPolicy{
			MaxChildren: 1, MaxTurnsPerChild: 2, MaxTokensPerChild: 32,
		})
	if err != nil {
		t.Fatal(err)
	}
	admitted, err := coord.AdmitSpecialist(ctx, coordinator.AdmitSpecialistRequest{
		RunID: run.ID, ParentAgentID: root.ID,
		Title: "OpenAPI cancellation target", Skills: []string{"model.chat"},
		TurnLimit: 2, TokenLimit: 32,
		IdempotencyKey: "openapi-specialist-admission-012345",
	})
	if err != nil {
		t.Fatal(err)
	}
	acquired, err := fixture.store.AcquireRunExecutionLease(ctx,
		domain.AcquireRunExecutionLeaseRequest{
			RunID: run.ID, OwnerID: "openapi-specialist-worker", TTL: time.Minute,
		})
	if err != nil {
		t.Fatal(err)
	}
	attemptID := "attempt-openapi-specialist-0001"
	attempt, _, err := fixture.store.BeginSpecialistAttempt(ctx, domain.AgentAttemptStart{
		AttemptID: attemptID, RunID: run.ID, AgentID: admitted.Agent.ID,
		ParentAgentID: root.ID, Lease: acquired.Lease, StartedAt: time.Now().UTC(),
	}, "openapi-specialist-start-012345")
	if err != nil {
		t.Fatal(err)
	}
	modelAttempt := llm.ModelAttempt{
		Number: 1, TransportAttempt: 1, MaxAttempts: 3,
		Provider: "openapi-specialist", Model: "test-model",
	}
	if inserted, err := fixture.store.RecordSpecialistModelStarted(ctx,
		domain.AgentAttemptRef{RunID: attempt.RunID, AgentID: attempt.AgentID,
			AttemptID: attempt.ID}, modelAttempt); err != nil || !inserted {
		t.Fatalf("OpenAPI Specialist model start inserted=%t err=%v", inserted, err)
	}
	return run, admitted.Agent, attempt, modelAttempt
}

// openAPIPriceSnapshotDocument builds one minimal valid operator price
// snapshot wire document whose validity window includes the current time.
func openAPIPriceSnapshotDocument(t *testing.T) string {
	t.Helper()
	now := time.Now().UTC()
	wire := pricing.Wire{
		ProtocolVersion: pricing.ProtocolVersion, ID: "openapi-live-price-table",
		Source: pricing.SourceOperatorImport, Currency: pricing.CurrencyUSD,
		ImportedBy: "openapi_test",
		ValidFrom:  now.Add(-time.Minute).Format(time.RFC3339),
		ValidUntil: now.Add(time.Hour).Format(time.RFC3339),
		Entries: []struct {
			Provider                  string `json:"provider"`
			Model                     string `json:"model"`
			InputPerMillionMicros     int64  `json:"input_per_million_micros"`
			OutputPerMillionMicros    int64  `json:"output_per_million_micros"`
			CacheReadPerMillionMicros int64  `json:"cache_read_per_million_micros"`
			ToolCallMicros            int64  `json:"tool_call_micros"`
		}{{Provider: "mock", Model: "mock-code", InputPerMillionMicros: 1000000,
			OutputPerMillionMicros: 2000000, CacheReadPerMillionMicros: 250000,
			ToolCallMicros: 50000}},
	}
	raw, err := json.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func assertOpenAPISchemaOmits(t *testing.T, schemas map[string]map[string]any, name string, property string) {
	t.Helper()
	schema, ok := schemas[name]
	if !ok {
		t.Fatalf("OpenAPI component %s is missing", name)
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("OpenAPI component %s has no properties", name)
	}
	if _, exposed := properties[property]; exposed {
		t.Fatalf("OpenAPI component %s exposed forbidden property %s", name, property)
	}
}

func assertOpenAPISchemaOptional(t *testing.T, schemas map[string]map[string]any,
	name string, property string,
) {
	t.Helper()
	schema, ok := schemas[name]
	if !ok {
		t.Fatalf("OpenAPI component %s is missing", name)
	}
	required, _ := schema["required"].([]any)
	for _, current := range required {
		if current == property {
			t.Fatalf("OpenAPI component %s unexpectedly requires %s", name, property)
		}
	}
}

func assertOpenAPIPropertyFlag(t *testing.T, schemas map[string]map[string]any,
	name string, property string, flag string, expected any,
) {
	t.Helper()
	schema, ok := schemas[name]
	if !ok {
		t.Fatalf("OpenAPI component %s is missing", name)
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("OpenAPI component %s has no properties", name)
	}
	value, ok := properties[property].(map[string]any)
	if !ok || !reflect.DeepEqual(value[flag], expected) {
		t.Fatalf("OpenAPI component %s property %s flag %s=%v want=%v",
			name, property, flag, value[flag], expected)
	}
}

func assertOpenAPIEnum(t *testing.T, schemas map[string]map[string]any,
	name string, property string, expected []string,
) {
	t.Helper()
	schema, ok := schemas[name]
	if !ok {
		t.Fatalf("OpenAPI component %s is missing", name)
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("OpenAPI component %s has no properties", name)
	}
	value, ok := properties[property].(map[string]any)
	if !ok {
		t.Fatalf("OpenAPI component %s property %s is missing", name, property)
	}
	raw, ok := value["enum"].([]any)
	if !ok {
		t.Fatalf("OpenAPI component %s property %s has no enum", name, property)
	}
	actual := make([]string, len(raw))
	for index, current := range raw {
		actual[index], ok = current.(string)
		if !ok {
			t.Fatalf("OpenAPI component %s property %s has a non-string enum", name, property)
		}
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("OpenAPI component %s property %s enum=%v want=%v",
			name, property, actual, expected)
	}
}
