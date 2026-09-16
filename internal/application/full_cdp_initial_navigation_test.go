package application

import (
	"context"
	"testing"

	"cyberagent-workbench/internal/browserruntime"
)

func TestFullCDPOpenPreservesInitialTargetPathAndReplayDoesNotNavigateAgain(t *testing.T) {
	service, store, launches, _ := newFullCDPProductionServiceFixture(t)
	t.Cleanup(func() {
		if err := service.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	request := fullCDPOpenFixture(service, store, "explicit-initial-path")
	request.Target = "http://127.0.0.1:18080/nested/app?view=preview"
	launch := service.launch
	service.launch = func(ctx context.Context, candidate browserruntime.FullCDPManagedLaunchRequest) (managedFullCDPRuntime, error) {
		if candidate.InitialURL != request.Target {
			t.Fatalf("target path was lost at runtime assembly: %q", candidate.InitialURL)
		}
		return launch(ctx, candidate)
	}
	opened, err := service.OpenFullCDPSession(t.Context(), request)
	if err != nil || opened.Session.State != FullCDPSessionReady || *launches != 1 {
		t.Fatalf("open=%+v launches=%d err=%v", opened, *launches, err)
	}
	replayed, err := service.OpenFullCDPSession(t.Context(), request)
	if err != nil || !replayed.Replayed || *launches != 1 || replayed.Session.SessionID != opened.Session.SessionID {
		t.Fatalf("open replay renavigated or changed the session: result=%+v launches=%d err=%v", replayed, *launches, err)
	}
}
