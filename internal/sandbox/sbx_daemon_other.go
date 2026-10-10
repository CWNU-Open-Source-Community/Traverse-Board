//go:build !windows

package sandbox

import "context"

// The accepted local API socket binding has currently been established only
// for Windows. Do not guess a user/environment-controlled Unix socket path;
// another platform needs its own verified fixed namespace endpoint contract.
type sbxUnsupportedDaemonTransport struct{}

func newSBXDaemonTransport() SBXDaemonTransport                   { return sbxUnsupportedDaemonTransport{} }
func (sbxUnsupportedDaemonTransport) Check(context.Context) error { return ErrSBXUnavailable }
func (sbxUnsupportedDaemonTransport) Inventory(context.Context) ([]byte, error) {
	return nil, ErrSBXUnavailable
}
func (sbxUnsupportedDaemonTransport) IsolationSetting(context.Context, string) ([]byte, error) {
	return nil, ErrSBXUnavailable
}
func (sbxUnsupportedDaemonTransport) Remove(context.Context, string, string) error {
	return ErrSBXCleanup
}

func (sbxUnsupportedDaemonTransport) VerifyHelper(context.Context, string, string, string) error {
	return ErrSBXOwnership
}
