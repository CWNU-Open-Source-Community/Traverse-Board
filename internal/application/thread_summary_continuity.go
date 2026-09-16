package application

import (
	"bytes"
	"encoding/json"
	"io"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/contextmgr"
	"cyberagent-workbench/internal/session"
)

// This is content inside the existing immutable ContinuitySnapshot, not a new
// authority or storage protocol. A newly combined value uses zero SummaryID;
// each flat entry retains its own original identity and content digest.
const threadSummaryBundleKind = "thread_summary_bundle"

type threadSummaryEntry struct {
	SummaryID     int64  `json:"summary_id"`
	ContentSHA256 string `json:"content_sha256"`
	Content       string `json:"content"`
}

type threadSummaryBundle struct {
	Kind      string               `json:"kind"`
	Summaries []threadSummaryEntry `json:"summaries"`
}

func mergeThreadContinuitySummary(snapshot *contextmgr.ContinuitySnapshot,
	current contextmgr.Summary,
	previousSource ...contextmgr.ContinuitySummarySource,
) error {
	if current.TaskID != snapshot.SourceSessionID || current.WorkspaceID != snapshot.WorkspaceID {
		return apperror.New(apperror.CodeFailedPrecondition,
			"Thread successor summary does not belong to its source Session and Workspace")
	}
	if err := contextmgr.ValidateStoredSummary(current); err != nil || current.ID <= 0 {
		return apperror.Wrap(apperror.CodeFailedPrecondition,
			"Thread successor summary is not a valid stored summary", err)
	}
	currentDigest := session.ContentSHA256(current.Content)
	if current.ContentSHA256 != "" && current.ContentSHA256 != currentDigest {
		return invalidThreadSummaryBinding()
	}
	// Migrated v0 rows can lack a stored digest. Use the original bytes to
	// identify this detached projection without rewriting their durable row.
	current.ContentSHA256 = currentDigest
	if len(previousSource) > 0 && previousSource[0].SourceID != "" {
		// Normal Thread continuation supplies a source anchored in the
		// predecessor's immutable config. The old merge below is only for
		// values without such a durable reference; those must stay complete.
		if snapshot.SummaryContent == "" || snapshot.SummaryID < 0 ||
			session.ContentSHA256(snapshot.SummaryContent) != snapshot.SummaryContentSHA256 ||
			previousSource[0].ContentSHA256 != snapshot.SummaryContentSHA256 {
			return invalidThreadSummaryBinding()
		}
		content, err := contextmgr.RollContinuitySummaries(snapshot.SummaryContent, current, previousSource[0])
		if err != nil {
			return apperror.Wrap(apperror.CodeFailedPrecondition,
				"Thread rolling summary could not preserve its source bindings", err)
		}
		snapshot.SummaryID, snapshot.SummaryContent = 0, content
		snapshot.SummaryContentSHA256 = session.ContentSHA256(content)
		return nil
	}
	entries, err := threadContinuitySummaryEntries(snapshot.SummaryID,
		snapshot.SummaryContentSHA256, snapshot.SummaryContent)
	if err != nil {
		return err
	}
	entries, err = appendThreadSummaryEntry(entries, threadSummaryEntry{
		SummaryID: current.ID, ContentSHA256: currentDigest, Content: current.Content,
	})
	if err != nil {
		return err
	}
	var id int64
	var content string
	if len(entries) == 1 {
		id, content = entries[0].SummaryID, entries[0].Content
	} else {
		encoded, err := json.Marshal(threadSummaryBundle{Kind: threadSummaryBundleKind, Summaries: entries})
		if err != nil {
			return err
		}
		content = string(encoded)
	}
	if len([]byte(content)) > contextmgr.MaxContinuitySummaryBytes {
		return apperror.New(apperror.CodeResourceExhausted,
			"Thread continuation summaries exceed the 16 KiB context limit; earlier summaries were preserved and no successor was created")
	}
	// Assign only after every identity and capacity check. An unsuccessful
	// successor preparation must not discard its previously inherited summary.
	snapshot.SummaryID, snapshot.SummaryContent = id, content
	snapshot.SummaryContentSHA256 = session.ContentSHA256(content)
	return nil
}

func threadContinuitySummaryEntries(id int64, digest, content string) ([]threadSummaryEntry, error) {
	if content == "" {
		if id != 0 || digest != "" {
			return nil, invalidThreadSummaryBinding()
		}
		return nil, nil
	}
	if id < 0 || len([]byte(content)) > contextmgr.MaxContinuitySummaryBytes || session.ContentSHA256(content) != digest {
		return nil, invalidThreadSummaryBinding()
	}
	var marker struct {
		Kind string `json:"kind"`
	}
	if id != 0 || json.Unmarshal([]byte(content), &marker) != nil || marker.Kind != threadSummaryBundleKind {
		// Preserve an older valid zero-ID opaque summary without pretending it
		// identifies a stored context_summaries row.
		return []threadSummaryEntry{{SummaryID: id, ContentSHA256: digest, Content: content}}, nil
	}
	var bundle threadSummaryBundle
	decoder := json.NewDecoder(bytes.NewBufferString(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&bundle); err != nil {
		return nil, invalidThreadSummaryBinding()
	}
	if err := decoder.Decode(new(any)); err != io.EOF || len(bundle.Summaries) < 2 {
		return nil, invalidThreadSummaryBinding()
	}
	var entries []threadSummaryEntry
	for _, entry := range bundle.Summaries {
		var nested struct {
			Kind string `json:"kind"`
		}
		if entry.SummaryID == 0 && json.Unmarshal([]byte(entry.Content), &nested) == nil && nested.Kind == threadSummaryBundleKind {
			return nil, invalidThreadSummaryBinding()
		}
		var err error
		entries, err = appendThreadSummaryEntry(entries, entry)
		if err != nil {
			return nil, err
		}
	}
	return entries, nil
}

func appendThreadSummaryEntry(entries []threadSummaryEntry, entry threadSummaryEntry) ([]threadSummaryEntry, error) {
	if entry.SummaryID < 0 || entry.Content == "" || session.ContentSHA256(entry.Content) != entry.ContentSHA256 {
		return nil, invalidThreadSummaryBinding()
	}
	for _, previous := range entries {
		if previous.SummaryID == entry.SummaryID {
			if previous.ContentSHA256 != entry.ContentSHA256 || previous.Content != entry.Content {
				return nil, apperror.New(apperror.CodeConflict,
					"Thread continuation contains inconsistent content for the same summary identity")
			}
			return entries, nil
		}
	}
	return append(entries, entry), nil
}

func invalidThreadSummaryBinding() error {
	return apperror.New(apperror.CodeFailedPrecondition, "Thread continuation summary content or digest binding is invalid")
}
