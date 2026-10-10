//go:build !windows

package application

import "cyberagent-workbench/internal/runner"

func fixedOperatorNativeProcessSample(runner.CommandRuntimeJob) string { return "" }
