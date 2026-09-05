//go:build windows

package browserruntime

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

type fakeWindowsBrowserStartCleanup struct {
	terminateJobErrors     []error
	terminateProcessErrors []error
	jobProofs              []bool
	processProofs          []bool
	terminateJobCalls      int
	terminateProcessCalls  int
	jobProofCalls          int
	processProofCalls      int
	pauses                 int
	pauseForDelay          bool
}

func (cleanup *fakeWindowsBrowserStartCleanup) TerminateJob(windows.Handle) error {
	index := cleanup.terminateJobCalls
	cleanup.terminateJobCalls++
	if index < len(cleanup.terminateJobErrors) {
		return cleanup.terminateJobErrors[index]
	}
	return nil
}

func (cleanup *fakeWindowsBrowserStartCleanup) TerminateProcess(windows.Handle) error {
	index := cleanup.terminateProcessCalls
	cleanup.terminateProcessCalls++
	if index < len(cleanup.terminateProcessErrors) {
		return cleanup.terminateProcessErrors[index]
	}
	return nil
}

func (cleanup *fakeWindowsBrowserStartCleanup) ProcessReaped(windows.Handle,
	time.Duration,
) bool {
	index := cleanup.processProofCalls
	cleanup.processProofCalls++
	return index < len(cleanup.processProofs) && cleanup.processProofs[index]
}

func (cleanup *fakeWindowsBrowserStartCleanup) JobReaped(windows.Handle,
	time.Duration,
) bool {
	index := cleanup.jobProofCalls
	cleanup.jobProofCalls++
	return index < len(cleanup.jobProofs) && cleanup.jobProofs[index]
}

func (cleanup *fakeWindowsBrowserStartCleanup) Pause(time.Duration) {
	cleanup.pauses++
	if cleanup.pauseForDelay {
		time.Sleep(25 * time.Millisecond)
	}
}

func TestFailedWindowsBrowserStartRetainsOwnersUntilBothProofsExist(t *testing.T) {
	cleanup := &fakeWindowsBrowserStartCleanup{
		terminateJobErrors:     []error{errors.New("transient terminate Job failure")},
		terminateProcessErrors: []error{errors.New("transient terminate process failure")},
		jobProofs:              []bool{false, true},
		processProofs:          []bool{false, false, true},
	}

	if err := reapFailedWindowsBrowserStart(context.Background(), cleanup,
		windows.Handle(1), windows.Handle(2)); err != nil {
		t.Fatal(err)
	}

	if cleanup.jobProofCalls != 2 || cleanup.processProofCalls != 3 {
		t.Fatalf("proof calls = job %d, process %d; want job 2, process 3",
			cleanup.jobProofCalls, cleanup.processProofCalls)
	}
	if cleanup.terminateJobCalls != 2 || cleanup.terminateProcessCalls != 3 {
		t.Fatalf("termination calls = job %d, process %d; want job 2, process 3",
			cleanup.terminateJobCalls, cleanup.terminateProcessCalls)
	}
	if cleanup.pauses != 2 {
		t.Fatalf("cleanup pauses = %d, want 2", cleanup.pauses)
	}
}

func TestFailedWindowsBrowserStartDoesNotRecheckEstablishedProof(t *testing.T) {
	cleanup := &fakeWindowsBrowserStartCleanup{
		jobProofs:     []bool{true},
		processProofs: []bool{false, false, true},
	}

	if err := reapFailedWindowsBrowserStart(context.Background(), cleanup,
		windows.Handle(1), windows.Handle(2)); err != nil {
		t.Fatal(err)
	}

	if cleanup.jobProofCalls != 1 || cleanup.terminateJobCalls != 1 {
		t.Fatalf("established Job proof was retried: proof=%d terminate=%d",
			cleanup.jobProofCalls, cleanup.terminateJobCalls)
	}
	if cleanup.processProofCalls != 3 || cleanup.terminateProcessCalls != 3 {
		t.Fatalf("process proof calls = %d and terminate calls = %d, want 3 and 3",
			cleanup.processProofCalls, cleanup.terminateProcessCalls)
	}
}

func TestFailedWindowsBrowserStartRejectsMissingOwnerHandles(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("cleanup accepted a missing process owner")
		}
	}()
	_ = reapFailedWindowsBrowserStart(context.Background(),
		&fakeWindowsBrowserStartCleanup{},
		windows.Handle(1), 0)
}

func TestFailedWindowsBrowserStartCleanupHasDeadline(t *testing.T) {
	cleanup := &fakeWindowsBrowserStartCleanup{pauseForDelay: true}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	startedAt := time.Now()
	err := reapFailedWindowsBrowserStart(ctx, cleanup, windows.Handle(1),
		windows.Handle(2))
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("bounded cleanup error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(startedAt); elapsed > time.Second {
		t.Fatalf("failed-start cleanup exceeded its bound: %s", elapsed)
	}
	if cleanup.terminateJobCalls == 0 || cleanup.terminateProcessCalls == 0 ||
		cleanup.jobProofCalls == 0 || cleanup.processProofCalls == 0 {
		t.Fatalf("bounded cleanup did not attempt every proof: %+v", cleanup)
	}
}

func TestRestrictedBrowserEnvironmentUsesKnownFoldersWithoutProcessSecrets(
	t *testing.T,
) {
	profilePath := filepath.Join(`C:\evidence`, "profile")
	systemRoot := `C:\Windows`
	values, err := restrictedBrowserEnvironmentValues(profilePath, systemRoot,
		[]string{
			`USERPROFILE=C:\Users\fixture`,
			`LOCALAPPDATA=C:\Users\fixture\AppData\Local`,
			`APPDATA=C:\Users\fixture\AppData\Roaming`,
			`USERNAME=fixture`,
			`USERDOMAIN=WORKSTATION`,
			`HOMEDRIVE=C:`,
			`HOMEPATH=\Users\fixture`,
			`ProgramData=C:\ProgramData`,
			`Path=C:\untrusted-bin`,
			`PATHEXT=.UNTRUSTED`,
			`SystemRoot=C:\tampered`,
			`SECRET_TOKEN=must-not-reach-browser`,
		})
	if err != nil {
		t.Fatal(err)
	}
	environment := make(map[string]string, len(values))
	for index, entry := range values {
		if index > 0 && strings.ToLower(values[index-1]) > strings.ToLower(entry) {
			t.Fatal("browser environment block is not sorted case-insensitively")
		}
		separator := strings.IndexByte(entry, '=')
		if separator <= 0 {
			t.Fatalf("invalid browser environment entry %q", entry)
		}
		environment[entry[:separator]] = entry[separator+1:]
	}
	if _, present := environment["SECRET_TOKEN"]; present {
		t.Fatal("process secret escaped into the browser environment")
	}
	for name, want := range map[string]string{
		"APPDATA":      `C:\Users\fixture\AppData\Roaming`,
		"LOCALAPPDATA": `C:\Users\fixture\AppData\Local`,
		"USERPROFILE":  `C:\Users\fixture`,
		"HOME":         profilePath,
		"TEMP":         filepath.Join(profilePath, "Temp"),
		"TMP":          filepath.Join(profilePath, "Temp"),
		"SYSTEMROOT":   systemRoot,
		"WINDIR":       systemRoot,
		"PATH":         filepath.Join(systemRoot, "System32") + ";" + systemRoot,
		"PATHEXT":      ".COM;.EXE;.BAT;.CMD",
	} {
		if got := environment[name]; got != want {
			t.Fatalf("browser environment %s=%q, want %q", name, got, want)
		}
	}
}

func TestRestrictedBrowserEnvironmentRequiresTokenKnownFolders(t *testing.T) {
	_, err := restrictedBrowserEnvironmentValues(`C:\evidence\profile`,
		`C:\Windows`, []string{
			`USERPROFILE=C:\Users\fixture`,
			`APPDATA=C:\Users\fixture\AppData\Roaming`,
		})
	if err == nil {
		t.Fatal("browser environment accepted a missing LOCALAPPDATA known folder")
	}
}

func TestRestrictedBrowserEnvironmentRejectsDuplicateStructuralVariable(t *testing.T) {
	_, err := restrictedBrowserEnvironmentValues(`C:\evidence\profile`,
		`C:\Windows`, []string{
			`USERPROFILE=C:\Users\fixture`,
			`LOCALAPPDATA=C:\Users\fixture\AppData\Local`,
			`APPDATA=C:\Users\fixture\AppData\Roaming`,
			`appdata=C:\duplicate`,
		})
	if err == nil {
		t.Fatal("browser environment accepted a duplicate structural variable")
	}
}
