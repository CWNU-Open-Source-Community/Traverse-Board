//go:build windows && desktop && wv2runtime.error

package main

import (
	"debug/pe"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"

	"cyberagent-workbench/internal/app"
	"cyberagent-workbench/internal/apperror"

	"github.com/wailsapp/go-webview2/webviewloader"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	syswindows "golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const minimumWebView2RuntimeVersion = "94.0.992.31"

var errWebView2RuntimeRequired = errors.New("required WebView2 Runtime is unavailable")

type webView2RuntimeProbe struct {
	detect    func(string) (string, error)
	compare   func(string, string) (int, error)
	integrity func(string) error
}

var webView2ChannelIDs = [...]string{
	"{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}",
	"{2CD8A007-E189-409D-A2C8-9AF4EF3C72AA}",
	"{0D50BFEC-CD6A-4F9A-964C-C7416E3ACB10}",
	"{65C35B14-6C1D-4122-AC46-7148CC9D6497}",
}

// trustedDesktopRendererHost pins the exact WebView2 authority: the Windows
// WebView2 AssetServer serves the renderer from http://wails.localhost.
func trustedDesktopRendererHost() string {
	return "wails.localhost"
}

func checkDesktopPrerequisites() error {
	return requireWebView2Runtime(webView2RuntimeProbe{
		detect:    webviewloader.GetAvailableCoreWebView2BrowserVersionString,
		compare:   webviewloader.CompareBrowserVersions,
		integrity: verifyInstalledWebView2Runtime,
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
	if probe.detect == nil || probe.compare == nil || probe.integrity == nil {
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
	if err := probe.integrity(version); err != nil {
		return apperror.Wrap(apperror.CodeFailedPrecondition,
			"desktop WebView2 prerequisite integrity check failed", errWebView2RuntimeRequired)
	}
	return nil
}

func verifyInstalledWebView2Runtime(version string) error {
	expectedVersion := strings.Fields(strings.TrimSpace(version))
	if len(expectedVersion) == 0 {
		return errors.New("WebView2 runtime version is unavailable")
	}
	architecture, machine, err := webView2RuntimeArchitecture()
	if err != nil {
		return err
	}
	for _, channelID := range webView2ChannelIDs {
		keyPath := `Software\Microsoft\EdgeUpdate\ClientState\` + channelID
		for _, root := range []registry.Key{registry.LOCAL_MACHINE, registry.CURRENT_USER} {
			key, openErr := registry.OpenKey(root, keyPath,
				registry.QUERY_VALUE|registry.WOW64_32KEY)
			if openErr != nil {
				continue
			}
			location, _, valueErr := key.GetStringValue("EBWebView")
			_ = key.Close()
			if valueErr != nil || !filepath.IsAbs(location) ||
				!strings.EqualFold(filepath.Base(filepath.Clean(location)), expectedVersion[0]) {
				continue
			}
			clientDLL := filepath.Join(location, "EBWebView", architecture,
				"EmbeddedBrowserWebView.dll")
			if validateWebView2ClientDLL(clientDLL, machine) == nil {
				return nil
			}
		}
	}
	return errors.New("WebView2 runtime client DLL is unavailable or damaged")
}

func webView2RuntimeArchitecture() (string, uint16, error) {
	switch goruntime.GOARCH {
	case "amd64":
		return "x64", pe.IMAGE_FILE_MACHINE_AMD64, nil
	case "arm64":
		return "arm64", pe.IMAGE_FILE_MACHINE_ARM64, nil
	default:
		return "", 0, fmt.Errorf("unsupported WebView2 architecture: %s", goruntime.GOARCH)
	}
}

func validateWebView2ClientDLL(path string, machine uint16) error {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return errors.New("WebView2 runtime client DLL is unavailable")
	}
	image, err := pe.Open(path)
	if err != nil {
		return errors.New("WebView2 runtime client DLL is not a valid PE image")
	}
	defer image.Close()
	if image.FileHeader.Machine != machine ||
		image.FileHeader.Characteristics&pe.IMAGE_FILE_DLL == 0 {
		return errors.New("WebView2 runtime client DLL architecture is invalid")
	}
	dll, err := syswindows.LoadDLL(path)
	if err != nil {
		return errors.New("WebView2 runtime client DLL cannot be loaded")
	}
	defer dll.Release()
	if _, err := dll.FindProc("CreateWebViewEnvironmentWithOptionsInternal"); err != nil {
		return errors.New("WebView2 runtime client DLL entry point is unavailable")
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
