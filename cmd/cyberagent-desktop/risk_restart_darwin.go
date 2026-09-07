//go:build darwin && desktop

package main

/*
#cgo LDFLAGS: -lproc
#include <libproc.h>
*/
import "C"

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
	"unsafe"
)

const riskRestartParentWaitTimeout = 2 * time.Minute

const darwinRiskRestartReadyFD = 3

func newRiskRestartReadyChannel() (*riskRestartReadyChannel, error) {
	reader, writer, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	token, err := newRiskRestartReadyToken()
	if err != nil {
		_ = reader.Close()
		_ = writer.Close()
		return nil, err
	}
	return &riskRestartReadyChannel{
		reader: reader,
		writer: writer,
		value:  strconv.Itoa(darwinRiskRestartReadyFD),
		token:  token,
	}, nil
}

func validRiskRestartReadyDescriptor(value string) bool {
	return value == strconv.Itoa(darwinRiskRestartReadyFD)
}

func configureRiskRestartHelperCommand(command *exec.Cmd, ready *riskRestartReadyChannel) {
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	command.ExtraFiles = []*os.File{ready.writer}
}

func signalRiskRestartReady(value, token string) error {
	writer, err := openRiskRestartReadyWriter(value)
	if err != nil {
		return err
	}
	return writeRiskRestartReady(writer, token)
}

func openRiskRestartReadyWriter(value string) (*os.File, error) {
	if !validRiskRestartReadyDescriptor(value) {
		return nil, errors.New("desktop restart helper ready descriptor is invalid")
	}
	writer := os.NewFile(darwinRiskRestartReadyFD, "desktop-risk-restart-ready")
	if writer == nil {
		return nil, errors.New("desktop restart helper ready descriptor is unavailable")
	}
	return writer, nil
}

type darwinRiskRestartParentWaiter struct {
	parentPID int
}

func prepareRiskRestartParent(parentPID int) (*darwinRiskRestartParentWaiter, error) {
	if parentPID <= 0 || parentPID != os.Getppid() {
		return nil, errors.New("desktop restart helper parent identity is invalid")
	}
	parentExecutable, err := darwinProcessExecutable(parentPID)
	if err != nil {
		return nil, errors.New("desktop restart helper parent identity is unavailable")
	}
	selfExecutable, err := darwinProcessExecutable(os.Getpid())
	if err != nil || !sameDarwinExecutable(parentExecutable, selfExecutable) {
		return nil, errors.New("desktop restart helper parent executable is invalid")
	}
	return &darwinRiskRestartParentWaiter{parentPID: parentPID}, nil
}

func (w *darwinRiskRestartParentWaiter) Wait() error {
	if w == nil || w.parentPID <= 0 {
		return errors.New("desktop restart helper parent waiter is unavailable")
	}
	deadline := time.Now().Add(riskRestartParentWaitTimeout)
	for {
		// A child is reparented when the original Desktop exits. Checking the
		// process relationship avoids adopting an unrelated process after PID
		// reuse and does not require a shell or renderer-supplied process data.
		if os.Getppid() != w.parentPID {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("desktop restart helper timed out waiting for its parent")
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func (w *darwinRiskRestartParentWaiter) Close() {}

func darwinProcessExecutable(pid int) (string, error) {
	if pid <= 0 {
		return "", errors.New("process id is invalid")
	}
	buffer := make([]byte, C.PROC_PIDPATHINFO_MAXSIZE)
	written := C.proc_pidpath(C.int(pid), unsafe.Pointer(&buffer[0]), C.uint32_t(len(buffer)))
	if written <= 0 || int(written) > len(buffer) {
		return "", errors.New("process executable path is unavailable")
	}
	path := C.GoString((*C.char)(unsafe.Pointer(&buffer[0])))
	if path == "" {
		return "", errors.New("process executable path is unavailable")
	}
	return path, nil
}

func sameDarwinExecutable(left, right string) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	leftInfo, leftErr := os.Stat(left)
	rightInfo, rightErr := os.Stat(right)
	return leftErr == nil && rightErr == nil && os.SameFile(leftInfo, rightInfo)
}
