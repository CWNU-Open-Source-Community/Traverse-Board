package domain

import (
	"encoding/hex"
	"errors"
	"strings"
)

// Text may be absent only when the caller supplies validated image references.
// No synthetic user instruction is inserted for an image-only message.
func NormalizeThreadMessageContent(content string, images []ImageReference, attachments ...[]FileAttachmentReference) (string, error) {
	if err := ValidateThreadMessageImages(images); err != nil {
		return "", err
	}
	attachmentCount := 0
	for _, refs := range attachments {
		if err := ValidateThreadMessageAttachments(refs); err != nil {
			return "", err
		}
		attachmentCount += len(refs)
	}
	if attachmentCount > MaxThreadMessageAttachments {
		return "", errors.New("too many file attachments")
	}
	if content == "" && (len(images) > 0 || attachmentCount > 0) {
		return "", nil
	}
	return NormalizeOperatorSteeringContent(content)
}

const MaxThreadMessageImages = 4

// ImageReference is an immutable workspace-scoped observation, never authority.
type ImageReference struct {
	ID     string `json:"id"`
	SHA256 string `json:"sha256"`
}

type WorkspaceImage struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspace_id"`
	SHA256      string `json:"sha256"`
	MIMEType    string `json:"mime_type"`
	ByteSize    int    `json:"byte_size"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	Name        string `json:"name,omitempty"`
}

type ModelImageEvidence struct {
	Image            WorkspaceImage
	MessageID        string
	SessionMessageID int64
	Current          bool
}

type ModelImageContext struct {
	Images             []ModelImageEvidence
	OmittedOlderImages int
}

func ValidateThreadMessageImages(images []ImageReference) error {
	if len(images) > MaxThreadMessageImages {
		return errors.New("at most four Thread images are allowed")
	}
	seen := map[string]bool{}
	for _, image := range images {
		digest, err := hex.DecodeString(image.SHA256)
		if !ValidAgentID(image.ID) || err != nil || len(digest) != 32 || image.SHA256 != strings.ToLower(image.SHA256) || seen[image.ID] {
			return errors.New("Thread images require unique image IDs and lowercase SHA-256 digests")
		}
		seen[image.ID] = true
	}
	return nil
}
