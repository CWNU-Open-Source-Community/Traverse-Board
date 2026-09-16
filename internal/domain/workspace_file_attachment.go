package domain

import (
	"encoding/hex"
	"errors"
	"strings"
)

const MaxThreadMessageAttachments = 4

// FileAttachmentReference identifies immutable uploaded bytes, not a repository
// path or permission to execute their contents.
type FileAttachmentReference struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspace_id"`
	SHA256      string `json:"sha256"`
	ByteSize    int    `json:"byte_size"`
}

type WorkspaceFileAttachment struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspace_id"`
	Name        string `json:"name"`
	MIMEType    string `json:"mime_type"`
	SHA256      string `json:"sha256"`
	ByteSize    int    `json:"byte_size"`
	Readability string `json:"readability"`
	TextSHA256  string `json:"text_sha256,omitempty"`
	TextBytes   int    `json:"text_bytes"`
	Redacted    bool   `json:"redacted"`
	Reason      string `json:"reason,omitempty"`
}

type FileAttachmentObservation struct {
	State      string                   `json:"state"`
	Attachment *WorkspaceFileAttachment `json:"attachment,omitempty"`
}

func ValidateThreadMessageAttachments(refs []FileAttachmentReference) error {
	if len(refs) > MaxThreadMessageAttachments {
		return errors.New("at most four uploaded file attachments are allowed")
	}
	seen := map[string]bool{}
	for _, ref := range refs {
		digest, err := hex.DecodeString(ref.SHA256)
		if !ValidAgentID(ref.ID) || !ValidAgentID(ref.WorkspaceID) || err != nil || len(digest) != 32 || ref.SHA256 != strings.ToLower(ref.SHA256) || ref.ByteSize < 0 || ref.ByteSize > 5*1024*1024 || seen[ref.ID] {
			return errors.New("uploaded file attachments require unique IDs, workspace, exact size and lowercase SHA-256")
		}
		seen[ref.ID] = true
	}
	return nil
}
