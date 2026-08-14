package sandbox

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"cyberagent-workbench/internal/redact"
)

const (
	DockerLogCaptureProtocolVersion         = "sandbox_docker_log_capture.v1"
	DockerLogCaptureStatusCompleted         = "completed"
	DockerLogCaptureStatusTruncatedBytes    = "truncated_bytes"
	DockerLogCaptureStatusTruncatedLines    = "truncated_lines"
	DockerLogCaptureStatusTruncatedDeadline = "truncated_deadline"
	DockerLogCaptureStatusInvalidStream     = "invalid_stream"
	MaxDockerLogCaptureBytes                = 256 * 1024
	MaxDockerLogCaptureLines                = 4096
	MaxDockerLogCaptureDurationSeconds      = 300
	MaxDockerLogFrameBytes                  = 256 * 1024
)

// DockerLogCapturePlan pins the bounded stdout/stderr capture contract for
// one lifecycle attempt. Raw content never persists: only the bounded,
// redacted in-memory digest set becomes part of the receipt.
type DockerLogCapturePlan struct {
	ProtocolVersion        string
	AttemptID              string
	Generation             int64
	RunID                  string
	ContainerIDFingerprint string
	MaxBytes               int64
	MaxLines               int
	DurationSeconds        int
	RedactSecrets          bool
	RequireUTF8            bool
	CaptureFingerprint     string
}

func NewDockerLogCapturePlan(attemptID string, generation int64, runID,
	containerIDFingerprint string, maxBytes int64, maxLines, durationSeconds int,
) (DockerLogCapturePlan, error) {
	plan := DockerLogCapturePlan{
		ProtocolVersion: DockerLogCaptureProtocolVersion, AttemptID: attemptID,
		Generation: generation, RunID: runID, ContainerIDFingerprint: containerIDFingerprint,
		MaxBytes: maxBytes, MaxLines: maxLines, DurationSeconds: durationSeconds,
		RedactSecrets: true, RequireUTF8: true,
	}
	plan.CaptureFingerprint = dockerLogCapturePlanFingerprint(plan)
	return plan, plan.Validate()
}

func (plan DockerLogCapturePlan) Validate() error {
	for label, value := range map[string]string{
		"docker log capture attempt id": plan.AttemptID,
		"docker log capture Run id":     plan.RunID,
	} {
		if validateStoredIdentity(label, value) != nil {
			return errors.New("docker log capture identity is invalid")
		}
	}
	if plan.ProtocolVersion != DockerLogCaptureProtocolVersion || plan.Generation != 1 ||
		!validDigest(plan.ContainerIDFingerprint) ||
		plan.MaxBytes < 1 || plan.MaxBytes > MaxDockerLogCaptureBytes ||
		plan.MaxLines < 1 || plan.MaxLines > MaxDockerLogCaptureLines ||
		plan.DurationSeconds < 1 || plan.DurationSeconds > MaxDockerLogCaptureDurationSeconds ||
		!plan.RedactSecrets || !plan.RequireUTF8 ||
		plan.CaptureFingerprint != dockerLogCapturePlanFingerprint(plan) {
		return errors.New("docker log capture plan violates a fixed bound")
	}
	return nil
}

func dockerLogCapturePlanFingerprint(plan DockerLogCapturePlan) string {
	return fingerprint(DockerLogCaptureProtocolVersion, plan.AttemptID,
		strconv.FormatInt(plan.Generation, 10), plan.RunID, plan.ContainerIDFingerprint,
		strconv.FormatInt(plan.MaxBytes, 10), strconv.Itoa(plan.MaxLines),
		strconv.Itoa(plan.DurationSeconds), strconv.FormatBool(plan.RedactSecrets),
		strconv.FormatBool(plan.RequireUTF8))
}

// DockerLogStreamRecord is the bounded, digest-only projection of one stream.
// It never carries raw content.
type DockerLogStreamRecord struct {
	Stream            string
	ByteCount         int64
	LineCount         int
	TruncatedBytes    bool
	TruncatedLines    bool
	TruncatedDeadline bool
	UTF8Violations    int
	RedactedSegments  int
	ContentDigest     string
}

func (record DockerLogStreamRecord) Validate() error {
	if record.Stream != "stdout" && record.Stream != "stderr" ||
		record.ByteCount < 0 || record.LineCount < 0 || record.UTF8Violations < 0 ||
		record.RedactedSegments < 0 || !validDigest(record.ContentDigest) {
		return errors.New("docker log stream record is invalid")
	}
	if record.TruncatedBytes && record.TruncatedLines {
		return errors.New("docker log stream cannot truncate on two bounds")
	}
	return nil
}

// DockerLogCaptureReceipt is the durable, content-free evidence of one
// bounded capture. Raw stdout/stderr bytes never enter SQLite or events.
type DockerLogCaptureReceipt struct {
	ProtocolVersion        string
	ID                     string
	AttemptID              string
	Generation             int64
	RunID                  string
	ContainerIDFingerprint string
	Status                 string
	Streams                []DockerLogStreamRecord
	StreamCount            int
	TotalBytes             int64
	TotalLines             int
	UTF8Violations         int
	RedactedSegments       int
	CaptureMaxBytes        int64
	CaptureMaxLines        int
	CaptureFingerprint     string
	ReceiptFingerprint     string
	CreatedAt              time.Time
}

func NewDockerLogCaptureReceipt(id, attemptID string, generation int64, runID,
	containerIDFingerprint string, plan DockerLogCapturePlan,
	streams []DockerLogStreamRecord, status string, createdAt time.Time,
) (DockerLogCaptureReceipt, error) {
	receipt := DockerLogCaptureReceipt{
		ProtocolVersion: DockerLogCaptureProtocolVersion, ID: id, AttemptID: attemptID,
		Generation: generation, RunID: runID, ContainerIDFingerprint: containerIDFingerprint,
		Status: status, Streams: append([]DockerLogStreamRecord(nil), streams...),
		CaptureMaxBytes: plan.MaxBytes, CaptureMaxLines: plan.MaxLines,
		CaptureFingerprint: plan.CaptureFingerprint, CreatedAt: createdAt,
	}
	receipt.StreamCount = len(receipt.Streams)
	for _, stream := range receipt.Streams {
		receipt.TotalBytes += stream.ByteCount
		receipt.TotalLines += stream.LineCount
		receipt.UTF8Violations += stream.UTF8Violations
		receipt.RedactedSegments += stream.RedactedSegments
	}
	receipt.ReceiptFingerprint = dockerLogCaptureReceiptFingerprint(receipt)
	return receipt, receipt.Validate()
}

func (receipt DockerLogCaptureReceipt) Validate() error {
	for label, value := range map[string]string{
		"docker log receipt id": receipt.ID, "docker log receipt attempt id": receipt.AttemptID,
		"docker log receipt Run id": receipt.RunID,
	} {
		if validateStoredIdentity(label, value) != nil {
			return errors.New("docker log capture receipt identity is invalid")
		}
	}
	if receipt.ProtocolVersion != DockerLogCaptureProtocolVersion || receipt.Generation != 1 ||
		!validDigest(receipt.ContainerIDFingerprint) || !validDigest(receipt.CaptureFingerprint) ||
		!validDockerLogCaptureStatus(receipt.Status) || receipt.CreatedAt.IsZero() ||
		receipt.StreamCount != len(receipt.Streams) || len(receipt.Streams) != 2 {
		return errors.New("docker log capture receipt is invalid")
	}
	seen := map[string]bool{}
	var totalBytes int64
	totalLines := 0
	truncated := false
	for index, stream := range receipt.Streams {
		if index == 0 && stream.Stream != "stdout" || index == 1 && stream.Stream != "stderr" ||
			seen[stream.Stream] || stream.Validate() != nil ||
			stream.ByteCount > receipt.CaptureMaxBytes || stream.LineCount > receipt.CaptureMaxLines {
			return errors.New("docker log capture stream sequence is invalid")
		}
		seen[stream.Stream] = true
		totalBytes += stream.ByteCount
		totalLines += stream.LineCount
		if stream.TruncatedBytes && stream.ByteCount != receipt.CaptureMaxBytes ||
			stream.TruncatedLines && stream.LineCount != receipt.CaptureMaxLines {
			return errors.New("docker log truncation flag does not bind its cap")
		}
		truncated = truncated || stream.TruncatedBytes || stream.TruncatedLines || stream.TruncatedDeadline
	}
	if receipt.CaptureMaxBytes < 1 || receipt.CaptureMaxBytes > MaxDockerLogCaptureBytes ||
		receipt.CaptureMaxLines < 1 || receipt.CaptureMaxLines > MaxDockerLogCaptureLines ||
		receipt.TotalBytes != totalBytes || receipt.TotalLines != totalLines ||
		receipt.UTF8Violations != receipt.Streams[0].UTF8Violations+receipt.Streams[1].UTF8Violations ||
		receipt.RedactedSegments != receipt.Streams[0].RedactedSegments+receipt.Streams[1].RedactedSegments ||
		receipt.ReceiptFingerprint != dockerLogCaptureReceiptFingerprint(receipt) {
		return errors.New("docker log capture receipt aggregate is invalid")
	}
	switch receipt.Status {
	case DockerLogCaptureStatusCompleted:
		if truncated {
			return errors.New("completed docker log receipt carries a truncation flag")
		}
	case DockerLogCaptureStatusInvalidStream, DockerLogCaptureStatusTruncatedDeadline:
	default:
		if !truncated {
			return errors.New("truncated docker log receipt misses its truncation flag")
		}
	}
	return nil
}

func validDockerLogCaptureStatus(status string) bool {
	switch status {
	case DockerLogCaptureStatusCompleted, DockerLogCaptureStatusTruncatedBytes,
		DockerLogCaptureStatusTruncatedLines, DockerLogCaptureStatusTruncatedDeadline,
		DockerLogCaptureStatusInvalidStream:
		return true
	default:
		return false
	}
}

func dockerLogCaptureReceiptFingerprint(receipt DockerLogCaptureReceipt) string {
	parts := []string{DockerLogCaptureProtocolVersion, receipt.ID, receipt.AttemptID,
		strconv.FormatInt(receipt.Generation, 10), receipt.RunID,
		receipt.ContainerIDFingerprint, receipt.Status, receipt.CaptureFingerprint,
		strconv.FormatInt(receipt.CaptureMaxBytes, 10), strconv.Itoa(receipt.CaptureMaxLines),
		strconv.Itoa(receipt.StreamCount)}
	for _, stream := range receipt.Streams {
		parts = append(parts, stream.Stream, strconv.FormatInt(stream.ByteCount, 10),
			strconv.Itoa(stream.LineCount), strconv.FormatBool(stream.TruncatedBytes),
			strconv.FormatBool(stream.TruncatedLines), strconv.FormatBool(stream.TruncatedDeadline),
			strconv.Itoa(stream.UTF8Violations), strconv.Itoa(stream.RedactedSegments),
			stream.ContentDigest)
	}
	return fingerprint(parts...)
}

type dockerLogStreamAccumulator struct {
	record      DockerLogStreamRecord
	pending     []byte
	budgetBytes int64
	budgetLines int
	content     strings.Builder
}

// DecodeDockerLogFrames consumes the raw Docker attach multiplexed stream
// (8-byte header: stream type, three zero bytes, big-endian size) under the
// plan bounds. It returns bounded, redacted, digest-only stream records plus
// a status. Raw bytes stay in memory and are never persisted.
func DecodeDockerLogFrames(ctx context.Context, plan DockerLogCapturePlan,
	src io.Reader,
) ([]DockerLogStreamRecord, string, error) {
	if err := plan.Validate(); err != nil {
		return nil, "", err
	}
	if src == nil {
		return nil, "", errors.New("docker log source is required")
	}
	streams := [2]*dockerLogStreamAccumulator{
		{record: DockerLogStreamRecord{Stream: "stdout"}, budgetBytes: plan.MaxBytes, budgetLines: plan.MaxLines},
		{record: DockerLogStreamRecord{Stream: "stderr"}, budgetBytes: plan.MaxBytes, budgetLines: plan.MaxLines},
	}
	status := DockerLogCaptureStatusCompleted
	for {
		if err := ctx.Err(); err != nil {
			streams[0].record.TruncatedDeadline = true
			streams[1].record.TruncatedDeadline = true
			status = DockerLogCaptureStatusTruncatedDeadline
			break
		}
		header := make([]byte, 8)
		_, err := io.ReadFull(src, header)
		if err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break
			}
			return nil, "", err
		}
		if header[1] != 0 || header[2] != 0 || header[3] != 0 ||
			(header[0] != 1 && header[0] != 2) {
			status = DockerLogCaptureStatusInvalidStream
			break
		}
		size := binary.BigEndian.Uint32(header[4:8])
		if size == 0 {
			continue
		}
		if size > MaxDockerLogFrameBytes {
			status = DockerLogCaptureStatusInvalidStream
			break
		}
		accumulator := streams[header[0]-1]
		if accumulator.record.TruncatedBytes || accumulator.record.TruncatedLines {
			if _, err := io.CopyN(io.Discard, src, int64(size)); err != nil {
				return nil, "", err
			}
			continue
		}
		remaining := accumulator.budgetBytes - accumulator.record.ByteCount
		readBytes := int64(size)
		if readBytes > remaining {
			readBytes = remaining
		}
		buffer := make([]byte, readBytes)
		if _, err := io.ReadFull(src, buffer); err != nil {
			return nil, "", err
		}
		if int64(size) > readBytes {
			if _, err := io.CopyN(io.Discard, src, int64(size)-readBytes); err != nil {
				return nil, "", err
			}
		}
		accumulator.record.ByteCount += int64(len(buffer))
		decoded, pending, violations := decodeDockerLogUTF8(accumulator.pending, buffer)
		accumulator.pending = pending
		accumulator.record.UTF8Violations += violations
		accumulator.record.LineCount += strings.Count(decoded, "\n")
		accumulator.content.WriteString(decoded)
		if accumulator.record.ByteCount >= accumulator.budgetBytes {
			accumulator.record.TruncatedBytes = true
		} else if accumulator.record.LineCount >= accumulator.budgetLines {
			accumulator.record.TruncatedLines = true
			// The crossing frame may overshoot the line cap; the receipt binds
			// the exact capped count so the digest stays deterministic.
			accumulator.record.LineCount = accumulator.budgetLines
		}
	}
	records := make([]DockerLogStreamRecord, 2)
	for index, accumulator := range streams {
		record := accumulator.record
		content := accumulator.content.String()
		if record.TruncatedLines {
			content = strings.Join(strings.SplitN(content, "\n", accumulator.budgetLines+1)[:accumulator.budgetLines], "\n")
		}
		redacted := redact.String(content)
		if redacted != content {
			record.RedactedSegments = strings.Count(redacted, "[REDACTED:")
		}
		record.ContentDigest = hashDockerLogContent(redacted)
		records[index] = record
	}
	if status == DockerLogCaptureStatusCompleted {
		switch {
		case streams[0].record.TruncatedBytes || streams[1].record.TruncatedBytes:
			status = DockerLogCaptureStatusTruncatedBytes
		case streams[0].record.TruncatedLines || streams[1].record.TruncatedLines:
			status = DockerLogCaptureStatusTruncatedLines
		}
	}
	return records, status, nil
}

// decodeDockerLogUTF8 decodes chunk on top of a pending incomplete-run tail,
// counts and replaces invalid sequences with U+FFFD, and returns the text,
// the new tail, and the violation count.
func decodeDockerLogUTF8(pending, chunk []byte) (string, []byte, int) {
	data := append(pending, chunk...)
	var builder strings.Builder
	violations := 0
	for len(data) > 0 {
		current, size := utf8.DecodeRune(data)
		if current == utf8.RuneError && size == 1 {
			if len(data) < utf8.UTFMax && !utf8.FullRune(data) {
				break
			}
			violations++
			builder.WriteRune(utf8.RuneError)
			data = data[1:]
			continue
		}
		builder.WriteRune(current)
		data = data[size:]
	}
	return builder.String(), append([]byte(nil), data...), violations
}

func hashDockerLogContent(content string) string {
	digest := sha256.Sum256([]byte(content))
	return hex.EncodeToString(digest[:])
}
