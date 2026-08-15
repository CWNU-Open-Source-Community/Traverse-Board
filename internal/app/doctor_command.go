package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/browserruntime"
	"cyberagent-workbench/internal/buildinfo"
)

func (a *App) doctorCommand(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: cyberagent doctor <portable|browser-network-probe|browser-network-review|browser-readiness>")
	}
	switch args[0] {
	case "portable":
		return a.doctorPortableCommand(args[1:])
	case "browser-network-probe":
		return a.doctorBrowserNetworkProbeCommand(ctx, args[1:])
	case "browser-network-review":
		return a.doctorBrowserNetworkReviewCommand(ctx, args[1:])
	case "browser-readiness":
		return a.doctorBrowserReadinessCommand(ctx, args[1:])
	default:
		return errors.New("usage: cyberagent doctor <portable|browser-network-probe|browser-network-review|browser-readiness>")
	}
}

func (a *App) doctorPortableCommand(args []string) error {
	flags := newFlagSet("doctor portable", a.errOut)
	jsonOutput := flags.Bool("json", false, "print the portable build diagnostic as JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("usage: cyberagent doctor portable [--json]")
	}
	diagnostic := buildinfo.PortableDiagnostic()
	if *jsonOutput {
		encoder := json.NewEncoder(a.out)
		encoder.SetIndent("", "  ")
		return encoder.Encode(diagnostic)
	}
	release := diagnostic.Release
	fmt.Fprintf(a.out, "protocol: %s\nversion: %s\nrevision: %s\nsource_date: %s\n",
		diagnostic.ProtocolVersion, release.AppVersion, release.Revision,
		valueOrUnknown(release.SourceDate))
	fmt.Fprintf(a.out, "target: %s/%s\ngo: %s\ncgo: %s\ntrimpath: %t\n",
		release.TargetOS, release.TargetArch, release.GoVersion,
		valueOrUnknown(release.CGOEnabled), release.Trimpath)
	fmt.Fprintf(a.out, "fingerprint: %s\nrelease_ready: %t\nchecks:\n",
		release.BuildFingerprint, diagnostic.ReleaseReady)
	for _, check := range diagnostic.Checks {
		fmt.Fprintf(a.out, "- %s: %s (%s)\n", check.ID, check.Status, check.Detail)
	}
	return nil
}

func (a *App) doctorBrowserNetworkProbeCommand(ctx context.Context,
	args []string,
) error {
	flags := newFlagSet("doctor browser-network-probe", a.errOut)
	product := flags.String("product", string(browserruntime.BrowserProductEdge),
		"accepted browser product: edge, chrome, or chromium")
	collector := flags.String("collector", "", "independent local operator identity")
	confirmation := flags.String("confirm", "", "exact production-probe confirmation")
	jsonOutput := flags.Bool("json", false, "print the bounded probe evidence as JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*collector) == "" ||
		*collector != strings.TrimSpace(*collector) ||
		*confirmation != browserruntime.BrowserNetworkProbeConfirmation {
		return fmt.Errorf("usage: cyberagent doctor browser-network-probe --product <edge|chrome|chromium> --collector <operator> --confirm %s [--json]",
			browserruntime.BrowserNetworkProbeConfirmation)
	}
	selectedProduct, err := parseBrowserNetworkProbeProduct(*product)
	if err != nil {
		return err
	}
	identity, acceptance, err := acceptedBrowserNetworkProbeCandidate(selectedProduct)
	if err != nil {
		return err
	}
	evidence, err := browserruntime.RunBrowserNetworkContainmentProbe(ctx, identity,
		acceptance, browserruntime.BrowserNetworkProbeRequest{
			ID:                "browser-network-probe-" + fmt.Sprintf("%d", time.Now().UTC().UnixNano()),
			CollectorIdentity: *collector, Confirmation: *confirmation,
		})
	if err != nil {
		return err
	}
	if err := a.ensureStore(); err != nil {
		return err
	}
	if _, err := a.store.RecordBrowserNetworkEvidence(ctx, evidence, evidence.ID); err != nil {
		return err
	}
	if *jsonOutput {
		encoder := json.NewEncoder(a.out)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(evidence); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(a.out, "protocol: %s\nadapter: %s\nproduct: %s\npassed: %t\nfailure_code: %s\n",
			evidence.ProtocolVersion, evidence.Adapter, selectedProduct, evidence.Passed,
			valueOrUnknown(evidence.FailureCode))
		fmt.Fprintf(a.out, "dynamic_session: %t\natomic_install: %t\nexact_target: %t\nwrong_port_denied: %t\nwrong_loopback_denied: %t\nnon_loopback_denied: %t\nipv6_denied: %t\nrule_cleanup: %t\ncdp_used: %t\nfingerprint: %s\n",
			evidence.DynamicSessionObserved, evidence.AtomicInstallObserved,
			evidence.ExactTargetObserved, evidence.WrongPortDenied,
			evidence.WrongLoopbackAddressDenied, evidence.NonLoopbackAddressDenied,
			evidence.IPv6Denied, evidence.RuleCleanupObserved, evidence.CDPUsed,
			evidence.Fingerprint)
	}
	if !evidence.Passed {
		return fmt.Errorf("browser network containment probe failed closed: %s",
			evidence.FailureCode)
	}
	return nil
}

func (a *App) doctorBrowserNetworkReviewCommand(ctx context.Context, args []string) error {
	flags := newFlagSet("doctor browser-network-review", a.errOut)
	product := flags.String("product", string(browserruntime.BrowserProductEdge),
		"accepted browser product: edge, chrome, or chromium")
	reviewer := flags.String("reviewer", "", "independent reviewer identity")
	operationKey := flags.String("operation-key", "", "stable review operation key")
	accept := flags.Bool("accept", false, "accept the latest production evidence")
	reject := flags.Bool("reject", false, "reject the latest production evidence")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *accept == *reject ||
		strings.TrimSpace(*reviewer) == "" || strings.TrimSpace(*operationKey) == "" {
		return errors.New("usage: cyberagent doctor browser-network-review --product <edge|chrome|chromium> --reviewer <operator> --operation-key <key> (--accept|--reject)")
	}
	selectedProduct, err := parseBrowserNetworkProbeProduct(*product)
	if err != nil {
		return err
	}
	identity, acceptance, err := acceptedBrowserNetworkProbeCandidate(selectedProduct)
	if err != nil {
		return err
	}
	if err := a.ensureStore(); err != nil {
		return err
	}
	evidence, err := a.store.LoadLatestBrowserNetworkEvidence(ctx, identity.Fingerprint)
	if err != nil {
		return err
	}
	if !evidence.Passed {
		return fmt.Errorf("latest browser network evidence did not pass: %s", evidence.FailureCode)
	}
	now := time.Now().UTC()
	review, err := browserruntime.BuildBrowserNetworkContainmentReview(evidence, identity,
		acceptance, "browser-network-review-"+fmt.Sprintf("%d", now.UnixNano()),
		strings.TrimSpace(*reviewer), *accept, now)
	if err != nil {
		return err
	}
	record, err := a.store.RecordBrowserNetworkReview(ctx, review, *operationKey)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.out, "evidence: %s\naccepted: %t\nreason: %s\nreplayed: %t\nreview_fingerprint: %s\n",
		evidence.ID, review.Accepted, review.ReasonCode, record.Replayed, review.Fingerprint)
	return nil
}

func (a *App) doctorBrowserReadinessCommand(ctx context.Context, args []string) error {
	flags := newFlagSet("doctor browser-readiness", a.errOut)
	product := flags.String("product", string(browserruntime.BrowserProductEdge),
		"accepted browser product: edge, chrome, or chromium")
	jsonOutput := flags.Bool("json", false, "print the readiness receipt as JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("usage: cyberagent doctor browser-readiness --product <edge|chrome|chromium> [--json]")
	}
	selectedProduct, err := parseBrowserNetworkProbeProduct(*product)
	if err != nil {
		return err
	}
	identity, acceptance, err := acceptedBrowserNetworkProbeCandidate(selectedProduct)
	if err != nil {
		return err
	}
	if err := a.ensureStore(); err != nil {
		return err
	}
	service := application.NewBrowserSafeWebRuntimeService(a.store)
	readiness, err := service.Readiness(ctx, identity, acceptance)
	if err != nil {
		return err
	}
	if *jsonOutput {
		encoder := json.NewEncoder(a.out)
		encoder.SetIndent("", "  ")
		return encoder.Encode(readiness)
	}
	fmt.Fprintf(a.out, "protocol: %s\nready: %t\nblocking_reason: %s\nadapter: %s\npolicy: %s\nexpires_at: %s\n",
		readiness.ProtocolVersion, readiness.Ready, valueOrUnknown(readiness.BlockingReason),
		readiness.Adapter, readiness.PolicyVersion, readiness.ExpiresAt.Format(time.RFC3339))
	return nil
}

func parseBrowserNetworkProbeProduct(raw string) (browserruntime.BrowserProduct, error) {
	product := browserruntime.BrowserProduct(strings.ToLower(strings.TrimSpace(raw)))
	switch product {
	case browserruntime.BrowserProductEdge, browserruntime.BrowserProductChrome,
		browserruntime.BrowserProductChromium:
		return product, nil
	default:
		return "", errors.New("browser network probe product must be edge, chrome, or chromium")
	}
}

func acceptedBrowserNetworkProbeCandidate(product browserruntime.BrowserProduct) (
	browserruntime.BrowserExecutableIdentity, browserruntime.BrowserAcceptanceCandidate, error,
) {
	identities, err := browserruntime.DiscoverInstalledBrowsers()
	if err != nil {
		return browserruntime.BrowserExecutableIdentity{},
			browserruntime.BrowserAcceptanceCandidate{}, err
	}
	for _, identity := range identities {
		if identity.Product != product {
			continue
		}
		acceptance, acceptanceErr := browserruntime.BuildBrowserAcceptanceCandidate(identity)
		if acceptanceErr == nil && acceptance.Decision == browserruntime.BrowserAcceptanceAccepted &&
			acceptance.ReviewEligible {
			return identity, acceptance, nil
		}
	}
	return browserruntime.BrowserExecutableIdentity{},
		browserruntime.BrowserAcceptanceCandidate{},
		fmt.Errorf("no accepted stable %s executable is available", product)
}

func valueOrUnknown(value string) string {
	if strings.TrimSpace(value) == "" {
		return "unknown"
	}
	return value
}
