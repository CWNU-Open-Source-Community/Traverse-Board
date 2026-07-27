//go:build windows

package terminal

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestWindowsHostBoundaryMonitorOptIn(t *testing.T) {
	if os.Getenv("CYBERAGENT_TEST_WINDOWS_HOST_BOUNDARY") != "1" {
		t.Skip("set CYBERAGENT_TEST_WINDOWS_HOST_BOUNDARY=1 for native smoke test")
	}
	manager, err := NewManager(&terminalBackendStub{}, &hostBoundaryRevokerStub{})
	if err != nil {
		t.Fatal(err)
	}
	monitor, err := NewPlatformHostBoundaryMonitor(manager)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := monitor.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := monitor.Stop(); err != nil {
		t.Fatal(err)
	}
}
