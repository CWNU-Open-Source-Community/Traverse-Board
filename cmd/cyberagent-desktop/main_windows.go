//go:build windows && desktop && wv2runtime.error

package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"cyberagent-workbench/internal/app"
	"cyberagent-workbench/internal/apperror"

	"github.com/wailsapp/go-webview2/webviewloader"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	syswindows "golang.org/x/sys/windows"
)

const minimumWebView2RuntimeVersion = "94.0.992.31"

var errWebView2RuntimeRequired = errors.New("required WebView2 Runtime is unavailable")

type webView2RuntimeProbe struct {
	detect  func(string) (string, error)
	compare func(string, string) (int, error)
}

// trustedDesktopRendererHost pins the exact WebView2 authority: the Windows
// WebView2 AssetServer serves the renderer from http://wails.localhost.
func trustedDesktopRendererHost() string {
	return "wails.localhost"
}

func checkDesktopPrerequisites() error {
	return requireWebView2Runtime(webView2RuntimeProbe{
		detect:  webviewloader.GetAvailableCoreWebView2BrowserVersionString,
		compare: webviewloader.CompareBrowserVersions,
	})
}

func reportDesktopStartupFailure(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	message, messageErr := syswindows.UTF16PtrFromString(desktopStartupFailureMessage(err))
	title, titleErr := syswindows.UTF16PtrFromString(app.Name)
	if messageErr != nil || titleErr != nil {
		return
	}
	_, _ = syswindows.MessageBox(0, message, title,
		syswindows.MB_OK|syswindows.MB_ICONERROR|syswindows.MB_SETFOREGROUND)
}

func desktopStartupFailureMessage(err error) string {
	if errors.Is(err, errWebView2RuntimeRequired) {
		return "Prayu requires Microsoft Edge WebView2 Runtime " +
			minimumWebView2RuntimeVersion + " or newer.\r\n\r\n" +
			"Install or update it through a trusted Windows software channel, then reopen the app.\r\n\r\n" +
			"No download or installation was started."
	}
	code := apperror.CodeOf(apperror.Normalize(err))
	return "Prayu could not start.\r\n\r\nError code: " + string(code) +
		"\r\n\r\nLocal data was not deleted or reset. Keep it for diagnosis."
}

func requireWebView2Runtime(probe webView2RuntimeProbe) error {
	if probe.detect == nil || probe.compare == nil {
		return apperror.Wrap(apperror.CodeFailedPrecondition,
			"desktop WebView2 prerequisite check is unavailable", errWebView2RuntimeRequired)
	}
	version, err := probe.detect("")
	if err != nil || strings.TrimSpace(version) == "" {
		return apperror.Wrap(apperror.CodeFailedPrecondition,
			"desktop WebView2 prerequisite is not satisfied", errWebView2RuntimeRequired)
	}
	comparison, err := probe.compare(version, minimumWebView2RuntimeVersion)
	if err != nil || comparison < 0 {
		return apperror.Wrap(apperror.CodeFailedPrecondition,
			"desktop WebView2 prerequisite is not satisfied", errWebView2RuntimeRequired)
	}
	return nil
}

func applyDesktopPlatformOptions(appOptions *options.App) {
	appOptions.Windows = desktopWindowsOptions()
}

func desktopWindowsOptions() *windows.Options {
	return &windows.Options{
		Theme: windows.SystemDefault, BackdropType: windows.Acrylic,
		WebviewIsTransparent: true, WindowIsTranslucent: true,
		DisablePinchZoom: true, IsZoomControlEnabled: true, EnableSwipeGestures: false,
		WebviewDisableRendererCodeIntegrity: false, WindowClassName: "CyberAgentWorkbench",
		Messages: desktopWebView2Messages(),
	}
}

func desktopWebView2Messages() *windows.Messages {
	return &windows.Messages{
		InstallationRequired: "Microsoft Edge WebView2 Runtime is required. Use a trusted Windows software channel, then reopen Prayu.",
		UpdateRequired:       "Microsoft Edge WebView2 Runtime must be updated through a trusted Windows software channel before Prayu can start.",
		MissingRequirements:  "Prayu prerequisite",
		Webview2NotInstalled: "Microsoft Edge WebView2 Runtime is unavailable.",
		Error:                "Prayu prerequisite",
		FailedToInstall:      "Microsoft Edge WebView2 Runtime remains unavailable.",
		DownloadPage:         "",
		PressOKToInstall:     "",
		ContactAdmin:         "Microsoft Edge WebView2 Runtime is required. Contact your administrator or use a trusted Windows software channel.",
		InvalidFixedWebview2: "The configured WebView2 Runtime does not meet the required version.",
		WebView2ProcessCrash: "The WebView2 process stopped. Reopen Prayu; local data was not reset.",
	}
}
