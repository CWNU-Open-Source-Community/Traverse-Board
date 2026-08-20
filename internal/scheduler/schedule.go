package scheduler

import (
	"errors"
	"time"

	"cyberagent-workbench/internal/domain"
)

// NextOccurrence returns the first schedule instant strictly after `after`.
// Periodic schedules use elapsed UTC seconds anchored at one immutable instant;
// therefore DST gaps/folds cannot duplicate or erase an occurrence identity.
func NextOccurrence(schedule domain.ScheduledJobSchedule, after time.Time) (time.Time, error) {
	if err := schedule.Validate(); err != nil {
		return time.Time{}, err
	}
	after = after.UTC()
	anchor := schedule.AnchorAt.UTC()
	if after.Before(anchor) {
		return anchor, nil
	}
	if schedule.Kind == domain.ScheduledJobOnce {
		return time.Time{}, nil
	}
	interval := time.Duration(schedule.IntervalSeconds) * time.Second
	elapsed := after.Sub(anchor)
	steps := elapsed/interval + 1
	if steps <= 0 || steps > time.Duration(1<<62)/interval {
		return time.Time{}, errors.New("scheduled job occurrence exceeds supported time range")
	}
	return anchor.Add(steps * interval), nil
}

// MissedPeriodicOccurrence reports whether a periodic job slept through at
// least one complete interval after the persisted due instant. One-time jobs
// are never silently discarded solely because the process woke late.
func MissedPeriodicOccurrence(schedule domain.ScheduledJobSchedule,
	due time.Time, now time.Time,
) bool {
	if schedule.Kind != domain.ScheduledJobPeriodic ||
		schedule.IntervalSeconds < domain.MinScheduledJobIntervalSeconds {
		return false
	}
	return !now.UTC().Before(due.UTC().Add(
		time.Duration(schedule.IntervalSeconds) * time.Second))
}

func RetryBackoff(policy domain.ScheduledJobRetryPolicy, attempt int) time.Duration {
	if policy.Validate() != nil || attempt < 1 {
		return 0
	}
	seconds := int64(policy.InitialBackoffSeconds)
	for index := 1; index < attempt && seconds < int64(policy.MaxBackoffSeconds); index++ {
		seconds *= 2
		if seconds > int64(policy.MaxBackoffSeconds) {
			seconds = int64(policy.MaxBackoffSeconds)
		}
	}
	return time.Duration(seconds) * time.Second
}

// IdleBackoff bounds unchanged-state polling without altering the durable
// periodic anchor. The store may postpone the next check to this delay, but it
// never advances beyond the next canonical occurrence for a one-time job.
func IdleBackoff(interval time.Duration, unchanged int) time.Duration {
	if interval < time.Second {
		interval = time.Second
	}
	if unchanged < 1 {
		return interval
	}
	delay := interval
	for index := 1; index < unchanged && delay < 15*time.Minute; index++ {
		delay *= 2
		if delay > 15*time.Minute {
			delay = 15 * time.Minute
		}
	}
	return delay
}
