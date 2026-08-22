//go:build !windows

package repository

import "os"

func isReparsePoint(_ os.FileInfo) bool { return false }
