package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path"
	"strings"
	"unicode"
)

const MaxRunFileAttachmentInputs = 64

// FileAttachmentInput describes an original file, not parsed document content
// or an instruction grant. RelativePath is relative to the runtime's fixed
// TRAVERSE_ATTACHMENTS_DIR; it is never a user-supplied host path.
type FileAttachmentInput struct {
	ID           string `json:"id"`
	WorkspaceID  string `json:"workspace_id"`
	Name         string `json:"name"`
	MIMEType     string `json:"mime_type"`
	SHA256       string `json:"sha256"`
	ByteSize     int    `json:"byte_size"`
	RelativePath string `json:"relative_path"`
}

type FileAttachmentInputSet struct {
	Version      string                `json:"version"`
	ThreadID     string                `json:"thread_id"`
	RunID        string                `json:"run_id"`
	SessionID    string                `json:"session_id"`
	WorkspaceID  string                `json:"workspace_id"`
	Files        []FileAttachmentInput `json:"files"`
	OmittedCount int                   `json:"omitted_count"`
}

func NewFileAttachmentInput(file WorkspaceFileAttachment) FileAttachmentInput {
	// Keep a useful extension without making an uploaded Windows device name,
	// trailing dot, or unusual filename a filesystem path. The original name is
	// preserved separately in the manifest and upload receipt.
	ext := strings.ToLower(path.Ext(file.Name))
	if len(ext) > 16 || strings.IndexFunc(ext, func(r rune) bool {
		return r != '.' && !(unicode.IsLetter(r) && r < 128) && !(r >= '0' && r <= '9')
	}) >= 0 {
		ext = ""
	}
	return FileAttachmentInput{ID: file.ID, WorkspaceID: file.WorkspaceID,
		Name: file.Name, MIMEType: file.MIMEType, SHA256: file.SHA256,
		ByteSize: file.ByteSize, RelativePath: path.Join(file.ID, "content"+ext)}
}

func (s FileAttachmentInputSet) Manifest() ([]byte, string) {
	body, _ := json.Marshal(s)
	digest := sha256.Sum256(body)
	return body, hex.EncodeToString(digest[:])
}
