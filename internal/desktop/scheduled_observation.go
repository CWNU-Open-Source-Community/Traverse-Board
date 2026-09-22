package desktop

import (
	"context"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/scheduler"
)

// The ordinary desktop worker only observes explicitly confirmed zero-model
// jobs. The service applies this restriction before both reconciliation and
// claiming, so opening the app cannot activate legacy scheduled work.
type scheduledObservationRunner struct {
	service *application.ScheduledJobService
}

func (r scheduledObservationRunner) RunDue(ctx context.Context, owner string, now time.Time) (bool, error) {
	return r.service.RunDueObservation(ctx, owner, now)
}

type scheduledDesktopWorkerHealth struct {
	*scheduler.Worker
	observationOnly bool
}

func (h scheduledDesktopWorkerHealth) SelectionScope() string {
	if h.observationOnly {
		return "confirmed_read_only"
	}
	return "all_jobs"
}
