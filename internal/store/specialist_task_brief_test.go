package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/idgen"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/policy"
)

func sendBriefOperation(t *testing.T, st *SQLiteStore, f specialistAttemptFixture, op, text string, target domain.AgentMessage) domain.AgentMessage {
	t.Helper()
	p := domain.AgentInstructionPayload{Version: domain.SpecialistInstructionOperationVersion, Operation: op, Instruction: text}
	if target.ID != "" {
		p.TargetMessageID = target.ID
		p.TargetPayloadSHA256 = domain.SpecialistInstructionPayloadSHA256(target.PayloadJSON)
	}
	raw, _ := json.Marshal(p)
	message, _, err := st.SendAgentMessage(t.Context(), domain.AgentMessage{ID: idgen.New("agentmsg"), RunID: f.Run.ID,
		SenderAgentID: f.Root.ID, RecipientAgentID: f.Child.ID, Kind: domain.AgentMessageInstruction,
		Semantic: domain.AgentMessageSemanticMessage, PayloadJSON: string(raw)}, idgen.New("brief-op"))
	if err != nil {
		t.Fatal(err)
	}
	return message
}

func continueBriefAttempt(t *testing.T, st *SQLiteStore, a domain.AgentAttempt) {
	t.Helper()
	if _, _, err := st.RecordSpecialistAttemptUsage(t.Context(), attemptRef(a), domain.AgentAttemptUsage{InputTokens: 1, OutputTokens: 1, TotalTokens: 2}, idgen.New("brief-usage")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.ContinueSpecialistAttempt(t.Context(), attemptRef(a), idgen.New("brief-continue")); err != nil {
		t.Fatal(err)
	}
}

func TestSpecialistTaskBriefPinsEmptyAndMutableWorkAcrossReopenAndAttempts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "brief.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	f := prepareSpecialistAttemptFixture(t, t.Context(), st, "pinned child constraints", 4, 256)
	a, _, err := st.BeginSpecialistAttempt(t.Context(), newAttemptStart(f, idgen.New("attempt")), idgen.New("brief-start"))
	if err != nil {
		t.Fatal(err)
	}
	empty, err := st.PrepareSpecialistContext(t.Context(), attemptRef(a))
	if err != nil {
		t.Fatal(err)
	}
	instruction := sendSpecialistInstructionTestMessage(t, t.Context(), st, f, "late instruction starts next attempt", idgen.New("brief-send"))
	work, err := application.NewWorkItemService(st).Create(t.Context(), application.CreateWorkItemRequest{RunID: f.Run.ID, OwnerAgentID: f.Child.ID, Title: "unfinished scoped analysis", Description: "version one must remain pinned"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	same, err := st.PrepareSpecialistContext(t.Context(), attemptRef(a))
	if err != nil {
		t.Fatal(err)
	}
	if !same.Recovered || !reflect.DeepEqual(same.TaskBrief, empty.TaskBrief) || len(same.Messages) != 0 {
		t.Fatalf("empty preparation refreshed silently: %#v", same)
	}
	continueBriefAttempt(t, st, a)
	next, _, err := st.BeginSpecialistAttempt(t.Context(), newAttemptStart(f, idgen.New("attempt")), idgen.New("brief-start"))
	if err != nil {
		t.Fatal(err)
	}
	pinned, err := st.PrepareSpecialistContext(t.Context(), attemptRef(next))
	if err != nil {
		t.Fatal(err)
	}
	if len(pinned.Messages) != 1 || pinned.Messages[0].ID != instruction.ID || len(pinned.TaskBrief.WorkItems) != 1 || pinned.TaskBrief.WorkItems[0].Version != 1 {
		t.Fatalf("next attempt missed new state: %#v", pinned)
	}
	changed := "version two applies to the following attempt"
	if _, err := application.NewWorkItemService(st).Update(t.Context(), application.UpdateWorkItemRequest{ID: work.ID, ExpectedVersion: 1, Description: &changed}); err != nil {
		t.Fatal(err)
	}
	same, err = st.PrepareSpecialistContext(t.Context(), attemptRef(next))
	if err != nil {
		t.Fatal(err)
	}
	if !same.Recovered || !reflect.DeepEqual(same.TaskBrief, pinned.TaskBrief) {
		t.Fatal("mutable WorkItem rewrote pinned snapshot")
	}
	continueBriefAttempt(t, st, next)
	third, _, err := st.BeginSpecialistAttempt(t.Context(), newAttemptStart(f, idgen.New("attempt")), idgen.New("brief-start"))
	if err != nil {
		t.Fatal(err)
	}
	refreshed, err := st.PrepareSpecialistContext(t.Context(), attemptRef(third))
	if err != nil {
		t.Fatal(err)
	}
	if len(refreshed.Messages) != 0 || len(refreshed.TaskBrief.Instructions) != 1 || refreshed.TaskBrief.WorkItems[0].Version != 2 || refreshed.TaskBrief.WorkItems[0].Description != changed {
		t.Fatalf("fresh attempt failed to refresh task truth: %#v", refreshed)
	}
	if refreshed.AgentAttemptID == pinned.AgentAttemptID {
		t.Fatal("reused old attempt receipt")
	}
	committed, err := listSpecialistContextDeliveriesDB(t.Context(), st, f.Run.ID, domain.RootInboxDeliveryCommitted)
	if err != nil || len(committed) != 1 || committed[0].AgentAttemptID != next.ID {
		t.Fatalf("re-presented consumed instruction generated another consume: %#v %v", committed, err)
	}
}

func TestSpecialistTaskBriefExplicitRetirementAndSourceRetention(t *testing.T) {
	st := openWorkItemTestStore(t)
	f := prepareSpecialistAttemptFixture(t, t.Context(), st, "effective corrections", 5, 256)
	original := sendSpecialistInstructionTestMessage(t, t.Context(), st, f, "original retired scope", idgen.New("brief-send"))
	correction := sendBriefOperation(t, st, f, "append", "retain narrow interface constraint", domain.AgentMessage{})
	replacement := sendBriefOperation(t, st, f, "replace", "replacement current scope", original)
	withdrawal := sendBriefOperation(t, st, f, "withdraw", "", correction)
	a, _, err := st.BeginSpecialistAttempt(t.Context(), newAttemptStart(f, idgen.New("attempt")), idgen.New("brief-start"))
	if err != nil {
		t.Fatal(err)
	}
	batch, err := st.PrepareSpecialistContext(t.Context(), attemptRef(a))
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.TaskBrief.Instructions) != 1 || batch.TaskBrief.Instructions[0].SourceID != replacement.ID || batch.TaskBrief.Instructions[0].Instruction != "replacement current scope" || len(batch.TaskBrief.Sources) != 4 {
		t.Fatalf("retirement reduction wrong: %#v", batch.TaskBrief)
	}
	continueBriefAttempt(t, st, a)
	for _, message := range []domain.AgentMessage{original, correction, replacement, withdrawal} {
		if _, err := st.db.Exec(`UPDATE agent_messages SET payload_json='{}' WHERE id=?`, message.ID); err == nil {
			t.Fatal("source content could be changed")
		}
		if _, err := st.db.Exec(`DELETE FROM agent_messages WHERE id=?`, message.ID); err == nil {
			t.Fatal("retirement source could be deleted")
		}
	}
	if _, err := st.db.Exec(`UPDATE specialist_task_briefs SET fingerprint=? WHERE agent_attempt_id=?`, strings.Repeat("a", 64), a.ID); err == nil {
		t.Fatal("pinned snapshot could be modified")
	}
	if _, err := st.db.Exec(`DELETE FROM specialist_task_briefs WHERE agent_attempt_id=?`, a.ID); err == nil {
		t.Fatal("snapshot evidence could be deleted")
	}
	next, _, err := st.BeginSpecialistAttempt(t.Context(), newAttemptStart(f, idgen.New("attempt")), idgen.New("brief-start"))
	if err != nil {
		t.Fatal(err)
	}
	current, err := st.PrepareSpecialistContext(t.Context(), attemptRef(next))
	if err != nil {
		t.Fatal(err)
	}
	if len(current.Messages) != 0 || current.TaskBrief.Fingerprint != batch.TaskBrief.Fingerprint {
		t.Fatalf("consumption revived retired constraints: %#v", current)
	}
}

func TestSpecialistTaskBriefRejectsForeignUnknownAndRetiredTargets(t *testing.T) {
	st := openWorkItemTestStore(t)
	f := prepareSpecialistAttemptFixture(t, t.Context(), st, "target binding", 4, 256)
	source := sendSpecialistInstructionTestMessage(t, t.Context(), st, f, "live target", idgen.New("brief-send"))
	other, _, err := st.AdmitSpecialist(t.Context(), domain.SpecialistAdmission{AgentID: idgen.New("agent"), SessionID: idgen.New("sess"), RunID: f.Run.ID, ParentAgentID: f.Root.ID,
		Title: "other child", Skills: f.Child.Skills, TurnLimit: 2, TokenLimit: 128, MaxChildren: 2, CreatedAt: time.Now().UTC()}, idgen.New("brief-admit"))
	if err != nil {
		t.Fatal(err)
	}
	for _, variant := range []string{"foreign", "unknown", "hash"} {
		p := domain.AgentInstructionPayload{Version: domain.SpecialistInstructionOperationVersion, Operation: "replace", Instruction: "candidate correction",
			TargetMessageID: source.ID, TargetPayloadSHA256: domain.SpecialistInstructionPayloadSHA256(source.PayloadJSON)}
		childID := f.Child.ID
		if variant == "foreign" {
			childID = other.ID
		}
		if variant == "unknown" {
			p.TargetMessageID = "agentmsg-unknown"
		}
		if variant == "hash" {
			p.TargetPayloadSHA256 = strings.Repeat("a", 64)
		}
		raw, _ := json.Marshal(p)
		_, _, err := st.SendAgentMessage(t.Context(), domain.AgentMessage{ID: idgen.New("agentmsg"), RunID: f.Run.ID, SenderAgentID: f.Root.ID, RecipientAgentID: childID,
			Kind: domain.AgentMessageInstruction, Semantic: domain.AgentMessageSemanticMessage, PayloadJSON: string(raw)}, idgen.New("brief-invalid"))
		if apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
			t.Fatalf("%s target accepted: %v", variant, err)
		}
	}
	sendBriefOperation(t, st, f, "withdraw", "", source)
	p := domain.AgentInstructionPayload{Version: domain.SpecialistInstructionOperationVersion, Operation: "withdraw", TargetMessageID: source.ID, TargetPayloadSHA256: domain.SpecialistInstructionPayloadSHA256(source.PayloadJSON)}
	raw, _ := json.Marshal(p)
	if _, _, err := st.SendAgentMessage(t.Context(), domain.AgentMessage{ID: idgen.New("agentmsg"), RunID: f.Run.ID, SenderAgentID: f.Root.ID, RecipientAgentID: f.Child.ID,
		Kind: domain.AgentMessageInstruction, Semantic: domain.AgentMessageSemanticMessage, PayloadJSON: string(raw)}, idgen.New("brief-invalid")); err == nil {
		t.Fatal("already retired source accepted")
	}
	a, _, err := st.BeginSpecialistAttempt(t.Context(), domain.AgentAttemptStart{AttemptID: idgen.New("attempt"), RunID: f.Run.ID, AgentID: other.ID, ParentAgentID: f.Root.ID, Lease: f.Lease, StartedAt: time.Now().UTC()}, idgen.New("brief-start"))
	if err != nil {
		t.Fatal(err)
	}
	empty, err := st.PrepareSpecialistContext(t.Context(), attemptRef(a))
	if err != nil {
		t.Fatal(err)
	}
	if len(empty.TaskBrief.Instructions) != 0 || len(empty.TaskBrief.Sources) != 0 {
		t.Fatal("child brief leaked another child's delegation")
	}
}

func TestSpecialistTaskBriefPreparationRollsBackWithDeliveryFailure(t *testing.T) {
	st := openWorkItemTestStore(t)
	f := prepareSpecialistAttemptFixture(t, t.Context(), st, "preparation atomicity", 3, 128)
	message := sendSpecialistInstructionTestMessage(t, t.Context(), st, f, "keep pending until commit", idgen.New("brief-send"))
	a, _, err := st.BeginSpecialistAttempt(t.Context(), newAttemptStart(f, idgen.New("attempt")), idgen.New("brief-start"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`CREATE TRIGGER fail_brief_delivery BEFORE INSERT ON specialist_context_deliveries
  BEGIN SELECT RAISE(ABORT,'forced brief delivery failure'); END;`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.PrepareSpecialistContext(t.Context(), attemptRef(a)); err == nil {
		t.Fatal("injected failure accepted")
	}
	var count int
	if err := st.db.QueryRow(`SELECT count(*) FROM specialist_task_briefs WHERE agent_attempt_id=?`, a.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed prepare retained a snapshot: %d %v", count, err)
	}
	if _, err := st.db.Exec(`DROP TRIGGER fail_brief_delivery`); err != nil {
		t.Fatal(err)
	}
	batch, err := st.PrepareSpecialistContext(t.Context(), attemptRef(a))
	if err != nil || batch.Recovered || len(batch.Messages) != 1 || batch.Messages[0].ID != message.ID {
		t.Fatalf("prepare retry failed: %#v %v", batch, err)
	}
	modern := llm.ModelAttempt{SpecialistAttemptID: a.ID, Number: 1, MaxAttempts: 1, Provider: "mock", Model: "mock-code"}
	if _, err := st.RecordSpecialistModelStarted(t.Context(), attemptRef(a), modern); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
		t.Fatalf("unbound brief dispatched: %v", err)
	}
	modern.Context = &llm.ModelContextAudit{TokenBudget: 1, EstimatedTokens: 1, Included: []llm.ModelContextSource{{Kind: "specialist_task_brief", SourceID: batch.TaskBrief.Fingerprint, Tokens: 1}}}
	if _, err := st.db.Exec(`CREATE TRIGGER fail_brief_model_event BEFORE INSERT ON run_events WHEN NEW.type='model.started'
  BEGIN SELECT RAISE(ABORT,'forced brief start failure'); END;`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RecordSpecialistModelStarted(t.Context(), attemptRef(a), modern); err == nil {
		t.Fatal("start event failure was ignored")
	}
	if err := st.db.QueryRow(`SELECT count(*) FROM specialist_model_calls WHERE agent_attempt_id=?`, a.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed event left a dispatch receipt")
	}
	if _, err := st.db.Exec(`DROP TRIGGER fail_brief_model_event`); err != nil {
		t.Fatal(err)
	}
	if inserted, err := st.RecordSpecialistModelStarted(t.Context(), attemptRef(a), modern); err != nil || !inserted {
		t.Fatalf("bound start failed: %v", err)
	}
}

func removeSchemaV175ForTestStatements() []string {
	statements := append(removeSchemaV176ForTestStatements(), []string{
		`CREATE TEMP TABLE legacy_fixture_empty_briefs(n INTEGER CHECK(n=0));`,
		`INSERT INTO legacy_fixture_empty_briefs SELECT count(*) FROM specialist_task_briefs;`,
		`INSERT INTO legacy_fixture_empty_briefs SELECT count(*) FROM agent_messages WHERE json_valid(payload_json) AND json_extract(payload_json,'$.version')='specialist_instruction.v2';`,
		`DROP TABLE legacy_fixture_empty_briefs;`,
		`DROP TRIGGER trg_specialist_task_brief_insert;`, `DROP TRIGGER trg_specialist_task_brief_immutable;`, `DROP TRIGGER trg_specialist_task_brief_delete;`,
		`DROP TABLE specialist_task_briefs;`,
		`DROP TRIGGER trg_specialist_instruction_source_immutable;`, `DROP TRIGGER trg_specialist_instruction_source_delete;`,
		`DROP TRIGGER trg_specialist_context_delivery_insert;`, `DROP TRIGGER trg_specialist_context_delivery_commit;`,
		`DELETE FROM schema_migrations WHERE version=175;`,
	}...)
	for _, statement := range specialistContextDeliveryStatements {
		if strings.HasPrefix(statement, "CREATE TRIGGER trg_specialist_context_delivery_insert\n") || strings.HasPrefix(statement, "CREATE TRIGGER trg_specialist_context_delivery_commit\n") {
			statements = append(statements, statement)
		}
	}
	return statements
}

func TestSchemaV175UpgradesV174WithoutInventingTaskDelivery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v174.db")
	st, err := openHistoricalMigrationFixture(t, path, 177)
	if err != nil {
		t.Fatal(err)
	}
	f := prepareSpecialistAttemptFixture(t, t.Context(), st, "legacy task truth", 3, 128)
	source := sendSpecialistInstructionTestMessage(t, t.Context(), st, f, "legacy still effective", idgen.New("brief-send"))
	for _, stmt := range removeSchemaV175ForTestStatements() {
		if _, err := st.db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	var checksum string
	if err := st.db.QueryRow(`SELECT checksum FROM schema_migrations WHERE version=27`).Scan(&checksum); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var count int
	if err := st.db.QueryRow(`SELECT count(*) FROM specialist_task_briefs`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("upgrade fabricated snapshots: %d %v", count, err)
	}
	var current string
	if err := st.db.QueryRow(`SELECT checksum FROM schema_migrations WHERE version=27`).Scan(&current); err != nil || current != checksum {
		t.Fatal("historical checksum changed")
	}
	a, _, err := st.BeginSpecialistAttempt(t.Context(), newAttemptStart(f, idgen.New("attempt")), idgen.New("brief-start"))
	if err != nil {
		t.Fatal(err)
	}
	batch, err := st.PrepareSpecialistContext(t.Context(), attemptRef(a))
	if err != nil || len(batch.TaskBrief.Instructions) != 1 || batch.TaskBrief.Instructions[0].SourceID != source.ID {
		t.Fatalf("legacy task source was not preserved: %#v %v", batch, err)
	}
	if _, err := st.RestoreAgentGraph(context.Background(), f.Run.ID); err != nil {
		t.Fatal(err)
	}
}

func TestSpecialistTaskBriefRejectsIncompleteCompletedInputWithoutCommittingUsage(t *testing.T) {
	st := openWorkItemTestStore(t)
	f := prepareSpecialistAttemptFixture(t, t.Context(), st, "completion source proof", 3, 128)
	sendSpecialistInstructionTestMessage(t, t.Context(), st, f, "complete current scope", idgen.New("brief-send"))
	workService := application.NewWorkItemService(st)
	work, err := workService.Create(t.Context(), application.CreateWorkItemRequest{
		RunID: f.Run.ID, OwnerAgentID: f.Child.ID, Title: "complete required title",
		Description: "complete required description tail", AcceptanceCriteria: []string{"required acceptance tail"}, Priority: "high",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workService.Transition(t.Context(), work.ID, work.Version, domain.WorkItemBlocked, "required blocked reason"); err != nil {
		t.Fatal(err)
	}
	a, _, err := st.BeginSpecialistAttempt(t.Context(), newAttemptStart(f, idgen.New("attempt")), idgen.New("brief-start"))
	if err != nil {
		t.Fatal(err)
	}
	batch, err := st.PrepareSpecialistContext(t.Context(), attemptRef(a))
	if err != nil {
		t.Fatal(err)
	}
	modern := llm.ModelAttempt{SpecialistAttemptID: a.ID, Number: 1, MaxAttempts: 1, Provider: "mock", Model: "mock-code",
		Context: &llm.ModelContextAudit{TokenBudget: 1, EstimatedTokens: 1, Included: []llm.ModelContextSource{{Kind: "specialist_task_brief", SourceID: batch.TaskBrief.Fingerprint, Tokens: 1}}}}
	if _, err := st.RecordSpecialistModelStarted(t.Context(), attemptRef(a), modern); err != nil {
		t.Fatal(err)
	}
	action := domain.SpecialistAction{Version: domain.SpecialistLifecycleVersion, Kind: domain.SpecialistActionContinue, Message: "continue analysis"}
	response := llm.ChatResponse{Text: specialistActionResponse(t, action), Usage: llm.Usage{InputTokens: 1, OutputTokens: 1, TotalTokens: 2}}
	input := `{"version":"specialist_context.v1","task_brief_fingerprint":"` + batch.TaskBrief.Fingerprint + `","parent_instructions":[],"work_items":[]}`
	if _, err := st.RecordSpecialistModelCompleted(t.Context(), attemptRef(a), modern, response, input, action, policy.Decision{Allowed: true, Reason: "allowed", Risk: "low"}); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
		t.Fatalf("incomplete input committed: %v", err)
	}
	type instruction struct {
		Text string `json:"instruction"`
	}
	type completeInput struct {
		Version      string                             `json:"version"`
		Fingerprint  string                             `json:"task_brief_fingerprint"`
		Instructions []instruction                      `json:"parent_instructions"`
		Work         []domain.SpecialistTaskWorkContext `json:"work_items"`
	}
	if len(batch.TaskBrief.Instructions) != 1 || len(batch.TaskBrief.WorkItems) != 1 {
		t.Fatal("fixture must bind one instruction and one complete owned WorkItem")
	}
	full := completeInput{Version: domain.SpecialistContextVersion, Fingerprint: batch.TaskBrief.Fingerprint,
		Instructions: []instruction{{Text: domain.SpecialistTaskInstructionProjection(batch.TaskBrief.Instructions[0].Instruction)}},
		Work:         []domain.SpecialistTaskWorkContext{domain.SpecialistTaskWorkProjection(batch.TaskBrief.WorkItems[0])}}
	for _, tc := range []struct {
		name   string
		change func(*completeInput)
	}{
		{"instruction", func(v *completeInput) { v.Instructions[0].Text = "changed required scope" }},
		{"title", func(v *completeInput) { v.Work[0].Title = "changed required title" }},
		{"description", func(v *completeInput) { v.Work[0].Description = "truncated description" }},
		{"acceptance", func(v *completeInput) { v.Work[0].AcceptanceCriteria = nil }},
		{"blocked_reason", func(v *completeInput) { v.Work[0].BlockedReason = "changed blocked reason" }},
		{"dependency", func(v *completeInput) { v.Work[0].Dependencies = []string{"work-unselected"} }},
		{"priority", func(v *completeInput) { v.Work[0].Priority = domain.WorkItemPriorityNormal }},
		{"item_version", func(v *completeInput) { v.Work[0].Version++ }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, _ := json.Marshal(full)
			var changed completeInput
			if err := json.Unmarshal(raw, &changed); err != nil {
				t.Fatal(err)
			}
			tc.change(&changed)
			raw, err := domain.MarshalSpecialistDeliveryContext(changed)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := st.RecordSpecialistModelCompleted(t.Context(), attemptRef(a), modern, response, string(raw), action,
				policy.Decision{Allowed: true, Reason: "allowed", Risk: "low"}); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
				t.Fatalf("changed required content committed with the correct brief fingerprint: %v", err)
			}
		})
	}
	saved, found, err := st.GetAgentAttempt(t.Context(), a.ID)
	if err != nil || !found || saved.UsageRecordedAt != nil || saved.Usage.TotalTokens != 0 {
		t.Fatal("rejected completion changed usage")
	}
	pending, err := st.ListAgentMessages(t.Context(), f.Child.ID, true, 10)
	if err != nil || len(pending) != 1 {
		t.Fatal("rejected completion consumed an instruction")
	}
	records, err := st.ListSessionMessages(t.Context(), f.Child.SessionID, true)
	if err != nil || len(records) != 0 {
		t.Fatal("rejected completion rewrote session history")
	}
}
