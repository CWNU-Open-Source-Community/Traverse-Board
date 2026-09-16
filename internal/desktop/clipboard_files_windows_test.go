//go:build windows

package desktop

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cyberagent-workbench/internal/apperror"
)

func TestNativeClipboardFilesReadOnlySelectedLocalRegularFiles(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "粘贴原文.md")
	content := []byte("# 原始内容\n")
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(root, "empty.txt")
	if err := os.WriteFile(empty, nil, 0600); err != nil {
		t.Fatal(err)
	}
	reader := nativeClipboardFileReader{paths: func() ([]string, error) { return []string{path, empty, root}, nil }}
	files, err := reader.ReadFiles(t.Context())
	if err != nil || len(files) != 3 {
		t.Fatalf("files=%v err=%v", files, err)
	}
	if files[0].Name != "粘贴原文.md" || string(files[0].Data) != string(content) || files[0].Error != nil {
		t.Fatalf("actual file mismatch: %+v", files[0])
	}
	if files[1].Error != nil || len(files[1].Data) != 0 {
		t.Fatalf("empty file rejected: %+v", files[1])
	}
	if apperror.CodeOf(files[2].Error) != apperror.CodeInvalidArgument || len(files[2].Data) != 0 {
		t.Fatal("directory was imported")
	}
	if current, err := os.ReadFile(path); err != nil || string(current) != string(content) {
		t.Fatal("clipboard read changed source")
	}
}

func TestNativeClipboardFilesRejectNetworkDevicesStreamsAndOversize(t *testing.T) {
	for _, path := range []string{`\\server\share\private.txt`, `\\?\C:\private.txt`, `\\.\pipe\private`, `C:\file.txt:secret`, `C:relative.txt`, `C:\NUL`, `C:\CON.txt`, `C:\COM1`} {
		file := readLocalClipboardFile(path)
		if file.Error == nil || len(file.Data) != 0 {
			t.Errorf("invalid path accepted: %q", path)
		}
		if file.Error != nil && strings.Contains(file.Error.Error(), path) {
			t.Error("error exposes original path")
		}
	}
	path := filepath.Join(t.TempDir(), "large.bin")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	err = file.Truncate(MaxClipboardFileBytes + 1)
	_ = file.Close()
	if err != nil {
		t.Fatal(err)
	}
	if value := readLocalClipboardFile(path); apperror.CodeOf(value.Error) != apperror.CodeResourceExhausted || len(value.Data) != 0 {
		t.Fatal("oversize file read")
	}
}

func TestNativeClipboardFilesNeverPollOrReadAfterCancellation(t *testing.T) {
	calls := 0
	reader := nativeClipboardFileReader{paths: func() ([]string, error) {
		calls++
		return nil, apperror.New(apperror.CodeUnavailable, "Clipboard is busy")
	}}
	if _, err := reader.ReadFiles(t.Context()); apperror.CodeOf(err) != apperror.CodeUnavailable || calls != 1 {
		t.Fatalf("busy clipboard was polled or concealed: %v calls=%d", err, calls)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := reader.ReadFiles(ctx); err == nil || calls != 1 {
		t.Fatal("cancelled request accessed clipboard")
	}
	reader.paths = func() ([]string, error) { return []string{"one", "two", "three", "four", "five"}, nil }
	if _, err := reader.ReadFiles(t.Context()); apperror.CodeOf(err) != apperror.CodeResourceExhausted {
		t.Fatal("oversized batch read")
	}
}
