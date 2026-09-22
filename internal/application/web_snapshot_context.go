package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"
	"unicode/utf8"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/toolgateway"
	"cyberagent-workbench/internal/webevidence"
)

// The character limit alone can admit a much larger multilingual prompt under
// the conservative tokenizer. Keep the public character/page limit and also
// bound the model-only body with the same estimator used by the request gate.
const webSnapshotContextBodyTokens = 2048

// Project only the model-bound copy. The durable tool result and its immutable
// snapshot remain available in full; every tool call retains its paired result.
func supervisorWebFetchContextResult(call domain.SupervisorToolCall) (string, error) {
	if call.ToolName != string(toolgateway.WebFetchTool) || call.Status != domain.SupervisorToolCompleted {
		return call.ResultJSON, nil
	}
	var envelope supervisorToolResultEnvelope
	if err := json.Unmarshal([]byte(call.ResultJSON), &envelope); err != nil {
		return "", err
	}
	var output webFetchToolOutput
	if err := json.Unmarshal([]byte(envelope.Stdout), &output); err != nil ||
		output.ProtocolVersion != webevidence.FetchProtocolVersion ||
		output.Snapshot.SourceID == "" || output.Snapshot.SnapshotID == "" {
		// Do not invent snapshot identity for an unsupported result shape.
		return call.ResultJSON, nil
	}
	body := []rune(output.Snapshot.Body)
	excerpt, _ := boundedWebContextText(string(body[:min(len(body), toolgateway.MaxWebSnapshotPageRunes)]), webSnapshotContextBodyTokens)
	if excerpt == output.Snapshot.Body {
		return call.ResultJSON, nil
	}
	if !output.Snapshot.BodyExcerptTruncated && output.Snapshot.BodyRunes == 0 {
		output.Snapshot.BodyRunes = len(body)
	}
	output.Snapshot.Body = excerpt
	output.Snapshot.BodyExcerptTruncated = true
	next := output.Snapshot.BodyOffset + utf8.RuneCountInString(excerpt)
	output.Snapshot.NextOffset = &next
	if output.Extraction != nil {
		output.Extraction.SpanEnd = next
	}
	encoded, err := marshalWebSnapshotPage(output)
	if err != nil {
		return "", err
	}
	envelope.Stdout = string(encoded)
	envelope.Truncated = true
	if envelope.Metadata == nil {
		envelope.Metadata = make(map[string]string)
	}
	envelope.Metadata["body_excerpt_truncated"] = "true"
	envelope.Metadata["context_excerpt"] = "true"
	envelope.Metadata["next_offset"] = strconv.Itoa(next)
	projected, err := marshalSupervisorToolResultEnvelope(envelope)
	return string(projected), err
}

// This is a read of an already fetched Run-local snapshot. It neither fetches
// a URL nor prepares/consumes another network authorization.
type threadPredecessorWebSnapshotReader interface {
	GetThreadPredecessorWebSnapshot(context.Context, string, string, string, string, string) (
		webevidence.Source, webevidence.Snapshot, error)
}

func (e *WebEvidenceToolExecutor) readWebSnapshotPage(ctx context.Context,
	scope toolgateway.WebEvidenceExecutionScope, request toolgateway.WebFetchPayload,
) (toolgateway.WebEvidenceExecutionResult, error) {
	questionRead := request.Question != ""
	if request.URL != "" || request.SourceID == "" || request.SnapshotID == "" || request.Connector != "" || request.MaxItems != 0 ||
		(questionRead && (request.Offset != nil || request.Limit != nil)) ||
		(!questionRead && (request.Offset == nil || request.Limit == nil || *request.Offset < 0 ||
			*request.Limit < 1 || *request.Limit > toolgateway.MaxWebSnapshotPageRunes)) {
		return toolgateway.WebEvidenceExecutionResult{}, apperror.New(apperror.CodeInvalidArgument,
			"saved web snapshot read requires an exact source and bounded character range")
	}
	source, err := e.store.GetWebSource(ctx, scope.RunID, request.SourceID)
	var snapshot webevidence.Snapshot
	historical := false
	if apperror.CodeOf(err) == apperror.CodeNotFound {
		if reader, ok := e.store.(threadPredecessorWebSnapshotReader); ok {
			source, snapshot, err = reader.GetThreadPredecessorWebSnapshot(ctx,
				scope.RunID, scope.MissionID, scope.WorkspaceID, request.SourceID, request.SnapshotID)
			historical = err == nil && source.RunID != scope.RunID
		}
	}
	if err != nil {
		return toolgateway.WebEvidenceExecutionResult{}, apperror.Normalize(err)
	}
	if !historical {
		snapshot, err = e.store.GetWebSnapshot(ctx, scope.RunID, request.SnapshotID)
		if err != nil {
			return toolgateway.WebEvidenceExecutionResult{}, apperror.Normalize(err)
		}
	}
	if source.Validate() != nil || snapshot.Validate() != nil ||
		source.ID != request.SourceID || snapshot.ID != request.SnapshotID ||
		(!historical && source.RunID != scope.RunID) || source.MissionID != scope.MissionID ||
		source.WorkspaceID != scope.WorkspaceID || snapshot.RunID != source.RunID ||
		snapshot.MissionID != scope.MissionID || snapshot.SourceID != source.ID ||
		(snapshot.State != webevidence.SourceFetched && snapshot.State != webevidence.SourcePartial) {
		return toolgateway.WebEvidenceExecutionResult{}, apperror.New(apperror.CodeFailedPrecondition,
			"saved web snapshot must belong to this Run, workspace and source")
	}
	body := []rune(snapshot.Body)
	start, end := 0, 0
	var extraction *webevidence.Extraction
	if questionRead {
		selected, err := webevidence.ExtractSnapshot(snapshot, request.Question)
		if err != nil {
			return toolgateway.WebEvidenceExecutionResult{}, apperror.Wrap(apperror.CodeInvalidArgument, "saved snapshot question is invalid", err)
		}
		extraction = &selected
		start, end = selected.SpanStart, selected.SpanEnd
	} else if *request.Offset > len(body) {
		return toolgateway.WebEvidenceExecutionResult{}, apperror.New(apperror.CodeInvalidArgument,
			"web snapshot offset exceeds the saved body")
	} else {
		start = *request.Offset
		end = start + min(*request.Limit, len(body)-start)
	}
	presentation := webevidence.PresentSnapshot(snapshot, time.Now().UTC())
	page := snapshot
	if !questionRead {
		page.Body = string(body[start:end])
	}
	encoded, _, err := encodeWebFetchToolOutput(webevidence.FetchResult{
		ProtocolVersion: webevidence.FetchProtocolVersion, Source: source, Snapshot: page, Extraction: extraction}, presentation)
	if err != nil {
		return toolgateway.WebEvidenceExecutionResult{}, err
	}
	var output webFetchToolOutput
	if err := json.Unmarshal(encoded, &output); err != nil {
		return toolgateway.WebEvidenceExecutionResult{}, err
	}
	output.SourceRunID, output.Historical = source.RunID, historical
	if questionRead {
		start = output.Snapshot.BodyOffset
	}
	output.Snapshot.BodyOffset = start
	output.Snapshot.BodyRunes = len(body)
	end = start + utf8.RuneCountInString(output.Snapshot.Body)
	output.Snapshot.BodyExcerptTruncated = start > 0 || end < len(body)
	if end < len(body) {
		output.Snapshot.NextOffset = &end
	}
	encoded, err = marshalWebSnapshotPage(output)
	if err != nil {
		return toolgateway.WebEvidenceExecutionResult{}, err
	}
	return toolgateway.WebEvidenceExecutionResult{Content: string(encoded),
		Truncated: snapshot.Truncated || output.Snapshot.BodyExcerptTruncated,
		Metadata: map[string]string{"source_id": source.ID, "snapshot_id": snapshot.ID,
			"source_run_id": source.RunID, "historical": strconv.FormatBool(historical),
			"url": presentation.URL, "title": presentation.Title, "digest": snapshot.Digest,
			"state": presentation.Status, "partial": strconv.FormatBool(presentation.Partial),
			"stale": strconv.FormatBool(presentation.Stale), "citeable": strconv.FormatBool(presentation.Citeable),
			"body_excerpt_truncated": strconv.FormatBool(output.Snapshot.BodyExcerptTruncated),
			"body_offset":            strconv.Itoa(start), "body_runes": strconv.Itoa(len(body)),
			"snapshot_read": "true", "network_called": "false", "untrusted": "true",
			"instruction_authorized": "false"}}, nil
}

func marshalWebSnapshotPage(output webFetchToolOutput) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(output); err != nil {
		return nil, err
	}
	encoded := bytes.TrimSuffix(buffer.Bytes(), []byte("\n"))
	if !utf8.Valid(encoded) || len(encoded) > toolgateway.MaxResultStdoutBytes-1024 {
		return nil, errors.New("saved web snapshot page exceeds the tool result limit")
	}
	return encoded, nil
}
