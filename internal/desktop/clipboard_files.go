package desktop

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/redact"
	"cyberagent-workbench/internal/runmutation"
)

const ClipboardFilesProtocolVersion = "desktop_clipboard_files.v1"
const MaxClipboardFiles = 4
const MaxClipboardFileBytes = 5 * 1024 * 1024

// ClipboardFile contains bytes from an explicit native paste. Source paths
// remain inside the native reader and are never part of a renderer binding.
type ClipboardFile struct {
	Name  string
	Data  []byte
	Error error
}

type ClipboardFileReader interface {
	ReadFiles(context.Context) ([]ClipboardFile, error)
}
type ClipboardFileImporter interface {
	Import(context.Context, application.FileAttachmentImportRequest) (domain.WorkspaceFileAttachment, error)
	Inspect(context.Context, string, string) (domain.FileAttachmentObservation, error)
}
type ClipboardImageImporter interface {
	SaveWorkspaceImage(context.Context, string, string, string, string, []byte) (domain.WorkspaceImage, error)
	GetWorkspaceImageByOperationKey(context.Context, string, string) (domain.WorkspaceImage, bool, error)
}

type ClipboardFilesRequest struct {
	Version      string `json:"version"`
	WorkspaceID  string `json:"workspace_id"`
	OperationKey string `json:"operation_key"`
}
type ClipboardFileRejection struct {
	Name    string `json:"name"`
	Code    string `json:"code"`
	Message string `json:"message"`
}
type ClipboardFilesResult struct {
	Version       string                           `json:"version"`
	WorkspaceID   string                           `json:"workspace_id"`
	Status        string                           `json:"status"`
	BatchComplete bool                             `json:"batch_complete"`
	Images        []domain.WorkspaceImage          `json:"images"`
	Attachments   []domain.WorkspaceFileAttachment `json:"attachments"`
	Rejected      []ClipboardFileRejection         `json:"rejected"`
}

// PasteClipboardFiles reads only a native file-list clipboard format. It is
// never called by startup, polling, model tools or API request handlers. Plain
// text/bitmap paste remains with the editable WebView and its DOM paste event.
func (b *DesktopBridge) PasteClipboardFiles(request ClipboardFilesRequest) (ClipboardFilesResult, error) {
	result := ClipboardFilesResult{Version: ClipboardFilesProtocolVersion, WorkspaceID: request.WorkspaceID,
		Status: "unsupported", Images: []domain.WorkspaceImage{}, Attachments: []domain.WorkspaceFileAttachment{}, Rejected: []ClipboardFileRejection{}}
	if !validClipboardFilesRequest(request) {
		return result, apperror.New(apperror.CodeInvalidArgument, "Clipboard attachment request is invalid")
	}
	if b == nil || b.clipboardReader == nil {
		return result, nil
	}
	if !b.bootstrap.RunCreationEnabled || !b.bootstrap.SessionMessageEnabled || b.bootstrap.ControlToken == "" ||
		b.workspaceResolver == nil || b.clipboardFiles == nil || b.clipboardImages == nil {
		return result, apperror.New(apperror.CodePolicyDenied, "Clipboard attachment import is unavailable for this connection")
	}
	ctx, err := b.lifecycleContext()
	if err != nil {
		return result, err
	}
	workspace, err := b.workspaceResolver.ResolveWorkspace(ctx, request.WorkspaceID)
	if err != nil {
		return result, apperror.Normalize(err)
	}
	if workspace.ID != request.WorkspaceID {
		return result, apperror.New(apperror.CodeConflict, "Clipboard workspace binding changed")
	}
	if !b.clipboardActive.CompareAndSwap(false, true) {
		return result, apperror.New(apperror.CodeResourceExhausted, "A clipboard attachment import is already in progress")
	}
	defer b.clipboardActive.Store(false)
	files, err := b.clipboardReader.ReadFiles(ctx)
	if err != nil {
		return result, apperror.Normalize(err)
	}
	if len(files) == 0 {
		result.Status = "empty"
		result.BatchComplete = true
		return result, nil
	}
	if len(files) > MaxClipboardFiles {
		return result, apperror.New(apperror.CodeResourceExhausted, "Paste at most four files at a time")
	}
	result.Status = "processed"
	for index, file := range files {
		if err := ctx.Err(); err != nil {
			return result, apperror.Normalize(err)
		}
		name := clipboardRejectionName(file.Name, index)
		failure := file.Error
		if failure == nil && len(file.Data) > MaxClipboardFileBytes {
			failure = apperror.New(apperror.CodeResourceExhausted, "Attachments must be at most 5 MiB")
		}
		if failure == nil {
			key := clipboardFileKey(request, index)
			mediaType := http.DetectContentType(file.Data)
			known, lookupErr := b.inspectClipboardItem(ctx, request.WorkspaceID, key)
			if lookupErr != nil {
				return result, lookupErr
			}
			if len(known.Images)+len(known.Attachments) > 0 {
				digest := sha256.Sum256(file.Data)
				sha := hex.EncodeToString(digest[:])
				matches := len(known.Images) == 1 && len(known.Attachments) == 0 && known.Images[0].SHA256 == sha && known.Images[0].Name == file.Name && known.Images[0].MIMEType == mediaType
				matches = matches || (len(known.Attachments) == 1 && len(known.Images) == 0 && known.Attachments[0].SHA256 == sha && known.Attachments[0].Name == file.Name && !strings.HasPrefix(mediaType, "image/"))
				if matches {
					result.Images = append(result.Images, known.Images...)
					result.Attachments = append(result.Attachments, known.Attachments...)
					continue
				}
				result.Rejected = append(result.Rejected, ClipboardFileRejection{Name: name, Code: string(apperror.CodeConflict), Message: "This paste key already identifies different saved file bytes or metadata"})
				continue
			}
			if strings.HasPrefix(mediaType, "image/") {
				if mediaType != "image/png" && mediaType != "image/jpeg" && mediaType != "image/webp" {
					failure = apperror.New(apperror.CodeInvalidArgument, "Image attachments support PNG, JPEG and WebP")
				} else {
					var image domain.WorkspaceImage
					image, failure = b.clipboardImages.SaveWorkspaceImage(ctx, request.WorkspaceID, key, mediaType, file.Name, file.Data)
					if failure == nil {
						result.Images = append(result.Images, image)
					}
				}
			} else {
				if extensionType := mime.TypeByExtension(filepath.Ext(file.Name)); extensionType != "" {
					mediaType = extensionType
				}
				if parsed, _, parseErr := mime.ParseMediaType(mediaType); parseErr == nil {
					mediaType = parsed
				}
				var attachment domain.WorkspaceFileAttachment
				attachment, failure = b.clipboardFiles.Import(ctx, application.FileAttachmentImportRequest{
					WorkspaceID: request.WorkspaceID, OperationKey: key, Name: file.Name, MIMEType: mediaType, Data: file.Data})
				if failure == nil {
					result.Attachments = append(result.Attachments, attachment)
				}
			}
		}
		if failure != nil {
			result.Rejected = append(result.Rejected, ClipboardFileRejection{Name: name, Code: string(apperror.CodeOf(failure)), Message: apperror.Normalize(failure).Error()})
		}
	}
	result.BatchComplete = true
	return result, nil
}

// The display label is bounded even when the original filename is rejected.
// Importers still receive the original name and enforce its immutable identity.
func clipboardRejectionName(name string, index int) string {
	if name == "" || !utf8.ValidString(name) || filepath.Base(name) != name || strings.ContainsAny(name, "/\\:") || strings.IndexFunc(name, unicode.IsControl) >= 0 || redact.String(name) != name {
		return fmt.Sprintf("File %d", index+1)
	}
	characters := []rune(name)
	if len(characters) > 160 {
		return string(characters[:159]) + "…"
	}
	return name
}

func validClipboardFilesRequest(request ClipboardFilesRequest) bool {
	return request.Version == ClipboardFilesProtocolVersion && validWorkspaceIdentity(request.WorkspaceID) && len(request.OperationKey) >= 16 && len(request.OperationKey) <= 256 && domain.ValidAgentID(request.OperationKey)
}
func clipboardFileKey(request ClipboardFilesRequest, index int) string {
	return runmutation.Fingerprint(ClipboardFilesProtocolVersion, request.WorkspaceID, request.OperationKey, fmt.Sprint(index))
}

// InspectClipboardFiles only reads previously saved original-key receipts. A
// batch manifest is not persisted, so even four receipts cannot certify the
// original paste completed; this method never reads a changed clipboard.
func (b *DesktopBridge) InspectClipboardFiles(request ClipboardFilesRequest) (ClipboardFilesResult, error) {
	result := ClipboardFilesResult{Version: ClipboardFilesProtocolVersion, WorkspaceID: request.WorkspaceID, Status: "unsupported", Images: []domain.WorkspaceImage{}, Attachments: []domain.WorkspaceFileAttachment{}, Rejected: []ClipboardFileRejection{}}
	if !validClipboardFilesRequest(request) {
		return result, apperror.New(apperror.CodeInvalidArgument, "Clipboard observation request is invalid")
	}
	if b == nil || b.clipboardFiles == nil || b.clipboardImages == nil {
		return result, nil
	}
	if b.bootstrap.ReadToken == "" || b.workspaceResolver == nil {
		return result, apperror.New(apperror.CodePolicyDenied, "Clipboard attachment observations are unavailable")
	}
	ctx, err := b.lifecycleContext()
	if err != nil {
		return result, err
	}
	workspace, err := b.workspaceResolver.ResolveWorkspace(ctx, request.WorkspaceID)
	if err != nil {
		return result, apperror.Normalize(err)
	}
	if workspace.ID != request.WorkspaceID {
		return result, apperror.New(apperror.CodeConflict, "Clipboard workspace binding changed")
	}
	result.Status = "unknown"
	for index := 0; index < MaxClipboardFiles; index++ {
		item, err := b.inspectClipboardItem(ctx, request.WorkspaceID, clipboardFileKey(request, index))
		if err != nil {
			return result, err
		}
		result.Images = append(result.Images, item.Images...)
		result.Attachments = append(result.Attachments, item.Attachments...)
	}
	if len(result.Images)+len(result.Attachments) > 0 {
		result.Status = "partial"
	}
	return result, nil
}
func (b *DesktopBridge) inspectClipboardItem(ctx context.Context, workspaceID, key string) (ClipboardFilesResult, error) {
	var result ClipboardFilesResult
	image, found, err := b.clipboardImages.GetWorkspaceImageByOperationKey(ctx, workspaceID, key)
	if err != nil {
		return result, apperror.Normalize(err)
	}
	if found {
		result.Images = append(result.Images, image)
	}
	attachment, err := b.clipboardFiles.Inspect(ctx, workspaceID, key)
	if err != nil {
		return result, apperror.Normalize(err)
	}
	if attachment.State == "stored" && attachment.Attachment != nil {
		result.Attachments = append(result.Attachments, *attachment.Attachment)
	} else if attachment.State != "not_received" || attachment.Attachment != nil {
		return result, apperror.New(apperror.CodeConflict, "Clipboard attachment observation is invalid")
	}
	return result, nil
}

func (c *ControlPlane) ClipboardFileImporter() ClipboardFileImporter {
	if c == nil || c.stateStore == nil {
		return nil
	}
	return application.NewFileAttachmentService(c.stateStore)
}
func (c *ControlPlane) ClipboardImageImporter() ClipboardImageImporter {
	if c == nil || c.stateStore == nil {
		return nil
	}
	return c.stateStore
}
