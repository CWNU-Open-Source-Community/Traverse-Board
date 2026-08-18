//go:build windows && desktop && wv2runtime.error

package main

import (
	"debug/pe"
	"encoding/binary"
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
	if !peImageExportsProcedure(image, "CreateWebViewEnvironmentWithOptionsInternal") {
		return errors.New("WebView2 runtime client DLL entry point is unavailable")
	}
	return nil
}

// peImageExportsProcedure inspects the on-disk export table instead of loading
// the candidate DLL. A prerequisite check must not execute DllMain or resolve
// arbitrary dependencies before Wails starts the trusted WebView2 runtime.
func peImageExportsProcedure(image *pe.File, procedure string) bool {
	directory, ok := peExportDirectory(image)
	if !ok || directory.VirtualAddress == 0 || directory.Size < 40 || procedure == "" {
		return false
	}
	header, ok := peImageBytes(image, directory.VirtualAddress, 40)
	if !ok {
		return false
	}
	functionCount := binary.LittleEndian.Uint32(header[20:24])
	nameCount := binary.LittleEndian.Uint32(header[24:28])
	functionTableRVA := binary.LittleEndian.Uint32(header[28:32])
	nameTableRVA := binary.LittleEndian.Uint32(header[32:36])
	ordinalTableRVA := binary.LittleEndian.Uint32(header[36:40])
	if functionCount == 0 || nameCount == 0 || nameCount > 1<<16 ||
		functionTableRVA == 0 || nameTableRVA == 0 || ordinalTableRVA == 0 {
		return false
	}
	_, tableOffset, tableSize, ok := peImageSectionRange(image, nameTableRVA, 0)
	if !ok || uint64(nameCount)*4 > tableSize-uint64(tableOffset) {
		return false
	}
	_, ordinalOffset, ordinalSize, ok := peImageSectionRange(image, ordinalTableRVA, 0)
	if !ok || uint64(nameCount)*2 > ordinalSize-uint64(ordinalOffset) {
		return false
	}
	for index := uint32(0); index < nameCount; index++ {
		entryRVA := uint64(nameTableRVA) + uint64(index)*4
		if entryRVA > uint64(^uint32(0)) {
			return false
		}
		entry, found := peImageBytes(image, uint32(entryRVA), 4)
		if !found {
			return false
		}
		procedureRVA := binary.LittleEndian.Uint32(entry)
		candidate, found := peImageBytes(image, procedureRVA, uint32(len(procedure)+1))
		if found && candidate[len(procedure)] == 0 &&
			string(candidate[:len(procedure)]) == procedure {
			ordinalEntryRVA := uint64(ordinalTableRVA) + uint64(index)*2
			if ordinalEntryRVA > uint64(^uint32(0)) {
				return false
			}
			ordinalEntry, ordinalFound := peImageBytes(image, uint32(ordinalEntryRVA), 2)
			if !ordinalFound {
				return false
			}
			ordinal := uint32(binary.LittleEndian.Uint16(ordinalEntry))
			if ordinal >= functionCount {
				return false
			}
			functionEntryRVA := uint64(functionTableRVA) + uint64(ordinal)*4
			if functionEntryRVA > uint64(^uint32(0)) {
				return false
			}
			functionEntry, functionFound := peImageBytes(image, uint32(functionEntryRVA), 4)
			if !functionFound {
				return false
			}
			targetRVA := binary.LittleEndian.Uint32(functionEntry)
			_, _, _, targetFound := peImageSectionRange(image, targetRVA, 1)
			return targetRVA != 0 && targetFound
		}
	}
	return false
}

func peExportDirectory(image *pe.File) (pe.DataDirectory, bool) {
	if image == nil {
		return pe.DataDirectory{}, false
	}
	switch header := image.OptionalHeader.(type) {
	case *pe.OptionalHeader32:
		return header.DataDirectory[0], header.NumberOfRvaAndSizes > 0
	case *pe.OptionalHeader64:
		return header.DataDirectory[0], header.NumberOfRvaAndSizes > 0
	default:
		return pe.DataDirectory{}, false
	}
}

func peImageBytes(image *pe.File, rva, size uint32) ([]byte, bool) {
	section, offset, _, ok := peImageSectionRange(image, rva, size)
	if !ok {
		return nil, false
	}
	buffer := make([]byte, int(size))
	read, err := section.ReadAt(buffer, offset)
	return buffer, err == nil && read == len(buffer)
}

func peImageSectionRange(image *pe.File, rva, size uint32) (*pe.Section, int64, uint64, bool) {
	if image == nil {
		return nil, 0, 0, false
	}
	address := uint64(rva)
	required := uint64(size)
	for _, section := range image.Sections {
		start := uint64(section.VirtualAddress)
		rawSize := uint64(section.Size)
		if address < start {
			continue
		}
		offset := address - start
		if offset <= rawSize && required <= rawSize-offset {
			return section, int64(offset), rawSize, true
		}
	}
	return nil, 0, 0, false
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
