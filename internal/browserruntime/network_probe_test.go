package browserruntime

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestBrowserNetworkProbeRequiresExactConfirmationBeforeRevalidation(t *testing.T) {
	_, identity, acceptance, _ := browserLaunchFixture(t)
	_, err := RunBrowserNetworkContainmentProbe(context.Background(), identity,
		acceptance, BrowserNetworkProbeRequest{
			ID:                "browser-network-probe-confirmation",
			CollectorIdentity: "browser-network-probe-operator",
			Confirmation:      "yes",
		})
	if err == nil || !strings.Contains(err.Error(), "exact local-production confirmation") {
		t.Fatalf("inexact browser network probe confirmation error=%v", err)
	}
}

func TestBrowserNetworkProbePassPredicateRequiresEveryProductionObservation(t *testing.T) {
	report := BrowserNetworkProbeReport{
		Adapter:                WindowsWFPBrowserContainmentAdapterName,
		DynamicSessionObserved: true, AtomicInstallObserved: true,
		ExactTargetObserved: true, WrongPortDenied: true,
		WrongLoopbackAddressDenied: true, NonLoopbackAddressDenied: true,
		IPv6Denied: true, RuleCleanupObserved: true, Production: true,
	}
	if !browserNetworkProbePassed(report) {
		t.Fatal("complete production browser network probe did not pass")
	}
	report.IPv6Denied = false
	if browserNetworkProbePassed(report) {
		t.Fatal("browser network probe passed without IPv6 default deny")
	}
	report.IPv6Denied = true
	report.CDPUsed = true
	if browserNetworkProbePassed(report) {
		t.Fatal("browser network probe passed after using CDP as evidence")
	}
}

func TestBrowserNetworkProbeReportAlwaysFinishesAfterStart(t *testing.T) {
	startedAt := time.Now().UTC()
	report := finishBrowserNetworkProbeReport(BrowserNetworkProbeReport{
		ID: "browser-network-probe-report", StartedAt: startedAt,
	}, "wfp_adapter_unavailable")
	if !report.CompletedAt.After(report.StartedAt) ||
		report.FailureCode != "wfp_adapter_unavailable" {
		t.Fatalf("unexpected finished browser network report: %#v", report)
	}
}

func TestBrowserNetworkProbeTokensAreLowercaseBoundedHex(t *testing.T) {
	for value, want := range map[string]bool{
		strings.Repeat("a", 24): true,
		strings.Repeat("0", 24): true,
		strings.Repeat("A", 24): false,
		strings.Repeat("a", 23): false,
		strings.Repeat("z", 24): false,
	} {
		if got := validBrowserNetworkProbeToken(value); got != want {
			t.Fatalf("validBrowserNetworkProbeToken(%q)=%t want %t", value, got, want)
		}
	}
}
