package webevidence

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"cyberagent-workbench/internal/redact"
)

const (
	ProtocolVersion                         = "web_evidence.v1"
	SearchProtocolVersion                   = "web_search.v1"
	FetchProtocolVersion                    = "web_fetch.v1"
	CitationProtocolVersion                 = "web_citation.v1"
	OperationProtocolVersion                = "web_evidence_operation.v1"
	ProviderGroundedCitationProtocolVersion = "provider_grounded_citation.v1"
	ProviderGroundedProvenance              = "provider_grounded"

	PublicHTTPSTarget = "public_https"
	MaxQueryRunes     = 1024
	MaxClaimRunes     = 2048
	MaxSnippetBytes   = 4096
	MaxBodyBytes      = 128 * 1024
	MaxSources        = 10
)

type SourceState string

const (
	SourceDiscovered SourceState = "discovered"
	SourceFetched    SourceState = "fetched"
	SourcePartial    SourceState = "partial"
	SourceBlocked    SourceState = "blocked"
	SourceFailed     SourceState = "failed"
)

func (s SourceState) Valid() bool {
	switch s {
	case SourceDiscovered, SourceFetched, SourcePartial, SourceBlocked, SourceFailed:
		return true
	default:
		return false
	}
}

// Source is the stable, Run-local identity of one canonical public URL. Search
// creates only a discovered source; it never makes the snippet citeable.
type Source struct {
	ProtocolVersion string      `json:"protocol_version"`
	ID              string      `json:"id"`
	RunID           string      `json:"run_id"`
	MissionID       string      `json:"mission_id"`
	WorkspaceID     string      `json:"workspace_id,omitempty"`
	CanonicalURL    string      `json:"canonical_url"`
	Title           string      `json:"title,omitempty"`
	Snippet         string      `json:"snippet,omitempty"`
	Provider        string      `json:"provider"`
	State           SourceState `json:"state"`
	DiscoveredAt    time.Time   `json:"discovered_at"`
	Fingerprint     string      `json:"fingerprint"`
}

func (s Source) Validate() error {
	if s.ProtocolVersion != ProtocolVersion || !validIdentity(s.ID) ||
		!validIdentity(s.RunID) || !validIdentity(s.MissionID) ||
		(s.WorkspaceID != "" && !validIdentity(s.WorkspaceID)) {
		return errors.New("web source identity is invalid")
	}
	canonical, err := CanonicalizePublicHTTPSURL(s.CanonicalURL)
	if err != nil || canonical != s.CanonicalURL {
		return errors.New("web source canonical URL is invalid")
	}
	if !validBoundedText(s.Title, 1024, true) ||
		!validBoundedBytes(s.Snippet, MaxSnippetBytes) ||
		!validBoundedText(s.Provider, 256, false) || !s.State.Valid() ||
		s.DiscoveredAt.IsZero() || !validDigest(s.Fingerprint) {
		return errors.New("web source metadata is invalid")
	}
	expected, err := sourceFingerprint(s)
	if err != nil || expected != s.Fingerprint {
		return errors.New("web source fingerprint mismatch")
	}
	return nil
}

type Snapshot struct {
	ProtocolVersion string `json:"protocol_version"`
	ID              string `json:"id"`
	SourceID        string `json:"source_id"`
	RunID           string `json:"run_id"`
	MissionID       string `json:"mission_id"`
	RequestedURL    string `json:"requested_url"`
	FinalURL        string `json:"final_url"`
	// HTTPStatus is optional for snapshots written before the status became a
	// durable fact. New fetches always record the observed final response code.
	HTTPStatus  int         `json:"http_status,omitempty"`
	Title       string      `json:"title,omitempty"`
	Byline      string      `json:"byline,omitempty"`
	PublishedAt string      `json:"published_at,omitempty"`
	FetchedAt   time.Time   `json:"fetched_at"`
	StaleAt     time.Time   `json:"stale_at"`
	Digest      string      `json:"digest"`
	MIME        string      `json:"mime"`
	Charset     string      `json:"charset,omitempty"`
	Body        string      `json:"body,omitempty"`
	State       SourceState `json:"state"`
	Truncated   bool        `json:"truncated"`
	Robots      string      `json:"robots"`
	ErrorCode   string      `json:"error_code,omitempty"`
	Redirects   int         `json:"redirects"`
	Provider    string      `json:"provider"`
	Fingerprint string      `json:"fingerprint"`
}

func (s Snapshot) Validate() error {
	for _, value := range []string{s.ID, s.SourceID, s.RunID, s.MissionID} {
		if !validIdentity(value) {
			return errors.New("web snapshot identity is invalid")
		}
	}
	if s.ProtocolVersion != ProtocolVersion || !s.State.Valid() ||
		s.State == SourceDiscovered || s.FetchedAt.IsZero() ||
		s.StaleAt.Before(s.FetchedAt) || !validDigest(s.Digest) ||
		!validDigest(s.Fingerprint) || s.Redirects < 0 ||
		s.Redirects > DefaultRedirectLimit ||
		!validBoundedText(s.Provider, 256, false) ||
		(s.HTTPStatus != 0 && (s.HTTPStatus < 100 || s.HTTPStatus > 599)) ||
		!validBoundedText(s.MIME, 256, false) ||
		!validBoundedText(s.Charset, 128, true) ||
		!validBoundedText(s.Title, 1024, true) || !validBoundedText(s.Byline, 512, true) ||
		!validBoundedText(s.PublishedAt, 128, true) || !validBoundedText(s.Robots, 64, false) ||
		!validBoundedText(s.ErrorCode, 128, true) || !validBoundedBytes(s.Body, MaxBodyBytes) {
		return errors.New("web snapshot metadata is invalid")
	}
	for _, rawURL := range []string{s.RequestedURL, s.FinalURL} {
		canonical, err := CanonicalizePublicHTTPSURL(rawURL)
		if err != nil || canonical != rawURL {
			return errors.New("web snapshot URL is invalid")
		}
	}
	switch s.State {
	case SourceFetched:
		if s.Truncated || s.ErrorCode != "" ||
			(s.HTTPStatus != 0 && (s.HTTPStatus < 200 || s.HTTPStatus >= 300)) {
			return errors.New("fetched web snapshot state is inconsistent")
		}
	case SourcePartial:
		if !s.Truncated || s.ErrorCode != "" ||
			(s.HTTPStatus != 0 && (s.HTTPStatus < 200 || s.HTTPStatus >= 300)) {
			return errors.New("partial web snapshot state is inconsistent")
		}
	case SourceBlocked, SourceFailed:
		if s.ErrorCode == "" || s.Body != "" || s.Truncated || s.Digest != DigestBytes(nil) {
			return errors.New("failed web snapshot state is inconsistent")
		}
	}
	expected, err := snapshotFingerprint(s)
	if err != nil || expected != s.Fingerprint {
		return errors.New("web snapshot fingerprint mismatch")
	}
	return nil
}

func (s Snapshot) Stale(at time.Time) bool {
	return !at.UTC().Before(s.StaleAt.UTC())
}

type Citation struct {
	ProtocolVersion string    `json:"protocol_version"`
	ID              string    `json:"id"`
	RunID           string    `json:"run_id"`
	SourceID        string    `json:"source_id"`
	SnapshotID      string    `json:"snapshot_id"`
	Claim           string    `json:"claim"`
	SpanStart       int       `json:"span_start,omitempty"`
	SpanEnd         int       `json:"span_end,omitempty"`
	URL             string    `json:"url"`
	Title           string    `json:"title,omitempty"`
	FetchedAt       time.Time `json:"fetched_at"`
	StaleAt         time.Time `json:"stale_at"`
	Digest          string    `json:"digest"`
	Partial         bool      `json:"partial"`
	Stale           bool      `json:"stale"`
	CreatedAt       time.Time `json:"created_at"`
	Fingerprint     string    `json:"fingerprint"`
}

// ProviderGroundedCitation is a durable source reference returned by a
// qualified Provider-hosted search tool. It is deliberately not a Snapshot and
// carries no claim that Traverse fetched, parsed, or independently verified the
// target page. It is persisted inside the immutable web_search operation.
type ProviderGroundedCitation struct {
	ProtocolVersion       string    `json:"protocol_version"`
	ID                    string    `json:"id"`
	RunID                 string    `json:"run_id"`
	SourceID              string    `json:"source_id"`
	URL                   string    `json:"url"`
	Title                 string    `json:"title,omitempty"`
	Provider              string    `json:"provider"`
	ProviderBinding       string    `json:"provider_binding"`
	Provenance            string    `json:"provenance"`
	SearchedAt            time.Time `json:"searched_at"`
	ProviderQualified     bool      `json:"provider_qualified"`
	LocallyVerified       bool      `json:"locally_verified"`
	Untrusted             bool      `json:"untrusted"`
	InstructionAuthorized bool      `json:"instruction_authorized"`
	Fingerprint           string    `json:"fingerprint"`
}

func (c ProviderGroundedCitation) Validate() error {
	for _, value := range []string{c.ID, c.RunID, c.SourceID} {
		if !validIdentity(value) {
			return errors.New("provider-grounded citation identity is invalid")
		}
	}
	canonical, err := CanonicalizePublicHTTPSURL(c.URL)
	if c.ProtocolVersion != ProviderGroundedCitationProtocolVersion || err != nil ||
		canonical != c.URL || !validBoundedText(c.Title, 1024, true) ||
		!validBoundedText(c.Provider, 256, false) ||
		!validDigest(c.ProviderBinding) || c.Provenance != ProviderGroundedProvenance ||
		c.SearchedAt.IsZero() || !c.ProviderQualified || c.LocallyVerified ||
		!c.Untrusted || c.InstructionAuthorized || !validDigest(c.Fingerprint) {
		return errors.New("provider-grounded citation metadata is invalid")
	}
	expected, err := providerGroundedCitationFingerprint(c)
	if err != nil || expected != c.Fingerprint {
		return errors.New("provider-grounded citation fingerprint mismatch")
	}
	return nil
}

func (c Citation) Validate() error {
	for _, value := range []string{c.ID, c.RunID, c.SourceID, c.SnapshotID} {
		if !validIdentity(value) {
			return errors.New("web citation identity is invalid")
		}
	}
	canonical, err := CanonicalizePublicHTTPSURL(c.URL)
	if c.ProtocolVersion != CitationProtocolVersion || err != nil || canonical != c.URL ||
		!validBoundedText(c.Claim, MaxClaimRunes, false) ||
		!validBoundedText(c.Title, 1024, true) || c.SpanStart < 0 || c.SpanEnd < 0 ||
		(c.SpanEnd == 0 && c.SpanStart != 0) ||
		(c.SpanEnd != 0 && c.SpanEnd <= c.SpanStart) || c.FetchedAt.IsZero() ||
		c.StaleAt.Before(c.FetchedAt) ||
		c.CreatedAt.IsZero() || c.CreatedAt.Before(c.FetchedAt) ||
		c.Stale != !c.CreatedAt.UTC().Before(c.StaleAt.UTC()) ||
		!validDigest(c.Digest) || !validDigest(c.Fingerprint) {
		return errors.New("web citation metadata is invalid")
	}
	expected, err := citationFingerprint(c)
	if err != nil || expected != c.Fingerprint {
		return errors.New("web citation fingerprint mismatch")
	}
	return nil
}

type SourcePresentation struct {
	SourceID              string      `json:"source_id"`
	URL                   string      `json:"url"`
	Title                 string      `json:"title,omitempty"`
	State                 SourceState `json:"state"`
	Provider              string      `json:"provider"`
	DiscoveredAt          time.Time   `json:"discovered_at"`
	Citeable              bool        `json:"citeable"`
	Untrusted             bool        `json:"untrusted"`
	InstructionAuthorized bool        `json:"instruction_authorized"`
}

type SnapshotPresentation struct {
	SourceID              string    `json:"source_id"`
	SnapshotID            string    `json:"snapshot_id"`
	URL                   string    `json:"url"`
	Title                 string    `json:"title,omitempty"`
	Status                string    `json:"status"`
	FetchedAt             time.Time `json:"fetched_at"`
	StaleAt               time.Time `json:"stale_at"`
	Digest                string    `json:"digest"`
	MIME                  string    `json:"mime"`
	Provider              string    `json:"provider"`
	Robots                string    `json:"robots"`
	Partial               bool      `json:"partial"`
	Stale                 bool      `json:"stale"`
	Truncated             bool      `json:"truncated"`
	Citeable              bool      `json:"citeable"`
	Untrusted             bool      `json:"untrusted"`
	InstructionAuthorized bool      `json:"instruction_authorized"`
}

type CitationPresentation struct {
	CitationID            string    `json:"citation_id"`
	SourceID              string    `json:"source_id"`
	SnapshotID            string    `json:"snapshot_id"`
	URL                   string    `json:"url"`
	Title                 string    `json:"title,omitempty"`
	Status                string    `json:"status"`
	FetchedAt             time.Time `json:"fetched_at"`
	StaleAt               time.Time `json:"stale_at"`
	Digest                string    `json:"digest"`
	Partial               bool      `json:"partial"`
	Stale                 bool      `json:"stale"`
	Citeable              bool      `json:"citeable"`
	Untrusted             bool      `json:"untrusted"`
	InstructionAuthorized bool      `json:"instruction_authorized"`
}

type Inventory struct {
	ProtocolVersion       string                 `json:"protocol_version"`
	RunID                 string                 `json:"run_id"`
	Sources               []SourcePresentation   `json:"sources"`
	Snapshots             []SnapshotPresentation `json:"snapshots"`
	Citations             []CitationPresentation `json:"citations"`
	Untrusted             bool                   `json:"untrusted"`
	InstructionAuthorized bool                   `json:"instruction_authorized"`
}

func PresentSource(source Source) SourcePresentation {
	return SourcePresentation{SourceID: source.ID, URL: source.CanonicalURL,
		Title: redact.String(source.Title), State: source.State,
		Provider:     redact.String(source.Provider),
		DiscoveredAt: source.DiscoveredAt, Citeable: false, Untrusted: true}
}

func PresentSnapshot(snapshot Snapshot, at time.Time) SnapshotPresentation {
	stale := snapshot.Stale(at)
	citeable := snapshot.State == SourceFetched || snapshot.State == SourcePartial
	status := string(snapshot.State)
	if stale && citeable {
		status = "stale"
	}
	return SnapshotPresentation{SourceID: snapshot.SourceID, SnapshotID: snapshot.ID,
		URL: snapshot.FinalURL, Title: redact.String(snapshot.Title), Status: status,
		FetchedAt: snapshot.FetchedAt, StaleAt: snapshot.StaleAt,
		Digest: snapshot.Digest, MIME: snapshot.MIME,
		Provider: redact.String(snapshot.Provider),
		Robots:   snapshot.Robots, Partial: snapshot.State == SourcePartial,
		Stale: stale, Truncated: snapshot.Truncated, Citeable: citeable, Untrusted: true}
}

func PresentCitation(citation Citation, at time.Time) CitationPresentation {
	stale := !at.UTC().Before(citation.StaleAt.UTC())
	status := "fetched"
	if citation.Partial {
		status = "partial"
	}
	if stale {
		status = "stale"
	}
	return CitationPresentation{CitationID: citation.ID, SourceID: citation.SourceID,
		SnapshotID: citation.SnapshotID, URL: citation.URL,
		Title:  redact.String(citation.Title),
		Status: status, FetchedAt: citation.FetchedAt, StaleAt: citation.StaleAt,
		Digest: citation.Digest, Partial: citation.Partial, Stale: stale,
		Citeable: true, Untrusted: true}
}

type Operation struct {
	ProtocolVersion    string          `json:"protocol_version"`
	KeyDigest          string          `json:"key_digest"`
	RequestFingerprint string          `json:"request_fingerprint"`
	RunID              string          `json:"run_id"`
	ToolName           string          `json:"tool_name"`
	Response           json.RawMessage `json:"response"`
	CreatedAt          time.Time       `json:"created_at"`
}

func (o Operation) Validate() error {
	if o.ProtocolVersion != OperationProtocolVersion || !validDigest(o.KeyDigest) ||
		!validDigest(o.RequestFingerprint) || !validIdentity(o.RunID) ||
		!validBoundedText(o.ToolName, 64, false) || len(o.Response) == 0 ||
		len(o.Response) > 512*1024 || !json.Valid(o.Response) || o.CreatedAt.IsZero() {
		return errors.New("web evidence operation is invalid")
	}
	return nil
}

type SearchStub struct {
	SourceID                 string                    `json:"source_id"`
	CanonicalURL             string                    `json:"canonical_url"`
	Title                    string                    `json:"title,omitempty"`
	Snippet                  string                    `json:"snippet,omitempty"`
	Rank                     int                       `json:"rank"`
	Provider                 string                    `json:"provider"`
	Fetched                  bool                      `json:"fetched"`
	Citeable                 bool                      `json:"citeable"`
	Provenance               string                    `json:"provenance,omitempty"`
	LocallyVerified          bool                      `json:"locally_verified"`
	Untrusted                bool                      `json:"untrusted"`
	InstructionAuthorized    bool                      `json:"instruction_authorized"`
	ProviderGroundedCitation *ProviderGroundedCitation `json:"provider_grounded_citation,omitempty"`
}

type SearchResult struct {
	ProtocolVersion string       `json:"protocol_version"`
	Query           string       `json:"query"`
	Sources         []SearchStub `json:"sources"`
	// Provider, SearchPolicy, and SelectionReason are the observed decision for
	// this exact Run operation. They deliberately do not promote a credential-
	// and-authority-scoped probe into a Registry-wide capability boolean.
	Provider        string    `json:"provider"`
	SearchPolicy    string    `json:"search_policy"`
	SelectionReason string    `json:"selection_reason"`
	SearchedAt      time.Time `json:"searched_at"`
	Replayed        bool      `json:"replayed"`
}

func (r SearchResult) HasProviderGroundedCitations() bool {
	if len(r.Sources) == 0 {
		return false
	}
	for _, source := range r.Sources {
		if !source.Citeable || source.Provenance != ProviderGroundedProvenance ||
			source.ProviderGroundedCitation == nil ||
			source.ProviderGroundedCitation.Validate() != nil {
			return false
		}
	}
	return true
}

type FetchResult struct {
	ProtocolVersion string   `json:"protocol_version"`
	Source          Source   `json:"source"`
	Snapshot        Snapshot `json:"snapshot"`
	Replayed        bool     `json:"replayed"`
}

type CitationResult struct {
	ProtocolVersion string   `json:"protocol_version"`
	Citation        Citation `json:"citation"`
	Replayed        bool     `json:"replayed"`
}

func SealSource(source Source) (Source, error) {
	source.ProtocolVersion = ProtocolVersion
	fingerprint, err := sourceFingerprint(source)
	if err != nil {
		return Source{}, err
	}
	source.Fingerprint = fingerprint
	return source, source.Validate()
}

func SealSnapshot(snapshot Snapshot) (Snapshot, error) {
	snapshot.ProtocolVersion = ProtocolVersion
	fingerprint, err := snapshotFingerprint(snapshot)
	if err != nil {
		return Snapshot{}, err
	}
	snapshot.Fingerprint = fingerprint
	return snapshot, snapshot.Validate()
}

func SealCitation(citation Citation) (Citation, error) {
	citation.ProtocolVersion = CitationProtocolVersion
	fingerprint, err := citationFingerprint(citation)
	if err != nil {
		return Citation{}, err
	}
	citation.Fingerprint = fingerprint
	return citation, citation.Validate()
}

func SealProviderGroundedCitation(citation ProviderGroundedCitation) (
	ProviderGroundedCitation, error,
) {
	citation.ProtocolVersion = ProviderGroundedCitationProtocolVersion
	fingerprint, err := providerGroundedCitationFingerprint(citation)
	if err != nil {
		return ProviderGroundedCitation{}, err
	}
	citation.Fingerprint = fingerprint
	return citation, citation.Validate()
}

func StableSourceID(runID, canonicalURL string) string {
	return "web-source-" + digestText(runID + "\x00" + canonicalURL)[:24]
}

func StableSnapshotID(sourceID, digest string, fetchedAt time.Time) string {
	return "web-snapshot-" + digestText(sourceID + "\x00" + digest + "\x00" +
		fetchedAt.UTC().Format(time.RFC3339Nano))[:24]
}

func StableCitationID(runID, operationDigest string) string {
	return "web-citation-" + digestText(runID + "\x00" + operationDigest)[:24]
}

func StableProviderGroundedCitationID(runID, operationDigest,
	sourceID string,
) string {
	return "provider-citation-" + digestText(runID + "\x00" + operationDigest + "\x00" +
		sourceID)[:24]
}

func DigestBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func RequestFingerprint(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return DigestBytes(raw), nil
}

func OperationKeyDigest(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || !utf8.ValidString(value) || len([]byte(value)) > 4096 {
		return "", errors.New("web evidence operation key is required and must be bounded UTF-8")
	}
	return digestText(value), nil
}

func ScopedOperationKeyDigest(runID, value string) (string, error) {
	if !validIdentity(runID) {
		return "", errors.New("web evidence operation Run identity is invalid")
	}
	value = strings.TrimSpace(value)
	if value == "" || !utf8.ValidString(value) || len([]byte(value)) > 4096 {
		return "", errors.New("web evidence operation key is required and must be bounded UTF-8")
	}
	return digestText(runID + "\x00" + value), nil
}

func sourceFingerprint(source Source) (string, error) {
	copyValue := source
	copyValue.Fingerprint = ""
	return fingerprintJSON(copyValue)
}

func snapshotFingerprint(snapshot Snapshot) (string, error) {
	copyValue := snapshot
	copyValue.Fingerprint = ""
	return fingerprintJSON(copyValue)
}

func citationFingerprint(citation Citation) (string, error) {
	copyValue := citation
	copyValue.Fingerprint = ""
	return fingerprintJSON(copyValue)
}

func providerGroundedCitationFingerprint(citation ProviderGroundedCitation) (string, error) {
	copyValue := citation
	copyValue.Fingerprint = ""
	return fingerprintJSON(copyValue)
}

func fingerprintJSON(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode web evidence fingerprint: %w", err)
	}
	return DigestBytes(raw), nil
}

func digestText(value string) string { return DigestBytes([]byte(value)) }

func validIdentity(value string) bool {
	if value == "" || strings.TrimSpace(value) != value || !utf8.ValidString(value) ||
		utf8.RuneCountInString(value) > 256 {
		return false
	}
	for _, current := range value {
		if unicode.IsControl(current) {
			return false
		}
	}
	return true
}

func validDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && strings.ToLower(value) == value
}

func validBoundedText(value string, maxRunes int, allowEmpty bool) bool {
	if !utf8.ValidString(value) || strings.TrimSpace(value) != value ||
		utf8.RuneCountInString(value) > maxRunes {
		return false
	}
	for _, current := range value {
		if unicode.IsControl(current) {
			return false
		}
	}
	return allowEmpty || value != ""
}

func validBoundedBytes(value string, max int) bool {
	if !utf8.ValidString(value) || len([]byte(value)) > max {
		return false
	}
	for _, current := range value {
		if unicode.IsControl(current) && current != '\n' && current != '\t' {
			return false
		}
	}
	return true
}
