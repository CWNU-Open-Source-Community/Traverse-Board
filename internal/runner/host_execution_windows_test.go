//go:build windows

package runner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

const windowsHostExecutionHelperEnvironment = "CYBERAGENT_HOST_EXECUTION_HELPER"

func TestWindowsHostExecutionOptIn(t *testing.T) {
	if os.Getenv("CYBERAGENT_TEST_WINDOWS_HOST_EXECUTION") != "1" {
		t.Skip("set CYBERAGENT_TEST_WINDOWS_HOST_EXECUTION=1 for the process smoke")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		t.Fatal(err)
	}
	executable = filepath.Clean(executable)
	digest, err := hostTestFileSHA256(executable)
	if err != nil {
		t.Fatal(err)
	}
	environment := []string{
		windowsHostExecutionHelperEnvironment + "=1",
		"NO_COLOR=1",
	}
	for _, key := range []string{"SystemRoot", "TEMP", "TMP"} {
		if value, ok := os.LookupEnv(key); ok && value != "" {
			environment = append(environment, key+"="+value)
		}
	}
	sort.Slice(environment, func(left, right int) bool {
		return strings.ToLower(environment[left]) <
			strings.ToLower(environment[right])
	})
	spec, err := NewHostCommandSpec(HostCommandSpecRequest{
		ExecutablePath:   executable,
		ExecutableSHA256: digest,
		Argv: []string{
			"-test.run=^TestWindowsHostExecutionHelper$",
		},
		WorkingDirectory:    filepath.Clean(t.TempDir()),
		Environment:         environment,
		NetworkIntent:       HostNetworkIntentHost,
		TimeoutMilliseconds: (30 * time.Second).Milliseconds(),
		Purpose:             "verify the Windows host adapter with the current Go test binary",
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := (windowsHostStarter{}).Start(context.Background(),
		HostStartSpec{
			RequestID: "host-exec-windows-smoke",
			Command:   spec, Environment: environment,
		})
	if err != nil {
		t.Fatal(err)
	}
	if err := result.Validate(); err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 ||
		!bytes.Contains(result.Stdout.Data, []byte("PRAYU_HOST_HELPER_OK")) ||
		len(result.Stderr.Data) != 0 ||
		!result.TreeReaped || !result.JobAssignedAtCreation ||
		!result.KillOnJobClose || !result.NonSandboxed ||
		result.RestrictedToken || result.LowIntegrityToken {
		t.Fatalf("unexpected Windows host execution result: %+v", result)
	}
}

func TestWindowsHostExecutionHelper(t *testing.T) {
	if os.Getenv(windowsHostExecutionHelperEnvironment) != "1" {
		t.Skip("host execution helper is child-process only")
	}
	_, _ = fmt.Fprintln(os.Stdout, "PRAYU_HOST_HELPER_OK")
}

func hostTestFileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}
