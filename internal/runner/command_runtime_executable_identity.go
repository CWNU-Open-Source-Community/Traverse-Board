package runner

import "cyberagent-workbench/internal/commandruntimeadapter"

// The immutable adapter family distinguishes a native binary digest from a
// pinned guest-template/path binding. Historical jobs retain the empty kind.
func CommandRuntimeExecutableIdentityKind(adapter commandruntimeadapter.Identity) string {
	if adapter.Kind == commandruntimeadapter.KindSandboxedWorkspace && adapter.Backend == "docker_sandboxes" {
		return CommandRuntimeExecutableTemplatePathSHA256
	}
	return ""
}
