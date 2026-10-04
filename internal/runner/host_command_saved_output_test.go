package runner

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

func TestHostCommandSavedOutputRejectsUnsafeHistoricalStreams(t *testing.T) {
	saved := HostCommandSavedOutput{
		Stdout: HostCommandSavedStream{Text: "中文\nstdout_end\nstderr_begin\nthis is still stdout\n", Redacted: true},
		Stderr: HostCommandSavedStream{Text: "stderr_begin\n", Redacted: true},
	}
	if err := saved.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*HostCommandSavedOutput){
		func(value *HostCommandSavedOutput) {
			value.Stdout.Text = strings.Repeat("x", MaxHostCommandSavedOutputBytes+1)
		},
		func(value *HostCommandSavedOutput) { value.Stderr.Text = "invalid\xff" },
		func(value *HostCommandSavedOutput) { value.Stderr.Text = "untrusted\x00control" },
		func(value *HostCommandSavedOutput) { value.Stderr.Text = "sk-" + strings.Repeat("a", 32) },
		func(value *HostCommandSavedOutput) { value.Stdout.Redacted = false },
	} {
		tampered := saved
		mutate(&tampered)
		if tampered.Validate() == nil {
			t.Fatal("unsafe saved stream unexpectedly validated")
		}
	}
	saved.Stdout.Text = "prefix"
	saved.Stderr.Text = ""
	if saved.ValidateReceipt(HostExecutionReceipt{StdoutCapturedBytes: 6, StdoutTruncated: true}) == nil {
		t.Fatal("saving projection concealed truncated capture")
	}
	saved.Stdout.Truncated = true
	if err := saved.ValidateReceipt(HostExecutionReceipt{StdoutCapturedBytes: 6, StdoutTruncated: true}); err != nil {
		t.Fatal(err)
	}
}

func TestHostCommandSavedOutputPreservesLegacyJSONAndSealsSavedText(t *testing.T) {
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
	review := hostCommandReviewFixture(proposal)
	result := HostCommandProposalResult{
		ID: "saved-output-result", ProtocolVersion: HostCommandResultProtocolVersion, PolicyVersion: HostCommandPolicyVersion,
		ProposalID: proposal.ID, ProposalFingerprint: proposal.Fingerprint, ReviewID: review.ID, ReviewFingerprint: review.Fingerprint,
		RequestID: "saved-output-request", RunID: proposal.RunID, SessionID: proposal.SessionID,
		Status: "completed", SourceKind: "go_command_result", SourceRef: "host-command-proposal:" + proposal.ID,
		ContentSHA256: hostCommandTestDigest, CreatedAt: proposal.CreatedAt,
		SavedOutput: &HostCommandSavedOutput{
			Stdout: HostCommandSavedStream{Text: "actual", Redacted: true},
			Stderr: HostCommandSavedStream{Redacted: true},
		},
	}
	result.Fingerprint = HostCommandProposalResultFingerprint(result)
	if err := result.Validate(); err != nil {
		t.Fatalf("historical saved output did not validate: %v", err)
	}
	result.SavedOutput.Stdout.Text = "different"
	if result.Validate() == nil {
		t.Fatal("modified saved text retained a valid result fingerprint")
	}
}
