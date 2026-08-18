package contextmgr

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"cyberagent-workbench/internal/redact"
)

const (
	MemoryProtocolVersion   = "context_memory.v1"
	LocalUserMemoryScope    = "local-user"
	MaxMemoryTitleBytes     = 256
	MaxMemoryContentBytes   = 16 * 1024
	MaxMemoryReferences     = 32
	MaxMemoryReferenceBytes = 512
	MaxMemoryListItems      = 500
)

type MemoryScope string

const (
	MemoryScopeUser    MemoryScope = "user"
	MemoryScopeProject MemoryScope = "project"
)

type MemoryStatus string

const (
	MemoryStatusActive   MemoryStatus = "active"
	MemoryStatusDisabled MemoryStatus = "disabled"
)

type Memory struct {
	ID              string       `json:"id"`
	ProtocolVersion string       `json:"protocol_version"`
	Scope           MemoryScope  `json:"scope"`
	ScopeID         string       `json:"scope_id"`
	Title           string       `json:"title"`
	Content         string       `json:"content"`
	ContentSHA256   string       `json:"content_sha256"`
	Status          MemoryStatus `json:"status"`
	SourceKind      string       `json:"source_kind"`
	SourceRef       string       `json:"source_ref,omitempty"`
	References      []string     `json:"references"`
	RetentionUntil  *time.Time   `json:"retention_until,omitempty"`
	Redacted        bool         `json:"redacted"`
	CreatedBy       string       `json:"created_by"`
	UpdatedBy       string       `json:"updated_by"`
	Version         int64        `json:"version"`
	CreatedAt       time.Time    `json:"created_at"`
	UpdatedAt       time.Time    `json:"updated_at"`
}

type CreateMemoryRequest struct {
	ID               string
	Scope            MemoryScope
	ScopeID          string
	Title            string
	Content          string
	SourceKind       string
	SourceRef        string
	References       []string
	RetentionUntil   *time.Time
	RequestedBy      string
	RedactSensitive  bool
	ExplicitOperator bool
}

type UpdateMemoryRequest struct {
	Title           *string
	Content         *string
	SourceRef       *string
	References      *[]string
	RetentionUntil  **time.Time
	Status          *MemoryStatus
	RequestedBy     string
	RedactSensitive bool
	ExpectedVersion int64
}

type MemoryFilter struct {
	Scope           MemoryScope
	ScopeID         string
	IncludeDisabled bool
	IncludeExpired  bool
	Limit           int
}

func (f MemoryFilter) Validate() error {
	if f.Scope != MemoryScopeUser && f.Scope != MemoryScopeProject {
		return errors.New("long-term memory filter scope is invalid")
	}
	if (f.Scope == MemoryScopeUser && f.ScopeID != LocalUserMemoryScope) ||
		(f.Scope == MemoryScopeProject && !validMemoryIdentity(f.ScopeID)) {
		return errors.New("long-term memory filter scope id is invalid")
	}
	if f.Limit < 0 || f.Limit > MaxMemoryListItems {
		return fmt.Errorf("long-term memory filter limit must be between 0 and %d", MaxMemoryListItems)
	}
	return nil
}

func PrepareMemory(request CreateMemoryRequest, now time.Time) (Memory, error) {
	if !request.ExplicitOperator {
		return Memory{}, errors.New("long-term memory creation requires an explicit operator action")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	references, err := NormalizeMemoryReferences(request.References)
	if err != nil {
		return Memory{}, err
	}
	memory := Memory{
		ID: strings.TrimSpace(request.ID), ProtocolVersion: MemoryProtocolVersion,
		Scope: request.Scope, ScopeID: strings.TrimSpace(request.ScopeID),
		Title: strings.TrimSpace(request.Title), Content: strings.TrimSpace(request.Content),
		Status: MemoryStatusActive, SourceKind: strings.TrimSpace(request.SourceKind),
		SourceRef:      strings.TrimSpace(request.SourceRef),
		References:     references,
		RetentionUntil: cloneMemoryTime(request.RetentionUntil),
		CreatedBy:      strings.TrimSpace(request.RequestedBy),
		UpdatedBy:      strings.TrimSpace(request.RequestedBy), Version: 1,
		CreatedAt: now, UpdatedAt: now,
	}
	if memory.SourceKind == "" {
		memory.SourceKind = "operator_explicit"
	}
	if err := rejectMemorySource(memory.SourceKind, memory.SourceRef); err != nil {
		return Memory{}, err
	}
	safe := redact.String(memory.Content)
	if safe != memory.Content {
		if !request.RedactSensitive {
			return Memory{}, errors.New("long-term memory contains sensitive material; retry only with explicit redaction")
		}
		memory.Content = safe
		memory.Redacted = true
	}
	memory.ContentSHA256 = memoryContentDigest(memory.Content)
	if err := memory.ValidateAt(now); err != nil {
		return Memory{}, err
	}
	return memory, nil
}

func UpdateMemory(existing Memory, request UpdateMemoryRequest, now time.Time) (Memory, error) {
	if err := existing.ValidateAt(existing.UpdatedAt); err != nil {
		return Memory{}, fmt.Errorf("stored long-term memory is invalid: %w", err)
	}
	if request.ExpectedVersion <= 0 || request.ExpectedVersion != existing.Version {
		return Memory{}, errors.New("long-term memory version changed concurrently")
	}
	actor := strings.TrimSpace(request.RequestedBy)
	if !validMemoryActor(actor) {
		return Memory{}, errors.New("long-term memory editor must be an explicit operator identity")
	}
	updated := existing
	if request.Title != nil {
		updated.Title = strings.TrimSpace(*request.Title)
	}
	if request.Content != nil {
		content := strings.TrimSpace(*request.Content)
		safe := redact.String(content)
		if safe != content && !request.RedactSensitive {
			return Memory{}, errors.New("long-term memory contains sensitive material; retry only with explicit redaction")
		}
		updated.Content = safe
		updated.Redacted = existing.Redacted || safe != content
		updated.ContentSHA256 = memoryContentDigest(updated.Content)
	}
	if request.SourceRef != nil {
		updated.SourceRef = strings.TrimSpace(*request.SourceRef)
	}
	if request.References != nil {
		references, err := NormalizeMemoryReferences(*request.References)
		if err != nil {
			return Memory{}, err
		}
		updated.References = references
	}
	if request.RetentionUntil != nil {
		updated.RetentionUntil = cloneMemoryTime(*request.RetentionUntil)
	}
	if request.Status != nil {
		updated.Status = *request.Status
	}
	if err := rejectMemorySource(updated.SourceKind, updated.SourceRef); err != nil {
		return Memory{}, err
	}
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	updated.UpdatedBy = actor
	updated.Version++
	updated.UpdatedAt = now
	if err := updated.ValidateAt(now); err != nil {
		return Memory{}, err
	}
	return updated, nil
}

func (m Memory) ValidateAt(now time.Time) error {
	if strings.TrimSpace(m.ID) == "" || len(m.ID) > 256 || strings.ContainsRune(m.ID, 0) {
		return errors.New("long-term memory id is invalid")
	}
	if m.ProtocolVersion != MemoryProtocolVersion {
		return fmt.Errorf("unsupported long-term memory protocol %q", m.ProtocolVersion)
	}
	switch m.Scope {
	case MemoryScopeUser:
		if m.ScopeID != LocalUserMemoryScope {
			return errors.New("user memory must use the local user scope")
		}
	case MemoryScopeProject:
		if !validMemoryIdentity(m.ScopeID) {
			return errors.New("project memory requires a normalized Workspace id")
		}
	default:
		return errors.New("long-term memory scope is invalid")
	}
	if m.Title == "" || len([]byte(m.Title)) > MaxMemoryTitleBytes ||
		!validMemoryText(m.Title) {
		return errors.New("long-term memory title is invalid")
	}
	if m.Content == "" || len([]byte(m.Content)) > MaxMemoryContentBytes ||
		!validMemoryText(m.Content) || m.ContentSHA256 != memoryContentDigest(m.Content) {
		return errors.New("long-term memory content or digest is invalid")
	}
	if m.Status != MemoryStatusActive && m.Status != MemoryStatusDisabled {
		return errors.New("long-term memory status is invalid")
	}
	if err := rejectMemorySource(m.SourceKind, m.SourceRef); err != nil {
		return err
	}
	if len(m.References) > MaxMemoryReferences {
		return fmt.Errorf("long-term memory references exceed %d", MaxMemoryReferences)
	}
	previous := ""
	for _, reference := range m.References {
		if reference != strings.TrimSpace(reference) || reference == "" ||
			len([]byte(reference)) > MaxMemoryReferenceBytes || !validMemoryText(reference) ||
			memorySensitivePath(reference) {
			return errors.New("long-term memory reference is invalid or sensitive")
		}
		if previous != "" && previous >= reference {
			return errors.New("long-term memory references must be sorted and unique")
		}
		previous = reference
	}
	if m.RetentionUntil != nil {
		retention := m.RetentionUntil.UTC()
		if retention.IsZero() || retention.Before(m.CreatedAt) ||
			retention.After(m.CreatedAt.AddDate(10, 0, 0)) {
			return errors.New("long-term memory retention must be within ten years of creation")
		}
	}
	if !validMemoryActor(m.CreatedBy) || !validMemoryActor(m.UpdatedBy) || m.Version < 1 ||
		m.CreatedAt.IsZero() || m.UpdatedAt.IsZero() || m.UpdatedAt.Before(m.CreatedAt) {
		return errors.New("long-term memory audit metadata is invalid")
	}
	_ = now // Expiration is state, not structural invalidity.
	return nil
}

func (m Memory) Expired(at time.Time) bool {
	if m.RetentionUntil == nil {
		return false
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	return !at.UTC().Before(m.RetentionUntil.UTC())
}

func NormalizeMemoryReferences(values []string) ([]string, error) {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, errors.New("long-term memory reference cannot be empty")
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out, nil
}

func ValidateMemoryActor(value string) error {
	if !validMemoryActor(strings.TrimSpace(value)) {
		return errors.New("long-term memory actor must be an explicit operator identity")
	}
	return nil
}

func rejectMemorySource(kind, reference string) error {
	kind = strings.TrimSpace(kind)
	reference = strings.TrimSpace(reference)
	switch kind {
	case "operator_explicit", "operator_import_explicit":
	default:
		return errors.New("long-term memory source must be an explicit operator action")
	}
	if len([]byte(reference)) > MaxMemoryReferenceBytes || !validMemoryText(reference) ||
		memorySensitivePath(reference) {
		return errors.New("long-term memory source reference is invalid or sensitive")
	}
	return nil
}

func memorySensitivePath(value string) bool {
	value = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), "\\", "/"))
	for _, marker := range []string{
		"/.env", ".env", "credentials", "credential", "secrets", "secret",
		"id_rsa", "id_ed25519", ".pem", ".pfx", ".key", "terminal-input",
		"terminal_input", "stdin", "keystrokes",
	} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

func validMemoryActor(value string) bool {
	value = strings.TrimSpace(value)
	if !validMemoryIdentity(value) {
		return false
	}
	switch strings.ToLower(value) {
	case "agent", "assistant", "llm", "model", "repository", "repo", "tool",
		"supervisor", "run_supervisor", "automatic", "auto", "system":
		return false
	default:
		return true
	}
}

func validMemoryIdentity(value string) bool {
	if value == "" || value != strings.TrimSpace(value) || len([]byte(value)) > 256 ||
		strings.ContainsRune(value, 0) || !utf8.ValidString(value) {
		return false
	}
	for _, current := range value {
		if unicode.IsControl(current) || unicode.IsSpace(current) {
			return false
		}
	}
	return true
}

func validMemoryText(value string) bool {
	if !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return false
	}
	for _, current := range value {
		if unicode.IsControl(current) && current != '\n' && current != '\t' {
			return false
		}
	}
	return true
}

func memoryContentDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func cloneMemoryTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := value.UTC()
	return &copy
}
