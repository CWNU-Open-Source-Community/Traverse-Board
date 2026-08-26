//go:build !windows

package desktop

import (
	"context"
	"errors"

	"cyberagent-workbench/internal/packagede2e"
)

type unavailableStandardCodeSecurityDriver struct{}

func NewStandardCodeSecurityDriver(string) packagede2e.SecurityMatrixDriver {
	return unavailableStandardCodeSecurityDriver{}
}

func (unavailableStandardCodeSecurityDriver) Open(context.Context,
	packagede2e.SecurityDriverConfig,
) ([]packagede2e.SecurityBackendEvidence, error) {
	return nil, errors.New("packaged Standard Code security execution requires Windows")
}

func (unavailableStandardCodeSecurityDriver) Execute(context.Context,
	packagede2e.SecurityDriverCase,
) (packagede2e.SecurityCaseBackendEvidence, error) {
	return packagede2e.SecurityCaseBackendEvidence{},
		errors.New("packaged Standard Code security execution requires Windows")
}

func (unavailableStandardCodeSecurityDriver) Close(context.Context) (
	packagede2e.SecurityCleanupEvidence, error,
) {
	return packagede2e.SecurityCleanupEvidence{},
		errors.New("packaged Standard Code security execution requires Windows")
}
