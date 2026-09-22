package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/contextmgr"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/redact"
	"cyberagent-workbench/internal/session"
)

// CompactSupervisorContext replaces only persisted Session history. The caller
// chooses the pressure boundary before its next model request; current input,
// tool rounds, approvals and their replay identities are never compacted here.
// Summary publication and monotonic message flags share one transaction, so a
// restart observes either the original history or its complete replacement.
func (s *SQLiteStore) CompactSupervisorContext(ctx context.Context,
	checkpoint domain.SupervisorCheckpoint, preserveRecent int,
) (contextmgr.Result, error) {
	return s.CompactSupervisorContextWithStrategy(ctx, checkpoint, preserveRecent, nil)
}

// CompactSupervisorContextWithStrategy computes the candidate after closing the
// read snapshot. A configured ranking strategy never runs in a DB transaction.
// It cannot change source text, provenance, authority, or the commit boundary.
func (s *SQLiteStore) CompactSupervisorContextWithStrategy(ctx context.Context,
	checkpoint domain.SupervisorCheckpoint, preserveRecent int, strategy contextmgr.SummaryStrategy,
) (contextmgr.Result, error) {
	if err := checkpoint.Validate(); err != nil {
		return contextmgr.Result{}, apperror.Wrap(apperror.CodeInvalidArgument,
			"valid Supervisor checkpoint is required for context compaction", err)
	}
	if preserveRecent < 1 || checkpoint.Phase != domain.SupervisorTurnStarted {
		return contextmgr.Result{}, apperror.New(apperror.CodeInvalidArgument,
			"context compaction requires a started Supervisor turn and positive preserved message count")
	}
	reader, finish, err := s.beginThreadRequestObservation(ctx)
	if err != nil {
		return contextmgr.Result{}, err
	}
	snapshot, err := readSupervisorCompactionSnapshot(ctx, reader, checkpoint)
	finish()
	if err != nil {
		return contextmgr.Result{}, err
	}
	history := snapshot.contextMessages()
	if len(history) <= preserveRecent {
		return contextmgr.Result{Preserved: history}, nil
	}
	config := contextmgr.DefaultConfig()
	config.PreserveRecentMessages = preserveRecent
	config.MaxMessagesBeforeCompact = max(config.MaxMessagesBeforeCompact, preserveRecent)
	manager := contextmgr.NewManager(nil, config).WithSummaryStrategy(strategy)
	result, err := manager.PrepareCandidate(ctx, snapshot.SessionID, snapshot.WorkspaceID,
		history, snapshot.Summary, snapshot.HasSummary)
	if err != nil {
		return contextmgr.Result{}, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return contextmgr.Result{}, err
	}
	defer func() { _ = tx.Rollback() }()
	current, err := readSupervisorCompactionSnapshot(ctx, tx, checkpoint)
	if err != nil {
		return contextmgr.Result{}, err
	}
	// Compare the entire uncompressed set (including the retained tail), not
	// just its last ID or count. A concurrent rewrite with a valid new digest,
	// provenance change, new input, or another summary cannot reuse this draft.
	if !snapshot.sameSources(current) {
		return contextmgr.Result{}, apperror.New(apperror.CodeConflict,
			"Supervisor context compaction source snapshot changed")
	}
	if err := requireSupervisorCheckpointLeaseTx(ctx, tx, checkpoint, current.Checkpoint); err != nil {
		return contextmgr.Result{}, err
	}
	if result.Summary.ID == 0 {
		result.Summary, err = saveContextSummaryTx(ctx, tx, result.Summary)
		if err != nil {
			return contextmgr.Result{}, err
		}
	}
	if result.Compacted && result.RemovedMessages > 0 {
		if err := markSupervisorContextMessagesCompacted(ctx, tx, snapshot.SessionID,
			history, result.RemovedMessages); err != nil {
			return contextmgr.Result{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return contextmgr.Result{}, err
	}
	return result, nil
}

// Persist bounded evidence only after the normal completion gate has verified
// all tool work settled. Completion replay returns before this helper, so it
// cannot manufacture a second evidence record or replay the original tools.
func saveSupervisorToolContextTx(ctx context.Context, tx *sql.Tx, run domain.Run,
	checkpoint domain.SupervisorCheckpoint,
) error {
	rows, err := tx.QueryContext(ctx, `SELECT call_id, tool_name, status, result_json, error_code
		FROM run_supervisor_tool_calls WHERE run_id=? AND turn=? AND attempt_id=?
		AND tool_name NOT IN ('history_search','history_read')
		ORDER BY round,position`, run.ID, checkpoint.NextTurn, checkpoint.AttemptID)
	if err != nil {
		return err
	}
	type toolFact struct {
		CallID        string                          `json:"call_id"`
		ResultSHA256  string                          `json:"result_sha256"`
		Tool          string                          `json:"tool"`
		Status        domain.SupervisorToolCallStatus `json:"status"`
		ErrorCode     string                          `json:"error_code,omitempty"`
		OutputExcerpt string                          `json:"output_excerpt,omitempty"`
		ErrorExcerpt  string                          `json:"error_excerpt,omitempty"`
	}
	var facts []toolFact
	for rows.Next() {
		var fact toolFact
		var raw string
		if err := rows.Scan(&fact.CallID, &fact.Tool, &fact.Status, &raw, &fact.ErrorCode); err != nil {
			_ = rows.Close()
			return err
		}
		if !fact.Status.Terminal() || raw == "" {
			_ = rows.Close()
			return apperror.New(apperror.CodeFailedPrecondition, "Supervisor context evidence requires recorded terminal tool results")
		}
		fact.ResultSHA256 = session.ContentSHA256(raw)
		var envelope struct {
			Stdout string `json:"stdout"`
			Stderr string `json:"stderr"`
		}
		if json.Unmarshal([]byte(raw), &envelope) == nil && (envelope.Stdout != "" || envelope.Stderr != "") {
			fact.OutputExcerpt = supervisorToolObservedExcerpt(envelope.Stdout)
			fact.ErrorExcerpt = supervisorToolContextExcerpt(envelope.Stderr, 192)
		} else {
			fact.OutputExcerpt = supervisorToolContextExcerpt(raw, 384)
		}
		facts = append(facts, fact)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil || len(facts) == 0 {
		return err
	}
	// Recall results remain in their original tool ledger but are not copied
	// back as new evidence about evidence. The other full immutable results
	// remain in the ledger too. Keep the latest six
	// facts and expose omissions rather than copying unbounded tool output.
	total := len(facts)
	if len(facts) > 6 {
		facts = facts[len(facts)-6:]
	}
	// Session/provenance already bind the Run and exact attempt. Avoid wrapping
	// a short observation in another large envelope: later bounded compaction
	// must retain useful output alongside its original receipt identity.
	var content strings.Builder
	for index, fact := range facts {
		if index > 0 {
			content.WriteByte('\n')
		}
		content.WriteString("call_id=" + fact.CallID + " result_sha256=" + fact.ResultSHA256 +
			" tool=" + fact.Tool + " status=" + string(fact.Status))
		if fact.ErrorCode != "" {
			content.WriteString(" error_code=" + fact.ErrorCode)
		}
		if fact.ErrorExcerpt != "" {
			content.WriteString("\nstderr excerpt: " + fact.ErrorExcerpt)
		}
		if fact.OutputExcerpt != "" {
			content.WriteString("\nstdout excerpt: " + fact.OutputExcerpt)
		}
	}
	if total > len(facts) {
		content.WriteString("\nEarlier tool facts omitted: " + strconv.Itoa(total-len(facts)))
	}
	_, err = saveSessionMessageTx(ctx, tx, session.NewEvidenceMessage(run.SessionID,
		session.SourceToolResult, fmt.Sprintf("supervisor-tools:%s", checkpoint.AttemptID), content.String()))
	return err
}

func supervisorToolObservedExcerpt(output string) string {
	// Tools commonly return a JSON object with a text content field. Preserve
	// that observation rather than spending its whole excerpt on workspace IDs,
	// protocol tags and hashes. This is still untrusted text, not parsed policy.
	var object map[string]json.RawMessage
	if json.Unmarshal([]byte(output), &object) == nil {
		var content string
		if json.Unmarshal(object["content"], &content) == nil && strings.TrimSpace(content) != "" {
			return "content field: " + supervisorToolContextExcerpt(content, 384)
		}
	}
	return supervisorToolContextExcerpt(output, 384)
}

func supervisorToolContextExcerpt(value string, limit int) string {
	value = strings.TrimSpace(redact.String(value))
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	const marker = "\n[excerpt omitted]\n"
	available := limit - len([]rune(marker))
	return string(runes[:available/2]) + marker + string(runes[len(runes)-(available-available/2):])
}
