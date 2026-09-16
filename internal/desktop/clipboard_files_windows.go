//go:build windows

package desktop

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"

	"cyberagent-workbench/internal/apperror"

	"golang.org/x/sys/windows"
)

type nativeClipboardFileReader struct{ paths func() ([]string, error) }

func NewNativeClipboardFileReader() ClipboardFileReader {
	return nativeClipboardFileReader{paths: readWindowsClipboardFilePaths}
}

func (reader nativeClipboardFileReader) ReadFiles(ctx context.Context) ([]ClipboardFile, error) {
	if err := ctx.Err(); err != nil {
		return nil, apperror.Normalize(err)
	}
	paths, err := reader.paths()
	if err != nil {
		return nil, err
	}
	if len(paths) > MaxClipboardFiles {
		return nil, apperror.New(apperror.CodeResourceExhausted, "Paste at most four files at a time")
	}
	files := make([]ClipboardFile, 0, len(paths))
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return nil, apperror.Normalize(err)
		}
		files = append(files, readLocalClipboardFile(path))
	}
	return files, nil
}

func readWindowsClipboardFilePaths() ([]string, error) {
	// The Windows clipboard must be opened and closed on the same OS thread.
	// Do not retry/poll a locked clipboard or inspect any non-file format.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	user := windows.NewLazySystemDLL("user32.dll")
	available, _, _ := user.NewProc("IsClipboardFormatAvailable").Call(15) // CF_HDROP
	if available == 0 {
		return nil, nil
	}
	opened, _, _ := user.NewProc("OpenClipboard").Call(0)
	if opened == 0 {
		return nil, apperror.New(apperror.CodeUnavailable, "Clipboard is busy; try pasting again")
	}
	defer user.NewProc("CloseClipboard").Call()
	handle, _, _ := user.NewProc("GetClipboardData").Call(15)
	if handle == 0 {
		return nil, apperror.New(apperror.CodeUnavailable, "Clipboard file list changed or could not be read")
	}
	query := windows.NewLazySystemDLL("shell32.dll").NewProc("DragQueryFileW")
	count, _, _ := query.Call(handle, 0xffffffff, 0, 0)
	if count == 0 {
		return nil, apperror.New(apperror.CodeInvalidArgument, "Clipboard file list is empty or invalid")
	}
	if count > MaxClipboardFiles {
		return nil, apperror.New(apperror.CodeResourceExhausted, "Paste at most four files at a time")
	}
	paths := make([]string, 0, count)
	for index := uintptr(0); index < count; index++ {
		length, _, _ := query.Call(handle, index, 0, 0)
		if length == 0 || length > 32767 {
			return nil, apperror.New(apperror.CodeInvalidArgument, "Clipboard file name is invalid")
		}
		buffer := make([]uint16, length+1)
		read, _, _ := query.Call(handle, index, uintptr(unsafe.Pointer(&buffer[0])), length+1)
		if read != length {
			return nil, apperror.New(apperror.CodeUnavailable, "Clipboard file list could not be read consistently")
		}
		paths = append(paths, windows.UTF16ToString(buffer))
	}
	return paths, nil
}

func readLocalClipboardFile(path string) ClipboardFile {
	file := ClipboardFile{Name: filepath.Base(path)}
	volume := filepath.VolumeName(path)
	if len(volume) != 2 || volume[1] != ':' || !((volume[0] >= 'A' && volume[0] <= 'Z') || (volume[0] >= 'a' && volume[0] <= 'z')) ||
		!filepath.IsAbs(path) || len(path) < 4 || strings.ContainsAny(path[2:], ":\x00") {
		file.Error = apperror.New(apperror.CodeInvalidArgument, "Only local disk files can be pasted; network, device and stream paths are unsupported")
		return file
	}
	// OpenRoot confines traversal to the selected local volume, including links;
	// a clipboard path cannot turn into an implicit UNC/network or device read.
	root, err := os.OpenRoot(volume + string(filepath.Separator))
	if err != nil {
		file.Error = apperror.New(apperror.CodeUnavailable, "Clipboard file volume could not be opened")
		return file
	}
	defer root.Close()
	relative, err := filepath.Rel(volume+string(filepath.Separator), path)
	if err != nil || !filepath.IsLocal(relative) {
		file.Error = apperror.New(apperror.CodeInvalidArgument, "Clipboard file path is invalid")
		return file
	}
	opened, err := root.Open(relative)
	if err != nil {
		file.Error = apperror.New(apperror.CodeUnavailable, "Clipboard file could not be opened safely")
		return file
	}
	defer opened.Close()
	before, err := opened.Stat()
	if err != nil || !before.Mode().IsRegular() {
		file.Error = apperror.New(apperror.CodeInvalidArgument, "Only regular files can be pasted; directories and special files are unsupported")
		return file
	}
	if before.Size() > MaxClipboardFileBytes {
		file.Error = apperror.New(apperror.CodeResourceExhausted, "Attachments must be at most 5 MiB")
		return file
	}
	data, err := io.ReadAll(io.LimitReader(opened, MaxClipboardFileBytes+1))
	if err != nil {
		file.Error = apperror.New(apperror.CodeUnavailable, "Clipboard file could not be read")
		return file
	}
	after, err := opened.Stat()
	if err != nil || before.Size() != int64(len(data)) || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		file.Error = apperror.New(apperror.CodeConflict, "Clipboard file changed while being read; copy it again")
		return file
	}
	file.Data = data
	return file
}
