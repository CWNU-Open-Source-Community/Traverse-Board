package store

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"strings"
	"testing"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
)

func TestThreadRequestObservationImageOrderScopeAndHashRemainExact(t *testing.T) {
	st, reader, run, request, _ := threadIntentFixture(t)
	request.Content, request.Files, request.OperationKey = "", nil, "image-observation-order-key"
	var pixels bytes.Buffer
	if err := png.Encode(&pixels, image.NewNRGBA(image.Rect(0, 0, 2, 3))); err != nil {
		t.Fatal(err)
	}
	for index := range 2 {
		uploaded, err := st.SaveWorkspaceImage(t.Context(), "ws-intent", fmt.Sprintf("observation-image-%d", index), "image/png", fmt.Sprintf("image-%d.png", index), pixels.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		request.Images = append(request.Images, domain.ImageReference{ID: uploaded.ID, SHA256: uploaded.SHA256})
	}
	if _, err := st.ReserveThreadMessageIntent(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	queued, err := st.CommitThreadMessage(t.Context(), request, run.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	requestObservationQueryOnly(t, reader, true)
	for range 2 {
		observed, err := reader.InspectThreadTurnRequest(t.Context(), request.ThreadID, request.OperationKey, request.RequestedBy)
		if err != nil || observed.State != "received" || observed.Settled || observed.MessageID != queued.Message.ID {
			t.Fatalf("read-only pending image observation failed: %#v %v", observed, err)
		}
	}
	badHash := append([]domain.ImageReference(nil), request.Images...)
	badHash[0].SHA256 = strings.Repeat("f", 64)
	for _, test := range []struct {
		name, workspace string
		refs            []domain.ImageReference
	}{
		{"reordered", "ws-intent", []domain.ImageReference{request.Images[1], request.Images[0]}},
		{"wrong-hash", "ws-intent", badHash},
		{"missing-reference", "ws-intent", request.Images[:1]},
		{"foreign-workspace", "ws-elsewhere", request.Images},
	} {
		t.Run(test.name, func(t *testing.T) {
			conn, finish, err := reader.beginThreadRequestObservation(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer finish()
			if err := validateObservedThreadImages(t.Context(), conn, test.workspace, queued.Message, test.refs); apperror.CodeOf(err) != apperror.CodeConflict {
				t.Fatalf("invalid original image binding accepted: %v", err)
			}
		})
	}
}
