package sandbox

import "sync"

// sbxNamespaceLock excludes cooperating product writers across journal roots,
// installations and login sessions belonging to the same host OS account.
// Read-only readiness probes do not need this lock. Keep it for the backend's
// entire mutating lifetime, including independent cancellation cleanup and
// startup recovery, and release it only after Close finishes recovery.
type sbxNamespaceLock struct {
	close func() error
	once  sync.Once
	err   error
}

func (lock *sbxNamespaceLock) Close() error {
	if lock == nil {
		return nil
	}
	lock.once.Do(func() { lock.err = lock.close() })
	return lock.err
}

func sbxAcquireNamespaceLock() (*sbxNamespaceLock, error) {
	return sbxAcquireNamespaceNamedLock(SBXAppName)
}
