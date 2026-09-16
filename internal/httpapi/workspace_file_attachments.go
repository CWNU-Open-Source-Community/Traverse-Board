package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/fileattachment"
	"mime"
)

const WorkspaceFileUploadVersion = "workspace_file_upload.v1"

type WorkspaceFileUploadRequestView struct {
	Version    string `json:"version"`
	Name       string `json:"name"`
	MIMEType   string `json:"mime_type"`
	DataBase64 string `json:"data_base64"`
}

type WorkspaceFileAttachmentView struct {
	Attachment domain.WorkspaceFileAttachment `json:"attachment"`
}

type workspaceFileAttachmentStore interface {
	SaveWorkspaceFileAttachment(context.Context, string, string, string, string, []byte) (domain.WorkspaceFileAttachment, error)
	GetWorkspaceFileAttachment(context.Context, string, string) (domain.WorkspaceFileAttachment, []byte, error)
	InspectWorkspaceFileAttachmentRequest(context.Context, string, string) (domain.FileAttachmentObservation, error)
}

func matchWorkspaceFileAttachmentPath(path string) (workspaceID, attachmentID string, content, matched bool) {
	parts := strings.Split(strings.TrimPrefix(path, "/api/v1/"), "/")
	if !strings.HasPrefix(path, "/api/v1/") || len(parts) < 3 || len(parts) > 5 || parts[0] != "workspaces" || parts[2] != "file-attachments" {
		return
	}
	workspaceID = parts[1]
	if len(parts) >= 4 {
		attachmentID = parts[3]
		if attachmentID == "" {
			return "", "", false, false
		}
	}
	if len(parts) == 5 {
		if parts[4] != "content" {
			return "", "", false, false
		}
		content = true
	}
	return workspaceID, attachmentID, content, true
}

func (a *API) serveWorkspaceFileAttachment(writer http.ResponseWriter, request *http.Request, requestID, workspaceID, attachmentID string, content bool) {
	store, ok := a.store.(workspaceFileAttachmentStore)
	if !ok {
		a.writeError(writer, requestID, apperror.New(apperror.CodeNotFound, "File attachment attachments are unavailable"), 0)
		return
	}
	if err := validatePathIdentity(workspaceID); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	if err := rejectQuery(request.URL.Query()); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	if attachmentID != "" {
		if !a.authorized(request, a.tokenHash) {
			a.writeError(writer, requestID, apperror.New(apperror.CodePolicyDenied, "valid read bearer authorization is required"), http.StatusUnauthorized)
			return
		}
		if request.Method != http.MethodGet {
			writer.Header().Set("Allow", "GET")
			a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument, "File attachment reads only support GET"), http.StatusMethodNotAllowed)
			return
		}
		if request.ContentLength != 0 || len(request.TransferEncoding) != 0 {
			a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument, "File attachment reads cannot contain a body"), 0)
			return
		}
		if err := validatePathIdentity(attachmentID); err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		if attachmentID == "request" {
			if content {
				a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument, "File lookup cannot request content"), 0)
				return
			}
			key, err := sessionControlIdempotencyKey(request.Header, "file upload lookup")
			if err != nil {
				a.writeError(writer, requestID, err, 0)
				return
			}
			value, err := store.InspectWorkspaceFileAttachmentRequest(request.Context(), workspaceID, key)
			if err != nil {
				a.writeError(writer, requestID, err, 0)
				return
			}
			a.writeSuccess(writer, requestID, value, nil)
			return
		}
		image, data, err := store.GetWorkspaceFileAttachment(request.Context(), workspaceID, attachmentID)
		if err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		if !content {
			a.writeSuccess(writer, requestID, WorkspaceFileAttachmentView{Attachment: image}, nil)
			return
		}
		writer.Header().Set("Content-Type", "application/octet-stream")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": image.Name}))
		writer.Header().Set("Content-Length", strconv.Itoa(image.ByteSize))
		writer.Header().Set("ETag", `"`+image.SHA256+`"`)
		writer.Header().Set("X-Cyberagent-Content-SHA256", image.SHA256)
		writer.Header().Set("Cache-Control", "no-store")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write(data)
		return
	}
	if !a.runCreationEnabled || !a.sessionMessageEnabled {
		a.writeError(writer, requestID, apperror.New(apperror.CodeNotFound, "File attachment uploads are unavailable"), 0)
		return
	}
	if !a.authorizeThreadControl(writer, request, requestID) {
		return
	}
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", "POST")
		a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument, "File attachment uploads only support POST"), http.StatusMethodNotAllowed)
		return
	}
	key, err := sessionControlIdempotencyKey(request.Header, "file upload")
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	if err := validateJSONContentType(request.Header); err != nil {
		a.writeError(writer, requestID, err, http.StatusUnsupportedMediaType)
		return
	}
	body, err := readBoundedRequestBody(request, int64(base64.StdEncoding.EncodedLen(fileattachment.MaxBytes)+4096))
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	var view WorkspaceFileUploadRequestView
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if !utf8.Valid(body) || rejectDuplicateJSONObjectFields(body, "file upload") != nil || decoder.Decode(&view) != nil || ensureJSONEOF(decoder) != nil || view.Version != WorkspaceFileUploadVersion {
		a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument, "File attachment upload payload is invalid"), 0)
		return
	}
	data, err := base64.StdEncoding.Strict().DecodeString(view.DataBase64)
	if err != nil || base64.StdEncoding.EncodeToString(data) != view.DataBase64 {
		a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument, "File attachment data must be canonical base64"), 0)
		return
	}
	image, err := application.NewFileAttachmentService(store).Import(request.Context(), application.FileAttachmentImportRequest{WorkspaceID: workspaceID, OperationKey: key, MIMEType: view.MIMEType, Name: view.Name, Data: data})
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	a.writeSuccess(writer, requestID, WorkspaceFileAttachmentView{Attachment: image}, nil)
}
