package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"cyberagent-workbench/internal/browserruntime"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
)

const fullCDPAuditSource = "browser_runtime"

// RecordFullCDPSessionOpened appends the bounded, metadata-only audit fact for
// a process-local Full CDP runtime. Live handles and reconstructable process or
// transport identity deliberately remain outside SQLite.
func (s *SQLiteStore) RecordFullCDPSessionOpened(ctx context.Context,
	runID, runtimeID, fullCDPSessionID, runSessionID, product, channel,
	targetOrigin string,
	startedAt, expiresAt time.Time,
) error {
	if s == nil || s.db == nil {
		return errors.New("sqlite store is not open")
	}
	if ctx == nil {
		return errors.New("full CDP session open context is required")
	}
	if !validFullCDPAuditIdentity(runID) || !validFullCDPAuditIdentity(runtimeID) ||
		!validFullCDPAuditIdentity(fullCDPSessionID) ||
		!validFullCDPAuditIdentity(runSessionID) {
		return errors.New("full CDP session open identity is invalid")
	}
	if !validFullCDPBrowserSelection(product, channel) {
		return errors.New("full CDP session browser selection is invalid")
	}
	if !validFullCDPTargetOrigin(targetOrigin) {
		return errors.New("full CDP session target origin is invalid")
	}
	startedAt, expiresAt = startedAt.UTC(), expiresAt.UTC()
	if startedAt.IsZero() || !expiresAt.After(startedAt) {
		return errors.New("full CDP session open timestamps are invalid")
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	missionID, status, err := fullCDPAuditRunBindingTx(ctx, tx, runID, runSessionID)
	if err != nil {
		return err
	}
	if !domain.CanChangeRunBrowserCDPPermission(status) {
		return errors.New("full CDP session cannot open for a terminal Run")
	}
	event, err := events.New(runID, missionID, events.FullCDPSessionOpenedEvent,
		fullCDPAuditSource, runtimeID, map[string]any{
			"runtime_id": runtimeID, "full_cdp_session_id": fullCDPSessionID,
			"run_session_id":  runSessionID,
			"browser_product": product, "browser_channel": channel,
			"target_origin": targetOrigin, "started_at": startedAt,
			"expires_at": expiresAt, "redacted": true,
			"runtime_recoverable": false, "raw_cdp_included": false,
		})
	if err != nil {
		return err
	}
	event.EventID = fullCDPAuditEventID("opened", runtimeID, fullCDPSessionID)
	event.CreatedAt = startedAt
	if existing, found, lookupErr := getRunEventByEventID(ctx, tx, event.EventID); lookupErr != nil {
		return lookupErr
	} else if found {
		if !sameFullCDPAuditEvent(existing, event) {
			return errors.New("full CDP session open audit id was reused")
		}
		return tx.Commit()
	}
	if _, err := insertRunEventTx(ctx, tx, event); err != nil {
		return err
	}
	return tx.Commit()
}

// RecordFullCDPSessionClosed appends only the cleanup outcome from the
// validated receipt. The receipt's authorization/process/profile fingerprints
// are intentionally not copied into the Run event.
func (s *SQLiteStore) RecordFullCDPSessionClosed(ctx context.Context,
	runID, runtimeID, fullCDPSessionID, runSessionID, reason string,
	receipt browserruntime.FullCDPRuntimeReceipt,
) error {
	if s == nil || s.db == nil {
		return errors.New("sqlite store is not open")
	}
	if ctx == nil {
		return errors.New("full CDP session close context is required")
	}
	if !validFullCDPAuditIdentity(runID) || !validFullCDPAuditIdentity(runtimeID) ||
		!validFullCDPAuditIdentity(fullCDPSessionID) ||
		!validFullCDPAuditIdentity(runSessionID) || !validFullCDPCloseReason(reason) {
		return errors.New("full CDP session close identity or reason is invalid")
	}
	if err := browserruntime.ValidateFullCDPRuntimeReceipt(receipt); err != nil {
		return err
	}
	if receipt.RunID != runID || receipt.RuntimeID != runtimeID ||
		receipt.SessionID != runSessionID {
		return errors.New("full CDP runtime receipt does not match the audited session")
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	missionID, _, err := fullCDPAuditRunBindingTx(ctx, tx, runID, runSessionID)
	if err != nil {
		return err
	}
	event, err := events.New(runID, missionID, events.FullCDPSessionClosedEvent,
		fullCDPAuditSource, runtimeID, map[string]any{
			"runtime_id": runtimeID, "full_cdp_session_id": fullCDPSessionID,
			"run_session_id": runSessionID,
			"close_reason":   reason, "cdp_closed": receipt.CDPClosed,
			"process_tree_quiescent": receipt.ProcessTreeQuiescent,
			"profile_released":       receipt.ProfileReleased,
			"profile_cleaned":        receipt.ProfileCleaned,
			"succeeded":              receipt.Succeeded,
			"recovery_required":      receipt.RecoveryRequired,
			"failure_code":           receipt.FailureCode,
			"started_at":             receipt.StartedAt.UTC(),
			"completed_at":           receipt.CompletedAt.UTC(),
			"redacted":               true, "raw_cdp_included": false,
		})
	if err != nil {
		return err
	}
	event.EventID = fullCDPAuditEventID("closed", runtimeID, fullCDPSessionID)
	event.CreatedAt = receipt.CompletedAt.UTC()
	if existing, found, lookupErr := getRunEventByEventID(ctx, tx, event.EventID); lookupErr != nil {
		return lookupErr
	} else if found {
		if !sameFullCDPAuditEvent(existing, event) {
			return errors.New("full CDP session close audit id was reused")
		}
		return tx.Commit()
	}
	if _, err := insertRunEventTx(ctx, tx, event); err != nil {
		return err
	}
	return tx.Commit()
}

func fullCDPAuditEventID(kind, runtimeID, fullCDPSessionID string) string {
	digest := sha256.Sum256([]byte(kind + "\x00" + runtimeID + "\x00" + fullCDPSessionID))
	return "evt-full-cdp-" + kind + "-" + hex.EncodeToString(digest[:])
}

func sameFullCDPAuditEvent(left, right events.Event) bool {
	var leftPayload, rightPayload any
	if json.Unmarshal([]byte(left.PayloadJSON), &leftPayload) != nil ||
		json.Unmarshal([]byte(right.PayloadJSON), &rightPayload) != nil {
		return false
	}
	leftEncoded, leftErr := json.Marshal(leftPayload)
	rightEncoded, rightErr := json.Marshal(rightPayload)
	return leftErr == nil && rightErr == nil &&
		left.EventID == right.EventID && left.Version == right.Version &&
		left.RunID == right.RunID && left.MissionID == right.MissionID &&
		left.Type == right.Type && left.Source == right.Source &&
		left.SubjectID == right.SubjectID &&
		string(leftEncoded) == string(rightEncoded) &&
		left.CreatedAt.Equal(right.CreatedAt)
}

func fullCDPAuditRunBindingTx(ctx context.Context, tx *sql.Tx,
	runID, sessionID string,
) (string, domain.RunStatus, error) {
	var missionID string
	var status domain.RunStatus
	err := tx.QueryRowContext(ctx, `SELECT mission_id, status FROM runs
		WHERE id = ? AND session_id = ?`, runID, sessionID).Scan(&missionID, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", errors.New("full CDP session is not bound to the exact Run")
	}
	return missionID, status, err
}

func validFullCDPAuditIdentity(value string) bool {
	if value == "" || value != strings.TrimSpace(value) || !utf8.ValidString(value) ||
		utf8.RuneCountInString(value) > 128 || strings.ContainsRune(value, 0) {
		return false
	}
	for _, current := range value {
		if unicode.IsControl(current) {
			return false
		}
	}
	return true
}

func validFullCDPBrowserSelection(product, channel string) bool {
	if product != string(browserruntime.BrowserProductChrome) &&
		product != string(browserruntime.BrowserProductEdge) {
		return false
	}
	return channel == string(browserruntime.BrowserChannelStable) ||
		channel == string(browserruntime.BrowserChannelBeta) ||
		channel == string(browserruntime.BrowserChannelDev) ||
		channel == string(browserruntime.BrowserChannelCanary)
}

func validFullCDPTargetOrigin(value string) bool {
	if value == "" || value != strings.TrimSpace(value) || len(value) > 2048 ||
		!utf8.ValidString(value) {
		return false
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.Hostname() == "" || parsed.User != nil || parsed.Path != "" ||
		parsed.RawPath != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	port, err := strconv.Atoi(parsed.Port())
	address, addressErr := netip.ParseAddr(parsed.Hostname())
	return err == nil && port > 0 && port <= 65535 && addressErr == nil &&
		address.Unmap().IsLoopback()
}

func validFullCDPCloseReason(value string) bool {
	switch value {
	case "operator_closed", "expired", "permission_revoked", "process_exited",
		"run_terminal", "desktop_shutdown", "open_failed":
		return true
	default:
		return false
	}
}
