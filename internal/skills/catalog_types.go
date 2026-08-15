package skills

import (
	"time"

	"cyberagent-workbench/internal/domain"
)

// CatalogPublisher is a trusted (or revoked) publisher identity bound to an
// Ed25519 public key. Trust is always an explicit operator/admin decision.
type CatalogPublisher struct {
	Fingerprint string
	Name        string
	Team        string
	PublicKey   string
	TrustClass  string
	TrustedBy   string
	TrustedAt   *time.Time
	RevokedBy   string
	RevokedAt   *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// CatalogPin is the active-version and enable/disable decision for one skill
// and surface.
type CatalogPin struct {
	SkillName string
	Surface   domain.ExecutionSurface
	Version   string
	Enabled   bool
	PinnedBy  string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// CatalogImport is one pinned import ledger row: the resolved source, its pin
// (URL SHA-256 or Git commit), the signed archive digest, the installed
// package fingerprint, and the publisher fingerprint when signed.
type CatalogImport struct {
	ID                   string
	SourceKind           string
	Source               string
	Pin                  string
	ArchiveSHA256        string
	PackageFingerprint   string
	PublisherFingerprint string
	ImportedBy           string
	CreatedAt            time.Time
}

// CatalogAuditEvent is one append-only catalog audit row.
type CatalogAuditEvent struct {
	Sequence    int64
	EventType   string
	Subject     string
	PayloadJSON string
	Actor       string
	CreatedAt   time.Time
}
