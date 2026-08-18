//go:build darwin || linux

package terminal

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTrustedPOSIXBashPathIgnoresPATH(t *testing.T) {
	before, err := trustedPOSIXBashPath()
	if err != nil {
		t.Skip("platform Bash is unavailable")
	}
	t.Setenv("PATH", t.TempDir())
	after, err := trustedPOSIXBashPath()
	if err != nil {
		t.Fatal(err)
	}
	if before != after || !filepath.IsAbs(after) || filepath.Base(after) != "bash" {
		t.Fatalf("fixed Bash resolution changed after PATH mutation: before=%q after=%q",
			before, after)
	}
}

func TestPOSIXBashPTYStartsResizesAndExits(t *testing.T) {
	backend := newPlatformBackend()
	if !backend.Available() {
		t.Skip("bash is unavailable")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	process, err := backend.Start(ctx, BackendStartRequest{
		SessionID: "posix-bash-pty-test", WorkspaceRoot: t.TempDir(),
		Columns: 100, Rows: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = process.Close() })
	if err := process.Boundary().Validate(); err != nil {
		t.Fatal(err)
	}
	if err := process.Resize(120, 32); err != nil {
		t.Fatal(err)
	}
	const marker = "__PRAYU_POSIX_PTY_OK__"
	// Split the marker across shell arguments so terminal echo alone cannot
	// satisfy the assertion; the Bash process must execute printf.
	if _, err := process.Write([]byte(
		"printf '__PRAYU_POSIX_%s\\n' 'PTY_OK__'\n")); err != nil {
		t.Fatal(err)
	}
	output := make(chan string, 1)
	go func() {
		buffer := make([]byte, 4096)
		var value strings.Builder
		for value.Len() < 32*1024 {
			count, readErr := process.Read(buffer)
			if count > 0 {
				value.Write(buffer[:count])
				if strings.Contains(value.String(), marker) {
					break
				}
			}
			if readErr != nil {
				break
			}
		}
		output <- value.String()
	}()
	select {
	case value := <-output:
		if !strings.Contains(value, marker) {
			t.Fatalf("PTY output omitted marker: %q", value)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if _, err := process.Write([]byte("exit\n")); err != nil {
		t.Fatal(err)
	}
	exitCode, err := process.Wait(ctx)
	if err != nil || exitCode != 0 {
		t.Fatalf("exit=%d err=%v", exitCode, err)
	}
}
