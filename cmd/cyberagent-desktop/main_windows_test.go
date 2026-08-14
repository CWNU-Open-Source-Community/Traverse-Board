//go:build windows && desktop && wv2runtime.error

package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cyberagent-workbench/internal/apperror"

	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

func TestTrustedDesktopRendererHostRejectsDarwinAuthority(t *testing.T) {
	if trustedDesktopRendererHost() != "wails.localhost" {
		t.Fatalf("unexpected Windows renderer host: %q", trustedDesktopRendererHost())
	}
	request := httptest.NewRequest(http.MethodGet, "http://wails.localhost/api/v1/health", nil)
	request.Host = "wails"
	request.Header.Set("User-Agent", "PrayuDesktopTest/1.0 wails.io")
	request.URL.Scheme = ""
	request.URL.Host = ""
	request.RequestURI = request.URL.RequestURI()
	called := false
	handler := inProcessAPIHandler{next: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	})}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || called {
		t.Fatalf("darwin authority reached API on Windows: status=%d called=%t",
			response.Code, called)
	}
}

func TestDesktopStartupFailureMessageIsBoundedAndPathFree(t *testing.T) {
	private := apperror.Wrap(apperror.CodeFailedPrecondition, "database validation failed",
		errors.New(`C:\PRIVATE\cyberagent.db`))
	message := desktopStartupFailureMessage(private)
	if !strings.Contains(message, string(apperror.CodeFailedPrecondition)) ||
		strings.Contains(message, "PRIVATE") || strings.Contains(message, "cyberagent.db") ||
		len(message) > 256 {
		t.Fatalf("unsafe startup failure message: %q", message)
	}
}

func TestDesktopWindowUsesNativeAcrylicWithoutWeakeningRendererIntegrity(t *testing.T) {
	window := desktopWindowsOptions()
	if window == nil || window.Theme != windows.SystemDefault ||
		window.BackdropType != windows.Acrylic ||
		!window.WebviewIsTransparent || !window.WindowIsTranslucent {
		t.Fatalf("native Acrylic boundary is incomplete: %#v", window)
	}
	if window.WebviewDisableRendererCodeIntegrity || window.EnableSwipeGestures ||
		!window.DisablePinchZoom || !window.IsZoomControlEnabled ||
		window.WindowClassName != "CyberAgentWorkbench" || window.Messages == nil {
		t.Fatalf("Acrylic changed protected desktop defaults: %#v", window)
	}
}

func TestApplyDesktopPlatformOptionsPinsOnlyWindowsOptions(t *testing.T) {
	appOptions := &options.App{}
	applyDesktopPlatformOptions(appOptions)
	if appOptions.Windows == nil || appOptions.Mac != nil || appOptions.Linux != nil {
		t.Fatalf("platform options drifted across operating systems: %#v", appOptions)
	}
}

func TestWebView2PrerequisiteFailsClosedWithoutStartingAnInstaller(t *testing.T) {
	tests := []struct {
		name    string
		detect  func(string) (string, error)
		compare func(string, string) (int, error)
	}{
		{name: "missing", detect: func(string) (string, error) { return "", nil },
			compare: func(string, string) (int, error) { return 0, nil }},
		{name: "probe error", detect: func(string) (string, error) { return "", errors.New(`C:\PRIVATE`) },
			compare: func(string, string) (int, error) { return 0, nil }},
		{name: "old", detect: func(string) (string, error) { return "93.0.1.0", nil },
			compare: func(string, string) (int, error) { return -1, nil }},
		{name: "invalid", detect: func(string) (string, error) { return "invalid", nil },
			compare: func(string, string) (int, error) { return 0, errors.New("invalid version") }},
	}
	for _, current := range tests {
		t.Run(current.name, func(t *testing.T) {
			err := requireWebView2Runtime(webView2RuntimeProbe{
				detect: current.detect, compare: current.compare,
			})
			if !errors.Is(err, errWebView2RuntimeRequired) ||
				apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
				t.Fatalf("prerequisite error = %v", err)
			}
			message := desktopStartupFailureMessage(err)
			if !strings.Contains(message, minimumWebView2RuntimeVersion) ||
				strings.Contains(message, "PRIVATE") || strings.Contains(strings.ToLower(message), "http") ||
				len(message) > 320 {
				t.Fatalf("unsafe WebView2 message: %q", message)
			}
		})
	}

	if err := requireWebView2Runtime(webView2RuntimeProbe{
		detect:  func(string) (string, error) { return "120.0.1.2", nil },
		compare: func(string, string) (int, error) { return 1, nil },
	}); err != nil {
		t.Fatalf("current WebView2 runtime was rejected: %v", err)
	}

	messages := desktopWebView2Messages()
	all := strings.ToLower(strings.Join([]string{
		messages.InstallationRequired, messages.UpdateRequired, messages.MissingRequirements,
		messages.Webview2NotInstalled, messages.Error, messages.FailedToInstall,
		messages.DownloadPage, messages.PressOKToInstall, messages.ContactAdmin,
		messages.InvalidFixedWebview2, messages.WebView2ProcessCrash,
	}, " "))
	if strings.Contains(all, "http://") || strings.Contains(all, "https://") ||
		strings.Contains(all, "silently") || strings.Contains(all, "press ok") ||
		messages.DownloadPage != "" || messages.PressOKToInstall != "" {
		t.Fatalf("WebView2 messages can trigger or direct an implicit installer: %q", all)
	}
}
