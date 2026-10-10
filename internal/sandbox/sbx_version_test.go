package sandbox

import (
	"context"
	"testing"
)

type sbxSettingTestDaemon struct {
	SBXDaemonTransport
	data string
}

func (transport sbxSettingTestDaemon) IsolationSetting(context.Context, string) ([]byte, error) {
	return []byte(transport.data), nil
}

func TestSBXIsolationSettingRequiresAnUnambiguousSuccessfulBoolean(t *testing.T) {
	for _, data := range []string{
		`{"key":"ssh.agentForwardingEnabled","type":"bool","value":false,"error":"read failed"}`,
		`{"key":"ssh.agentForwardingEnabled","type":"bool","value":false,"error":true}`,
		`{"key":"ssh.agentForwardingEnabled","type":"bool"}`,
		`{"key":"ssh.agentForwardingEnabled","type":"bool","value":null}`,
		`{"key":"ssh.agentForwardingEnabled","type":"bool","value":"false"}`,
		`{"key":"mcp.forceLocalGateway","type":"bool","value":false}`,
		`{"key":"ssh.agentForwardingEnabled","type":"string","value":false}`,
		`{"key":"ssh.agentForwardingEnabled","type":"bool","value":true,"value":false}`,
	} {
		backend := &SBXBackend{daemon: sbxSettingTestDaemon{data: data}}
		if _, err := backend.isolationSetting(t.Context(), "ssh.agentForwardingEnabled"); err == nil {
			t.Fatalf("accepted ambiguous or failed isolation setting: %s", data)
		}
	}
	backend := &SBXBackend{daemon: sbxSettingTestDaemon{data: `{"key":"ssh.agentForwardingEnabled","type":"bool","value":false,"error":""}`}}
	if enabled, err := backend.isolationSetting(t.Context(), "ssh.agentForwardingEnabled"); err != nil || enabled {
		t.Fatalf("valid disabled SSH setting rejected: %t %v", enabled, err)
	}
}
