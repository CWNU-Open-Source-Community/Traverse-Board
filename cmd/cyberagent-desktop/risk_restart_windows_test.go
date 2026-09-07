//go:build windows && desktop && wv2runtime.error

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsRiskRestartRejectsAParentOtherThanItsCreator(t *testing.T) {
	if _, err := prepareRiskRestartParent(os.Getpid()); err == nil ||
		!strings.Contains(err.Error(), "parent identity is invalid") {
		t.Fatalf("unrelated restart parent error = %v", err)
	}
}

func TestWindowsRiskRestartExecutableIdentityIsCanonicalAndCaseInsensitive(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	absolute, err := filepath.Abs(executable)
	if err != nil {
		t.Fatal(err)
	}
	if !sameWindowsExecutable(absolute, strings.ToUpper(absolute)) {
		t.Fatal("the same Windows executable was not recognized")
	}
	if sameWindowsExecutable(absolute, filepath.Join(filepath.Dir(absolute), "other.exe")) {
		t.Fatal("a different Windows executable was accepted as the restart parent")
	}
}
