package httpapi

import (
	"fmt"
	"net/http"
	"strings"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/browserruntime"
)

// browserSafeWebReadiness is a read-only query that collapses the latest
// durable production containment evidence and its operator review into one
// fail-closed readiness receipt. It never starts a browser and never grants
// authority; a missing evidence or review is reported as a Ready=false receipt.
func (a *API) browserSafeWebReadiness(request *http.Request) (any, *Page, error) {
	if err := validateSingleQueryValues(request.URL.Query(), "product"); err != nil {
		return nil, nil, err
	}
	productRaw := strings.ToLower(strings.TrimSpace(request.URL.Query().Get("product")))
	if productRaw == "" {
		productRaw = string(browserruntime.BrowserProductChrome)
	}
	product := browserruntime.BrowserProduct(productRaw)
	switch product {
	case browserruntime.BrowserProductEdge, browserruntime.BrowserProductChrome,
		browserruntime.BrowserProductChromium:
	default:
		return nil, nil, apperror.New(apperror.CodeInvalidArgument,
			"browser-readiness product must be edge, chrome, or chromium")
	}
	identity, acceptance, err := discoverAcceptedBrowser(product)
	if err != nil {
		return nil, nil, apperror.Normalize(err)
	}
	service := application.NewBrowserSafeWebRuntimeService(a.store)
	readiness, err := service.Readiness(request.Context(), identity, acceptance)
	if err != nil {
		return nil, nil, apperror.Normalize(err)
	}
	return readiness, nil, nil
}

func discoverAcceptedBrowser(product browserruntime.BrowserProduct) (
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
