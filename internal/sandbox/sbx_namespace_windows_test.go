//go:build windows

package sandbox

import (
	"context"
	"errors"
	"os/exec"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestSBXNamespaceWindowsCIHelpersDoNotPrepareHostACLs(t *testing.T) {
	// CI's parent TestMain can keep this host-fixture mutex alive while its
	// temporary ACL changes are active. Keep the object present without making
	// any ACL changes here, so a child that re-enters PrepareHost fails before
	// it can touch the host. Open an existing object when TestMain owns it.
	name, err := windows.UTF16PtrFromString(`Local\TraverseBoard.LocalSandbox.Test.Host.v1`)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateMutexEx(nil, name, 0,
		windows.SYNCHRONIZE|windows.MUTEX_MODIFY_STATE)
	if (err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS)) || handle == 0 {
		t.Fatalf("keep parent host-fixture mutex present: %v", err)
	}
	t.Cleanup(func() {
		if err := windows.CloseHandle(handle); err != nil {
			t.Errorf("close host-fixture mutex handle: %v", err)
		}
	})
	for _, mode := range []string{"contend", "exit-without-close"} {
		t.Run(mode, func(t *testing.T) {
			namespace := sbxNamespaceTestName(t)
			if mode == "contend" {
				owner, err := sbxAcquireNamespaceNamedLock(namespace)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = owner.Close() })
			}
			helper := sbxNamespaceHelper(t, namespace, mode)
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, helper.Path, helper.Args[1:]...)
			cmd.Dir, cmd.Env, cmd.WaitDelay = helper.Dir, helper.Env, 2*time.Second
			for key, value := range map[string]string{
				"CI": "true", "GITHUB_ACTIONS": "true", "RUNNER_ENVIRONMENT": "github-hosted",
				"TRAVERSE_BOARD_LOCAL_SANDBOX_TEST_HOST_PREP": "1",
			} {
				cmd.Env = sbxNamespaceSetEnv(cmd.Env, key, value)
			}
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("CI helper did not reach its namespace test: %v: %s", err, output)
			}
			want := "PASS\n"
			if mode == "exit-without-close" {
				want = "namespace-owned\n"
			}
			if string(output) != want {
				t.Fatalf("unexpected helper output: %q", output)
			}
			if mode == "exit-without-close" {
				owner, err := sbxAcquireNamespaceNamedLock(namespace)
				if err != nil {
					t.Fatalf("CI helper exit retained namespace ownership: %v", err)
				}
				if err := owner.Close(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
