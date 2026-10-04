//go:build !windows

package runner

func newFixedCommandRuntimeStarter(*fixedCommandRuntime) (commandRuntimeStarter, CommandRuntimeSpec, error) {
	return nil, CommandRuntimeSpec{}, ErrControlledExecutionPlatform
}

func (*fixedCommandRuntime) normalize(CommandRuntimeSpec, string) (CommandRuntimeResolvedSpec, error) {
	return CommandRuntimeResolvedSpec{}, ErrControlledExecutionPlatform
}
