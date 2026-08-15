package httpapi

import (
	"net/http"
	"testing"
)

func TestBrowserSafeWebReadinessRejectsUnknownProduct(t *testing.T) {
	fixture := newAPIFixture(t)
	response := fixture.get(t, "/api/v1/browser/safe-web-readiness?product=firefox")
	assertAPIError(t, response, http.StatusBadRequest, "INVALID_ARGUMENT")
}

func TestBrowserSafeWebReadinessRejectsUnknownQueryParameter(t *testing.T) {
	fixture := newAPIFixture(t)
	response := fixture.get(t, "/api/v1/browser/safe-web-readiness?product=chrome&extra=1")
	assertAPIError(t, response, http.StatusBadRequest, "INVALID_ARGUMENT")
}
