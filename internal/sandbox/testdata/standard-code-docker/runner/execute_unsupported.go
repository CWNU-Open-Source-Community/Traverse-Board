//go:build !linux

package main

import "errors"

func executeTool(_ string, _, _ []string, _ string,
	_ executionLimits, _ bool,
) (int, error) {
	return runnerFailureExitCode, errors.New("fixed Standard Code runner requires Linux")
}
