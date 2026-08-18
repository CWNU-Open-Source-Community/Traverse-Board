//go:build windows && desktop && wv2runtime.error

package main

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cyberagent-workbench/internal/apperror"

	"github.com/wailsapp/go-webview2/webviewloader"
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
		name      string
		detect    func(string) (string, error)
		compare   func(string, string) (int, error)
		integrity func(string) error
	}{
		{name: "missing", detect: func(string) (string, error) { return "", nil },
			compare: func(string, string) (int, error) { return 0, nil }, integrity: func(string) error { return nil }},
		{name: "probe error", detect: func(string) (string, error) { return "", errors.New(`C:\PRIVATE`) },
			compare: func(string, string) (int, error) { return 0, nil }, integrity: func(string) error { return nil }},
		{name: "old", detect: func(string) (string, error) { return "93.0.1.0", nil },
			compare: func(string, string) (int, error) { return -1, nil }, integrity: func(string) error { return nil }},
		{name: "invalid", detect: func(string) (string, error) { return "invalid", nil },
			compare: func(string, string) (int, error) { return 0, errors.New("invalid version") }, integrity: func(string) error { return nil }},
		{name: "damaged", detect: func(string) (string, error) { return "120.0.1.2", nil },
			compare: func(string, string) (int, error) { return 1, nil }, integrity: func(string) error { return errors.New(`C:\PRIVATE`) }},
	}
	for _, current := range tests {
		t.Run(current.name, func(t *testing.T) {
			err := requireWebView2Runtime(webView2RuntimeProbe{
				detect: current.detect, compare: current.compare, integrity: current.integrity,
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
		detect:    func(string) (string, error) { return "120.0.1.2", nil },
		compare:   func(string, string) (int, error) { return 1, nil },
		integrity: func(string) error { return nil },
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

func TestValidateWebView2ClientDLLRejectsDamagedImages(t *testing.T) {
	path := filepath.Join(t.TempDir(), "EmbeddedBrowserWebView.dll")
	if err := os.WriteFile(path, []byte{'M', 'Z', 0, 0}, 0o600); err != nil {
		t.Fatal(err)
	}
	_, machine, err := webView2RuntimeArchitecture()
	if err != nil {
		t.Fatal(err)
	}
	if err := validateWebView2ClientDLL(path, machine); err == nil {
		t.Fatal("damaged WebView2 client DLL was accepted")
	}
}

func TestValidateWebView2ClientDLLRejectsUnexpectedDLL(t *testing.T) {
	systemRoot := os.Getenv("SystemRoot")
	if systemRoot == "" {
		t.Skip("SystemRoot is unavailable")
	}
	_, machine, err := webView2RuntimeArchitecture()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(systemRoot, "System32", "kernel32.dll")
	if err := validateWebView2ClientDLL(path, machine); err == nil {
		t.Fatal("DLL without the WebView2 client entry point was accepted")
	}
}

func TestPEImageExportsProcedureWithoutLoadingDLL(t *testing.T) {
	systemRoot := os.Getenv("SystemRoot")
	if systemRoot == "" {
		t.Skip("SystemRoot is unavailable")
	}
	image, err := pe.Open(filepath.Join(systemRoot, "System32", "kernel32.dll"))
	if err != nil {
		t.Fatal(err)
	}
	defer image.Close()
	if !peImageExportsProcedure(image, "GetProcAddress") {
		t.Fatal("known kernel32 export was not found")
	}
	if peImageExportsProcedure(image, "CreateWebViewEnvironmentWithOptionsInternal") {
		t.Fatal("unexpected WebView2 export was found")
	}
}

func TestPEImageExportsProcedureRejectsInvalidFunctionTarget(t *testing.T) {
	const (
		sectionRVA = uint32(0x1000)
		targetRVA  = uint32(0x1100)
		procedure  = "ExampleProcedure"
	)
	imageBytes := make([]byte, 512)
	binary.LittleEndian.PutUint32(imageBytes[20:24], 1)
	binary.LittleEndian.PutUint32(imageBytes[24:28], 1)
	binary.LittleEndian.PutUint32(imageBytes[28:32], sectionRVA+64)
	binary.LittleEndian.PutUint32(imageBytes[32:36], sectionRVA+68)
	binary.LittleEndian.PutUint32(imageBytes[36:40], sectionRVA+72)
	binary.LittleEndian.PutUint32(imageBytes[64:68], targetRVA)
	binary.LittleEndian.PutUint32(imageBytes[68:72], sectionRVA+128)
	binary.LittleEndian.PutUint16(imageBytes[72:74], 0)
	copy(imageBytes[128:], procedure+"\x00")
	optionalHeader := &pe.OptionalHeader64{NumberOfRvaAndSizes: 1}
	optionalHeader.DataDirectory[0] = pe.DataDirectory{VirtualAddress: sectionRVA, Size: 128}
	image := &pe.File{
		OptionalHeader: optionalHeader,
		Sections: []*pe.Section{{
			SectionHeader: pe.SectionHeader{VirtualAddress: sectionRVA, Size: uint32(len(imageBytes))},
			ReaderAt:      bytes.NewReader(imageBytes),
		}},
	}
	if !peImageExportsProcedure(image, procedure) {
		t.Fatal("valid export target was rejected")
	}
	binary.LittleEndian.PutUint32(imageBytes[64:68], sectionRVA+uint32(len(imageBytes)))
	if peImageExportsProcedure(image, procedure) {
		t.Fatal("out-of-image export target was accepted")
	}
}

func TestInstalledWebView2RuntimeIntegrityWhenAvailable(t *testing.T) {
	version, err := webviewloader.GetAvailableCoreWebView2BrowserVersionString("")
	if err != nil || strings.TrimSpace(version) == "" {
		t.Skip("WebView2 Runtime is unavailable")
	}
	if err := verifyInstalledWebView2Runtime(version); err != nil {
		t.Fatalf("installed WebView2 Runtime failed integrity inspection: %v", err)
	}
}
