//go:build linux

package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	workspaceEntryPollInterval = 100 * time.Millisecond
	workspaceUsagePollInterval = time.Second
)

func executeTool(executable string, arguments, environment []string,
	workingDirectory string, limits executionLimits,
) (int, error) {
	ledger, err := captureWorkspaceLedger(workspaceRoot, workspaceInitialEntries)
	if err != nil {
		return runnerFailureExitCode, errors.New("workspace resource baseline is unavailable")
	}
	baseline := ledger.usage
	watcher, err := newWorkspaceWatcher(workspaceRoot, workspaceInitialEntries)
	if err != nil {
		return runnerFailureExitCode, errors.New("workspace resource monitor is unavailable")
	}
	if err := requireWorkspaceFreeResources(workspaceRoot, limits); err != nil {
		watcher.close()
		return runnerFailureExitCode, err
	}
	if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &syscall.Rlimit{
		Cur: uint64(limits.FileBytes), Max: uint64(limits.FileBytes),
	}); err != nil {
		watcher.close()
		return runnerFailureExitCode, errors.New("workspace file limit is unavailable")
	}
	command := exec.Command(executable, arguments[1:]...)
	command.Args = append([]string(nil), arguments...)
	command.Env = append([]string(nil), environment...)
	command.Dir = workingDirectory
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	monitorStop := make(chan struct{})
	monitorResult := make(chan error, 1)
	monitorReady := make(chan struct{})
	monitorDone := make(chan struct{})
	go monitorWorkspaceGrowth(monitorStop, monitorResult, monitorReady, monitorDone,
		watcher, ledger, baseline, limits)
	<-monitorReady
	var stopOnce sync.Once
	stopMonitor := func() { stopOnce.Do(func() { close(monitorStop) }) }
	defer func() {
		stopMonitor()
		<-monitorDone
	}()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	if err := command.Start(); err != nil {
		return runnerFailureExitCode, err
	}
	waited := make(chan error, 1)
	go func() { waited <- command.Wait() }()

	for {
		select {
		case waitErr := <-waited:
			stopMonitor()
			<-monitorDone
			_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
			select {
			case monitorErr := <-monitorResult:
				return runnerFailureExitCode, monitorErr
			default:
			}
			current, usageErr := captureWorkspaceUsage(workspaceRoot,
				baseline.Entries+limits.GrowthEntries+1)
			if usageErr != nil || workspaceGrowthExceeded(baseline, current, limits) {
				return runnerFailureExitCode,
					errors.New("workspace resource limit exceeded")
			}
			return processExitCode(waitErr)
		case monitorErr := <-monitorResult:
			waitErr := terminateTool(command.Process.Pid, waited, syscall.SIGKILL)
			if monitorErr == nil {
				monitorErr = errors.New("workspace resource monitor stopped")
			}
			_ = waitErr
			return runnerFailureExitCode, monitorErr
		case received := <-signals:
			sig, ok := received.(syscall.Signal)
			if !ok {
				sig = syscall.SIGTERM
			}
			waitErr := terminateTool(command.Process.Pid, waited, sig)
			return processExitCode(waitErr)
		}
	}
}

func monitorWorkspaceGrowth(stop <-chan struct{}, result chan<- error,
	ready chan<- struct{}, done chan<- struct{}, watcher *workspaceWatcher,
	ledger *workspaceLedger, baseline workspaceUsage, limits executionLimits,
) {
	defer close(done)
	defer watcher.close()
	events := make(chan workspaceEvent, 1_024)
	failures := make(chan error, 1)
	readerReady := make(chan struct{})
	go watcher.read(events, failures, readerReady, baseline.Entries,
		limits.GrowthEntries)
	go scanWorkspaceUsage(stop, failures, baseline, limits)
	<-readerReady
	close(ready)
	entryTicker := time.NewTicker(workspaceEntryPollInterval)
	defer entryTicker.Stop()
	for {
		select {
		case <-failures:
			result <- errors.New("workspace resource monitor is unavailable")
			return
		default:
		}
		select {
		case <-stop:
			return
		case event := <-events:
			if event.mask&syscall.IN_ISDIR != 0 &&
				event.mask&(syscall.IN_CREATE|syscall.IN_MOVED_TO) != 0 {
				// Watch the directory before scanning its existing contents so
				// concurrent children cannot appear between the scan and watch.
				if err := watcher.addDirectories(event.path,
					baseline.Entries+limits.GrowthEntries+1); err != nil {
					result <- errors.New("workspace resource monitor is unavailable")
					return
				}
			}
			directory, err := ledger.apply(event,
				baseline.Entries+limits.GrowthEntries+1)
			if err != nil {
				result <- errors.New("workspace resource monitor is unavailable")
				return
			}
			if directory && event.mask&syscall.IN_ISDIR == 0 &&
				event.mask&(syscall.IN_CREATE|syscall.IN_MOVED_TO) != 0 {
				if err := watcher.addDirectories(event.path,
					baseline.Entries+limits.GrowthEntries+1); err != nil {
					result <- errors.New("workspace resource monitor is unavailable")
					return
				}
			}
			if workspaceGrowthExceeded(baseline, ledger.usage, limits) {
				result <- errors.New("workspace resource limit exceeded")
				return
			}
		case <-entryTicker.C:
			exceeded, err := workspaceEntryGrowthExceeded(workspaceRoot,
				baseline.Entries, limits.GrowthEntries)
			if err != nil || exceeded {
				result <- errors.New("workspace resource limit exceeded")
				return
			}
		case <-failures:
			result <- errors.New("workspace resource monitor is unavailable")
			return
		}
	}
}

func scanWorkspaceUsage(stop <-chan struct{}, failures chan<- error,
	baseline workspaceUsage, limits executionLimits,
) {
	ticker := time.NewTicker(workspaceUsagePollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			current, err := captureWorkspaceUsage(workspaceRoot,
				baseline.Entries+limits.GrowthEntries+1)
			if err != nil || workspaceGrowthExceeded(baseline, current, limits) {
				reportWorkspaceMonitorFailure(failures,
					errors.New("workspace resource limit exceeded"))
				return
			}
		}
	}
}

func workspaceEntryGrowthExceeded(root string, baselineEntries,
	maximumGrowthEntries int64,
) (bool, error) {
	if baselineEntries < 0 || maximumGrowthEntries < 0 ||
		baselineEntries > math.MaxInt64-maximumGrowthEntries {
		return false, errors.New("workspace entry accounting overflowed")
	}
	maximumEntries := baselineEntries + maximumGrowthEntries
	var entries int64
	limitReached := errors.New("workspace entry growth exceeded")
	err := filepath.WalkDir(root, func(current string, entry os.DirEntry,
		walkErr error,
	) error {
		if walkErr != nil {
			return walkErr
		}
		if current == root {
			return nil
		}
		entries++
		if entries > maximumEntries {
			return limitReached
		}
		return nil
	})
	if errors.Is(err, limitReached) {
		return true, nil
	}
	return false, err
}

func reportWorkspaceMonitorFailure(failures chan<- error, err error) {
	select {
	case failures <- err:
	default:
	}
}

type workspaceEntry struct {
	size      int64
	directory bool
}

type workspaceLedger struct {
	root    string
	entries map[string]workspaceEntry
	usage   workspaceUsage
}

func captureWorkspaceLedger(root string, maximumEntries int64) (*workspaceLedger, error) {
	value := &workspaceLedger{root: root, entries: make(map[string]workspaceEntry)}
	if err := value.refreshTree(root, maximumEntries); err != nil {
		return nil, err
	}
	return value, nil
}

func (ledger *workspaceLedger) apply(event workspaceEvent,
	maximumEntries int64,
) (bool, error) {
	if !pathInsideWorkspace(ledger.root, event.path) {
		return false, errors.New("workspace event escaped its root")
	}
	directory := event.mask&syscall.IN_ISDIR != 0
	if event.mask&(syscall.IN_DELETE|syscall.IN_DELETE_SELF|syscall.IN_MOVED_FROM|
		syscall.IN_MOVE_SELF) != 0 {
		ledger.removeTree(event.path)
	}
	if event.mask&(syscall.IN_ATTRIB|syscall.IN_CLOSE_WRITE|syscall.IN_CREATE|
		syscall.IN_MODIFY|syscall.IN_MOVED_TO) == 0 {
		return directory, nil
	}
	info, err := os.Lstat(event.path)
	if errors.Is(err, os.ErrNotExist) {
		ledger.removeTree(event.path)
		return directory, nil
	}
	if err != nil {
		return directory, err
	}
	directory = info.IsDir() && info.Mode()&os.ModeSymlink == 0
	if directory && event.mask&(syscall.IN_CREATE|syscall.IN_MOVED_TO) != 0 {
		return true, ledger.refreshTree(event.path, maximumEntries)
	}
	return directory, ledger.record(event.path, info, maximumEntries)
}

func (ledger *workspaceLedger) refreshTree(root string, maximumEntries int64) error {
	return filepath.WalkDir(root, func(current string, entry os.DirEntry,
		walkErr error,
	) error {
		if walkErr != nil {
			return walkErr
		}
		if current == ledger.root {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		return ledger.record(current, info, maximumEntries)
	})
}

func (ledger *workspaceLedger) record(path string, info os.FileInfo,
	maximumEntries int64,
) error {
	entry := workspaceEntry{directory: info.IsDir() && info.Mode()&os.ModeSymlink == 0}
	if info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		entry.size = info.Size()
	}
	if entry.size < 0 {
		return errors.New("workspace entry size is invalid")
	}
	if previous, exists := ledger.entries[path]; exists {
		ledger.usage.Bytes -= previous.size
	} else {
		ledger.usage.Entries++
	}
	if ledger.usage.Entries > maximumEntries ||
		entry.size > math.MaxInt64-ledger.usage.Bytes {
		return errors.New("workspace ledger exceeded its accounting bound")
	}
	ledger.entries[path] = entry
	ledger.usage.Bytes += entry.size
	return nil
}

func (ledger *workspaceLedger) removeTree(root string) {
	prefix := root + string(filepath.Separator)
	for path, entry := range ledger.entries {
		if path != root && !strings.HasPrefix(path, prefix) {
			continue
		}
		ledger.usage.Bytes -= entry.size
		ledger.usage.Entries--
		delete(ledger.entries, path)
	}
}

func pathInsideWorkspace(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." &&
		!filepath.IsAbs(relative) &&
		(relative == "." || !bytes.HasPrefix([]byte(relative),
			[]byte(".."+string(filepath.Separator))))
}

type workspaceEvent struct {
	path string
	mask uint32
}

type workspaceWatcher struct {
	fd           int
	closeOnce    sync.Once
	mu           sync.RWMutex
	byPath       map[string]int
	byDescriptor map[int]string
}

func newWorkspaceWatcher(root string, maximumEntries int64) (*workspaceWatcher, error) {
	fd, err := syscall.InotifyInit1(syscall.IN_CLOEXEC)
	if err != nil {
		return nil, err
	}
	value := &workspaceWatcher{fd: fd, byPath: make(map[string]int),
		byDescriptor: make(map[int]string)}
	if err := value.addDirectories(root, maximumEntries); err != nil {
		value.close()
		return nil, err
	}
	return value, nil
}

func (watcher *workspaceWatcher) addDirectories(root string,
	maximumEntries int64,
) error {
	var entries int64
	return filepath.WalkDir(root, func(current string, entry os.DirEntry,
		walkErr error,
	) error {
		if walkErr != nil {
			return walkErr
		}
		entries++
		if entries > maximumEntries {
			return errors.New("workspace watch entry limit exceeded")
		}
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		watcher.mu.RLock()
		_, exists := watcher.byPath[current]
		watcher.mu.RUnlock()
		if exists {
			return nil
		}
		mask := uint32(syscall.IN_ATTRIB | syscall.IN_CLOSE_WRITE | syscall.IN_CREATE |
			syscall.IN_DELETE | syscall.IN_DELETE_SELF | syscall.IN_MODIFY |
			syscall.IN_MOVE_SELF | syscall.IN_MOVED_FROM | syscall.IN_MOVED_TO)
		descriptor, err := syscall.InotifyAddWatch(watcher.fd, current, mask)
		if err != nil {
			return err
		}
		watcher.mu.Lock()
		watcher.byPath[current] = descriptor
		watcher.byDescriptor[descriptor] = current
		watcher.mu.Unlock()
		return nil
	})
}

func (watcher *workspaceWatcher) read(events chan<- workspaceEvent,
	failures chan<- error, ready chan<- struct{}, baselineEntries,
	maximumGrowthEntries int64,
) {
	currentEntries := baselineEntries
	maximumEntries := baselineEntries + maximumGrowthEntries
	buffer := make([]byte, 64*1024)
	close(ready)
	for {
		count, err := syscall.Read(watcher.fd, buffer)
		if err != nil {
			reportWorkspaceMonitorFailure(failures, err)
			return
		}
		for offset := 0; offset+16 <= count; {
			descriptor := int(int32(binary.LittleEndian.Uint32(buffer[offset : offset+4])))
			mask := binary.LittleEndian.Uint32(buffer[offset+4 : offset+8])
			nameBytes := int(binary.LittleEndian.Uint32(buffer[offset+12 : offset+16]))
			if mask&syscall.IN_Q_OVERFLOW != 0 || nameBytes < 0 ||
				offset+16+nameBytes > count {
				reportWorkspaceMonitorFailure(failures,
					errors.New("workspace event queue overflowed"))
				return
			}
			created := mask&(syscall.IN_CREATE|syscall.IN_MOVED_TO) != 0
			removed := mask&(syscall.IN_DELETE|syscall.IN_MOVED_FROM) != 0
			if created != removed {
				if created {
					if currentEntries == math.MaxInt64 {
						reportWorkspaceMonitorFailure(failures,
							errors.New("workspace entry accounting overflowed"))
						return
					}
					currentEntries++
				} else if currentEntries > 0 {
					currentEntries--
				}
			}
			if currentEntries > maximumEntries {
				reportWorkspaceMonitorFailure(failures,
					errors.New("workspace entry growth exceeded"))
				return
			}
			watcher.mu.Lock()
			parent, exists := watcher.byDescriptor[descriptor]
			if exists && mask&syscall.IN_IGNORED != 0 {
				delete(watcher.byDescriptor, descriptor)
				if watcher.byPath[parent] == descriptor {
					delete(watcher.byPath, parent)
				}
			}
			watcher.mu.Unlock()
			if exists && mask&syscall.IN_IGNORED == 0 {
				name := string(bytes.TrimRight(buffer[offset+16:offset+16+nameBytes], "\x00"))
				path := parent
				if name != "" {
					path = filepath.Join(parent, name)
				}
				select {
				case events <- workspaceEvent{path: path, mask: mask}:
				default:
					reportWorkspaceMonitorFailure(failures,
						errors.New("workspace event consumer fell behind"))
					return
				}
			}
			offset += 16 + nameBytes
		}
	}
}

func (watcher *workspaceWatcher) close() {
	if watcher != nil {
		watcher.closeOnce.Do(func() { _ = syscall.Close(watcher.fd) })
	}
}

func requireWorkspaceFreeResources(root string, limits executionLimits) error {
	var state syscall.Statfs_t
	if err := syscall.Statfs(root, &state); err != nil || state.Bsize <= 0 {
		return errors.New("workspace free-space accounting is unavailable")
	}
	availableBlocks := uint64(state.Bavail)
	blockBytes := uint64(state.Bsize)
	if availableBlocks != 0 && blockBytes > ^uint64(0)/availableBlocks {
		return errors.New("workspace free-space accounting overflowed")
	}
	if availableBlocks*blockBytes < uint64(limits.FreeBytes) ||
		uint64(state.Ffree) < uint64(limits.FreeEntries) {
		return errors.New("workspace free-space reserve is unavailable")
	}
	return nil
}

func terminateTool(processID int, waited <-chan error,
	requested syscall.Signal,
) error {
	_ = syscall.Kill(-processID, requested)
	select {
	case err := <-waited:
		return err
	case <-time.After(time.Second):
		_ = syscall.Kill(-processID, syscall.SIGKILL)
		return <-waited
	}
}

func processExitCode(err error) (int, error) {
	if err == nil {
		return 0, nil
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return runnerFailureExitCode, err
	}
	if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return 128 + int(status.Signal()), nil
	}
	code := exitErr.ExitCode()
	if code < 0 || code > 255 {
		return runnerFailureExitCode, errors.New("tool exit status is invalid")
	}
	return code, nil
}
