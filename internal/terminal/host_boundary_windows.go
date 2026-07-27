//go:build windows

package terminal

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	hostBoundaryWMClose            = 0x0010
	hostBoundaryWMDestroy          = 0x0002
	hostBoundaryWMPowerBroadcast   = 0x0218
	hostBoundaryWMTSSessionChange  = 0x02B1
	hostBoundaryWTSSessionLock     = 0x7
	hostBoundaryWTSSessionLogoff   = 0x6
	hostBoundaryWTSSessionRemoteDC = 0x4
	hostBoundaryPBTAPMSuspend      = 0x0004
	hostBoundaryPBTAPMStandby      = 0x0005
	hostBoundaryPBTAPMResumeCrit   = 0x0006
	hostBoundaryPBTAPMResume       = 0x0007
	hostBoundaryPBTAPMResumeAuto   = 0x0012
	hostBoundaryNotifyThisSession  = 0
)

var (
	hostBoundaryUser32   = windows.NewLazySystemDLL("user32.dll")
	hostBoundaryKernel32 = windows.NewLazySystemDLL("kernel32.dll")
	hostBoundaryWTSAPI32 = windows.NewLazySystemDLL("wtsapi32.dll")

	hostBoundaryRegisterClass = hostBoundaryUser32.NewProc("RegisterClassExW")
	hostBoundaryUnregister    = hostBoundaryUser32.NewProc("UnregisterClassW")
	hostBoundaryCreateWindow  = hostBoundaryUser32.NewProc("CreateWindowExW")
	hostBoundaryDestroyWindow = hostBoundaryUser32.NewProc("DestroyWindow")
	hostBoundaryDefWindowProc = hostBoundaryUser32.NewProc("DefWindowProcW")
	hostBoundaryGetMessage    = hostBoundaryUser32.NewProc("GetMessageW")
	hostBoundaryTranslate     = hostBoundaryUser32.NewProc("TranslateMessage")
	hostBoundaryDispatch      = hostBoundaryUser32.NewProc("DispatchMessageW")
	hostBoundaryPostMessage   = hostBoundaryUser32.NewProc("PostMessageW")
	hostBoundaryPostQuit      = hostBoundaryUser32.NewProc("PostQuitMessage")
	hostBoundaryGetModule     = hostBoundaryKernel32.NewProc("GetModuleHandleW")
	hostBoundaryWTSRegister   = hostBoundaryWTSAPI32.NewProc("WTSRegisterSessionNotification")
	hostBoundaryWTSUnregister = hostBoundaryWTSAPI32.NewProc("WTSUnRegisterSessionNotification")

	hostBoundaryWindows sync.Map
	hostBoundaryWndProc = syscall.NewCallback(hostBoundaryWindowProcedure)
)

type windowsHostBoundarySource struct{}

type hostBoundaryWindowState struct {
	emit func(HostBoundaryEvent)
}

type hostBoundaryWindowClass struct {
	size        uint32
	style       uint32
	windowProc  uintptr
	classExtra  int32
	windowExtra int32
	instance    windows.Handle
	icon        windows.Handle
	cursor      windows.Handle
	background  windows.Handle
	menuName    *uint16
	className   *uint16
	smallIcon   windows.Handle
}

type hostBoundaryMessage struct {
	window  windows.Handle
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pointX  int32
	pointY  int32
	private uint32
}

func newPlatformHostBoundarySource() HostBoundarySource {
	return windowsHostBoundarySource{}
}

func (windowsHostBoundarySource) Run(ctx context.Context,
	emit func(HostBoundaryEvent),
	ready func(error),
) error {
	if ctx == nil || emit == nil || ready == nil {
		return ErrHostBoundaryMonitor
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	className, err := windows.UTF16PtrFromString(fmt.Sprintf(
		"PrayuHostBoundary_%d_%d", windows.GetCurrentProcessId(),
		time.Now().UnixNano()))
	if err != nil {
		return errors.Join(ErrHostBoundaryMonitor, err)
	}
	instance, _, instanceErr := hostBoundaryGetModule.Call(0)
	if instance == 0 {
		return errors.Join(ErrHostBoundaryMonitor, instanceErr)
	}
	windowClass := hostBoundaryWindowClass{
		size:       uint32(unsafe.Sizeof(hostBoundaryWindowClass{})),
		windowProc: hostBoundaryWndProc,
		instance:   windows.Handle(instance), className: className,
	}
	atom, _, registerErr := hostBoundaryRegisterClass.Call(
		uintptr(unsafe.Pointer(&windowClass)))
	if atom == 0 {
		return errors.Join(ErrHostBoundaryMonitor, registerErr)
	}
	defer hostBoundaryUnregister.Call(
		uintptr(unsafe.Pointer(className)), instance)

	window, _, createErr := hostBoundaryCreateWindow.Call(
		0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(className)),
		0, 0, 0, 0, 0, 0, 0, instance, 0)
	if window == 0 {
		return errors.Join(ErrHostBoundaryMonitor, createErr)
	}
	hostBoundaryWindows.Store(window, hostBoundaryWindowState{emit: emit})
	defer hostBoundaryWindows.Delete(window)
	defer hostBoundaryDestroyWindow.Call(window)

	registered, _, registerSessionErr := hostBoundaryWTSRegister.Call(
		window, hostBoundaryNotifyThisSession)
	if registered == 0 {
		return errors.Join(ErrHostBoundaryMonitor, registerSessionErr)
	}
	defer hostBoundaryWTSUnregister.Call(window)
	ready(nil)

	cancelDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			hostBoundaryPostMessage.Call(window, hostBoundaryWMClose, 0, 0)
		case <-cancelDone:
		}
	}()
	defer close(cancelDone)

	var message hostBoundaryMessage
	for {
		result, _, messageErr := hostBoundaryGetMessage.Call(
			uintptr(unsafe.Pointer(&message)), 0, 0, 0)
		switch int32(result) {
		case -1:
			return errors.Join(ErrHostBoundaryMonitor, messageErr)
		case 0:
			return ctx.Err()
		default:
			hostBoundaryTranslate.Call(uintptr(unsafe.Pointer(&message)))
			hostBoundaryDispatch.Call(uintptr(unsafe.Pointer(&message)))
		}
	}
}

func hostBoundaryWindowProcedure(window uintptr, message uint32,
	wParam uintptr, lParam uintptr,
) uintptr {
	stateValue, ok := hostBoundaryWindows.Load(window)
	if ok {
		state := stateValue.(hostBoundaryWindowState)
		switch message {
		case hostBoundaryWMTSSessionChange:
			switch wParam {
			case hostBoundaryWTSSessionLock:
				state.emit(HostBoundarySessionLocked)
			case hostBoundaryWTSSessionLogoff, hostBoundaryWTSSessionRemoteDC:
				state.emit(HostBoundarySessionDisconnected)
			}
		case hostBoundaryWMPowerBroadcast:
			switch wParam {
			case hostBoundaryPBTAPMSuspend, hostBoundaryPBTAPMStandby:
				state.emit(HostBoundarySystemSuspending)
			case hostBoundaryPBTAPMResumeCrit, hostBoundaryPBTAPMResume,
				hostBoundaryPBTAPMResumeAuto:
				state.emit(HostBoundarySystemResumed)
			}
		}
	}
	switch message {
	case hostBoundaryWMClose:
		hostBoundaryDestroyWindow.Call(window)
		return 0
	case hostBoundaryWMDestroy:
		hostBoundaryPostQuit.Call(0)
		return 0
	default:
		result, _, _ := hostBoundaryDefWindowProc.Call(
			window, uintptr(message), wParam, lParam)
		return result
	}
}
