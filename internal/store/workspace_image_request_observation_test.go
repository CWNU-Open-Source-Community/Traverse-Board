package store

import (
	"bytes"
	"database/sql"
	"image"
	"image/png"
	"path/filepath"
	"testing"
	"time"
)

func TestWorkspaceImageOperationObservationAfterReopenIsReadOnlyAndScopeBound(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clipboard-image.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveWorkspace(t.Context(), WorkspaceRecord{ID: "clipboard-workspace", Name: "Clipboard", RootPath: t.TempDir(), CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	const key = "native-original-item-key"
	saved, err := st.SaveWorkspaceImage(t.Context(), "clipboard-workspace", key, "image/png", "pixel.png", data.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.db.SetMaxOpenConns(1)
	if _, err = st.db.ExecContext(t.Context(), `PRAGMA query_only=ON`); err != nil {
		t.Fatal(err)
	}
	// A competing writer reservation must not block this original-key SELECT.
	writer, err := sql.Open("sqlite3", path+"?_txlock=immediate&_busy_timeout=100")
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	tx, err := writer.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	actual, found, err := st.GetWorkspaceImageByOperationKey(t.Context(), "clipboard-workspace", key)
	if err != nil || !found || actual != saved {
		t.Fatalf("read-only replay mismatch: %+v %t %v", actual, found, err)
	}
	if _, found, err = st.GetWorkspaceImageByOperationKey(t.Context(), "other-workspace", key); err != nil || found {
		t.Fatal("other workspace reused image")
	}
	if _, found, err = st.GetWorkspaceImageByOperationKey(t.Context(), "clipboard-workspace", "missing-original-item-key"); err != nil || found {
		t.Fatal("unknown request was reserved or invented")
	}
}
