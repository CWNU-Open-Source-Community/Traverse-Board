package sandbox

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestDockerLogOutputBindsRedactedTextAcrossFrames(t *testing.T) {
	plan := testDockerLogCapturePlan(t, 4096, 64)
	// Both a multibyte character and a synthetic token cross Docker frames.
	stream := dockerLogFrame(1, []byte("test \xe4\xb8"))
	stream = append(stream, dockerLogFrame(1, []byte("\xad\n"))...)
	stream = append(stream, dockerLogFrame(2, []byte("failure token sk-abcdefghijklm"))...)
	stream = append(stream, dockerLogFrame(2, []byte("nopqrstuvwxyz012345\n"))...)
	records, status, output, err := DecodeDockerLogFramesWithOutput(
		context.Background(), plan, bytes.NewReader(stream))
	if err != nil || status != DockerLogCaptureStatusCompleted {
		t.Fatalf("capture failed: %v, %s", err, status)
	}
	if output.Stdout != "test 中\n" || !strings.HasPrefix(output.Stderr, "failure token ") ||
		strings.Contains(output.Stderr, "sk-abcdefgh") || !strings.Contains(output.Stderr, "[REDACTED:") {
		t.Fatalf("unexpected sanitized output: %#v", output)
	}
	for index, text := range []string{output.Stdout, output.Stderr} {
		sum := sha256.Sum256([]byte(text))
		if records[index].ContentDigest != hex.EncodeToString(sum[:]) {
			t.Fatalf("stream %d text does not bind its receipt digest", index)
		}
	}
	if records[0].UTF8Violations != 0 || records[1].RedactedSegments < 1 {
		t.Fatalf("capture metadata lost: %#v", records)
	}
}

func TestDockerLogOutputKeepsCaptureLimitsAndStatus(t *testing.T) {
	for _, tc := range []struct {
		name, content, want, status string
		bytes                       int64
		lines                       int
	}{
		{"byte cap", "0123456789", "01234567", DockerLogCaptureStatusTruncatedBytes, 8, 64},
		{"line cap", "a\nb\nc\nd\ne\n", "a\nb\nc", DockerLogCaptureStatusTruncatedLines, 1024, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := testDockerLogCapturePlan(t, tc.bytes, tc.lines)
			stream := append(dockerLogFrame(1, []byte(tc.content)), dockerLogFrame(2, []byte("err"))...)
			records, status, output, err := DecodeDockerLogFramesWithOutput(context.Background(), plan, bytes.NewReader(stream))
			if err != nil || status != tc.status || output.Stdout != tc.want || output.Stderr != "err" {
				t.Fatalf("capture = %#v, %s, %v", output, status, err)
			}
			if records[0].ByteCount > tc.bytes || records[0].LineCount > tc.lines {
				t.Fatalf("capture exceeded bounds: %#v", records[0])
			}
		})
	}
	plan := testDockerLogCapturePlan(t, 1024, 64)
	stream := append(dockerLogFrame(1, []byte("partial")), dockerLogFrame(3, []byte("invalid"))...)
	_, status, output, err := DecodeDockerLogFramesWithOutput(context.Background(), plan, bytes.NewReader(stream))
	if err != nil || status != DockerLogCaptureStatusInvalidStream || output.Stdout != "partial" || output.Stderr != "" {
		t.Fatalf("invalid stream was represented as complete: %#v, %s, %v", output, status, err)
	}
	for headerBytes := 1; headerBytes < 8; headerBytes++ {
		stream := append(dockerLogFrame(1, []byte("partial")), make([]byte, headerBytes)...)
		_, status, output, err := DecodeDockerLogFramesWithOutput(context.Background(), plan, bytes.NewReader(stream))
		if err != nil || status != DockerLogCaptureStatusInvalidStream || output.Stdout != "partial" {
			t.Fatalf("short header (%d bytes) was represented as complete: %#v, %s, %v", headerBytes, output, status, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, status, output, err = DecodeDockerLogFramesWithOutput(ctx, plan, bytes.NewReader(stream))
	if err != nil || status != DockerLogCaptureStatusTruncatedDeadline || output != (DockerLogOutput{}) {
		t.Fatalf("cancelled capture returned text: %#v, %s, %v", output, status, err)
	}
}

func TestDockerLogOutputDoesNotReturnPartialContentOnReadFailure(t *testing.T) {
	plan := testDockerLogCapturePlan(t, 1024, 64)
	readErr := errors.New("fixture transport disconnected")
	src := io.MultiReader(bytes.NewReader(dockerLogFrame(1, []byte("partial"))), dockerLogErrorReader{readErr})
	records, _, output, err := DecodeDockerLogFramesWithOutput(context.Background(), plan, src)
	if !errors.Is(err, readErr) || records != nil || output != (DockerLogOutput{}) {
		t.Fatalf("failed capture leaked unreceipted text: %#v, %v", output, err)
	}
}

type dockerLogErrorReader struct{ err error }

func (r dockerLogErrorReader) Read([]byte) (int, error) { return 0, r.err }
