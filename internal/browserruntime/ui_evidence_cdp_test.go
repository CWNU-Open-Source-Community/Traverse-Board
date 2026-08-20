package browserruntime

import (
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"
	"image/png"
	"strings"
	"testing"

	"cyberagent-workbench/internal/uievidence"
)

func TestUIEvidenceCDPUsesFixedActionsCapturesDiagnosticsAndMasksScreenshot(t *testing.T) {
	facts := newLoopbackBrowserRuntimeFacts(t)
	profileLease := facts.materialize(t)
	allowedURL := "http://127.0.0.1:18080/page"
	server := newScriptedCDPServer(t, allowedURL)
	defer server.Close(t)
	writeDevToolsActivePort(t, profileLease.DirectoryPath, server.port, server.path)
	process := startFakeBrowserProcess(t, facts, profileLease)
	defer func() { _ = process.Stop(context.Background()) }()
	authorization, err := AuthorizeUIEvidenceCDP(facts.authorization, facts.session,
		facts.identity, facts.acceptance, facts.ownership, facts.attempt,
		facts.launchLease, facts.review, facts.networkEvidence, facts.networkReview,
		facts.networkPlan, facts.permission,
		ProductionRuntimeCapabilities{SafeWebStartEnabled: true,
			DisposableProfileEnabled: true, NetworkContainmentEnabled: true,
			RestrictedCDPEnabled: true}, facts.now)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := OpenRestrictedBrowserSession(t.Context(), authorization,
		facts.authorization, facts.session, facts.identity, facts.acceptance,
		facts.ownership, facts.attempt, facts.launchLease, facts.review,
		facts.networkEvidence, facts.networkReview, facts.networkPlan,
		facts.permission, profileLease, process)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close(context.Background())

	environment := uievidence.Environment{Viewport: uievidence.Viewport{
		Width: 1280, Height: 720, DPR: 1}, Locale: "en-US",
		Theme: uievidence.ThemeDark, ReducedMotion: true}
	if err := runtime.ConfigureUIEvidence(t.Context(), environment); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Navigate(t.Context(), allowedURL); err != nil {
		t.Fatal(err)
	}
	if err := runtime.AssertUIEvidenceSelector(t.Context(), "main", true); err != nil {
		t.Fatal(err)
	}
	if err := runtime.AssertUIEvidenceSelector(t.Context(), "#absent", false); err != nil {
		t.Fatal(err)
	}
	if err := runtime.AssertUIEvidenceSelector(t.Context(), "#eventual", true); err != nil {
		t.Fatal(err)
	}
	if err := runtime.ClickUIEvidence(t.Context(), "button"); err != nil {
		t.Fatal(err)
	}
	inputDigest, err := uievidence.InputSHA256("fixture input")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.TypeUIEvidence(t.Context(), "input", "fixture input", inputDigest); err != nil {
		t.Fatal(err)
	}

	dom, err := runtime.DOMUIEvidence(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !dom.Redacted || bytes.Contains(dom.Content, []byte("abcdefghijklmnopqrstuvwxyz1234567890")) ||
		!bytes.Contains(dom.Content, []byte("[REDACTED:secret]")) {
		t.Fatalf("DOM was not safely redacted: %s", dom.Content)
	}
	if _, err := runtime.AccessibilityUIEvidence(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.PerformanceUIEvidence(t.Context()); err != nil {
		t.Fatal(err)
	}
	diagnostics, diagnosticArtifact, err := runtime.DiagnosticsUIEvidence(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if diagnostics.Summary.ConsoleWarnings != 2 || diagnostics.Summary.HTTPFailures != 1 ||
		diagnostics.Summary.ConsoleErrors != 0 ||
		bytes.Contains(diagnosticArtifact.Content, []byte("token=hidden")) {
		t.Fatalf("unexpected diagnostics: %+v %s", diagnostics.Summary, diagnosticArtifact.Content)
	}
	screenshot, width, height, err := runtime.ScreenshotUIEvidence(t.Context(),
		[]string{"[data-dynamic]"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := png.Decode(bytes.NewReader(screenshot.PNG))
	if err != nil || width != 10 || height != 10 {
		t.Fatalf("screenshot=%dx%d err=%v", width, height, err)
	}
	r, g, b, _ := decoded.At(3, 3).RGBA()
	if r > 0x2000 || g > 0x3000 || b > 0x4000 {
		t.Fatalf("dynamic region was not masked: %x %x %x", r, g, b)
	}

	methods := strings.Join(server.Methods(), "\n")
	for _, forbidden := range []string{"Runtime.evaluate", "Runtime.callFunctionOn",
		"Network.getAllCookies", "Network.getResponseBody", "Fetch.fulfillRequest"} {
		if strings.Contains(methods, forbidden) {
			t.Fatalf("UI evidence used forbidden CDP method %s", forbidden)
		}
	}
}

func TestUIEvidenceCDPMethodSetRemainsClosed(t *testing.T) {
	want := []string{"Accessibility.enable", "Accessibility.getFullAXTree",
		"DOM.focus", "DOM.getBoxModel", "DOM.getOuterHTML", "DOM.querySelector",
		"DOM.scrollIntoViewIfNeeded", "Emulation.setDeviceMetricsOverride",
		"Emulation.setEmulatedMedia", "Emulation.setLocaleOverride",
		"Input.dispatchMouseEvent", "Input.insertText", "Log.enable",
		"Performance.enable", "Performance.getMetrics", "Runtime.enable"}
	if len(uiEvidenceCDPMethods) != len(want) {
		t.Fatalf("UI evidence CDP method set changed: %#v", uiEvidenceCDPMethods)
	}
	for _, method := range want {
		if uiEvidenceCDPMethods[method] != restrictedCDPTargetMethod {
			t.Fatalf("UI evidence CDP method %q is absent or mis-scoped", method)
		}
	}
	for _, forbidden := range []string{"Runtime.evaluate", "Runtime.callFunctionOn",
		"Network.getAllCookies", "Network.getResponseBody", "Fetch.fulfillRequest",
		"Storage.getCookies"} {
		if _, ok := uiEvidenceCDPMethods[forbidden]; ok {
			t.Fatalf("forbidden method %q entered UI evidence allowlist", forbidden)
		}
	}
}

func TestUIEvidenceCDPRequiresItsExactAuthorizationDerivation(t *testing.T) {
	facts := newLoopbackBrowserRuntimeFacts(t)
	capabilities := ProductionRuntimeCapabilities{SafeWebStartEnabled: true,
		DisposableProfileEnabled: true, NetworkContainmentEnabled: true,
		RestrictedCDPEnabled: true}
	restricted, err := AuthorizeRestrictedCDP(facts.authorization, facts.session,
		facts.identity, facts.acceptance, facts.ownership, facts.attempt,
		facts.launchLease, facts.review, facts.networkEvidence, facts.networkReview,
		facts.networkPlan, facts.permission, capabilities, facts.now)
	if err != nil {
		t.Fatal(err)
	}
	restricted.UIEvidenceAuthorized = true
	restricted.Fingerprint = browserRuntimeFingerprint(restricted)
	if ValidateRestrictedCDPAuthorization(restricted, facts.authorization,
		facts.session, facts.permission) == nil {
		t.Fatal("ordinary restricted CDP authorization widened to UI evidence")
	}

	uiEvidence, err := AuthorizeUIEvidenceCDP(facts.authorization, facts.session,
		facts.identity, facts.acceptance, facts.ownership, facts.attempt,
		facts.launchLease, facts.review, facts.networkEvidence, facts.networkReview,
		facts.networkPlan, facts.permission, capabilities, facts.now)
	if err != nil {
		t.Fatal(err)
	}
	uiEvidence.UIEvidenceAuthorized = false
	uiEvidence.Fingerprint = browserRuntimeFingerprint(uiEvidence)
	if ValidateUIEvidenceCDPAuthorization(uiEvidence, facts.authorization,
		facts.session, facts.permission) == nil {
		t.Fatal("UI evidence authorization lost its dedicated method-set bit")
	}
}

func TestUIEvidenceDiagnosticBudgetFailsClosedInsteadOfDroppingLateErrors(t *testing.T) {
	client := &restrictedCDPClient{uiEvidence: true, maxRequests: 1}
	if err := client.appendConsole(UIEvidenceConsoleEntry{Level: "info"}); err != nil {
		t.Fatal(err)
	}
	if err := client.appendConsole(UIEvidenceConsoleEntry{Level: "error"}); err == nil ||
		client.budgetErr == nil {
		t.Fatal("late console error was silently dropped after the diagnostic bound")
	}

	client = &restrictedCDPClient{uiEvidence: true, maxRequests: 1}
	if err := client.appendPageError(UIEvidencePageError{Text: "first"}); err != nil {
		t.Fatal(err)
	}
	if err := client.appendPageError(UIEvidencePageError{Text: "late"}); err == nil ||
		client.budgetErr == nil {
		t.Fatal("late page error was silently dropped after the diagnostic bound")
	}
}

func TestUIEvidenceDiagnosticURLsStayInsideScopeAndDropSecrets(t *testing.T) {
	scope, err := NewTargetScope(ProfileSafeWeb,
		[]string{"http://127.0.0.1:18080"})
	if err != nil {
		t.Fatal(err)
	}
	client := &restrictedCDPClient{scope: scope}
	if got := client.safeEvidenceURL(
		"http://127.0.0.1:18080/page?token=hidden"); got != "http://127.0.0.1:18080/page" {
		t.Fatalf("allowed diagnostic URL=%q", got)
	}
	for _, rawURL := range []string{
		"data:text/plain,token=abcdefghijklmnopqrstuvwxyz1234567890",
		"file:///C:/Users/example/private.txt",
		"blob:http://127.0.0.1:18080/secret-identifier",
		"http://127.0.0.1:18081/other-origin?token=hidden",
	} {
		if got := client.safeEvidenceURL(rawURL); got != "[blocked-url]" {
			t.Fatalf("out-of-scope diagnostic URL %q persisted as %q", rawURL, got)
		}
	}
}

func TestUIEvidencePNGDimensionsAreBoundedBeforeDecode(t *testing.T) {
	content := append([]byte(nil), restrictedTestPNG()...)
	// PNG IHDR stores width/height at byte offsets 16 and 20. Recompute the
	// IHDR CRC so DecodeConfig observes a structurally valid oversized image.
	binary.BigEndian.PutUint32(content[16:20],
		uint32(uievidence.MaxScreenshotWidth+1))
	binary.BigEndian.PutUint32(content[29:33], crc32.ChecksumIEEE(content[12:29]))
	if _, err := decodeBoundedUIEvidencePNG(content); err == nil {
		t.Fatal("oversized PNG reached full screenshot decoding")
	}
	if _, err := decodeBoundedUIEvidencePNG(restrictedTestPNG()); err != nil {
		t.Fatalf("bounded PNG rejected: %v", err)
	}
}
