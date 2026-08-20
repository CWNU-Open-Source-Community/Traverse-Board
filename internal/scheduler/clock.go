package scheduler

import "time"

// Clock keeps wall-clock decisions deterministic in tests. Persisted schedule
// instants are UTC; a timezone is display/validation metadata and never changes
// the identity of an occurrence during a DST transition.
type Clock interface {
	Now() time.Time
	NewTimer(time.Duration) Timer
}

type Timer interface {
	C() <-chan time.Time
	Reset(time.Duration) bool
	Stop() bool
}

type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now().UTC() }

func (RealClock) NewTimer(duration time.Duration) Timer {
	return realTimer{timer: time.NewTimer(duration)}
}

type realTimer struct{ timer *time.Timer }

func (t realTimer) C() <-chan time.Time        { return t.timer.C }
func (t realTimer) Reset(d time.Duration) bool { return t.timer.Reset(d) }
func (t realTimer) Stop() bool                 { return t.timer.Stop() }
