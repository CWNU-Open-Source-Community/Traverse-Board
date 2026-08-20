package application

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"cyberagent-workbench/internal/browserruntime"
	"cyberagent-workbench/internal/uievidence"
)

type UIEvidenceBrowserSelection struct {
	Product browserruntime.BrowserProduct `json:"product"`
	Channel browserruntime.BrowserChannel `json:"channel"`
}

type UIEvidenceBrowserPreparation struct {
	ManifestIdentity uievidence.BrowserIdentity
	Identity         browserruntime.BrowserExecutableIdentity
	Acceptance       browserruntime.BrowserAcceptanceCandidate
}

type UIEvidenceBrowserDriver interface {
	ConfigureUIEvidence(context.Context, uievidence.Environment) error
	Navigate(context.Context, string) (browserruntime.RestrictedNavigationResult, error)
	ClickUIEvidence(context.Context, string) error
	TypeUIEvidence(context.Context, string, string, string) error
	AssertUIEvidenceSelector(context.Context, string, bool) error
	DOMUIEvidence(context.Context) (browserruntime.UIEvidenceTextCapture, error)
	AccessibilityUIEvidence(context.Context) (browserruntime.UIEvidenceTextCapture, error)
	PerformanceUIEvidence(context.Context) (browserruntime.UIEvidenceTextCapture, error)
	DiagnosticsUIEvidence(context.Context) (browserruntime.UIEvidenceDiagnostics,
		browserruntime.UIEvidenceTextCapture, error)
	ScreenshotUIEvidence(context.Context, []string, float64) (
		browserruntime.RestrictedScreenshot, int, int, error)
}

type UIEvidenceBrowserRun struct {
	Driver UIEvidenceBrowserDriver
	handle *BrowserRuntimeHandle
}

type UIEvidenceBrowserProvider interface {
	Prepare(context.Context, UIEvidenceBrowserSelection) (UIEvidenceBrowserPreparation, error)
	Open(context.Context, UIEvidenceBrowserPreparation, BrowserRuntimeLaunchRequest) (
		*UIEvidenceBrowserRun, error)
	Close(context.Context, *UIEvidenceBrowserRun) (browserruntime.BrowserRuntimeReceipt, error)
}

// SafeWebUIEvidenceBrowserProvider is the production adapter. Discovery uses
// only the fixed OS installation registry; Open revalidates the exact bytes
// before entering the reviewed Safe Web/WFP/Job/Profile launch path.
type SafeWebUIEvidenceBrowserProvider struct {
	runtime *BrowserRuntimeService
}

func NewSafeWebUIEvidenceBrowserProvider(runtime *BrowserRuntimeService) (
	*SafeWebUIEvidenceBrowserProvider, error,
) {
	if runtime == nil {
		return nil, errors.New("UI evidence browser runtime is required")
	}
	return &SafeWebUIEvidenceBrowserProvider{runtime: runtime}, nil
}

func (p *SafeWebUIEvidenceBrowserProvider) Prepare(ctx context.Context,
	selection UIEvidenceBrowserSelection,
) (UIEvidenceBrowserPreparation, error) {
	if p == nil || p.runtime == nil || !validUIEvidenceBrowserSelection(selection) {
		return UIEvidenceBrowserPreparation{}, errors.New("UI evidence browser selection is invalid")
	}
	if ctx == nil {
		return UIEvidenceBrowserPreparation{}, errors.New("UI evidence browser preparation context is required")
	}
	if err := ctx.Err(); err != nil {
		return UIEvidenceBrowserPreparation{}, err
	}
	identities, err := browserruntime.DiscoverInstalledBrowsers()
	if err != nil {
		return UIEvidenceBrowserPreparation{}, err
	}
	for _, identity := range identities {
		if identity.Product != selection.Product || identity.Channel != selection.Channel {
			continue
		}
		if !identity.VersionVerified || strings.TrimSpace(identity.Version) == "" {
			return UIEvidenceBrowserPreparation{}, errors.New(
				"UI evidence requires a browser with a verified version")
		}
		acceptance, err := browserruntime.BuildBrowserAcceptanceCandidate(identity)
		if err != nil {
			return UIEvidenceBrowserPreparation{}, err
		}
		if !acceptance.ReviewEligible {
			return UIEvidenceBrowserPreparation{}, fmt.Errorf(
				"UI evidence browser publisher is not eligible: %s", acceptance.ReasonCode)
		}
		manifestIdentity := uievidence.BrowserIdentity{Product: string(identity.Product),
			Version: identity.Version, ExecutableSHA256: identity.ExecutableSHA256,
			DriverProtocol: uievidence.DriverProtocolVersion, Headless: true,
			TemporaryProfile: true}
		if err := manifestIdentity.Validate(); err != nil {
			return UIEvidenceBrowserPreparation{}, err
		}
		return UIEvidenceBrowserPreparation{ManifestIdentity: manifestIdentity,
			Identity: identity, Acceptance: acceptance}, nil
	}
	return UIEvidenceBrowserPreparation{}, errors.New(
		"selected UI evidence browser is not installed in a fixed trusted location")
}

func (p *SafeWebUIEvidenceBrowserProvider) Open(ctx context.Context,
	preparation UIEvidenceBrowserPreparation, request BrowserRuntimeLaunchRequest,
) (*UIEvidenceBrowserRun, error) {
	if p == nil || p.runtime == nil || preparation.ManifestIdentity.Validate() != nil ||
		browserruntime.ValidateBrowserExecutableIdentity(preparation.Identity) != nil ||
		browserruntime.ValidateBrowserAcceptanceCandidate(preparation.Acceptance,
			preparation.Identity) != nil ||
		preparation.ManifestIdentity.Product != string(preparation.Identity.Product) ||
		preparation.ManifestIdentity.Version != preparation.Identity.Version ||
		preparation.ManifestIdentity.ExecutableSHA256 != preparation.Identity.ExecutableSHA256 {
		return nil, errors.New("UI evidence browser preparation is invalid")
	}
	request.Identity = preparation.Identity
	request.Acceptance = preparation.Acceptance
	handle, err := p.runtime.LaunchUIEvidence(ctx, request)
	if err != nil {
		return nil, err
	}
	if handle == nil || handle.UIEvidence == nil {
		if handle != nil {
			_, _ = p.runtime.Close(context.Background(), handle)
		}
		return nil, errors.New("UI evidence browser opened without its restricted driver")
	}
	return &UIEvidenceBrowserRun{Driver: handle.UIEvidence, handle: handle}, nil
}

func (p *SafeWebUIEvidenceBrowserProvider) Close(ctx context.Context,
	run *UIEvidenceBrowserRun,
) (browserruntime.BrowserRuntimeReceipt, error) {
	if p == nil || p.runtime == nil || run == nil || run.handle == nil {
		return browserruntime.BrowserRuntimeReceipt{}, errors.New(
			"UI evidence browser run is unavailable")
	}
	return p.runtime.Close(ctx, run.handle)
}

func validUIEvidenceBrowserSelection(value UIEvidenceBrowserSelection) bool {
	if value.Channel != browserruntime.BrowserChannelStable &&
		value.Channel != browserruntime.BrowserChannelBeta &&
		value.Channel != browserruntime.BrowserChannelDev &&
		value.Channel != browserruntime.BrowserChannelCanary {
		return false
	}
	return value.Product == browserruntime.BrowserProductChrome ||
		value.Product == browserruntime.BrowserProductEdge
}
