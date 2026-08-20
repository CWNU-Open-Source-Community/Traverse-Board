package browserruntime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"cyberagent-workbench/internal/outputsafe"
	"cyberagent-workbench/internal/redact"
	"cyberagent-workbench/internal/uievidence"
)

const (
	MaxUIEvidenceTextArtifactBytes   = 4 * 1024 * 1024
	MaxUIEvidenceDiagnosticTextBytes = 8 * 1024
	MaxUIEvidenceInputBytes          = 64 * 1024
)

type UIEvidenceConsoleEntry struct {
	Level     string    `json:"level"`
	Source    string    `json:"source"`
	Text      string    `json:"text"`
	Timestamp time.Time `json:"timestamp"`
}

type UIEvidencePageError struct {
	Text      string    `json:"text"`
	Timestamp time.Time `json:"timestamp"`
}

type UIEvidenceNetworkEntry struct {
	RequestID    string `json:"request_id"`
	URL          string `json:"url"`
	Method       string `json:"method"`
	ResourceType string `json:"resource_type"`
	Status       int    `json:"status,omitempty"`
	MIME         string `json:"mime,omitempty"`
	Failed       bool   `json:"failed"`
	Cancelled    bool   `json:"cancelled"`
	ErrorText    string `json:"error_text,omitempty"`
}

type UIEvidenceDiagnostics struct {
	Console           []UIEvidenceConsoleEntry      `json:"console"`
	PageErrors        []UIEvidencePageError         `json:"page_errors"`
	Network           []UIEvidenceNetworkEntry      `json:"network"`
	Summary           uievidence.DiagnosticsSummary `json:"summary"`
	UntrustedEvidence bool                          `json:"untrusted_evidence"`
	CapturedAt        time.Time                     `json:"captured_at"`
}

type UIEvidenceTextCapture struct {
	MIME       string
	Content    []byte
	Redacted   bool
	CapturedAt time.Time
}

// ConfigureUIEvidence applies the exact viewport, DPR, locale, theme, and
// reduced-motion tuple from the manifest. It cannot change origin policy,
// certificates, downloads, cookies, or network scope.
func (runtime *RestrictedBrowserSession) ConfigureUIEvidence(ctx context.Context,
	environment uievidence.Environment,
) error {
	if runtime == nil || !runtime.authorization.UIEvidenceAuthorized ||
		environment.Validate() != nil {
		return ErrBrowserRuntimeBoundary
	}
	release, operationContext, cancel, err := runtime.beginOperation(ctx)
	if err != nil {
		return err
	}
	defer release()
	defer cancel()
	viewport := environment.Viewport
	if err := runtime.client.call(operationContext, runtime.client.sessionID,
		"Emulation.setDeviceMetricsOverride", map[string]any{
			"width": viewport.Width, "height": viewport.Height,
			"deviceScaleFactor": viewport.DPR, "mobile": false,
			"screenWidth": viewport.Width, "screenHeight": viewport.Height,
		}, &struct{}{}); err != nil {
		return err
	}
	if err := runtime.client.call(operationContext, runtime.client.sessionID,
		"Emulation.setLocaleOverride", map[string]any{"locale": environment.Locale},
		&struct{}{}); err != nil {
		return err
	}
	features := []map[string]string{{"name": "prefers-color-scheme",
		"value": string(environment.Theme)}}
	motion := "no-preference"
	if environment.ReducedMotion {
		motion = "reduce"
	}
	features = append(features, map[string]string{"name": "prefers-reduced-motion",
		"value": motion})
	return runtime.client.call(operationContext, runtime.client.sessionID,
		"Emulation.setEmulatedMedia", map[string]any{"media": "screen", "features": features},
		&struct{}{})
}

func (runtime *RestrictedBrowserSession) ClickUIEvidence(ctx context.Context,
	selector string,
) error {
	if runtime == nil || !runtime.authorization.UIEvidenceAuthorized ||
		!validUIEvidenceSelector(selector) {
		return ErrBrowserRuntimeBoundary
	}
	release, operationContext, cancel, err := runtime.beginOperation(ctx)
	if err != nil {
		return err
	}
	defer release()
	defer cancel()
	nodeID, found, err := runtime.client.querySelector(operationContext, selector)
	if err != nil {
		return err
	}
	if !found {
		return errors.New("UI evidence selector did not match")
	}
	if err := runtime.client.call(operationContext, runtime.client.sessionID,
		"DOM.scrollIntoViewIfNeeded", map[string]any{"nodeId": nodeID}, &struct{}{}); err != nil {
		return err
	}
	rectangle, err := runtime.client.nodeRectangle(operationContext, nodeID)
	if err != nil {
		return err
	}
	x := float64(rectangle.Min.X+rectangle.Max.X) / 2
	y := float64(rectangle.Min.Y+rectangle.Max.Y) / 2
	for _, eventType := range []string{"mousePressed", "mouseReleased"} {
		params := map[string]any{"type": eventType, "x": x, "y": y,
			"button": "left", "clickCount": 1}
		if err := runtime.client.call(operationContext, runtime.client.sessionID,
			"Input.dispatchMouseEvent", params, &struct{}{}); err != nil {
			return err
		}
	}
	return nil
}

// TypeUIEvidence sends bounded fixture text to one selected node. The caller
// must provide the digest sealed in the manifest; raw input is never returned
// or persisted by this package.
func (runtime *RestrictedBrowserSession) TypeUIEvidence(ctx context.Context,
	selector, value, expectedSHA256 string,
) error {
	if runtime == nil || !runtime.authorization.UIEvidenceAuthorized ||
		!validUIEvidenceSelector(selector) || len([]byte(value)) > MaxUIEvidenceInputBytes ||
		!utf8.ValidString(value) || strings.ContainsRune(value, 0) ||
		redact.String(value) != value {
		return ErrBrowserRuntimeBoundary
	}
	digest := sha256.Sum256([]byte(value))
	if hex.EncodeToString(digest[:]) != expectedSHA256 {
		return errors.New("UI evidence input does not match the sealed digest")
	}
	release, operationContext, cancel, err := runtime.beginOperation(ctx)
	if err != nil {
		return err
	}
	defer release()
	defer cancel()
	nodeID, found, err := runtime.client.querySelector(operationContext, selector)
	if err != nil {
		return err
	}
	if !found {
		return errors.New("UI evidence selector did not match")
	}
	if err := runtime.client.call(operationContext, runtime.client.sessionID,
		"DOM.focus", map[string]any{"nodeId": nodeID}, &struct{}{}); err != nil {
		return err
	}
	return runtime.client.call(operationContext, runtime.client.sessionID,
		"Input.insertText", map[string]any{"text": value}, &struct{}{})
}

func (runtime *RestrictedBrowserSession) AssertUIEvidenceSelector(ctx context.Context,
	selector string, expectedPresent bool,
) error {
	if runtime == nil || !runtime.authorization.UIEvidenceAuthorized ||
		!validUIEvidenceSelector(selector) {
		return ErrBrowserRuntimeBoundary
	}
	release, operationContext, cancel, err := runtime.beginOperation(ctx)
	if err != nil {
		return err
	}
	defer release()
	defer cancel()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		_, found, err := runtime.client.querySelector(operationContext, selector)
		if err != nil {
			return err
		}
		if found == expectedPresent {
			return nil
		}
		select {
		case <-operationContext.Done():
			if ctx != nil && ctx.Err() != nil {
				return ctx.Err()
			}
			if expectedPresent {
				return errors.New("UI evidence assertion expected selector to be present")
			}
			return errors.New("UI evidence assertion expected selector to be absent")
		case <-ticker.C:
		}
	}
}

func (runtime *RestrictedBrowserSession) DOMUIEvidence(ctx context.Context) (
	UIEvidenceTextCapture, error,
) {
	if runtime == nil || !runtime.authorization.UIEvidenceAuthorized {
		return UIEvidenceTextCapture{}, ErrBrowserRuntimeBoundary
	}
	release, operationContext, cancel, err := runtime.beginOperation(ctx)
	if err != nil {
		return UIEvidenceTextCapture{}, err
	}
	defer release()
	defer cancel()
	rootID, err := runtime.client.documentNodeID(operationContext)
	if err != nil {
		return UIEvidenceTextCapture{}, err
	}
	var result struct {
		OuterHTML string `json:"outerHTML"`
	}
	if err := runtime.client.call(operationContext, runtime.client.sessionID,
		"DOM.getOuterHTML", map[string]any{"nodeId": rootID}, &result); err != nil {
		return UIEvidenceTextCapture{}, err
	}
	content, err := sanitizeUIEvidenceText([]byte(result.OuterHTML))
	if err != nil {
		return UIEvidenceTextCapture{}, err
	}
	return UIEvidenceTextCapture{MIME: "text/html; charset=utf-8", Content: content,
		Redacted: true, CapturedAt: time.Now().UTC()}, nil
}

func (runtime *RestrictedBrowserSession) AccessibilityUIEvidence(ctx context.Context) (
	UIEvidenceTextCapture, error,
) {
	if runtime == nil || !runtime.authorization.UIEvidenceAuthorized {
		return UIEvidenceTextCapture{}, ErrBrowserRuntimeBoundary
	}
	release, operationContext, cancel, err := runtime.beginOperation(ctx)
	if err != nil {
		return UIEvidenceTextCapture{}, err
	}
	defer release()
	defer cancel()
	var tree struct {
		Nodes []json.RawMessage `json:"nodes"`
	}
	if err := runtime.client.call(operationContext, runtime.client.sessionID,
		"Accessibility.getFullAXTree", map[string]any{"depth": 64}, &tree); err != nil {
		return UIEvidenceTextCapture{}, err
	}
	raw, err := json.Marshal(tree)
	if err != nil {
		return UIEvidenceTextCapture{}, err
	}
	content, err := sanitizeUIEvidenceText(raw)
	if err != nil {
		return UIEvidenceTextCapture{}, err
	}
	return UIEvidenceTextCapture{MIME: "application/json", Content: content,
		Redacted: true, CapturedAt: time.Now().UTC()}, nil
}

func (runtime *RestrictedBrowserSession) PerformanceUIEvidence(ctx context.Context) (
	UIEvidenceTextCapture, error,
) {
	if runtime == nil || !runtime.authorization.UIEvidenceAuthorized {
		return UIEvidenceTextCapture{}, ErrBrowserRuntimeBoundary
	}
	release, operationContext, cancel, err := runtime.beginOperation(ctx)
	if err != nil {
		return UIEvidenceTextCapture{}, err
	}
	defer release()
	defer cancel()
	var metrics struct {
		Metrics []struct {
			Name  string  `json:"name"`
			Value float64 `json:"value"`
		} `json:"metrics"`
	}
	if err := runtime.client.call(operationContext, runtime.client.sessionID,
		"Performance.getMetrics", map[string]any{}, &metrics); err != nil {
		return UIEvidenceTextCapture{}, err
	}
	sort.Slice(metrics.Metrics, func(i, j int) bool {
		return metrics.Metrics[i].Name < metrics.Metrics[j].Name
	})
	raw, err := json.Marshal(metrics)
	if err != nil {
		return UIEvidenceTextCapture{}, err
	}
	content, err := sanitizeUIEvidenceText(raw)
	if err != nil {
		return UIEvidenceTextCapture{}, err
	}
	return UIEvidenceTextCapture{MIME: "application/json", Content: content,
		Redacted: true, CapturedAt: time.Now().UTC()}, nil
}

func (runtime *RestrictedBrowserSession) DiagnosticsUIEvidence(ctx context.Context) (
	UIEvidenceDiagnostics, UIEvidenceTextCapture, error,
) {
	if runtime == nil || !runtime.authorization.UIEvidenceAuthorized {
		return UIEvidenceDiagnostics{}, UIEvidenceTextCapture{}, ErrBrowserRuntimeBoundary
	}
	release, operationContext, cancel, err := runtime.beginOperation(ctx)
	if err != nil {
		return UIEvidenceDiagnostics{}, UIEvidenceTextCapture{}, err
	}
	defer release()
	defer cancel()
	// Fixed metrics calls drain queued protocol events until all observed
	// requests have completed and the event stream has remained quiet. This
	// prevents a delayed console/network failure from racing a green receipt.
	if err := runtime.client.drainUIEvidenceEventsUntilIdle(operationContext); err != nil {
		return UIEvidenceDiagnostics{}, UIEvidenceTextCapture{}, err
	}
	diagnostics := runtime.client.diagnostics()
	raw, err := json.Marshal(diagnostics)
	if err != nil {
		return UIEvidenceDiagnostics{}, UIEvidenceTextCapture{}, err
	}
	content, err := sanitizeUIEvidenceText(raw)
	if err != nil {
		return UIEvidenceDiagnostics{}, UIEvidenceTextCapture{}, err
	}
	return diagnostics, UIEvidenceTextCapture{MIME: "application/json", Content: content,
		Redacted: true, CapturedAt: diagnostics.CapturedAt}, nil
}

func (runtime *RestrictedBrowserSession) ScreenshotUIEvidence(ctx context.Context,
	maskSelectors []string, dpr float64,
) (RestrictedScreenshot, int, int, error) {
	if runtime == nil || !runtime.authorization.UIEvidenceAuthorized ||
		len(maskSelectors) > uievidence.MaxMasks || dpr < .5 || dpr > 4 {
		return RestrictedScreenshot{}, 0, 0, ErrBrowserRuntimeBoundary
	}
	for _, selector := range maskSelectors {
		if !validUIEvidenceSelector(selector) {
			return RestrictedScreenshot{}, 0, 0, ErrBrowserRuntimeBoundary
		}
	}
	release, operationContext, cancel, err := runtime.beginOperation(ctx)
	if err != nil {
		return RestrictedScreenshot{}, 0, 0, err
	}
	defer release()
	defer cancel()
	rectangles := make([]image.Rectangle, 0, len(maskSelectors))
	for _, selector := range maskSelectors {
		nodeID, found, err := runtime.client.querySelector(operationContext, selector)
		if err != nil {
			return RestrictedScreenshot{}, 0, 0, err
		}
		if !found {
			return RestrictedScreenshot{}, 0, 0,
				fmt.Errorf("UI evidence mask selector did not match")
		}
		rectangle, err := runtime.client.nodeRectangle(operationContext, nodeID)
		if err != nil {
			return RestrictedScreenshot{}, 0, 0, err
		}
		rectangles = append(rectangles, scaleRectangle(rectangle, dpr))
	}
	screenshot, err := runtime.client.captureScreenshot(operationContext,
		runtime.authorization.Fingerprint)
	if err != nil {
		return RestrictedScreenshot{}, 0, 0, err
	}
	decoded, err := decodeBoundedUIEvidencePNG(screenshot.PNG)
	if err != nil {
		return RestrictedScreenshot{}, 0, 0,
			err
	}
	canvas := image.NewRGBA(decoded.Bounds())
	draw.Draw(canvas, canvas.Bounds(), decoded, decoded.Bounds().Min, draw.Src)
	for _, rectangle := range rectangles {
		draw.Draw(canvas, rectangle.Intersect(canvas.Bounds()),
			&image.Uniform{C: color.RGBA{R: 20, G: 24, B: 32, A: 255}}, image.Point{}, draw.Src)
	}
	var output bytes.Buffer
	if err := png.Encode(&output, canvas); err != nil || output.Len() > MaxScreenshotBytes {
		return RestrictedScreenshot{}, 0, 0, errors.New("UI evidence screenshot exceeds its bound")
	}
	digest := sha256.Sum256(output.Bytes())
	screenshot.PNG = append([]byte(nil), output.Bytes()...)
	screenshot.Bytes = len(screenshot.PNG)
	screenshot.SHA256 = hex.EncodeToString(digest[:])
	screenshot.CompletedAt = time.Now().UTC()
	screenshot.Fingerprint = browserRuntimeFingerprint(screenshot)
	return screenshot, canvas.Bounds().Dx(), canvas.Bounds().Dy(), nil
}

func decodeBoundedUIEvidencePNG(content []byte) (image.Image, error) {
	configuration, err := png.DecodeConfig(bytes.NewReader(content))
	if err != nil || configuration.Width < 1 || configuration.Height < 1 ||
		configuration.Width > uievidence.MaxScreenshotWidth ||
		configuration.Height > uievidence.MaxScreenshotHeight {
		return nil, errors.New("chromium returned an invalid or out-of-bounds PNG screenshot")
	}
	decoded, err := png.Decode(bytes.NewReader(content))
	if err != nil {
		return nil, errors.New("chromium returned an invalid PNG screenshot")
	}
	return decoded, nil
}

func (client *restrictedCDPClient) documentNodeID(ctx context.Context) (int64, error) {
	var document struct {
		Root struct {
			NodeID      int64  `json:"nodeId"`
			DocumentURL string `json:"documentURL"`
		} `json:"root"`
	}
	if err := client.call(ctx, client.sessionID, "DOM.getDocument",
		map[string]any{"depth": 0, "pierce": false}, &document); err != nil {
		return 0, err
	}
	decision := client.scope.AuthorizeNavigation(document.Root.DocumentURL)
	if !decision.Allowed || document.Root.NodeID <= 0 {
		return 0, errors.New("UI evidence document is outside the exact scope")
	}
	return document.Root.NodeID, nil
}

func (client *restrictedCDPClient) querySelector(ctx context.Context,
	selector string,
) (int64, bool, error) {
	rootID, err := client.documentNodeID(ctx)
	if err != nil {
		return 0, false, err
	}
	var result struct {
		NodeID int64 `json:"nodeId"`
	}
	if err := client.call(ctx, client.sessionID, "DOM.querySelector",
		map[string]any{"nodeId": rootID, "selector": selector}, &result); err != nil {
		return 0, false, err
	}
	if result.NodeID < 0 {
		return 0, false, errors.New("chromium returned an invalid DOM node")
	}
	return result.NodeID, result.NodeID > 0, nil
}

func (client *restrictedCDPClient) nodeRectangle(ctx context.Context,
	nodeID int64,
) (image.Rectangle, error) {
	var result struct {
		Model struct {
			Border  []float64 `json:"border"`
			Content []float64 `json:"content"`
		} `json:"model"`
	}
	if err := client.call(ctx, client.sessionID, "DOM.getBoxModel",
		map[string]any{"nodeId": nodeID}, &result); err != nil {
		return image.Rectangle{}, err
	}
	quad := result.Model.Border
	if len(quad) != 8 {
		quad = result.Model.Content
	}
	if len(quad) != 8 {
		return image.Rectangle{}, errors.New("UI evidence node has no bounded box")
	}
	minX, minY, maxX, maxY := quad[0], quad[1], quad[0], quad[1]
	for index := 0; index < len(quad); index += 2 {
		x, y := quad[index], quad[index+1]
		if math.IsNaN(x) || math.IsInf(x, 0) || math.IsNaN(y) || math.IsInf(y, 0) ||
			x < -100000 || x > 100000 || y < -100000 || y > 100000 {
			return image.Rectangle{}, errors.New("UI evidence node box is invalid")
		}
		minX, minY, maxX, maxY = math.Min(minX, x), math.Min(minY, y),
			math.Max(maxX, x), math.Max(maxY, y)
	}
	if maxX <= minX || maxY <= minY {
		return image.Rectangle{}, errors.New("UI evidence node is not visible")
	}
	return image.Rect(int(math.Floor(minX)), int(math.Floor(minY)),
		int(math.Ceil(maxX)), int(math.Ceil(maxY))), nil
}

func (client *restrictedCDPClient) captureScreenshot(ctx context.Context,
	authorization string,
) (RestrictedScreenshot, error) {
	canonicalURL, _, _, err := client.documentIdentity(ctx)
	if err != nil {
		return RestrictedScreenshot{}, err
	}
	var capture struct {
		Data string `json:"data"`
	}
	if err := client.call(ctx, client.sessionID, "Page.captureScreenshot",
		map[string]any{"format": "png", "fromSurface": true,
			"captureBeyondViewport": false}, &capture); err != nil {
		return RestrictedScreenshot{}, err
	}
	pngBytes, err := decodeRestrictedScreenshot(capture.Data, MaxScreenshotBytes)
	if err != nil {
		return RestrictedScreenshot{}, err
	}
	digest := sha256.Sum256(pngBytes)
	result := RestrictedScreenshot{ProtocolVersion: RestrictedScreenshotProtocolVersion,
		Authorization: authorization, CanonicalURL: canonicalURL, MediaType: "image/png",
		Bytes: len(pngBytes), SHA256: hex.EncodeToString(digest[:]),
		PNG: append([]byte(nil), pngBytes...), UntrustedEvidence: true,
		CompletedAt: time.Now().UTC()}
	result.Fingerprint = browserRuntimeFingerprint(result)
	return result, nil
}

func (client *restrictedCDPClient) captureConsoleAPICalled(raw json.RawMessage) error {
	if !client.uiEvidence {
		return nil
	}
	var event struct {
		Type      string  `json:"type"`
		Timestamp float64 `json:"timestamp"`
		Args      []struct {
			Type        string `json:"type"`
			Value       any    `json:"value"`
			Description string `json:"description"`
		} `json:"args"`
	}
	if json.Unmarshal(raw, &event) != nil {
		return errors.New("UI evidence console event is malformed")
	}
	parts := make([]string, 0, len(event.Args))
	for _, argument := range event.Args {
		value := argument.Description
		if value == "" && argument.Value != nil {
			encoded, _ := json.Marshal(argument.Value)
			value = string(encoded)
		}
		parts = append(parts, value)
	}
	return client.appendConsole(UIEvidenceConsoleEntry{Level: normalizeConsoleLevel(event.Type),
		Source: "console", Text: safeDiagnosticText(strings.Join(parts, " ")),
		Timestamp: cdpTimestamp(event.Timestamp)})
}

func (client *restrictedCDPClient) captureLogEntry(raw json.RawMessage) error {
	if !client.uiEvidence {
		return nil
	}
	var event struct {
		Entry struct {
			Source    string  `json:"source"`
			Level     string  `json:"level"`
			Text      string  `json:"text"`
			Timestamp float64 `json:"timestamp"`
		} `json:"entry"`
	}
	if json.Unmarshal(raw, &event) != nil {
		return errors.New("UI evidence log event is malformed")
	}
	return client.appendConsole(UIEvidenceConsoleEntry{Level: normalizeConsoleLevel(event.Entry.Level),
		Source: safeDiagnosticText(event.Entry.Source), Text: safeDiagnosticText(event.Entry.Text),
		Timestamp: cdpTimestamp(event.Entry.Timestamp)})
}

func (client *restrictedCDPClient) capturePageException(raw json.RawMessage) error {
	if !client.uiEvidence {
		return nil
	}
	var event struct {
		Timestamp        float64 `json:"timestamp"`
		ExceptionDetails struct {
			Text      string `json:"text"`
			Exception struct {
				Description string `json:"description"`
			} `json:"exception"`
		} `json:"exceptionDetails"`
	}
	if json.Unmarshal(raw, &event) != nil {
		return errors.New("UI evidence page error is malformed")
	}
	text := event.ExceptionDetails.Exception.Description
	if text == "" {
		text = event.ExceptionDetails.Text
	}
	return client.appendPageError(UIEvidencePageError{
		Text: safeDiagnosticText(text), Timestamp: cdpTimestamp(event.Timestamp)})
}

func (client *restrictedCDPClient) captureNetworkRequest(raw json.RawMessage) error {
	if !client.uiEvidence {
		return nil
	}
	var event struct {
		RequestID string `json:"requestId"`
		Type      string `json:"type"`
		Request   struct {
			URL    string `json:"url"`
			Method string `json:"method"`
		} `json:"request"`
	}
	if json.Unmarshal(raw, &event) != nil || !validRestrictedCDPToken(event.RequestID) {
		return errors.New("UI evidence network request is malformed")
	}
	if client.networkPending == nil {
		client.networkPending = make(map[string]struct{})
	}
	if _, exists := client.networkPending[event.RequestID]; !exists {
		if len(client.networkPending) >= client.maxRequests {
			client.budgetErr = errors.New("UI evidence network event budget exhausted")
			return client.budgetErr
		}
		client.networkPending[event.RequestID] = struct{}{}
	}
	if len(client.networkEntries) >= client.maxRequests {
		return client.exhaustUIEvidenceDiagnosticBudget()
	}
	client.networkEntries = append(client.networkEntries, UIEvidenceNetworkEntry{
		RequestID: event.RequestID, URL: client.safeEvidenceURL(event.Request.URL),
		Method: safeHTTPMethod(event.Request.Method), ResourceType: safeDiagnosticText(event.Type)})
	return nil
}

func (client *restrictedCDPClient) captureNetworkResponse(raw json.RawMessage) error {
	if !client.uiEvidence {
		return nil
	}
	var event struct {
		RequestID string `json:"requestId"`
		Response  struct {
			URL      string  `json:"url"`
			Status   float64 `json:"status"`
			MimeType string  `json:"mimeType"`
		} `json:"response"`
	}
	if json.Unmarshal(raw, &event) != nil || !validRestrictedCDPToken(event.RequestID) ||
		math.IsNaN(event.Response.Status) || event.Response.Status < 0 || event.Response.Status > 999 {
		return errors.New("UI evidence network response is malformed")
	}
	entry := client.networkEntry(event.RequestID)
	if entry == nil {
		if len(client.networkEntries) >= client.maxRequests {
			return client.exhaustUIEvidenceDiagnosticBudget()
		}
		client.networkEntries = append(client.networkEntries, UIEvidenceNetworkEntry{
			RequestID: event.RequestID, URL: client.safeEvidenceURL(event.Response.URL)})
		entry = &client.networkEntries[len(client.networkEntries)-1]
	}
	if entry != nil {
		entry.Status = int(math.Round(event.Response.Status))
		entry.MIME = safeDiagnosticText(event.Response.MimeType)
	}
	return nil
}

func (client *restrictedCDPClient) captureNetworkFailure(raw json.RawMessage) error {
	if !client.uiEvidence {
		return nil
	}
	var event struct {
		RequestID string `json:"requestId"`
		ErrorText string `json:"errorText"`
		Canceled  bool   `json:"canceled"`
	}
	if json.Unmarshal(raw, &event) != nil || !validRestrictedCDPToken(event.RequestID) {
		return errors.New("UI evidence network failure is malformed")
	}
	delete(client.networkPending, event.RequestID)
	entry := client.networkEntry(event.RequestID)
	if entry == nil {
		if len(client.networkEntries) >= client.maxRequests {
			return client.exhaustUIEvidenceDiagnosticBudget()
		}
		client.networkEntries = append(client.networkEntries,
			UIEvidenceNetworkEntry{RequestID: event.RequestID})
		entry = &client.networkEntries[len(client.networkEntries)-1]
	}
	if entry != nil {
		entry.Failed, entry.Cancelled = true, event.Canceled
		entry.ErrorText = safeDiagnosticText(event.ErrorText)
	}
	return nil
}

func (client *restrictedCDPClient) captureNetworkFinished(raw json.RawMessage) error {
	if !client.uiEvidence {
		return nil
	}
	var event struct {
		RequestID string `json:"requestId"`
	}
	if json.Unmarshal(raw, &event) != nil || !validRestrictedCDPToken(event.RequestID) {
		return errors.New("UI evidence network completion is malformed")
	}
	delete(client.networkPending, event.RequestID)
	return nil
}

func (client *restrictedCDPClient) drainUIEvidenceEventsUntilIdle(ctx context.Context) error {
	const quietWindow = 100 * time.Millisecond
	quietSince := time.Time{}
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		var ignored struct {
			Metrics []json.RawMessage `json:"metrics"`
		}
		if err := client.call(ctx, client.sessionID, "Performance.getMetrics",
			map[string]any{}, &ignored); err != nil {
			return err
		}
		if client.budgetErr != nil {
			return client.budgetErr
		}
		now := time.Now()
		if len(client.networkPending) == 0 {
			if quietSince.IsZero() {
				quietSince = now
			} else if now.Sub(quietSince) >= quietWindow {
				return nil
			}
		} else {
			quietSince = time.Time{}
		}
		select {
		case <-ctx.Done():
			return errors.New("UI evidence diagnostics did not reach bounded network idle")
		case <-ticker.C:
		}
	}
}

func (client *restrictedCDPClient) appendConsole(entry UIEvidenceConsoleEntry) error {
	if len(client.consoleEntries) >= client.maxRequests {
		return client.exhaustUIEvidenceDiagnosticBudget()
	}
	client.consoleEntries = append(client.consoleEntries, entry)
	return nil
}

func (client *restrictedCDPClient) appendPageError(entry UIEvidencePageError) error {
	if len(client.pageErrors) >= client.maxRequests {
		return client.exhaustUIEvidenceDiagnosticBudget()
	}
	client.pageErrors = append(client.pageErrors, entry)
	return nil
}

func (client *restrictedCDPClient) exhaustUIEvidenceDiagnosticBudget() error {
	if client.budgetErr == nil {
		client.budgetErr = errors.New("UI evidence diagnostic event budget exhausted")
	}
	return client.budgetErr
}

func (client *restrictedCDPClient) networkEntry(requestID string) *UIEvidenceNetworkEntry {
	for index := len(client.networkEntries) - 1; index >= 0; index-- {
		if client.networkEntries[index].RequestID == requestID {
			return &client.networkEntries[index]
		}
	}
	return nil
}

func (client *restrictedCDPClient) diagnostics() UIEvidenceDiagnostics {
	diagnostics := UIEvidenceDiagnostics{
		Console:           append([]UIEvidenceConsoleEntry(nil), client.consoleEntries...),
		PageErrors:        append([]UIEvidencePageError(nil), client.pageErrors...),
		Network:           append([]UIEvidenceNetworkEntry(nil), client.networkEntries...),
		UntrustedEvidence: true, CapturedAt: time.Now().UTC()}
	diagnostics.Summary.AllowedRequests = client.allowedRequests
	diagnostics.Summary.BlockedRequests = client.blockedRequests
	for _, entry := range diagnostics.Console {
		switch entry.Level {
		case "error":
			diagnostics.Summary.ConsoleErrors++
		case "warning":
			diagnostics.Summary.ConsoleWarnings++
		}
	}
	diagnostics.Summary.PageErrors = len(diagnostics.PageErrors)
	for _, entry := range diagnostics.Network {
		if entry.Failed && !entry.Cancelled {
			diagnostics.Summary.FailedRequests++
		}
		if entry.Status >= 400 {
			diagnostics.Summary.HTTPFailures++
		}
	}
	return diagnostics
}

func validUIEvidenceSelector(value string) bool {
	return value != "" && value == strings.TrimSpace(value) &&
		len([]byte(value)) <= uievidence.MaxSelectorBytes && utf8.ValidString(value) &&
		!strings.ContainsAny(value, "\x00\r\n") && redact.String(value) == value
}

func safeDiagnosticText(value string) string {
	value = strings.TrimSpace(outputsafe.Sanitize([]byte(value)))
	for len([]byte(value)) > MaxUIEvidenceDiagnosticTextBytes {
		_, size := utf8.DecodeLastRuneInString(value)
		value = value[:len(value)-size]
	}
	return value
}

func (client *restrictedCDPClient) safeEvidenceURL(value string) string {
	decision := client.scope.AuthorizeNavigation(value)
	if !decision.Allowed {
		return "[blocked-url]"
	}
	parsed, err := url.Parse(decision.CanonicalURL)
	if err != nil {
		return "[blocked-url]"
	}
	parsed.User, parsed.RawQuery, parsed.Fragment = nil, "", ""
	return safeDiagnosticText(parsed.String())
}

func safeHTTPMethod(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	for _, current := range value {
		if current < 'A' || current > 'Z' {
			return "UNKNOWN"
		}
	}
	if value == "" || len(value) > 16 {
		return "UNKNOWN"
	}
	return value
}

func normalizeConsoleLevel(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "error", "assert":
		return "error"
	case "warning", "warn":
		return "warning"
	case "debug":
		return "debug"
	default:
		return "info"
	}
}

func cdpTimestamp(value float64) time.Time {
	if math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 {
		return time.Now().UTC()
	}
	seconds, fraction := math.Modf(value / 1000)
	return time.Unix(int64(seconds), int64(fraction*float64(time.Second))).UTC()
}

func sanitizeUIEvidenceText(raw []byte) ([]byte, error) {
	if len(raw) == 0 || len(raw) > MaxUIEvidenceTextArtifactBytes {
		return nil, errors.New("UI evidence text artifact is empty or exceeds its bound")
	}
	value := []byte(outputsafe.Sanitize(raw))
	if len(value) == 0 || len(value) > MaxUIEvidenceTextArtifactBytes {
		return nil, errors.New("sanitized UI evidence text artifact exceeds its bound")
	}
	return value, nil
}

func scaleRectangle(value image.Rectangle, dpr float64) image.Rectangle {
	return image.Rect(int(math.Floor(float64(value.Min.X)*dpr)),
		int(math.Floor(float64(value.Min.Y)*dpr)),
		int(math.Ceil(float64(value.Max.X)*dpr)),
		int(math.Ceil(float64(value.Max.Y)*dpr)))
}
