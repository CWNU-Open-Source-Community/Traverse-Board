package commandruntimeadapter

import (
	"testing"

	"cyberagent-workbench/internal/domain"
)

func TestAdapterPermissionMatrixFailsClosed(t *testing.T) {
	sandboxed := SandboxedWorkspace("local_windows_lpac", "local-windows-lpac.v1", "local-generation-1")
	host := HostUnsandboxed("host-generation-1")
	legacy := LegacyUnbound()
	for name, test := range map[string]struct {
		identity   Identity
		permission domain.RunExecutionPermissionMode
		allowed    bool
		executable bool
	}{
		"sandbox workspace":   {sandboxed, domain.RunExecutionPermissionWorkspaceAccess, true, true},
		"sandbox full access": {sandboxed, domain.RunExecutionPermissionFullAccess, false, true},
		"sandbox debug":       {sandboxed, domain.RunExecutionPermissionDebug, false, true},
		"host full access":    {host, domain.RunExecutionPermissionFullAccess, true, true},
		"host debug":          {host, domain.RunExecutionPermissionDebug, true, true},
		"host workspace":      {host, domain.RunExecutionPermissionWorkspaceAccess, false, true},
		"legacy full access":  {legacy, domain.RunExecutionPermissionFullAccess, false, false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := test.identity.AllowsPermission(test.permission); got != test.allowed {
				t.Fatalf("AllowsPermission() = %v, want %v", got, test.allowed)
			}
			if got := test.identity.Executable(); got != test.executable {
				t.Fatalf("Executable() = %v, want %v", got, test.executable)
			}
		})
	}
}

func TestAdapterReceiptCannotMasqueradeAsSandboxed(t *testing.T) {
	receipt := HostUnsandboxed("host-generation-1")
	receipt.IsolationGrade = IsolationWorkspaceSandbox
	receipt.NetworkPolicy = NetworkDenied
	receipt.CredentialPolicy = CredentialsNone
	if receipt.Validate() == nil {
		t.Fatal("host adapter receipt must not validate as a sandbox receipt")
	}
}

func TestAdapterGenerationIsPartOfAuthorityIdentity(t *testing.T) {
	first := HostUnsandboxed("host-generation-1")
	second := HostUnsandboxed("host-generation-2")
	if first.SameBackend(second) {
		t.Fatal("adapter replacement must change the bound identity")
	}
	if authority := NewAuthority("run-adapter-test", first); authority.Validate() != nil {
		t.Fatalf("authority validation failed: %v", authority.Validate())
	}
}
