package executionauth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"cyberagent-workbench/internal/domain"
)

const (
	TerminalInputLeaseProtocolVersion = "terminal_input_lease.v1"
	DefaultTerminalInputLeaseTTL      = 5 * time.Minute
	MaxTerminalInputLeaseTTL          = 15 * time.Minute
	MinTerminalInputLeaseTTL          = 15 * time.Second
	MaxActiveTerminalInputLeases      = 256
	MaxRevokedTerminalInputLeaseMarks = 256
	terminalLeaseTokenBytes           = 32
)

var (
	ErrLeaseBoundary = errors.New("terminal input lease boundary is invalid")
	ErrLeaseDenied   = errors.New("terminal input lease is denied")
	ErrLeaseExpired  = errors.New("terminal input lease has expired")
	ErrLeaseRevoked  = errors.New("terminal input lease has been revoked")
)

type TerminalInputScope struct {
	WorkspaceID           string
	RunID                 string
	TerminalSessionID     string
	InteractionSnapshotID string
	InteractionRevision   int64
	Mode                  domain.RunExecutionInteractionMode
}

func (s TerminalInputScope) Validate() error {
	for label, value := range map[string]string{
		"Workspace id":            s.WorkspaceID,
		"Run id":                  s.RunID,
		"Terminal id":             s.TerminalSessionID,
		"Interaction snapshot id": s.InteractionSnapshotID,
	} {
		if !domain.ValidAgentID(value) || strings.ContainsRune(value, 0) {
			return fmt.Errorf("%w: %s is invalid", ErrLeaseBoundary, label)
		}
	}
	if s.InteractionRevision <= 0 {
		return fmt.Errorf("%w: interaction revision must be positive",
			ErrLeaseBoundary)
	}
	switch s.Mode {
	case domain.RunExecutionInteractionDebug, domain.RunExecutionInteractionCyber:
		return nil
	default:
		return fmt.Errorf("%w: mode %q cannot receive persistent Agent input",
			ErrLeaseBoundary, s.Mode)
	}
}

type IssueTerminalInputLeaseRequest struct {
	Scope             TerminalInputScope
	RequestedBy       string
	OperatorConfirmed bool
	TTL               time.Duration
}

type TerminalInputLease struct {
	ID              string
	ProtocolVersion string
	Scope           TerminalInputScope
	RequestedBy     string
	IssuedAt        time.Time
	ExpiresAt       time.Time
	Revoked         bool
}

func (l TerminalInputLease) Validate() error {
	if !domain.ValidAgentID(l.ID) || l.ProtocolVersion !=
		TerminalInputLeaseProtocolVersion {
		return ErrLeaseBoundary
	}
	if err := l.Scope.Validate(); err != nil {
		return err
	}
	if !validOperator(l.RequestedBy) || l.IssuedAt.IsZero() ||
		!l.ExpiresAt.After(l.IssuedAt) ||
		l.ExpiresAt.Sub(l.IssuedAt) > MaxTerminalInputLeaseTTL {
		return ErrLeaseBoundary
	}
	return nil
}

type IssuedTerminalInputLease struct {
	Lease TerminalInputLease
	Token string
}

type terminalLeaseEntry struct {
	lease       TerminalInputLease
	tokenDigest string
}

// TerminalInputBroker is intentionally process-local. It never persists bearer
// tokens or leases, so an application restart revokes every Agent-input grant.
type TerminalInputBroker struct {
	mu      sync.Mutex
	entries map[string]terminalLeaseEntry
	byID    map[string]string
	revoked map[string]time.Time
	now     func() time.Time
	random  io.Reader
}

func NewTerminalInputBroker() *TerminalInputBroker {
	return newTerminalInputBroker(time.Now, rand.Reader)
}

func newTerminalInputBroker(now func() time.Time, random io.Reader) *TerminalInputBroker {
	return &TerminalInputBroker{
		entries: make(map[string]terminalLeaseEntry),
		byID:    make(map[string]string),
		revoked: make(map[string]time.Time),
		now:     now,
		random:  random,
	}
}

func (b *TerminalInputBroker) Issue(
	request IssueTerminalInputLeaseRequest,
) (IssuedTerminalInputLease, error) {
	if b == nil || b.now == nil || b.random == nil ||
		b.entries == nil || b.byID == nil || b.revoked == nil {
		return IssuedTerminalInputLease{}, ErrLeaseBoundary
	}
	if err := request.Scope.Validate(); err != nil {
		return IssuedTerminalInputLease{}, err
	}
	request.RequestedBy = strings.TrimSpace(request.RequestedBy)
	if !request.OperatorConfirmed || !validOperator(request.RequestedBy) {
		return IssuedTerminalInputLease{}, ErrLeaseDenied
	}
	if request.TTL == 0 {
		request.TTL = DefaultTerminalInputLeaseTTL
	}
	if request.TTL < MinTerminalInputLeaseTTL ||
		request.TTL > MaxTerminalInputLeaseTTL {
		return IssuedTerminalInputLease{}, fmt.Errorf(
			"%w: TTL must be between %s and %s", ErrLeaseBoundary,
			MinTerminalInputLeaseTTL, MaxTerminalInputLeaseTTL)
	}
	tokenBytes := make([]byte, terminalLeaseTokenBytes)
	if _, err := io.ReadFull(b.random, tokenBytes); err != nil {
		return IssuedTerminalInputLease{}, fmt.Errorf(
			"generate terminal input lease token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	digestBytes := sha256.Sum256([]byte(token))
	digest := hex.EncodeToString(digestBytes[:])
	now := b.now().UTC()
	lease := TerminalInputLease{
		ID:              "terminal-input-" + digest[:24],
		ProtocolVersion: TerminalInputLeaseProtocolVersion,
		Scope:           request.Scope, RequestedBy: request.RequestedBy,
		IssuedAt: now, ExpiresAt: now.Add(request.TTL),
	}
	if err := lease.Validate(); err != nil {
		return IssuedTerminalInputLease{}, err
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	b.pruneExpiredLocked(now)
	if len(b.entries) >= MaxActiveTerminalInputLeases {
		return IssuedTerminalInputLease{}, fmt.Errorf(
			"%w: active lease limit reached", ErrLeaseDenied)
	}
	if _, exists := b.entries[digest]; exists {
		return IssuedTerminalInputLease{}, fmt.Errorf(
			"%w: token collision", ErrLeaseDenied)
	}
	if _, exists := b.revoked[digest]; exists {
		return IssuedTerminalInputLease{}, fmt.Errorf(
			"%w: token collision", ErrLeaseDenied)
	}
	b.entries[digest] = terminalLeaseEntry{lease: lease, tokenDigest: digest}
	b.byID[lease.ID] = digest
	return IssuedTerminalInputLease{Lease: lease, Token: token}, nil
}

func (b *TerminalInputBroker) Authorize(token string,
	scope TerminalInputScope,
) (TerminalInputLease, error) {
	if b == nil || b.now == nil || b.entries == nil || b.revoked == nil {
		return TerminalInputLease{}, ErrLeaseBoundary
	}
	if err := scope.Validate(); err != nil {
		return TerminalInputLease{}, err
	}
	token = strings.TrimSpace(token)
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(decoded) != terminalLeaseTokenBytes {
		return TerminalInputLease{}, ErrLeaseDenied
	}
	digestBytes := sha256.Sum256([]byte(token))
	digest := hex.EncodeToString(digestBytes[:])
	now := b.now().UTC()

	b.mu.Lock()
	defer b.mu.Unlock()
	entry, ok := b.entries[digest]
	if !ok {
		if expiresAt, revoked := b.revoked[digest]; revoked {
			if !now.Before(expiresAt) {
				delete(b.revoked, digest)
				return TerminalInputLease{}, ErrLeaseExpired
			}
			return TerminalInputLease{}, ErrLeaseRevoked
		}
		b.pruneExpiredLocked(now)
		return TerminalInputLease{}, ErrLeaseDenied
	}
	if entry.tokenDigest != digest {
		return TerminalInputLease{}, ErrLeaseDenied
	}
	if !now.Before(entry.lease.ExpiresAt) {
		delete(b.entries, digest)
		delete(b.byID, entry.lease.ID)
		return TerminalInputLease{}, ErrLeaseExpired
	}
	if entry.lease.Scope != scope {
		return TerminalInputLease{}, ErrLeaseDenied
	}
	return entry.lease, nil
}

func (b *TerminalInputBroker) Revoke(leaseID string, requestedBy string,
	operatorConfirmed bool,
) (TerminalInputLease, error) {
	if b == nil || b.entries == nil || b.byID == nil || b.revoked == nil {
		return TerminalInputLease{}, ErrLeaseBoundary
	}
	leaseID = strings.TrimSpace(leaseID)
	requestedBy = strings.TrimSpace(requestedBy)
	if !operatorConfirmed || !domain.ValidAgentID(leaseID) ||
		!validOperator(requestedBy) {
		return TerminalInputLease{}, ErrLeaseDenied
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	digest, ok := b.byID[leaseID]
	if !ok {
		return TerminalInputLease{}, ErrLeaseDenied
	}
	entry := b.entries[digest]
	entry.lease.Revoked = true
	b.revokeEntryLocked(digest, entry)
	return entry.lease, nil
}

func (b *TerminalInputBroker) RevokeRun(runID string) int {
	return b.revokeMatching(func(scope TerminalInputScope) bool {
		return scope.RunID == strings.TrimSpace(runID)
	})
}

func (b *TerminalInputBroker) RevokeTerminal(terminalSessionID string) int {
	return b.revokeMatching(func(scope TerminalInputScope) bool {
		return scope.TerminalSessionID == strings.TrimSpace(terminalSessionID)
	})
}

func (b *TerminalInputBroker) RevokeWorkspace(workspaceID string) int {
	return b.revokeMatching(func(scope TerminalInputScope) bool {
		return scope.WorkspaceID == strings.TrimSpace(workspaceID)
	})
}

func (b *TerminalInputBroker) RevokeAll() int {
	return b.revokeMatching(func(TerminalInputScope) bool { return true })
}

func (b *TerminalInputBroker) revokeMatching(
	match func(TerminalInputScope) bool,
) int {
	if b == nil || b.entries == nil || b.byID == nil ||
		b.revoked == nil || match == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	count := 0
	for digest, entry := range b.entries {
		if !match(entry.lease.Scope) {
			continue
		}
		entry.lease.Revoked = true
		b.revokeEntryLocked(digest, entry)
		count++
	}
	return count
}

func (b *TerminalInputBroker) pruneExpiredLocked(now time.Time) {
	for digest, entry := range b.entries {
		if !now.Before(entry.lease.ExpiresAt) {
			delete(b.entries, digest)
			delete(b.byID, entry.lease.ID)
		}
	}
	for digest, expiresAt := range b.revoked {
		if !now.Before(expiresAt) {
			delete(b.revoked, digest)
		}
	}
}

func (b *TerminalInputBroker) revokeEntryLocked(digest string,
	entry terminalLeaseEntry,
) {
	delete(b.entries, digest)
	delete(b.byID, entry.lease.ID)
	if len(b.revoked) >= MaxRevokedTerminalInputLeaseMarks {
		var oldestDigest string
		var oldestExpiry time.Time
		for candidate, expiresAt := range b.revoked {
			if oldestDigest == "" || expiresAt.Before(oldestExpiry) {
				oldestDigest = candidate
				oldestExpiry = expiresAt
			}
		}
		delete(b.revoked, oldestDigest)
	}
	b.revoked[digest] = entry.lease.ExpiresAt
}

func validOperator(value string) bool {
	if !domain.ValidAgentID(value) || strings.ContainsRune(value, 0) {
		return false
	}
	switch strings.ToLower(value) {
	case "agent", "llm", "model", "repository", "repo", "skill":
		return false
	default:
		return true
	}
}
