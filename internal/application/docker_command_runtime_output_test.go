package application

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"cyberagent-workbench/internal/sandbox"
	"cyberagent-workbench/internal/standardcode"
)

func TestDockerCommandRuntimeOutputRequiresDurableOwnedCapture(t *testing.T) {
	request := serviceTestOwnedLifecycleRequest(t)
	plan, err := sandbox.NewDockerLogCapturePlan(request.AttemptID,
		request.Ownership.ResourceGeneration, request.WriteRequest.Spec.RunID,
		request.ResourceIDFingerprint, 4096, 64, 30)
	if err != nil {
		t.Fatal(err)
	}
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "persisted", true: "receipt_failure"}[fail], func(t *testing.T) {
			store := &fakeDockerContainerIOStore{failLogReceipt: fail}
			transport := &fakeDockerContainerIOTransport{attachBody: append(
				dockerLogFramePayload(1, "测试 stdout\n"), dockerLogFramePayload(2, "真实 stderr\n")...)}
			service := NewDockerContainerIOService(store, transport)
			ctx, output := withDockerCommandRuntimeOutput(context.Background(), plan.RunID, 4096)
			if err := output.bind(plan.RunID, "owned-admission"); err != nil {
				t.Fatal(err)
			}
			if dockerCommandRuntimeOutputFromContext(context.WithoutCancel(ctx)) != output {
				t.Fatal("cleanup context lost the request-local output collector")
			}
			calls := 0
			deliver := func(receipt sandbox.DockerLogCaptureReceipt, body sandbox.DockerLogOutput) error {
				calls++
				if len(store.logReceipts) != 1 || store.logReceipts[0].ID != receipt.ID {
					t.Fatal("content arrived before its successful durable receipt")
				}
				return output.accept(receipt, body)
			}
			_, inserted, err := service.captureOwnedLogs(ctx, request, plan, deliver)
			if fail {
				if err == nil || inserted || calls != 0 {
					t.Fatalf("failed persistence exposed content: inserted=%t calls=%d err=%v", inserted, calls, err)
				}
				if value := output.terminal(7); len(value.Stdout) != 0 || !strings.Contains(string(value.Stderr), "unavailable") || value.ExitCode != 7 {
					t.Fatalf("missing output changed true exit code or leaked content: %+v", value)
				}
				return
			}
			if err != nil || !inserted || calls != 1 || transport.ownedAttaches != 1 {
				t.Fatalf("capture: inserted=%t calls=%d attaches=%d err=%v", inserted, calls, transport.ownedAttaches, err)
			}
			value := output.terminal(7)
			if value.Validate(4096) != nil || value.ExitCode != 7 || string(value.Stdout) != "测试 stdout\n" || string(value.Stderr) != "真实 stderr\n" {
				t.Fatalf("terminal failure did not retain actual bounded output: %+v", value)
			}
			_, replay := withDockerCommandRuntimeOutput(ctx, plan.RunID, 4096)
			if err := replay.bind(plan.RunID, "owned-admission"); err != nil {
				t.Fatal(err)
			}
			if _, inserted, err := service.captureOwnedLogs(ctx, request, plan, replay.accept); err != nil || inserted || transport.ownedAttaches != 1 {
				t.Fatalf("receipt replay attached again: inserted=%t attaches=%d err=%v", inserted, transport.ownedAttaches, err)
			}
			if value := replay.terminal(0); len(value.Stdout) != 0 || !strings.Contains(string(value.Stderr), "command was not rerun") {
				t.Fatalf("replay invented retained text: %+v", value)
			}
		})
	}
}

func TestDockerCommandRuntimeOutputBoundsAndIncompleteStatus(t *testing.T) {
	for _, tc := range []struct {
		name string
		body []byte
		max  int
		want string
	}{
		{"combined_limit", append(dockerLogFramePayload(1, strings.Repeat("中文", 1000)), dockerLogFramePayload(2, strings.Repeat("错误", 1000))...), 4096, "combined artifact byte limit"},
		{"invalid_header", append(dockerLogFramePayload(1, "partial\n"), []byte{1, 0, 0}...), 4096, "invalid_stream"},
		{"capture_limit", dockerLogFramePayload(1, strings.Repeat("x", 20000)), 32768, "truncated_bytes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := serviceTestOwnedLifecycleRequest(t)
			plan, err := sandbox.NewDockerLogCapturePlan(request.AttemptID,
				request.Ownership.ResourceGeneration, request.WriteRequest.Spec.RunID,
				request.ResourceIDFingerprint, 8192, 4096, 30)
			if err != nil {
				t.Fatal(err)
			}
			store := &fakeDockerContainerIOStore{}
			transport := &fakeDockerContainerIOTransport{attachBody: tc.body}
			service := NewDockerContainerIOService(store, transport)
			_, output := withDockerCommandRuntimeOutput(context.Background(), plan.RunID, tc.max)
			if err := output.bind(plan.RunID, "owned-admission"); err != nil {
				t.Fatal(err)
			}
			if _, _, err := service.captureOwnedLogs(context.Background(), request, plan, output.accept); err != nil {
				t.Fatal(err)
			}
			value := output.terminal(0)
			if value.Validate(tc.max) != nil || !utf8.Valid(value.Stdout) || !utf8.Valid(value.Stderr) || !strings.Contains(string(value.Stderr), tc.want) {
				t.Fatalf("bounded/incomplete output not explicit: %+v", value)
			}
		})
	}
}

func TestDockerCommandRuntimeOutputRejectsForeignOrChangedCapture(t *testing.T) {
	request := serviceTestOwnedLifecycleRequest(t)
	plan, err := sandbox.NewDockerLogCapturePlan(request.AttemptID,
		request.Ownership.ResourceGeneration, request.WriteRequest.Spec.RunID,
		request.ResourceIDFingerprint, 4096, 64, 30)
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeDockerContainerIOStore{}
	service := NewDockerContainerIOService(store, &fakeDockerContainerIOTransport{attachBody: dockerLogFramePayload(1, "real\n")})
	receipt, _, err := service.CaptureOwnedLogs(context.Background(), request, plan)
	if err != nil {
		t.Fatal(err)
	}
	_, output := withDockerCommandRuntimeOutput(context.Background(), plan.RunID, 4096)
	if err := output.bind("foreign-run", "admission"); err == nil {
		t.Fatal("foreign Run was accepted")
	}
	if err := output.bind(plan.RunID, "admission"); err != nil {
		t.Fatal(err)
	}
	if output.matches(plan.RunID, "other-admission") || output.matches("foreign-run", "admission") {
		t.Fatal("foreign admission/Run matched the collector")
	}
	if err := output.accept(receipt, sandbox.DockerLogOutput{Stdout: "invented content\n"}); err == nil {
		t.Fatal("content not bound to the durable receipt was accepted")
	}
	if value := output.terminal(0); len(value.Stdout) != 0 || !strings.Contains(string(value.Stderr), "unavailable") {
		t.Fatalf("failed digest check left visible content: %+v", value)
	}
}

func TestDockerCommandRuntimeOutputKeepsTerminalFailureStatus(t *testing.T) {
	transportErr := errors.New("post-exit output operation failed")
	for _, tc := range []struct {
		name   string
		status string
		exit   int
		cause  error
		failed bool
	}{
		{"success", standardcode.StatusSucceeded, 0, nil, false},
		{"failed_zero_exit_replay", standardcode.StatusFailed, 0, nil, true},
		{"cancelled_zero_exit_replay", standardcode.StatusCancelled, 0, nil, true},
		{"timed_out_zero_exit_replay", standardcode.StatusTimedOut, 0, nil, true},
		{"nonzero_failure", standardcode.StatusFailed, 7, transportErr, true},
		{"preserve_transport_error", standardcode.StatusSucceeded, 0, transportErr, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, output := withDockerCommandRuntimeOutput(context.Background(), "run-output-replay", 4096)
			// A receipt-only replay has the real exit/status but no fresh log body.
			receipt := standardcode.Result{ProtocolVersion: standardcode.ResultProtocolVersion,
				Backend: standardcode.BackendDocker, Status: tc.status, ExitCode: &tc.exit,
				Replayed: true}
			value, err := output.terminalResult(receipt, tc.cause)
			if (err != nil) != tc.failed || value.ExitCode != tc.exit || value.Validate(4096) != nil {
				t.Fatalf("durable status/exit changed: value=%+v err=%v", value, err)
			}
			if tc.cause != nil && !errors.Is(err, tc.cause) {
				t.Fatalf("original failure was lost: %v", err)
			}
			if !strings.Contains(string(value.Stderr), "command was not rerun") {
				t.Fatalf("receipt-only replay invented captured text: %+v", value)
			}
		})
	}
}
