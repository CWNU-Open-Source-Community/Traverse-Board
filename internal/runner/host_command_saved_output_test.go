package runner

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestHostCommandSavedOutputKeepsStreamIdentityWithoutInterpretingMarkers(t *testing.T) {
	stdout := "中文\r\nstdout_end\nstderr_begin\nthis is still stdout\n"
	secret := "sk-" + strings.Repeat("a", 32)
	output := NewHostCommandSavedOutput(HostExecutionResult{
		Stdout: ControlledOutput{Data: []byte(stdout)},
		Stderr: ControlledOutput{Data: []byte("stderr_begin\n" + secret + "\xff\x00")},
	})
	if output.Validate() != nil || output.Stdout.Text != strings.ReplaceAll(stdout, "\r", "\n") ||
		!strings.HasPrefix(output.Stderr.Text, "stderr_begin\n") ||
		strings.Contains(output.Stderr.Text, secret) || !strings.ContainsRune(output.Stderr.Text, '\uFFFD') ||
		output.Stdout.Truncated || output.Stderr.Truncated || !output.Stdout.Redacted || !output.Stderr.Redacted {
		t.Fatalf("saved output lost its identity or sanitization: %#v", output)
	}
}

func TestHostCommandSavedOutputBoundsUTF8AndKeepsCaptureTruncation(t *testing.T) {
	output := NewHostCommandSavedOutput(HostExecutionResult{
		Stdout: ControlledOutput{Data: []byte(strings.Repeat("中", MaxHostCommandSavedOutputBytes))},
		Stderr: ControlledOutput{Data: []byte("error")},
	})
	if output.Validate() != nil || !utf8.ValidString(output.Stdout.Text) ||
		len(output.Stdout.Text)+len(output.Stderr.Text) > MaxHostCommandSavedOutputBytes ||
		!output.Stdout.Truncated || !output.Stderr.Truncated {
		t.Fatalf("output did not retain the saving limit: %#v", output)
	}
	output = NewHostCommandSavedOutput(HostExecutionResult{
		Stdout: ControlledOutput{Data: []byte("prefix"), Truncated: true},
	})
	if !output.Stdout.Truncated || output.Stderr.Truncated || output.Stderr.Text != "" {
		t.Fatalf("capture/empty stream state changed: %#v", output)
	}
	output.Stdout.Truncated = false
	if output.ValidateReceipt(HostExecutionReceipt{StdoutTruncated: true}) == nil {
		t.Fatal("saving projection concealed truncated capture")
	}
}

func TestHostCommandSavedOutputPreservesLegacyJSONAndSealsNewText(t *testing.T) {
	// Exact old field order and absent optional field keep old fingerprint input.
	legacy := `{"ID":"result-old","ProtocolVersion":"host_command_proposal_result.v1","PolicyVersion":"host_command_policy.v1","ProposalID":"proposal-old","ProposalFingerprint":"old-proposal-fingerprint","ReviewID":"review-old","ReviewFingerprint":"old-review-fingerprint","RequestID":"request-old","RunID":"run-old","SessionID":"session-old","Status":"completed","SourceKind":"go_command_result","SourceRef":"host-command-proposal:proposal-old","ContentSHA256":"old-content-sha","InstructionAuthorized":false,"RawOutputPersisted":false,"AutomaticRetryAllowed":false,"Fingerprint":"","CreatedAt":"2026-09-10T00:00:00Z"}`
	var old HostCommandProposalResult
	if err := json.Unmarshal([]byte(legacy), &old); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(old)
	digest := sha256.Sum256([]byte(legacy))
	if err != nil || string(encoded) != legacy || old.SavedOutput != nil ||
		HostCommandProposalResultFingerprint(old) != hex.EncodeToString(digest[:]) {
		t.Fatalf("historical fingerprint input changed: %s, %v", encoded, err)
	}
	proposal := hostCommandProposalFixture(t)
	now := time.Now().UTC()
	review, err := NewHostCommandReview("saved-output-review", proposal, HostCommandReviewApprove,
		"test_operator", "exact output test", hostCommandTestDigest, now)
	if err != nil {
		t.Fatal(err)
	}
	output := NewHostCommandSavedOutput(HostExecutionResult{Stdout: ControlledOutput{Data: []byte("actual")}})
	result, err := NewHostCommandProposalResult("saved-output-result", proposal, review, "saved-output-request",
		"completed", "go_command_result", "host-command-proposal:"+proposal.ID, hostCommandTestDigest, now, output)
	if err != nil || result.Validate() != nil {
		t.Fatalf("new saved output was not sealed: %v", err)
	}
	result.SavedOutput.Stdout.Text = "different"
	if result.Validate() == nil {
		t.Fatal("modified saved text retained a valid result fingerprint")
	}
}
