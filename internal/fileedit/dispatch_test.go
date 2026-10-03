package fileedit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestManagerDeleteRechecksAuthorityAndTargetBeforeRemoval(t *testing.T) {
	for _, change := range []string{"revoke", "replace", "allow"} {
		t.Run(change, func(t *testing.T) {
			root := t.TempDir()
			target := filepath.Join(root, "delete.txt")
			if err := os.WriteFile(target, []byte("original\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			hash, _ := CurrentHash(root, "delete.txt")
			manager := NewManager(newMemoryStore())
			edit, err := manager.Propose(context.Background(), Proposal{WorkspaceID: "workspace", WorkspaceRoot: root,
				Path: "delete.txt", Operation: OperationDelete, ExpectedOriginalHash: hash})
			if err != nil {
				t.Fatal(err)
			}
			checks := 0
			_, err = manager.ApproveWithPreWriteCheck(context.Background(), edit.ID, root, func() error {
				checks++
				if change == "revoke" {
					return errors.New("authority revoked")
				}
				if change == "replace" {
					return os.WriteFile(target, []byte("concurrent\n"), 0o644)
				}
				return nil
			})
			if checks != 1 {
				t.Fatalf("checks=%d", checks)
			}
			data, readErr := os.ReadFile(target)
			if change == "allow" {
				if err != nil || !os.IsNotExist(readErr) {
					t.Fatalf("delete err=%v read=%v", err, readErr)
				}
			} else {
				want := "original\n"
				if change == "replace" {
					want = "concurrent\n"
				}
				if err == nil || readErr != nil || string(data) != want {
					t.Fatalf("denied delete data=%q err=%v read=%v", data, err, readErr)
				}
			}
		})
	}
}

func TestManagerReplaceRechecksContentAfterDispatchCheck(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "replace.txt")
	if err := os.WriteFile(target, []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(newMemoryStore())
	edit, err := manager.Propose(context.Background(), Proposal{WorkspaceID: "workspace", WorkspaceRoot: root,
		Path: "replace.txt", ProposedText: "proposed\n"})
	if err != nil {
		t.Fatal(err)
	}
	checks := 0
	_, err = manager.ApproveWithPreWriteCheck(context.Background(), edit.ID, root, func() error {
		checks++
		if checks == 2 {
			return os.WriteFile(target, []byte("concurrent\n"), 0o644)
		}
		return nil
	})
	data, readErr := os.ReadFile(target)
	if err == nil || checks != 2 || readErr != nil || string(data) != "concurrent\n" {
		t.Fatalf("replace checks=%d data=%q err=%v read=%v", checks, data, err, readErr)
	}
}
