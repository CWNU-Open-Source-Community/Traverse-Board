//go:build !windows && !linux

package runner

import "syscall"

func commandRuntimeSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}
