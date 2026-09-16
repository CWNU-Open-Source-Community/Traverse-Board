package store

import (
	"context"
	"database/sql"
	"encoding/json"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/session"
)

// Only the original Go-admitted call selects this path. A JSON version or
// metadata flag cannot opt another tool out of normal redaction. The stdout is
// rebuilt from the same Thread's sealed sources and original call arguments in
// the result's persistence transaction, then compared byte for byte. The rest
// of the outer envelope always retains the normal redaction policy.
func (s *SQLiteStore) preserveSupervisorHistoryResultTx(ctx context.Context, tx *sql.Tx,
	call domain.SupervisorToolCall, original, redacted string,
) (string, error) {
	var envelope struct {
		Version string `json:"version"`
		Tool    string `json:"tool"`
		Status  string `json:"status"`
		Code    string `json:"code"`
		Stdout  string `json:"stdout"`
	}
	if json.Unmarshal([]byte(original), &envelope) != nil || envelope.Version != "supervisor_tool_result.v1" ||
		envelope.Tool != call.ToolName || envelope.Status != string(domain.SupervisorToolCompleted) || envelope.Code != "" {
		return "", invalidHistoryProjection()
	}
	var safe map[string]json.RawMessage
	if err := json.Unmarshal([]byte(redacted), &safe); err != nil {
		return "", err
	}
	safe["stdout"], _ = json.Marshal(envelope.Stdout)
	encoded, err := json.Marshal(safe)
	if err != nil {
		return "", err
	}
	// An identical terminal replay uses its already sealed projection. A newer
	// compaction flag or later history cannot rewrite this historical receipt.
	if call.Status.Terminal() {
		if call.Status == domain.SupervisorToolCompleted && call.ResultJSON == string(encoded) {
			return call.ResultJSON, nil
		}
		return "", invalidHistoryProjection()
	}
	var expected any
	switch call.ToolName {
	case "history_read":
		var request domain.HistoryReadRequest
		if json.Unmarshal([]byte(call.PayloadJSON), &request) != nil {
			return "", invalidHistoryProjection()
		}
		expected, err = s.readThreadHistory(ctx, tx, call.RunID, request)
	case "history_search":
		var request domain.HistorySearchRequest
		var output domain.HistorySearchResult
		if json.Unmarshal([]byte(call.PayloadJSON), &request) != nil || json.Unmarshal([]byte(envelope.Stdout), &output) != nil {
			return "", invalidHistoryProjection()
		}
		var cursor historyCursor
		if output.SnapshotCursor == "" || decodeHistoryToken(output.SnapshotCursor, &cursor) != nil ||
			encodeHistoryToken(cursor) != output.SnapshotCursor || cursor.Run != call.RunID ||
			cursor.QuerySHA != session.ContentSHA256(request.Query) ||
			(request.Cursor == "" && cursor.Offset != 0) ||
			(request.Cursor != "" && request.Cursor != output.SnapshotCursor) {
			return "", invalidHistoryProjection()
		}
		request.Cursor = output.SnapshotCursor
		expected, err = s.searchThreadHistory(ctx, tx, call.RunID, request)
	default:
		return "", invalidHistoryProjection()
	}
	if err != nil {
		return "", err
	}
	verified, err := json.Marshal(expected)
	if err != nil {
		return "", err
	}
	if string(verified) != envelope.Stdout {
		return "", invalidHistoryProjection()
	}
	return string(encoded), nil
}

func invalidHistoryProjection() error {
	return apperror.New(apperror.CodeConflict, "history result does not match its exact stored source projection")
}
