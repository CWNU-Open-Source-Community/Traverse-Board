package uievidence

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"cyberagent-workbench/internal/redact"
)

func NewAttempt(manifest Manifest, operationKey string, now time.Time) (Attempt, error) {
	if err := manifest.Validate(); err != nil {
		return Attempt{}, err
	}
	operationKey = strings.TrimSpace(operationKey)
	if operationKey == "" || len([]byte(operationKey)) > 1024 ||
		!utf8.ValidString(operationKey) || strings.ContainsRune(operationKey, 0) ||
		redact.String(operationKey) != operationKey {
		return Attempt{}, errors.New("UI evidence operation key is invalid")
	}
	now = now.UTC()
	if now.IsZero() || now.Before(manifest.CreatedAt) {
		return Attempt{}, errors.New("UI evidence attempt time is invalid")
	}
	operation, _ := OperationIdentity(manifest.RunID, operationKey)
	attempt := Attempt{ProtocolVersion: AttemptProtocolVersion, Manifest: manifest,
		OperationDigest: operation, RequestFingerprint: manifest.Fingerprint,
		Status: StatusNotRun, FailureStage: FailureNone, Version: 1,
		CreatedAt: now, UpdatedAt: now}
	return attempt, attempt.Validate()
}

func OperationIdentity(runID, operationKey string) (string, string) {
	digest := sha256.Sum256([]byte(ProtocolVersion + "\x00" + runID + "\x00" + operationKey))
	encoded := hex.EncodeToString(digest[:])
	return encoded, "ui-attempt-" + encoded[:24]
}

func StartAttempt(attempt Attempt, now time.Time) (Attempt, error) {
	if err := attempt.Validate(); err != nil || attempt.Status != StatusNotRun {
		return Attempt{}, errors.New("only not-run UI evidence can start")
	}
	now = now.UTC()
	if now.Before(attempt.UpdatedAt) {
		return Attempt{}, errors.New("UI evidence start time moved backwards")
	}
	attempt.Status = StatusRunning
	attempt.StartedAt = &now
	attempt.UpdatedAt = now
	attempt.Version++
	return attempt, attempt.Validate()
}

func CompleteAttempt(attempt Attempt, status Status, stage FailureStage,
	code, message string, diagnostics DiagnosticsSummary, cleanup CleanupReceipt,
	artifactCount int, artifactBytes int64, now time.Time,
) (Attempt, error) {
	if err := attempt.Validate(); err != nil || attempt.Status != StatusRunning {
		return Attempt{}, errors.New("only running UI evidence can complete")
	}
	if !status.Terminal() {
		return Attempt{}, errors.New("UI evidence completion status is not terminal")
	}
	now = now.UTC()
	if now.Before(attempt.UpdatedAt) {
		return Attempt{}, errors.New("UI evidence completion time moved backwards")
	}
	attempt.Status = status
	attempt.FailureStage = stage
	attempt.FailureCode = sanitizeFailureText(code, 128, false)
	attempt.FailureMessage = sanitizeFailureText(message, 2048, true)
	attempt.Diagnostics = diagnostics
	attempt.Cleanup = cleanup
	attempt.ArtifactCount = artifactCount
	attempt.ArtifactBytes = artifactBytes
	attempt.CompletedAt = &now
	attempt.UpdatedAt = now
	attempt.Version++
	return attempt, attempt.Validate()
}

func InterruptAttempt(attempt Attempt, artifactCount int, artifactBytes int64,
	now time.Time,
) (Attempt, error) {
	if attempt.Status != StatusRunning {
		return Attempt{}, errors.New("only running UI evidence can be interrupted")
	}
	return CompleteAttempt(attempt, StatusInterrupted, FailureCleanup,
		"runtime_restarted", "UI evidence owner restarted before cleanup could be proven",
		attempt.Diagnostics, attempt.Cleanup, artifactCount, artifactBytes, now)
}

func sanitizeFailureText(value string, maxBytes int, allowLines bool) string {
	value = redact.String(strings.TrimSpace(value))
	if !utf8.ValidString(value) {
		value = strings.ToValidUTF8(value, "�")
	}
	value = strings.Map(func(current rune) rune {
		if current == 0 || unicode.IsControl(current) &&
			!(allowLines && (current == '\n' || current == '\r' || current == '\t')) {
			return ' '
		}
		return current
	}, value)
	value = strings.TrimSpace(value)
	for len([]byte(value)) > maxBytes {
		_, size := utf8.DecodeLastRuneInString(value)
		value = value[:len(value)-size]
	}
	return strings.TrimSpace(value)
}
