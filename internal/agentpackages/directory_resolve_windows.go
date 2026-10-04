//go:build windows

package agentpackages

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func directoryCanonical(root *os.Root, _ string) (string, error) {
	file, err := root.Open(".")
	if err != nil {
		return "", err
	}
	defer file.Close()
	for size := 512; size <= 32768; {
		buffer := make([]uint16, size)
		length, err := windows.GetFinalPathNameByHandle(windows.Handle(file.Fd()), &buffer[0], uint32(len(buffer)), 0)
		if err != nil {
			return "", err
		}
		if length < uint32(len(buffer)) {
			return filepath.Clean(windowsLinkPath(windows.UTF16ToString(buffer[:length]))), nil
		}
		size = int(length) + 1
	}
	return "", fs.ErrInvalid
}

func windowsLinkPath(name string) string {
	if strings.HasPrefix(name, `\\?\UNC\`) {
		return `\\` + name[len(`\\?\UNC\`):]
	}
	return strings.TrimPrefix(name, `\\?\`)
}

// Since Go 1.23 junctions are ModeIrregular rather than ModeSymlink and
// EvalSymlinks leaves them in place. Inspect each link through the held root;
// no metadata or content is opened at an external link target. Windows cleans
// dot components lexically, including in junction and symbolic-link targets.
func resolveDirectoryPath(root *os.Root, canonical, local string) (string, error) {
	for links := 0; links <= 64; links++ {
		parts := strings.Split(local, string(filepath.Separator))
		current := "."
		changed := false
		for index, part := range parts {
			current = filepath.Join(current, part)
			info, err := root.Lstat(current)
			if err != nil {
				return "", err
			}
			if info.Mode()&(fs.ModeSymlink|fs.ModeIrregular) == 0 {
				continue
			}
			target, err := root.Readlink(current)
			if err != nil {
				return "", err
			}
			target = windowsLinkPath(target)
			if filepath.IsAbs(target) {
				target, err = filepath.Rel(canonical, target)
			} else {
				target = filepath.Join(filepath.Dir(current), target)
			}
			if err != nil || !filepath.IsLocal(target) {
				return "", &fs.PathError{Op: "resolve", Path: local, Err: fs.ErrPermission}
			}
			local = filepath.Join(append([]string{target}, parts[index+1:]...)...)
			changed = true
			break
		}
		if !changed {
			return local, nil
		}
	}
	return "", &fs.PathError{Op: "resolve", Path: local, Err: fs.ErrInvalid}
}
