package toolgateway

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestHostCommandProposalNormalizesProcessAndShellTransports(t *testing.T) {
	legacy := json.RawMessage(`{"version":"host_command_proposal.v1","executable_path":"/usr/bin/git","argv":["status","--short"],"working_directory":"/workspace","timeout_milliseconds":30000,"purpose":"inspect repository state"}`)
	process, canonical, err := normalizeHostCommandProposalPayload(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if process.Transport != HostCommandTransportProcess ||
		!strings.Contains(string(canonical), `"transport":"process"`) {
		t.Fatalf("legacy process transport did not canonicalize: %#v %s", process, canonical)
	}

	shellPayload := json.RawMessage(`{"version":"host_command_proposal.v1","transport":"shell","shell":"powershell","command":"git status --short","working_directory":"C:\\workspace","timeout_milliseconds":30000,"purpose":"inspect repository state"}`)
	shell, canonical, err := normalizeHostCommandProposalPayload(shellPayload)
	if err != nil {
		t.Fatal(err)
	}
	if shell.Transport != HostCommandTransportShell ||
		shell.Shell != HostCommandShellPowerShell || shell.Command != "git status --short" ||
		shell.ExecutablePath != "" || len(shell.Argv) != 0 || !json.Valid(canonical) {
		t.Fatalf("shell transport did not canonicalize: %#v %s", shell, canonical)
	}
}

func TestHostCommandProposalRejectsAmbiguousOrUnsafeShellPayloads(t *testing.T) {
	for _, payload := range []json.RawMessage{
		json.RawMessage(`{"version":"host_command_proposal.v1","transport":"shell","shell":"bash","command":"first\nsecond","working_directory":"/workspace","timeout_milliseconds":30000,"purpose":"invalid multiline"}`),
		json.RawMessage(`{"version":"host_command_proposal.v1","transport":"shell","shell":"bash","command":"echo sk-abcdefghijklmnopqrstuvwxyz123456","working_directory":"/workspace","timeout_milliseconds":30000,"purpose":"secret-like input"}`),
		json.RawMessage(`{"version":"host_command_proposal.v1","transport":"shell","shell":"bash","command":"git status","executable_path":"/bin/bash","working_directory":"/workspace","timeout_milliseconds":30000,"purpose":"ambiguous transport"}`),
		json.RawMessage(`{"version":"host_command_proposal.v1","transport":"shell","shell":"bash","command":"git status","argv":null,"working_directory":"/workspace","timeout_milliseconds":30000,"purpose":"ambiguous transport"}`),
		json.RawMessage(`{"version":"host_command_proposal.v1","transport":"process","executable_path":"/usr/bin/git","argv":["status"],"shell":"bash","working_directory":"/workspace","timeout_milliseconds":30000,"purpose":"ambiguous transport"}`),
		json.RawMessage(`{"version":"host_command_proposal.v1","transport":"process","executable_path":"/usr/bin/git","working_directory":"/workspace","timeout_milliseconds":30000,"purpose":"missing argv"}`),
		json.RawMessage(`{"version":"host_command_proposal.v1","transport":"process","executable_path":"/usr/bin/git","argv":null,"working_directory":"/workspace","timeout_milliseconds":30000,"purpose":"null argv"}`),
		json.RawMessage(`{"version":"host_command_proposal.v1","transport":null,"executable_path":"/usr/bin/git","argv":[],"working_directory":"/workspace","timeout_milliseconds":30000,"purpose":"null transport"}`),
	} {
		if _, _, err := normalizeHostCommandProposalPayload(payload); err == nil {
			t.Fatalf("unsafe shell payload was accepted: %s", payload)
		}
	}
}
