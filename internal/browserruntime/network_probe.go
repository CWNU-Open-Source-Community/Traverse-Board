package browserruntime

import (
	"context"
	"errors"
	"strings"
	"time"
)

const (
	BrowserNetworkProbeConfirmation = "RUN-LOCAL-WFP-BROWSER-PROBE"
	BrowserNetworkProbeTimeout      = 12 * time.Second
)

type BrowserNetworkProbeRequest struct {
	ID                string `json:"id"`
	CollectorIdentity string `json:"collector_identity"`
	Confirmation      string `json:"-"`
}

// RunBrowserNetworkContainmentProbe launches one accepted browser executable
// against local canaries only. The platform implementation must not use CDP,
// a proxy, DNS, repository content, or a caller-provided URL.
func RunBrowserNetworkContainmentProbe(ctx context.Context,
	identity BrowserExecutableIdentity, acceptance BrowserAcceptanceCandidate,
	request BrowserNetworkProbeRequest,
) (BrowserNetworkContainmentEvidence, error) {
	if ctx == nil || ctx.Err() != nil || !validPlanIdentity(request.ID) ||
		!validPlanIdentity(request.CollectorIdentity) ||
		request.Confirmation != BrowserNetworkProbeConfirmation {
		return BrowserNetworkContainmentEvidence{}, errors.New(
			"browser network probe requires an exact local-production confirmation")
	}
	if err := ValidateBrowserAcceptanceCandidate(acceptance, identity); err != nil {
		return BrowserNetworkContainmentEvidence{}, err
	}
	if acceptance.Decision != BrowserAcceptanceAccepted || !acceptance.ReviewEligible {
		return BrowserNetworkContainmentEvidence{}, errors.New(
			"browser network probe requires an accepted executable identity")
	}
	if err := revalidateAcceptedBrowserExecutable(identity, acceptance); err != nil {
		return BrowserNetworkContainmentEvidence{}, err
	}
	report := runPlatformBrowserNetworkContainmentProbe(ctx, identity, request)
	if report.ID != request.ID || report.CollectorIdentity != request.CollectorIdentity ||
		report.CDPUsed || report.Synthetic || report.StartedAt.IsZero() ||
		!report.CompletedAt.After(report.StartedAt) {
		return BrowserNetworkContainmentEvidence{}, errors.New(
			"browser network probe adapter returned an invalid report")
	}
	if strings.TrimSpace(report.FailureCode) == "" && !browserNetworkProbePassed(report) {
		return BrowserNetworkContainmentEvidence{}, errors.New(
			"browser network probe adapter omitted its failure code")
	}
	return BuildBrowserNetworkContainmentEvidence(identity, acceptance, report)
}

func browserNetworkProbePassed(report BrowserNetworkProbeReport) bool {
	return report.Adapter == WindowsWFPBrowserContainmentAdapterName &&
		report.DynamicSessionObserved && report.AtomicInstallObserved &&
		report.ExactTargetObserved && report.WrongPortDenied &&
		report.WrongLoopbackAddressDenied && report.NonLoopbackAddressDenied &&
		report.IPv6Denied && report.RuleCleanupObserved && !report.CDPUsed &&
		!report.Synthetic && report.Production && strings.TrimSpace(report.FailureCode) == ""
}

func finishBrowserNetworkProbeReport(report BrowserNetworkProbeReport,
	failureCode string,
) BrowserNetworkProbeReport {
	if strings.TrimSpace(report.FailureCode) == "" {
		report.FailureCode = strings.TrimSpace(failureCode)
	}
	report.CompletedAt = time.Now().UTC()
	if !report.CompletedAt.After(report.StartedAt) {
		report.CompletedAt = report.StartedAt.Add(time.Nanosecond)
	}
	return report
}

func validBrowserNetworkProbeToken(value string) bool {
	if len(value) != 24 {
		return false
	}
	for _, character := range value {
		if !((character >= '0' && character <= '9') ||
			(character >= 'a' && character <= 'f')) {
			return false
		}
	}
	return true
}
