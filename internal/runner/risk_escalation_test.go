package runner

import (
	"path/filepath"
	"testing"
)

func TestRiskEscalationScopeIsStableCategorizedAndRejectsSecrets(t *testing.T) {
	request := RiskEscalationScopeRequest{
		Kinds: []RiskEscalationKind{RiskEscalationPolicyDenial,
			RiskEscalationNetwork, RiskEscalationCredential},
		NetworkTargets:  []string{"z.example.test:443", "a.example.test:443"},
		NetworkPurpose:  "send one exact request",
		CredentialKinds: []string{"github_app", "client_certificate"},
		PolicyCode:      "workspace.network_denied",
		PolicyReason:    "the target is outside Workspace Access",
	}
	first, err := NewRiskEscalationScope(request)
	if err != nil {
		t.Fatal(err)
	}
	request.Kinds[0], request.Kinds[2] = request.Kinds[2], request.Kinds[0]
	request.NetworkTargets[0], request.NetworkTargets[1] =
		request.NetworkTargets[1], request.NetworkTargets[0]
	request.CredentialKinds[0], request.CredentialKinds[1] =
		request.CredentialKinds[1], request.CredentialKinds[0]
	second, err := NewRiskEscalationScope(request)
	if err != nil || second.Fingerprint != first.Fingerprint {
		t.Fatalf("normalized scope is unstable: first=%+v second=%+v err=%v",
			first, second, err)
	}
	secretRequest := request
	secretRequest.NetworkTargets = []string{
		"Authorization: Bearer ghp_abcdefghijklmnopqrstuvwxyz1234567890",
	}
	if _, err := NewRiskEscalationScope(secretRequest); err == nil {
		t.Fatal("secret-like risk metadata was accepted")
	}
}

func TestRiskEscalationScopeBuildsEachRequiredStableCategory(t *testing.T) {
	hostPath := filepath.Join(t.TempDir(), "outside-cache")
	tests := []struct {
		name    string
		kind    RiskEscalationKind
		request RiskEscalationScopeRequest
	}{
		{name: "network", kind: RiskEscalationNetwork,
			request: RiskEscalationScopeRequest{Kinds: []RiskEscalationKind{RiskEscalationNetwork},
				NetworkTargets: []string{"proxy.example.test:443"},
				NetworkPurpose: "download one declared dependency"}},
		{name: "credential kind", kind: RiskEscalationCredential,
			request: RiskEscalationScopeRequest{Kinds: []RiskEscalationKind{RiskEscalationCredential},
				CredentialKinds: []string{"private_registry_token"}}},
		{name: "host path", kind: RiskEscalationHostPath,
			request: RiskEscalationScopeRequest{Kinds: []RiskEscalationKind{RiskEscalationHostPath},
				HostPaths: []string{hostPath}}},
		{name: "policy refusal", kind: RiskEscalationPolicyDenial,
			request: RiskEscalationScopeRequest{Kinds: []RiskEscalationKind{RiskEscalationPolicyDenial},
				PolicyCode:   "workspace.network_denied",
				PolicyReason: "the requested operation exceeds Workspace Access"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			first, err := NewRiskEscalationScope(test.request)
			if err != nil {
				t.Fatal(err)
			}
			second, err := NewRiskEscalationScope(test.request)
			if err != nil || first.Fingerprint != second.Fingerprint ||
				len(first.Kinds) != 1 || first.Kinds[0] != test.kind {
				t.Fatalf("category proposal is unstable: first=%+v second=%+v err=%v",
					first, second, err)
			}
		})
	}
}
