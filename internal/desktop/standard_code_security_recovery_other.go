//go:build !windows

package desktop

import (
	"context"
	"errors"
)

type StandardCodeSecurityRecoveryWorkerConfig struct {
	Root    string
	CaseID  string
	Backend string
	Phase   string
}

func RunStandardCodeSecurityRecoveryWorker(context.Context,
	StandardCodeSecurityRecoveryWorkerConfig,
) error {
	return errors.New("packaged Standard Code recovery worker requires Windows")
}
