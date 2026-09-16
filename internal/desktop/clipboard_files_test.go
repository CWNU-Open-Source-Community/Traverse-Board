package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/store"
)

type clipboardReaderFixture struct {
	files []ClipboardFile
	err   error
	calls int
}

func (r *clipboardReaderFixture) ReadFiles(context.Context) ([]ClipboardFile, error) {
	r.calls++
	return r.files, r.err
}

func newClipboardBridgeFixture(t *testing.T) (*DesktopBridge, *clipboardReaderFixture) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "clipboard.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	workspace := t.TempDir()
	if err := st.SaveWorkspace(t.Context(), store.WorkspaceRecord{ID: "workspace-clipboard", Name: "Clipboard", RootPath: workspace, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	b := newTestDesktopBridge(t, t.Context(), &testSkillPackagePicker{})
	b.workspaceResolver = testWorkspaceResolver{target: WorkspaceOpenTarget{ID: "workspace-clipboard", RootPath: workspace}}
	reader := &clipboardReaderFixture{}
	b.clipboardReader, b.clipboardFiles, b.clipboardImages = reader, application.NewFileAttachmentService(st), st
	return b, reader
}
func clipboardRequest() ClipboardFilesRequest {
	return ClipboardFilesRequest{Version: ClipboardFilesProtocolVersion, WorkspaceID: "workspace-clipboard", OperationKey: "native-paste-event-0001"}
}

func TestClipboardFilesImportUsesRealImageAndFileReceiptsWithoutExposingPaths(t *testing.T) {
	b, reader := newClipboardBridgeFixture(t)
	var pixel bytes.Buffer
	if err := png.Encode(&pixel, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	reader.files = []ClipboardFile{{Name: "requirements.md", Data: []byte("original requirement\n")}, {Name: "pixel.png", Data: pixel.Bytes()}, {Name: "empty.txt", Data: []byte{}}, {Name: "directory", Error: apperror.New(apperror.CodeInvalidArgument, "Directories cannot be pasted")}}
	first, err := b.PasteClipboardFiles(clipboardRequest())
	if err != nil || first.Status != "processed" || len(first.Attachments) != 2 || len(first.Images) != 1 || len(first.Rejected) != 1 {
		t.Fatalf("paste=%+v err=%v", first, err)
	}
	if first.Images[0].Width != 2 || first.Images[0].MIMEType != "image/png" || first.Attachments[0].Name != "requirements.md" || first.Attachments[1].ByteSize != 0 {
		t.Fatal("mixed attachment routes lost original contents")
	}
	again, err := b.PasteClipboardFiles(clipboardRequest())
	if err != nil || again.Images[0].ID != first.Images[0].ID || again.Attachments[0].ID != first.Attachments[0].ID || again.Attachments[1].ID != first.Attachments[1].ID {
		t.Fatal("same event changed saved identities")
	}
	reader.files[0].Data = []byte("clipboard changed")
	changed, err := b.PasteClipboardFiles(clipboardRequest())
	if err != nil || len(changed.Rejected) != 2 || changed.Rejected[0].Code != string(apperror.CodeConflict) {
		t.Fatalf("changed clipboard reused original item key: %+v %v", changed, err)
	}
	encoded, _ := json.Marshal(first)
	if strings.Contains(string(encoded), "RootPath") || strings.Contains(string(encoded), `C:\`) || strings.Contains(string(encoded), `D:\`) {
		t.Fatal("native path leaked to renderer")
	}
}

func TestClipboardFilesRequireSupportedAuthorizedLiveWorkspaceBeforeReading(t *testing.T) {
	for _, name := range []string{"unsupported", "disabled", "wrong workspace", "cancelled", "busy", "invalid request"} {
		t.Run(name, func(t *testing.T) {
			b, reader := newClipboardBridgeFixture(t)
			request := clipboardRequest()
			switch name {
			case "unsupported":
				b.clipboardReader = nil
			case "disabled":
				b.bootstrap.SessionMessageEnabled = false
			case "wrong workspace":
				b.workspaceResolver = testWorkspaceResolver{target: WorkspaceOpenTarget{ID: "other"}}
			case "cancelled":
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				b.contextProvider = func() context.Context { return ctx }
			case "busy":
				b.clipboardActive.Store(true)
			case "invalid request":
				request.OperationKey = "short"
			}
			result, err := b.PasteClipboardFiles(request)
			if name == "unsupported" {
				if err != nil || result.Status != "unsupported" {
					t.Fatal("unsupported platform hidden")
				}
			} else if err == nil {
				t.Fatal("invalid request permitted")
			}
			if reader.calls != 0 {
				t.Fatal("clipboard accessed before guards")
			}
		})
	}
}

func TestClipboardFilesDistinguishEmptyFromReadFailureAndRejectOversizedBatch(t *testing.T) {
	b, reader := newClipboardBridgeFixture(t)
	empty, err := b.PasteClipboardFiles(clipboardRequest())
	if err != nil || empty.Status != "empty" || empty.Images == nil || empty.Attachments == nil || empty.Rejected == nil {
		t.Fatalf("empty result=%+v %v", empty, err)
	}
	reader.err = apperror.New(apperror.CodeUnavailable, "Clipboard is busy")
	if _, err := b.PasteClipboardFiles(clipboardRequest()); apperror.CodeOf(err) != apperror.CodeUnavailable {
		t.Fatal("read failure misreported empty")
	}
	reader.err = nil
	reader.files = make([]ClipboardFile, 5)
	if _, err := b.PasteClipboardFiles(clipboardRequest()); apperror.CodeOf(err) != apperror.CodeResourceExhausted {
		t.Fatal("oversized batch accepted")
	}
}

func TestClipboardFilesRejectedNamesRemainReadableWithoutChangingImportIdentity(t *testing.T) {
	b, reader := newClipboardBridgeFixture(t)
	reader.files = []ClipboardFile{
		{Name: strings.Repeat("文", 200) + ".txt", Data: []byte("must not import using a shortened name")},
		{Name: "bad\nname.txt", Data: []byte("rejected")},
	}
	result, err := b.PasteClipboardFiles(clipboardRequest())
	if err != nil || !result.BatchComplete || result.Status != "processed" || len(result.Rejected) != 2 || len(result.Attachments) != 0 || len(result.Images) != 0 {
		t.Fatalf("invalid names did not produce a bounded rejection: %+v %v", result, err)
	}
	if len([]rune(result.Rejected[0].Name)) != 160 || !strings.HasSuffix(result.Rejected[0].Name, "…") || result.Rejected[1].Name != "File 2" {
		t.Fatalf("rejected display labels cannot be parsed safely: %+v", result.Rejected)
	}
	observed, err := b.InspectClipboardFiles(clipboardRequest())
	if err != nil || observed.Status != "unknown" || len(observed.Attachments) != 0 {
		t.Fatal("invalid original name was silently rewritten and imported")
	}
}

func TestClipboardObservationRecoversSavedItemsWithoutReadingChangedClipboardOrClaimingWholeBatch(t *testing.T) {
	b, reader := newClipboardBridgeFixture(t)
	reader.files = []ClipboardFile{{Name: "saved.txt", Data: []byte("original file")}}
	saved, err := b.PasteClipboardFiles(clipboardRequest())
	if err != nil || !saved.BatchComplete {
		t.Fatalf("paste=%+v %v", saved, err)
	}
	reader.files = []ClipboardFile{{Name: "different.txt", Data: []byte("do not read this clipboard")}}
	reader.err = apperror.New(apperror.CodeUnavailable, "must not touch current clipboard")
	before := reader.calls
	observed, err := b.InspectClipboardFiles(clipboardRequest())
	if err != nil || observed.Status != "partial" || observed.BatchComplete || len(observed.Attachments) != 1 || observed.Attachments[0].ID != saved.Attachments[0].ID || reader.calls != before {
		t.Fatalf("observation=%+v err=%v calls=%d", observed, err, reader.calls)
	}
	other := clipboardRequest()
	other.OperationKey = "native-paste-event-unknown"
	unknown, err := b.InspectClipboardFiles(other)
	if err != nil || unknown.Status != "unknown" || unknown.BatchComplete || len(unknown.Attachments) != 0 || reader.calls != before {
		t.Fatal("missing receipts were interpreted as a completed/retryable batch")
	}
	// Recovery is a read operation and remains possible after upload control is disabled.
	b.bootstrap.RunCreationEnabled = false
	b.bootstrap.SessionMessageEnabled = false
	b.bootstrap.ControlToken = ""
	if current, err := b.InspectClipboardFiles(clipboardRequest()); err != nil || current.Status != "partial" {
		t.Fatalf("read observation acquired write requirements: %v", err)
	}
}
