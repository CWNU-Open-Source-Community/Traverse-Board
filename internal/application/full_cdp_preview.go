package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image/png"
	"net/url"
	"strings"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/browserruntime"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/idgen"
	"cyberagent-workbench/internal/toolgateway"
)

const FullCDPPreviewProtocolVersion = "full_cdp_preview.v1"
const FullCDPPreviewActionProtocolVersion = "full_cdp_preview_action.v1"

type FullCDPPreviewPage struct {
	SnapshotID         string                              `json:"snapshot_id"`
	Title              string                              `json:"title"`
	Text               string                              `json:"text"`
	Elements           []browserruntime.FullCDPPageElement `json:"elements"`
	AccessibilityNodes int                                 `json:"accessibility_nodes"`
	Truncated          bool                                `json:"truncated"`
	UntrustedEvidence  bool                                `json:"untrusted_evidence"`
}

type FullCDPPreviewImage struct {
	MediaType string `json:"media_type"`
	Bytes     int    `json:"bytes"`
	SHA256    string `json:"sha256"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
}

// FullCDPPreviewView describes the most recent observation, not a durable test
// receipt. Page text and screenshot are sequential observations of this URL.
type FullCDPPreviewView struct {
	Version      string              `json:"version"`
	RunID        string              `json:"run_id"`
	SessionID    string              `json:"session_id"`
	CanonicalURL string              `json:"canonical_url"`
	CapturedAt   time.Time           `json:"captured_at"`
	Page         FullCDPPreviewPage  `json:"page"`
	Image        FullCDPPreviewImage `json:"image"`
}

type FullCDPPreviewCapture struct {
	View FullCDPPreviewView
	PNG  []byte
}

type FullCDPPreviewController interface {
	CaptureFullCDPPreview(context.Context, string, string) (FullCDPPreviewView, error)
	ReadFullCDPPreviewImage(context.Context, string, string, string) (FullCDPPreviewCapture, error)
	ActFullCDPPreview(context.Context, FullCDPPreviewActionRequest) (FullCDPPreviewView, error)
}

type FullCDPPreviewActionRequest struct {
	RunID      string
	SessionID  string
	SnapshotID string
	Action     string
	Selector   string
	Value      string
}

func (s *FullCDPProductionService) previewBinding(ctx context.Context, runID, sessionID string) (fullCDPBrowserActionBinding, error) {
	if !domain.ValidAgentID(runID) || runID != strings.TrimSpace(runID) ||
		!domain.ValidAgentID(sessionID) || sessionID != strings.TrimSpace(sessionID) {
		return fullCDPBrowserActionBinding{}, apperror.New(apperror.CodeInvalidArgument, "browser preview identity is invalid")
	}
	binding, available, err := s.browserActionBinding(ctx, runID)
	if err != nil {
		return binding, err
	}
	if !available || binding.view.SessionID != sessionID || binding.view.ExpiresAt == nil ||
		!binding.view.ExpiresAt.After(time.Now().UTC()) {
		return binding, apperror.New(apperror.CodeConflict, "browser preview session is closed, expired, or its permission changed")
	}
	return binding, nil
}

// CaptureFullCDPPreview uses the same live binding and revocation monitor as
// model browser actions. HTTP control does not bypass the owned CDP runtime.
func (s *FullCDPProductionService) CaptureFullCDPPreview(ctx context.Context, runID, sessionID string) (FullCDPPreviewView, error) {
	binding, err := s.previewBinding(ctx, runID, sessionID)
	if err != nil {
		return FullCDPPreviewView{}, err
	}
	s.mu.Lock()
	entry := s.latestByRun[runID]
	if entry == nil || entry.view.SessionID != sessionID || entry.previewBusy {
		s.mu.Unlock()
		return FullCDPPreviewView{}, apperror.New(apperror.CodeConflict, "browser preview capture is already active or its session changed")
	}
	entry.previewBusy = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); entry.previewBusy = false; s.mu.Unlock() }()
	return s.captureFullCDPPreview(ctx, binding, entry)
}

func (s *FullCDPProductionService) captureFullCDPPreview(ctx context.Context, binding fullCDPBrowserActionBinding, entry *fullCDPSessionEntry) (FullCDPPreviewView, error) {
	runID, sessionID := binding.view.RunID, binding.view.SessionID
	actionCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	stopMonitor := s.monitorFullCDPBrowserActionAuthority(actionCtx, cancel, runID, binding)
	defer stopMonitor()
	page, err := binding.runtime.BrowserSnapshot(actionCtx)
	if err != nil {
		return FullCDPPreviewView{}, normalizeFullCDPBrowserActionContextError(ctx, actionCtx, err)
	}
	capture, err := binding.runtime.BrowserScreenshot(actionCtx)
	if err != nil {
		return FullCDPPreviewView{}, normalizeFullCDPBrowserActionContextError(ctx, actionCtx, err)
	}
	image, err := fullCDPPreviewImage(capture)
	if err != nil {
		return FullCDPPreviewView{}, err
	}
	observedURL, urlErr := url.Parse(capture.Metadata.CanonicalURL)
	if urlErr != nil || observedURL.Scheme+"://"+observedURL.Host != binding.view.TargetOrigin ||
		page.CanonicalURL != capture.Metadata.CanonicalURL || !page.UntrustedEvidence ||
		page.ProtocolVersion != browserruntime.FullCDPPageSnapshotProtocolVersion ||
		len(page.Elements) > browserruntime.MaxFullCDPPageSnapshotElements ||
		len(page.Text) > browserruntime.MaxFullCDPPageSnapshotTextBytes || page.AccessibilityNodes < 0 {
		return FullCDPPreviewView{}, apperror.New(apperror.CodeConflict, "browser page changed while the preview was captured")
	}
	current, err := s.previewBinding(actionCtx, runID, sessionID)
	if err != nil || !sameFullCDPBrowserActionBinding(binding, current) {
		if err != nil {
			return FullCDPPreviewView{}, err
		}
		return FullCDPPreviewView{}, apperror.New(apperror.CodeConflict, "browser preview authority changed")
	}
	elements := append([]browserruntime.FullCDPPageElement{}, page.Elements...)
	// Each observation is a single-use UI action reference even when two frames
	// have identical pixels. It conveys no executable selector authority itself.
	snapshotDigest := sha256.Sum256([]byte(page.Fingerprint + idgen.New("browser-preview")))
	view := FullCDPPreviewView{Version: FullCDPPreviewProtocolVersion, RunID: runID,
		SessionID: sessionID, CanonicalURL: capture.Metadata.CanonicalURL, CapturedAt: capture.Metadata.CompletedAt,
		Page: FullCDPPreviewPage{SnapshotID: hex.EncodeToString(snapshotDigest[:]), Title: page.Title, Text: page.Text, Elements: elements,
			AccessibilityNodes: page.AccessibilityNodes, Truncated: page.Truncated, UntrustedEvidence: true}, Image: image}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.latestByRun[runID] != entry || entry.view.State != FullCDPSessionReady || actionCtx.Err() != nil {
		return FullCDPPreviewView{}, apperror.New(apperror.CodeConflict, "browser preview session changed before publication")
	}
	entry.preview = &FullCDPPreviewCapture{View: view, PNG: append([]byte(nil), capture.PNG...)}
	return view, nil
}

// ActFullCDPPreview consumes the exact UI observation before interacting. A
// lost response must be followed by a fresh observation, never blind replay.
func (s *FullCDPProductionService) ActFullCDPPreview(ctx context.Context, request FullCDPPreviewActionRequest) (FullCDPPreviewView, error) {
	name, version := toolgateway.BrowserClickTool, "browser_click.v1"
	if request.Action == "type" {
		name, version = toolgateway.BrowserTypeTool, "browser_type.v1"
	}
	if request.Action != "click" && request.Action != "type" {
		return FullCDPPreviewView{}, apperror.New(apperror.CodeInvalidArgument, "browser preview action is invalid")
	}
	payload, err := json.Marshal(toolgateway.BrowserActionPayload{Version: version, Selector: request.Selector, Value: request.Value})
	if err != nil {
		return FullCDPPreviewView{}, err
	}
	if _, err := toolgateway.NormalizeBrowserActionPayload(name, payload); err != nil {
		return FullCDPPreviewView{}, apperror.Wrap(apperror.CodeInvalidArgument, "browser preview action payload is invalid", err)
	}
	binding, err := s.previewBinding(ctx, request.RunID, request.SessionID)
	if err != nil {
		return FullCDPPreviewView{}, err
	}
	s.mu.Lock()
	entry := s.latestByRun[request.RunID]
	allowed := false
	if entry != nil && entry.view.SessionID == request.SessionID && entry.view.State == FullCDPSessionReady &&
		!entry.previewBusy && entry.preview != nil && entry.preview.View.Page.SnapshotID == request.SnapshotID {
		for _, element := range entry.preview.View.Page.Elements {
			if element.Selector == request.Selector && !element.Disabled {
				allowed = true
				break
			}
		}
	}
	if !allowed {
		s.mu.Unlock()
		return FullCDPPreviewView{}, apperror.New(apperror.CodeConflict, "browser preview action requires a fresh observed element")
	}
	entry.previewBusy, entry.preview = true, nil
	s.mu.Unlock()
	defer func() { s.mu.Lock(); entry.previewBusy = false; s.mu.Unlock() }()
	actionCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	stop := s.monitorFullCDPBrowserActionAuthority(actionCtx, cancel, request.RunID, binding)
	defer stop()
	if request.Action == "type" {
		_, err = binding.runtime.BrowserType(actionCtx, request.Selector, request.Value)
	} else {
		_, err = binding.runtime.BrowserClick(actionCtx, request.Selector)
	}
	if err != nil {
		return FullCDPPreviewView{}, normalizeFullCDPBrowserActionContextError(ctx, actionCtx, err)
	}
	current, err := s.previewBinding(actionCtx, request.RunID, request.SessionID)
	if err != nil {
		return FullCDPPreviewView{}, err
	}
	if !sameFullCDPBrowserActionBinding(binding, current) {
		return FullCDPPreviewView{}, apperror.New(apperror.CodeConflict, "browser preview authority changed after interaction")
	}
	return s.captureFullCDPPreview(actionCtx, current, entry)
}

func fullCDPPreviewImage(capture browserruntime.FullCDPScreenshotCapture) (FullCDPPreviewImage, error) {
	metadata := capture.Metadata
	digest := sha256.Sum256(capture.PNG)
	config, err := png.DecodeConfig(bytes.NewReader(capture.PNG))
	if err != nil || len(capture.PNG) == 0 || len(capture.PNG) > browserruntime.MaxScreenshotBytes ||
		metadata.ProtocolVersion != browserruntime.FullCDPScreenshotProtocolVersion || metadata.MediaType != "image/png" ||
		metadata.Bytes != len(capture.PNG) || metadata.SHA256 != hex.EncodeToString(digest[:]) ||
		!metadata.UntrustedEvidence || metadata.CompletedAt.IsZero() ||
		config.Width <= 0 || config.Height <= 0 || config.Width > 16384 || config.Height > 16384 ||
		int64(config.Width)*int64(config.Height) > 32*1024*1024 {
		return FullCDPPreviewImage{}, apperror.New(apperror.CodeUnavailable, "browser preview PNG failed integrity validation")
	}
	return FullCDPPreviewImage{MediaType: "image/png", Bytes: len(capture.PNG),
		SHA256: metadata.SHA256, Width: config.Width, Height: config.Height}, nil
}

// Reading never captures again. A replaced image returns conflict instead of
// substituting a newer frame for the caller's exact session/hash reference.
func (s *FullCDPProductionService) ReadFullCDPPreviewImage(ctx context.Context, runID, sessionID, digest string) (FullCDPPreviewCapture, error) {
	if len(digest) != 64 || strings.ToLower(digest) != digest {
		return FullCDPPreviewCapture{}, apperror.New(apperror.CodeInvalidArgument, "browser preview image hash is invalid")
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return FullCDPPreviewCapture{}, apperror.New(apperror.CodeInvalidArgument, "browser preview image hash is invalid")
	}
	if _, err := s.previewBinding(ctx, runID, sessionID); err != nil {
		return FullCDPPreviewCapture{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.latestByRun[runID]
	if entry == nil || entry.view.SessionID != sessionID || entry.view.State != FullCDPSessionReady ||
		entry.preview == nil || entry.preview.View.Image.SHA256 != digest {
		return FullCDPPreviewCapture{}, apperror.New(apperror.CodeConflict, "browser preview image is no longer current")
	}
	value := *entry.preview
	value.PNG = append([]byte(nil), value.PNG...)
	value.View.Page.Elements = append([]browserruntime.FullCDPPageElement{}, value.View.Page.Elements...)
	return value, nil
}
