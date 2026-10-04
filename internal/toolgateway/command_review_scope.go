package toolgateway

import "cyberagent-workbench/internal/runner"

// CommandReviewScope groups independently reviewed commands by declared risk
// and purpose. It grants no native authority and is not an isolation boundary.
type CommandReviewScope struct {
	RiskKinds       []string `json:"risk_kinds"`
	NetworkTargets  []string `json:"network_targets,omitempty"`
	NetworkPurpose  string   `json:"network_purpose,omitempty"`
	CredentialKinds []string `json:"credential_kinds,omitempty"`
	HostPaths       []string `json:"host_paths,omitempty"`
	PolicyCode      string   `json:"policy_code,omitempty"`
	PolicyReason    string   `json:"policy_reason,omitempty"`
	RequestedTool   string   `json:"requested_tool,omitempty"`
	OtherRiskReason string   `json:"other_risk_reason,omitempty"`
}

func (s CommandReviewScope) RiskScope() (runner.RiskEscalationScope, error) {
	kinds := make([]runner.RiskEscalationKind, len(s.RiskKinds))
	for i, kind := range s.RiskKinds {
		kinds[i] = runner.RiskEscalationKind(kind)
	}
	return runner.NewRiskEscalationScope(runner.RiskEscalationScopeRequest{Kinds: kinds,
		NetworkTargets: s.NetworkTargets, NetworkPurpose: s.NetworkPurpose,
		CredentialKinds: s.CredentialKinds, HostPaths: s.HostPaths, PolicyCode: s.PolicyCode,
		PolicyReason: s.PolicyReason, RequestedTool: s.RequestedTool, OtherReason: s.OtherRiskReason})
}

func (s CommandReviewScope) normalized() (CommandReviewScope, error) {
	risk, err := s.RiskScope()
	if err != nil {
		return CommandReviewScope{}, err
	}
	kinds := make([]string, len(risk.Kinds))
	for i, kind := range risk.Kinds {
		kinds[i] = string(kind)
	}
	return CommandReviewScope{RiskKinds: kinds, NetworkTargets: risk.NetworkTargets,
		NetworkPurpose: risk.NetworkPurpose, CredentialKinds: risk.CredentialKinds,
		HostPaths: risk.HostPaths, PolicyCode: risk.PolicyCode, PolicyReason: risk.PolicyReason,
		RequestedTool: risk.RequestedTool, OtherRiskReason: risk.OtherReason}, nil
}
