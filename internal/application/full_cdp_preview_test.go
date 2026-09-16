package application

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/browserruntime"
	"cyberagent-workbench/internal/domain"
)

func newFullCDPPreviewFixture(t *testing.T) (*FullCDPProductionService, *fakeFullCDPProductionStore, *fakeFullCDPBrowserActionRuntime, string) {
	t.Helper()
	service, store, _, latest := newFullCDPProductionServiceFixture(t)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := service.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	opened, err := service.OpenFullCDPSession(t.Context(), fullCDPOpenFixture(service, store, "open-preview-test"))
	if err != nil {
		t.Fatal(err)
	}
	bitmap := image.NewRGBA(image.Rect(0, 0, 2, 3))
	bitmap.Set(0, 0, color.RGBA{R: 255, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, bitmap); err != nil {
		t.Fatal(err)
	}
	runtime := &fakeFullCDPBrowserActionRuntime{fakeManagedFullCDPRuntime: *latest, png: encoded.Bytes()}
	service.mu.Lock()
	service.latestByRun[store.run.ID].runtime = runtime
	service.mu.Unlock()
	return service, store, runtime, opened.Session.SessionID
}

type previewInteractionRuntime struct {
	*fakeFullCDPBrowserActionRuntime
	clicks int
	types  int
}

func (r *previewInteractionRuntime) BrowserSnapshot(ctx context.Context) (browserruntime.FullCDPPageSnapshot, error) {
	value, err := r.fakeFullCDPBrowserActionRuntime.BrowserSnapshot(ctx)
	value.Elements = []browserruntime.FullCDPPageElement{{Selector: "#press", Tag: "button"}, {Selector: "#input", Tag: "input", Type: "text"}, {Selector: "#disabled", Tag: "button", Disabled: true}}
	return value, err
}
func (r *previewInteractionRuntime) BrowserClick(ctx context.Context, selector string) (browserruntime.FullCDPInteractionResult, error) {
	r.clicks++
	return r.fakeFullCDPBrowserActionRuntime.BrowserClick(ctx, selector)
}
func (r *previewInteractionRuntime) BrowserType(ctx context.Context, selector, value string) (browserruntime.FullCDPInteractionResult, error) {
	r.types++
	return r.fakeFullCDPBrowserActionRuntime.BrowserType(ctx, selector, value)
}

func TestFullCDPPreviewActionConsumesObservedSnapshotOnce(t *testing.T) {
	service, store, base, sessionID := newFullCDPPreviewFixture(t)
	runtime := &previewInteractionRuntime{fakeFullCDPBrowserActionRuntime: base}
	service.mu.Lock()
	service.latestByRun[store.run.ID].runtime = runtime
	service.mu.Unlock()
	view, err := service.CaptureFullCDPPreview(t.Context(), store.run.ID, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	request := FullCDPPreviewActionRequest{RunID: store.run.ID, SessionID: sessionID, SnapshotID: view.Page.SnapshotID, Action: "click", Selector: "#press"}
	for _, selector := range []string{"#not-observed", "#disabled"} {
		invalid := request
		invalid.Selector = selector
		if _, err := service.ActFullCDPPreview(t.Context(), invalid); apperror.CodeOf(err) != apperror.CodeConflict {
			t.Fatalf("unobserved selector allowed: %v", err)
		}
	}
	after, err := service.ActFullCDPPreview(t.Context(), request)
	if err != nil || runtime.clicks != 1 || after.Page.SnapshotID == view.Page.SnapshotID {
		t.Fatalf("clicks=%d view=%+v err=%v", runtime.clicks, after, err)
	}
	if _, err := service.ActFullCDPPreview(t.Context(), request); apperror.CodeOf(err) != apperror.CodeConflict || runtime.clicks != 1 {
		t.Fatalf("old snapshot replayed action: %v", err)
	}
	request.Action, request.Selector, request.Value, request.SnapshotID = "type", "#input", "preview example", after.Page.SnapshotID
	if _, err := service.ActFullCDPPreview(t.Context(), request); err != nil || runtime.types != 1 {
		t.Fatalf("type failed: %v", err)
	}
}

func TestFullCDPPreviewReadKeepsExactImageAndRevalidatesSessionPermission(t *testing.T) {
	service, store, runtime, sessionID := newFullCDPPreviewFixture(t)
	view, err := service.CaptureFullCDPPreview(t.Context(), store.run.ID, sessionID)
	if err != nil || view.Image.Width != 2 || view.Image.Height != 3 || view.Page.Text != "bounded page" || !view.Page.UntrustedEvidence {
		t.Fatalf("view=%+v err=%v", view, err)
	}
	read, err := service.ReadFullCDPPreviewImage(t.Context(), store.run.ID, sessionID, view.Image.SHA256)
	if err != nil || !bytes.Equal(read.PNG, runtime.png) {
		t.Fatalf("read=%+v err=%v", read.View, err)
	}
	read.PNG[0] = 0 // No caller can mutate the cached observation.
	read, err = service.ReadFullCDPPreviewImage(t.Context(), store.run.ID, sessionID, view.Image.SHA256)
	if err != nil || !bytes.Equal(read.PNG, runtime.png) {
		t.Fatal("cached PNG was modified by a reader")
	}
	if runtime.screenshots != 1 {
		t.Fatalf("GET captured again: %d", runtime.screenshots)
	}
	for _, values := range [][2]string{{"wrong-session", view.Image.SHA256}, {sessionID, strings.Repeat("0", 64)}} {
		if _, err := service.ReadFullCDPPreviewImage(t.Context(), store.run.ID, values[0], values[1]); apperror.CodeOf(err) != apperror.CodeConflict {
			t.Fatalf("wrong identity allowed: %v", err)
		}
	}
	store.mu.Lock()
	store.browserPermission.Mode = domain.RunBrowserCDPPermissionRestricted
	store.mu.Unlock()
	if _, err := service.ReadFullCDPPreviewImage(t.Context(), store.run.ID, sessionID, view.Image.SHA256); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("revoked PNG allowed: %v", err)
	}
	if _, err := service.CaptureFullCDPPreview(t.Context(), store.run.ID, sessionID); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("revoked capture allowed: %v", err)
	}
}

func TestFullCDPPreviewRejectsInvalidPNGAndClosedSession(t *testing.T) {
	service, store, runtime, sessionID := newFullCDPPreviewFixture(t)
	runtime.png = []byte("not a png")
	if _, err := service.CaptureFullCDPPreview(t.Context(), store.run.ID, sessionID); apperror.CodeOf(err) != apperror.CodeUnavailable {
		t.Fatalf("invalid PNG published: %v", err)
	}
	service.mu.Lock()
	cached := service.latestByRun[store.run.ID].preview
	service.mu.Unlock()
	if cached != nil {
		t.Fatal("invalid PNG retained")
	}
	_, err := service.CloseFullCDPSession(t.Context(), CloseFullCDPSessionRequest{RunID: store.run.ID, ExpectedSessionID: sessionID, OperationKey: "close-preview-test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CaptureFullCDPPreview(t.Context(), store.run.ID, sessionID); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("closed capture allowed: %v", err)
	}
}

func TestFullCDPPreviewRevocationInterruptsCaptureBeforePublication(t *testing.T) {
	service, store, runtime, sessionID := newFullCDPPreviewFixture(t)
	runtime.blockScreenshot = true
	runtime.screenshotEntered = make(chan struct{})
	result := make(chan error, 1)
	go func() { _, err := service.CaptureFullCDPPreview(t.Context(), store.run.ID, sessionID); result <- err }()
	select {
	case <-runtime.screenshotEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("capture did not start")
	}
	store.mu.Lock()
	store.browserPermission.Mode = domain.RunBrowserCDPPermissionRestricted
	store.mu.Unlock()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("revoked in-flight preview published")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("revoked capture was not interrupted")
	}
	service.mu.Lock()
	cached := service.latestByRun[store.run.ID].preview
	service.mu.Unlock()
	if cached != nil {
		t.Fatal("revoked screenshot retained")
	}
}
