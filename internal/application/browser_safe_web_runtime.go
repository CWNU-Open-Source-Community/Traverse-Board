package application

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/browserruntime"
)

// BrowserSafeWebRuntimeStore supplies the durable production containment
// evidence and operator review the readiness judgment consumes.
type BrowserSafeWebRuntimeStore interface {
	LoadLatestBrowserNetworkEvidence(ctx context.Context,
		executableIdentityFingerprint string) (browserruntime.BrowserNetworkContainmentEvidence, error)
	LoadBrowserNetworkReview(ctx context.Context,
		evidenceFingerprint string) (browserruntime.BrowserNetworkContainmentReview, error)
}

// BrowserSafeWebRuntimeService turns durable network containment facts into a
// process-local Safe Web readiness judgment. It never starts a browser and never
// widens launch authority; it only decides whether the entry may be offered.
type BrowserSafeWebRuntimeService struct {
	store BrowserSafeWebRuntimeStore
}

func NewBrowserSafeWebRuntimeService(store BrowserSafeWebRuntimeStore) *BrowserSafeWebRuntimeService {
	return &BrowserSafeWebRuntimeService{store: store}
}

// Readiness loads the latest containment evidence and its operator review for
// the exact accepted browser and collapses them into a fail-closed readiness
// receipt. A missing evidence or review is not an error: it yields a
// Ready=false receipt so callers can surface the precise blocking reason.
func (s *BrowserSafeWebRuntimeService) Readiness(ctx context.Context,
	identity browserruntime.BrowserExecutableIdentity,
	acceptance browserruntime.BrowserAcceptanceCandidate,
) (browserruntime.BrowserSafeWebReadiness, error) {
	if s == nil || s.store == nil {
		return browserruntime.BrowserSafeWebReadiness{}, apperror.New(
			apperror.CodeFailedPrecondition, "browser safe-web runtime store is required")
	}
	if err := browserruntime.ValidateBrowserExecutableIdentity(identity); err != nil {
		return browserruntime.BrowserSafeWebReadiness{}, apperror.Wrap(
			apperror.CodeInvalidArgument, "browser executable identity is invalid", err)
	}
	if err := browserruntime.ValidateBrowserAcceptanceCandidate(acceptance, identity); err != nil {
		return browserruntime.BrowserSafeWebReadiness{}, apperror.Wrap(
			apperror.CodeInvalidArgument, "browser acceptance candidate is invalid", err)
	}
	evidence, err := s.store.LoadLatestBrowserNetworkEvidence(ctx, identity.Fingerprint)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return browserruntime.BrowserSafeWebReadiness{}, apperror.Normalize(err)
	}
	review := browserruntime.BrowserNetworkContainmentReview{}
	if evidence.Fingerprint != "" {
		review, err = s.store.LoadBrowserNetworkReview(ctx, evidence.Fingerprint)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return browserruntime.BrowserSafeWebReadiness{}, apperror.Normalize(err)
		}
	}
	readiness, err := browserruntime.BuildBrowserSafeWebReadiness(evidence, review,
		identity, acceptance, time.Now().UTC())
	if err != nil {
		return browserruntime.BrowserSafeWebReadiness{}, apperror.Wrap(
			apperror.CodeInvalidArgument, "browser safe-web readiness judgment failed", err)
	}
	return readiness, nil
}
