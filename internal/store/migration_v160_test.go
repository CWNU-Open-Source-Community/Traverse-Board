package store

import (
	"bytes"
	"image"
	"image/png"
	"path/filepath"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
)

func TestSchemaV160PreservesOldImageRowsBindingsAndForeignKeys(t *testing.T) {
	st := openUnmigratedSQLiteStore(t, filepath.Join(t.TempDir(), "migration160.db"))
	defer st.Close()
	if err := applyMigrationPrefixForTest(t.Context(), st, migrationPlan(), 159); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveWorkspace(t.Context(), WorkspaceRecord{ID: "ws-old-image", Name: "old image", RootPath: t.TempDir(), CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	_, run, err := application.NewRunService(st).Create(t.Context(), application.CreateRunRequest{Goal: "Preserve old image input", Profile: "review", WorkspaceID: "ws-old-image"})
	if err != nil {
		t.Fatal(err)
	}
	var pngBytes bytes.Buffer
	if err := png.Encode(&pngBytes, image.NewNRGBA(image.Rect(0, 0, 2, 3))); err != nil {
		t.Fatal(err)
	}
	old, err := st.SaveWorkspaceImage(t.Context(), "ws-old-image", "old-image-migration-key", "image/png", "old.png", pngBytes.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`INSERT INTO operator_steering_messages(id,run_id,session_id,sequence,status,content,content_sha256,requested_by,created_at,image_count) VALUES(?,?,?,1,'pending','',?,'operator',?,1)`, "steer-image-v159", run.ID, run.SessionID, domain.OperatorSteeringContentSHA256(""), ts(time.Now().UTC())); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`INSERT INTO thread_message_images(message_id,ordinal,image_id) VALUES('steer-image-v159',0,?)`, old.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.applyMigration(t.Context(), migrationPlan()[159]); err != nil {
		t.Fatal(err)
	}
	// Assert the v160 row directly: the current reader requires columns added
	// by later migrations and must not change this historical test's schema.
	var message domain.OperatorSteeringMessage
	err = st.db.QueryRowContext(t.Context(), `SELECT id,content,image_count,attachment_count
		FROM operator_steering_messages WHERE id=?`, "steer-image-v159").
		Scan(&message.ID, &message.Content, &message.ImageCount, &message.AttachmentCount)
	if err != nil || message.Content != "" || message.ImageCount != 1 || message.AttachmentCount != 0 {
		t.Fatalf("old image row changed %#v %v", message, err)
	}
	stored, raw, err := st.GetWorkspaceImage(t.Context(), old.WorkspaceID, old.ID)
	if err != nil || stored != old || !bytes.Equal(raw, pngBytes.Bytes()) {
		t.Fatal("old image bytes changed", err)
	}
	images, err := st.ListOperatorMessageImages(t.Context(), run.ID, message.ID)
	if err != nil || len(images) != 1 || images[0] != old {
		t.Fatal("old image binding lost", err)
	}
	for _, sql := range []string{`UPDATE operator_steering_messages SET attachment_count=1 WHERE id='steer-image-v159'`, `UPDATE operator_steering_messages SET image_count=0 WHERE id='steer-image-v159'`, `DELETE FROM thread_message_images`} {
		if _, err := st.db.Exec(sql); err == nil {
			t.Fatal("old immutable guard lost", sql)
		}
	}
	rows, err := st.db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("old foreign key corrupted")
	}
}
