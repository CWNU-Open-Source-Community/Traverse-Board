// Package fileattachment imports immutable external bytes without writing the
// project or claiming that stored binary documents have been parsed.
package fileattachment

import (
	"crypto/sha256"
	"encoding/hex"
	"mime"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/redact"
)

const MaxBytes = 5 * 1024 * 1024
const MaxTextBytes = 64 * 1024

func digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

func Validate(name, mediaType string, data []byte) (domain.WorkspaceFileAttachment, string, error) {
	var value domain.WorkspaceFileAttachment
	if len(data) > MaxBytes {
		return value, "", apperror.New(apperror.CodeResourceExhausted, "File attachment exceeds 5 MiB")
	}
	if name == "" || name != strings.TrimSpace(name) || !utf8.ValidString(name) || len([]rune(name)) > 160 || strings.ContainsAny(name, `/\:`) || name == "." || name == ".." || strings.IndexFunc(name, unicode.IsControl) >= 0 || redact.String(name) != name {
		return value, "", apperror.New(apperror.CodeInvalidArgument, "File attachment requires a bounded filename without a path or sensitive material")
	}
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	parsed, params, err := mime.ParseMediaType(mediaType)
	if err != nil || len(params) > 0 || parsed != mediaType || len(mediaType) > 128 || redact.String(mediaType) != mediaType {
		return value, "", apperror.New(apperror.CodeInvalidArgument, "File attachment MIME type is invalid")
	}
	value = domain.WorkspaceFileAttachment{Name: name, MIMEType: mediaType, SHA256: digest(data), ByteSize: len(data), Readability: "stored_only", Reason: "binary_or_unsupported_format"}
	// An ASCII PDF or ZIP header is not evidence of a parsed text document.
	blockedExt := strings.Contains("|.pdf|.doc|.docx|.xls|.xlsx|.ppt|.pptx|.odt|.ods|.odp|.rtf|.zip|.gz|.tar|.7z|.rar|.png|.jpg|.jpeg|.webp|.svg|.exe|.dll|.wasm|", "|"+strings.ToLower(path.Ext(name))+"|")
	blockedMIME := mediaType == "application/pdf" || strings.Contains(mediaType, "officedocument") || strings.Contains(mediaType, "msword") || strings.Contains(mediaType, "ms-excel") || strings.Contains(mediaType, "ms-powerpoint") || strings.Contains(mediaType, "zip") || strings.HasPrefix(mediaType, "image/") || strings.HasPrefix(mediaType, "audio/") || strings.HasPrefix(mediaType, "video/")
	if blockedExt || blockedMIME || strings.HasPrefix(string(data), "%PDF-") || strings.HasPrefix(string(data), "PK\x03\x04") {
		return value, "", nil
	}
	if !utf8.Valid(data) {
		value.Reason = "not_utf8"
		return value, "", nil
	}
	if strings.IndexFunc(string(data), func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' }) >= 0 {
		value.Reason = "binary_control_bytes"
		return value, "", nil
	}
	// Redact the full bounded upload before truncation, so a secret crossing the
	// excerpt boundary cannot escape a whole-token redaction rule.
	clean := redact.Text(string(data))
	text := clean.Text
	value.Readability = "text"
	value.Reason = ""
	value.Redacted = len(clean.Findings) > 0
	if len(text) > MaxTextBytes {
		text = text[:MaxTextBytes]
		for !utf8.ValidString(text) {
			text = text[:len(text)-1]
		}
		value.Readability = "partial_text"
		value.Reason = "text_excerpt_limit"
	}
	value.TextBytes = len(text)
	value.TextSHA256 = digest([]byte(text))
	return value, text, nil
}
