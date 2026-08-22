//go:build linux

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceEntryGrowthFastPathUsesCurrentAggregate(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"baseline-a", "baseline-b"} {
		if err := os.WriteFile(filepath.Join(root, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	baseline, err := captureWorkspaceUsage(root, 3)
	if err != nil || baseline.Entries != 2 {
		t.Fatalf("baseline=%#v err=%v", baseline, err)
	}
	for _, name := range []string{"created-a", "created-b"} {
		if err := os.WriteFile(filepath.Join(root, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	exceeded, err := workspaceEntryGrowthExceeded(root, baseline.Entries, 2)
	if err != nil || exceeded {
		t.Fatalf("exact aggregate limit exceeded=%t err=%v", exceeded, err)
	}
	if err := os.WriteFile(filepath.Join(root, "created-c"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	exceeded, err = workspaceEntryGrowthExceeded(root, baseline.Entries, 2)
	if err != nil || !exceeded {
		t.Fatalf("aggregate overflow exceeded=%t err=%v", exceeded, err)
	}
	if err := os.Remove(filepath.Join(root, "baseline-a")); err != nil {
		t.Fatal(err)
	}
	exceeded, err = workspaceEntryGrowthExceeded(root, baseline.Entries, 2)
	if err != nil || exceeded {
		t.Fatalf("deletion did not restore aggregate budget: exceeded=%t err=%v",
			exceeded, err)
	}
}
