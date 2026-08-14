package sandbox

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/idgen"
)

func testDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func testDockerContainerFingerprint() string {
	return testDigest("container")
}

func testDockerInputProjection(t *testing.T, entries ...DockerInputProjectionEntry) DockerInputProjection {
	t.Helper()
	projection, err := NewDockerInputProjection(idgen.New("sandbox-docker-input"),
		idgen.New("sandbox-docker-attempt"), 1, idgen.New("sandbox-docker-plan"),
		idgen.New("sandbox-docker-observation"), idgen.New("sandbox-docker-run"),
		idgen.New("sandbox-docker-mission"), idgen.New("sandbox-docker-workspace"),
		testDigest("input-artifacts"), testDigest("spec"), testDigest("authority"),
		DockerInputArtifactMountTarget, entries, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	return projection
}

func TestDockerInputProjectionRejectsEscapeAndTampering(t *testing.T) {
	valid := []DockerInputProjectionEntry{
		{Ordinal: 1, Path: "data/input.json", SHA256: testDigest("one"),
			SizeBytes: 4, MediaType: "application/json"},
		{Ordinal: 2, Path: "data/notes.txt", SHA256: testDigest("two"),
			SizeBytes: 3, MediaType: "text/plain"},
	}
	projection := testDockerInputProjection(t, valid...)
	for name, mutate := range map[string]func(*DockerInputProjection){
		"absolute":          func(p *DockerInputProjection) { p.Entries[0].Path = "/etc/passwd" },
		"traversal":         func(p *DockerInputProjection) { p.Entries[0].Path = "../secret" },
		"windows separator": func(p *DockerInputProjection) { p.Entries[0].Path = "a\\b" },
		"drive letter":      func(p *DockerInputProjection) { p.Entries[0].Path = "C:evil" },
		"duplicate path":    func(p *DockerInputProjection) { p.Entries[1].Path = p.Entries[0].Path },
		"bad digest":        func(p *DockerInputProjection) { p.Entries[0].SHA256 = strings.Repeat("g", 64) },
		"empty media":       func(p *DockerInputProjection) { p.Entries[0].MediaType = "" },
		"mount writable":    func(p *DockerInputProjection) { p.MountReadOnly = false },
		"wrong mount":       func(p *DockerInputProjection) { p.MountTarget = "/tmp" },
		"generation":        func(p *DockerInputProjection) { p.Generation = 2 },
		"total drift":       func(p *DockerInputProjection) { p.TotalBytes = 1 },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := projection
			candidate.Entries = append([]DockerInputProjectionEntry(nil), projection.Entries...)
			mutate(&candidate)
			candidate.ProjectionFingerprint = dockerInputProjectionFingerprint(candidate)
			if err := candidate.Validate(); err == nil {
				t.Fatal("invalid projection was accepted")
			}
		})
	}
	if err := projection.Validate(); err != nil {
		t.Fatalf("valid projection rejected: %v", err)
	}
}

func TestVerifyDockerContainerMountIsolation(t *testing.T) {
	input := "/run/cyberagent/inputs"
	output := "/run/cyberagent/outputs"
	workspace := "/workspace"
	tests := []struct {
		name    string
		mounts  []DockerContainerMountState
		wantErr bool
	}{
		{name: "isolated", mounts: []DockerContainerMountState{
			{Target: workspace, Access: "ro"},
			{Target: input, Access: "ro"},
			{Target: output, Access: "rw"},
			{Target: "/etc/hosts", Access: "rw"},
		}, wantErr: false},
		{name: "missing output", mounts: []DockerContainerMountState{
			{Target: workspace, Access: "ro"},
		}, wantErr: true},
		{name: "output read only", mounts: []DockerContainerMountState{
			{Target: workspace, Access: "ro"},
			{Target: output, Access: "ro"},
		}, wantErr: true},
		{name: "input writable", mounts: []DockerContainerMountState{
			{Target: workspace, Access: "ro"},
			{Target: input, Access: "rw"},
			{Target: output, Access: "rw"},
		}, wantErr: true},
		{name: "workspace writable", mounts: []DockerContainerMountState{
			{Target: workspace, Access: "rw"},
			{Target: output, Access: "rw"},
		}, wantErr: true},
		{name: "escaped writable inside output tree", mounts: []DockerContainerMountState{
			{Target: workspace, Access: "ro"},
			{Target: output, Access: "rw"},
			{Target: output + "/nested", Access: "rw"},
		}, wantErr: true},
		{name: "duplicate output", mounts: []DockerContainerMountState{
			{Target: workspace, Access: "ro"},
			{Target: output, Access: "rw"},
			{Target: output, Access: "rw"},
		}, wantErr: true},
		{name: "invalid access", mounts: []DockerContainerMountState{
			{Target: workspace, Access: "ro"},
			{Target: output, Access: "shared"},
		}, wantErr: true},
	}
	for _, current := range tests {
		t.Run(current.name, func(t *testing.T) {
			err := VerifyDockerContainerMountIsolation(current.mounts, input, output, workspace)
			if (err != nil) != current.wantErr {
				t.Fatalf("mount isolation error = %v, wantErr %t", err, current.wantErr)
			}
		})
	}
}

func dockerLogFrame(stream byte, payload []byte) []byte {
	frame := make([]byte, 8+len(payload))
	frame[0] = stream
	frame[4] = byte(len(payload) >> 24)
	frame[5] = byte(len(payload) >> 16)
	frame[6] = byte(len(payload) >> 8)
	frame[7] = byte(len(payload))
	copy(frame[8:], payload)
	return frame
}

func testDockerLogCapturePlan(t *testing.T, maxBytes int64, maxLines int) DockerLogCapturePlan {
	t.Helper()
	plan, err := NewDockerLogCapturePlan(idgen.New("sandbox-docker-attempt"), 1,
		idgen.New("sandbox-docker-run"), testDockerContainerFingerprint(), maxBytes, maxLines, 60)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestDecodeDockerLogFramesGoldenVectors(t *testing.T) {
	t.Run("completed", func(t *testing.T) {
		plan := testDockerLogCapturePlan(t, 1024, 64)
		stream := append(dockerLogFrame(1, []byte("hello\n")),
			dockerLogFrame(2, []byte("oops\n"))...)
		records, status, err := DecodeDockerLogFrames(context.Background(), plan, bytes.NewReader(stream))
		if err != nil || status != DockerLogCaptureStatusCompleted {
			t.Fatalf("decode = %v, %q", err, status)
		}
		if records[0].ByteCount != 6 || records[0].LineCount != 1 ||
			records[1].ByteCount != 5 || records[1].LineCount != 1 {
			t.Fatalf("unexpected records: %#v", records)
		}
	})
	t.Run("byte truncation", func(t *testing.T) {
		plan := testDockerLogCapturePlan(t, 8, 64)
		stream := append(dockerLogFrame(1, []byte("0123456789")),
			dockerLogFrame(2, []byte("later"))...)
		records, status, err := DecodeDockerLogFrames(context.Background(), plan, bytes.NewReader(stream))
		if err != nil || status != DockerLogCaptureStatusTruncatedBytes {
			t.Fatalf("decode = %v, %q", err, status)
		}
		if !records[0].TruncatedBytes || records[0].ByteCount != 8 || records[1].ByteCount != 5 {
			t.Fatalf("unexpected truncation: %#v", records)
		}
	})
	t.Run("line truncation clamps count", func(t *testing.T) {
		plan := testDockerLogCapturePlan(t, 1024, 3)
		stream := append(dockerLogFrame(1, []byte("a\nb\nc\nd\ne\n")),
			dockerLogFrame(2, []byte("x\n"))...)
		records, status, err := DecodeDockerLogFrames(context.Background(), plan, bytes.NewReader(stream))
		if err != nil || status != DockerLogCaptureStatusTruncatedLines {
			t.Fatalf("decode = %v, %q", err, status)
		}
		if !records[0].TruncatedLines || records[0].LineCount != 3 {
			t.Fatalf("unexpected line truncation: %#v", records)
		}
	})
	t.Run("invalid stream type", func(t *testing.T) {
		plan := testDockerLogCapturePlan(t, 1024, 64)
		records, status, err := DecodeDockerLogFrames(context.Background(), plan,
			bytes.NewReader(dockerLogFrame(3, []byte("x"))))
		if err != nil || status != DockerLogCaptureStatusInvalidStream {
			t.Fatalf("decode = %v, %q", err, status)
		}
		_ = records
	})
	t.Run("oversized frame", func(t *testing.T) {
		plan := testDockerLogCapturePlan(t, 1024, 64)
		stream := dockerLogFrame(1, make([]byte, MaxDockerLogFrameBytes+1))
		records, status, err := DecodeDockerLogFrames(context.Background(), plan, bytes.NewReader(stream))
		if err != nil || status != DockerLogCaptureStatusInvalidStream {
			t.Fatalf("decode = %v, %q", err, status)
		}
		_ = records
	})
	t.Run("utf8 violations and redaction", func(t *testing.T) {
		plan := testDockerLogCapturePlan(t, 4096, 64)
		stream := dockerLogFrame(1, []byte("token sk-abcdefghijklmnopqrstuvwxyz012345 \xff\xfe plain\n"))
		records, status, err := DecodeDockerLogFrames(context.Background(), plan, bytes.NewReader(stream))
		if err != nil || status != DockerLogCaptureStatusCompleted {
			t.Fatalf("decode = %v, %q", err, status)
		}
		if records[0].UTF8Violations != 2 {
			t.Fatalf("utf8 violations = %d, want 2", records[0].UTF8Violations)
		}
		if records[0].RedactedSegments < 1 {
			t.Fatalf("secret was not redacted: %#v", records[0])
		}
	})
	t.Run("deadline", func(t *testing.T) {
		plan := testDockerLogCapturePlan(t, 4096, 64)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		records, status, err := DecodeDockerLogFrames(ctx, plan, bytes.NewReader(nil))
		if err != nil || status != DockerLogCaptureStatusTruncatedDeadline {
			t.Fatalf("decode = %v, %q", err, status)
		}
		if !records[0].TruncatedDeadline || !records[1].TruncatedDeadline {
			t.Fatalf("deadline flags missing: %#v", records)
		}
	})
}

func TestDockerLogCaptureReceiptRejectsTampering(t *testing.T) {
	plan := testDockerLogCapturePlan(t, 1024, 64)
	records := []DockerLogStreamRecord{
		{Stream: "stdout", ByteCount: 2, LineCount: 1, ContentDigest: testDigest("a")},
		{Stream: "stderr", ContentDigest: testDigest("")},
	}
	receipt, err := NewDockerLogCaptureReceipt(idgen.New("sandbox-docker-log"),
		plan.AttemptID, 1, plan.RunID, plan.ContainerIDFingerprint, plan, records,
		DockerLogCaptureStatusCompleted, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*DockerLogCaptureReceipt){
		"status mismatch":     func(r *DockerLogCaptureReceipt) { r.Status = DockerLogCaptureStatusTruncatedBytes },
		"stream order":        func(r *DockerLogCaptureReceipt) { r.Streams[0], r.Streams[1] = r.Streams[1], r.Streams[0] },
		"flag without cap":    func(r *DockerLogCaptureReceipt) { r.Streams[0].TruncatedBytes = true },
		"bytes over plan cap": func(r *DockerLogCaptureReceipt) { r.Streams[0].ByteCount = r.CaptureMaxBytes + 1 },
		"stream count drift":  func(r *DockerLogCaptureReceipt) { r.StreamCount = 3 },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := receipt
			candidate.Streams = append([]DockerLogStreamRecord(nil), receipt.Streams...)
			mutate(&candidate)
			candidate.ReceiptFingerprint = dockerLogCaptureReceiptFingerprint(candidate)
			if err := candidate.Validate(); err == nil {
				t.Fatal("tampered receipt was accepted")
			}
		})
	}
}

func buildDockerOutputTar(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sortStrings(names)
	for _, name := range names {
		if err := writer.WriteHeader(&tar.Header{Name: name, Mode: 0o644,
			Size: int64(len(files[name])), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte(files[name])); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

func testDockerOutputExportPlan(t *testing.T) DockerOutputExportPlan {
	t.Helper()
	plan, err := NewDockerOutputExportPlan(idgen.New("sandbox-docker-attempt"), 1,
		idgen.New("sandbox-docker-run"), testDockerContainerFingerprint(),
		"/run/cyberagent/outputs", MaxDockerOutputFiles, MaxDockerOutputFileBytes,
		MaxDockerOutputTotalBytes)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestStageDockerOutputArchiveBoundary(t *testing.T) {
	t.Run("completed with directories and redaction", func(t *testing.T) {
		plan := testDockerOutputExportPlan(t)
		archive := buildDockerOutputTar(t, map[string]string{
			"report/summary.json": "{\"ok\": true}\n",
			"token.txt":           "token sk-abcdefghijklmnopqrstuvwxyz012345\n",
		})
		staging := t.TempDir()
		receipt, err := StageDockerOutputArchive(context.Background(), plan,
			bytes.NewReader(archive), staging, idgen.New("sandbox-docker-staging"),
			time.Now().UTC())
		if err != nil || receipt.Status != DockerOutputStagingStatusCompleted {
			t.Fatalf("stage = %v, %q", err, receipt.Status)
		}
		if receipt.FileCount != 2 || receipt.RedactedCount != 1 || receipt.TotalBytes == 0 {
			t.Fatalf("unexpected receipt: %#v", receipt)
		}
		staged, err := os.ReadFile(filepath.Join(staging, "report", "summary.json"))
		if err != nil || !strings.Contains(string(staged), "ok") {
			t.Fatalf("staged file missing: %v", err)
		}
		redactedFile, err := os.ReadFile(filepath.Join(staging, "token.txt"))
		if err != nil || strings.Contains(string(redactedFile), "sk-abcdefghijklmnopqrstuvwxyz012345") {
			t.Fatal("secret survived staging redaction")
		}
	})
	t.Run("path traversal rejected", func(t *testing.T) {
		plan := testDockerOutputExportPlan(t)
		var buffer bytes.Buffer
		writer := tar.NewWriter(&buffer)
		_ = writer.WriteHeader(&tar.Header{Name: "../escape", Mode: 0o644,
			Size: 3, Typeflag: tar.TypeReg})
		_, _ = writer.Write([]byte("x"))
		_ = writer.Close()
		receipt, err := StageDockerOutputArchive(context.Background(), plan,
			bytes.NewReader(buffer.Bytes()), t.TempDir(), idgen.New("sandbox-docker-staging"),
			time.Now().UTC())
		if err != nil || receipt.Status != DockerOutputStagingStatusPath {
			t.Fatalf("stage = %v, %q", err, receipt.Status)
		}
	})
	t.Run("symlink rejected", func(t *testing.T) {
		plan := testDockerOutputExportPlan(t)
		var buffer bytes.Buffer
		writer := tar.NewWriter(&buffer)
		_ = writer.WriteHeader(&tar.Header{Name: "link", Mode: 0o777,
			Linkname: "/etc/passwd", Typeflag: tar.TypeSymlink})
		_ = writer.Close()
		receipt, err := StageDockerOutputArchive(context.Background(), plan,
			bytes.NewReader(buffer.Bytes()), t.TempDir(), idgen.New("sandbox-docker-staging"),
			time.Now().UTC())
		if err != nil || receipt.Status != DockerOutputStagingStatusLink {
			t.Fatalf("stage = %v, %q", err, receipt.Status)
		}
	})
	t.Run("duplicate rejected", func(t *testing.T) {
		plan := testDockerOutputExportPlan(t)
		var buffer bytes.Buffer
		writer := tar.NewWriter(&buffer)
		_ = writer.WriteHeader(&tar.Header{Name: "dup.txt", Mode: 0o644, Size: 1, Typeflag: tar.TypeReg})
		_, _ = writer.Write([]byte("a"))
		_ = writer.WriteHeader(&tar.Header{Name: "dup.txt", Mode: 0o644, Size: 1, Typeflag: tar.TypeReg})
		_, _ = writer.Write([]byte("b"))
		_ = writer.Close()
		receipt, err := StageDockerOutputArchive(context.Background(), plan,
			bytes.NewReader(buffer.Bytes()), t.TempDir(), idgen.New("sandbox-docker-staging"),
			time.Now().UTC())
		if err != nil || receipt.Status != DockerOutputStagingStatusDuplicate {
			t.Fatalf("stage = %v, %q", err, receipt.Status)
		}
	})
	t.Run("total size truncation", func(t *testing.T) {
		plan, err := NewDockerOutputExportPlan(idgen.New("sandbox-docker-attempt"), 1,
			idgen.New("sandbox-docker-run"), testDockerContainerFingerprint(),
			"/run/cyberagent/outputs", 8, 64, 128)
		if err != nil {
			t.Fatal(err)
		}
		archive := buildDockerOutputTar(t, map[string]string{"a.txt": strings.Repeat("a", 100)})
		receipt, err := StageDockerOutputArchive(context.Background(), plan,
			bytes.NewReader(archive), t.TempDir(), idgen.New("sandbox-docker-staging"),
			time.Now().UTC())
		if err != nil || receipt.Status != DockerOutputStagingStatusTruncated {
			t.Fatalf("stage = %v, %q", err, receipt.Status)
		}
	})
}

func TestDockerOutputCommitBindsAndVerifiesStaging(t *testing.T) {
	plan := testDockerOutputExportPlan(t)
	archive := buildDockerOutputTar(t, map[string]string{"report/result.json": "{\"ok\": true}\n"})
	stagingRoot := t.TempDir()
	staging, err := StageDockerOutputArchive(context.Background(), plan,
		bytes.NewReader(archive), stagingRoot, idgen.New("sandbox-docker-staging"),
		time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	entry := DockerOutputCommitEntry{
		Path: staging.Entries[0].Path, SHA256: staging.Entries[0].SHA256,
		SizeBytes: staging.Entries[0].SizeBytes, MediaType: staging.Entries[0].MediaType,
	}
	request, err := NewDockerOutputCommitRequest(staging.AttemptID, 1, staging.RunID,
		idgen.New("sandbox-docker-workspace"), staging.ID, testDigest("operation"),
		[]DockerOutputCommitEntry{entry})
	if err != nil {
		t.Fatal(err)
	}
	if err := request.Binds(staging); err != nil {
		t.Fatalf("accepted manifest should bind: %v", err)
	}
	verified, err := VerifyDockerOutputCommit(stagingRoot, request)
	if err != nil || len(verified) != 1 || verified[0].SHA256 != entry.SHA256 {
		t.Fatalf("commit verification = %#v, %v", verified, err)
	}
	for name, mutate := range map[string]func(*DockerOutputCommitRequest){
		"un-staged entry": func(r *DockerOutputCommitRequest) {
			r.AcceptedEntries[0].Path = "missing.txt"
			r.RequestFingerprint = DockerOutputCommitRequestFingerprint(*r)
		},
		"digest drift": func(r *DockerOutputCommitRequest) {
			r.AcceptedEntries[0].SHA256 = testDigest("drift")
			r.RequestFingerprint = DockerOutputCommitRequestFingerprint(*r)
		},
		"empty manifest": func(r *DockerOutputCommitRequest) {
			r.AcceptedEntries = nil
			r.RequestFingerprint = DockerOutputCommitRequestFingerprint(*r)
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := request
			candidate.AcceptedEntries = append([]DockerOutputCommitEntry(nil), request.AcceptedEntries...)
			mutate(&candidate)
			if err := candidate.Binds(staging); err == nil {
				t.Fatal("invalid manifest bound to staging")
			}
		})
	}
	stagedFile := filepath.Join(stagingRoot, filepath.FromSlash(entry.Path))
	if err := os.Remove(stagedFile); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyDockerOutputCommit(stagingRoot, request); err == nil {
		t.Fatal("missing staged file verified")
	}
}

func TestDockerContainerIOPathBoundaryMatrix(t *testing.T) {
	for name, value := range map[string]string{
		"absolute":          "/etc/passwd",
		"parent traversal":  "../secret",
		"nested traversal":  "a/../../secret",
		"windows separator": "a\\b",
		"drive letter":      "C:secret",
		"empty component":   "a//b",
		"dot component":     "a/./b",
		"nul byte":          "a\x00b",
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateContainerRelativePath("matrix", value); err == nil {
				t.Fatalf("path %q was accepted", value)
			}
		})
	}
	for name, value := range map[string]string{
		"plain":  "report/summary.json",
		"nested": "a/b/c.txt",
	} {
		t.Run("valid "+name, func(t *testing.T) {
			if err := validateContainerRelativePath("matrix", value); err != nil {
				t.Fatalf("path %q rejected: %v", value, err)
			}
		})
	}
}

func TestDockerContainerIOErrors(t *testing.T) {
	if code := DockerContainerIOErrorCode(errors.New("other")); code != "" {
		t.Fatalf("unexpected code %q", code)
	}
	if code := DockerContainerIOErrorCode(newDockerContainerIOError(DockerContainerIOFailureConnection)); code != DockerContainerIOFailureConnection {
		t.Fatalf("code = %q", code)
	}
	unavailable := NewUnavailableDockerContainerIOTransport("missing", "unavailable")
	if _, err := unavailable.AttachLogs(context.Background(), testDockerLogCapturePlan(t, 1024, 64)); err == nil || DockerContainerIOErrorCode(err) != DockerContainerIOFailureUnavailable {
		t.Fatalf("unavailable attach = %v", err)
	}
	if _, err := unavailable.ExportOutputs(context.Background(), testDockerOutputExportPlan(t)); err == nil {
		t.Fatal("unavailable export succeeded")
	}
}

var _ = io.EOF
