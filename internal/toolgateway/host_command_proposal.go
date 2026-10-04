package toolgateway

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"cyberagent-workbench/internal/redact"
	"cyberagent-workbench/internal/runner"
)

type HostCommandProposalSpec struct {
	Version             string   `json:"version"`
	Transport           string   `json:"transport,omitempty"`
	ExecutablePath      string   `json:"executable_path,omitempty"`
	Argv                []string `json:"argv,omitempty"`
	Shell               string   `json:"shell,omitempty"`
	Command             string   `json:"command,omitempty"`
	WorkingDirectory    string   `json:"working_directory"`
	TimeoutMilliseconds int64    `json:"timeout_milliseconds"`
	Purpose             string   `json:"purpose"`
	RiskKinds           []string `json:"risk_kinds,omitempty"`
	NetworkTargets      []string `json:"network_targets,omitempty"`
	NetworkPurpose      string   `json:"network_purpose,omitempty"`
	CredentialKinds     []string `json:"credential_kinds,omitempty"`
	HostPaths           []string `json:"host_paths,omitempty"`
	PolicyCode          string   `json:"policy_code,omitempty"`
	PolicyReason        string   `json:"policy_reason,omitempty"`
	RequestedTool       string   `json:"requested_tool,omitempty"`
	OtherRiskReason     string   `json:"other_risk_reason,omitempty"`
}

const (
	HostCommandTransportProcess = "process"
	HostCommandTransportShell   = "shell"
	HostCommandShellPowerShell  = "powershell"
	HostCommandShellBash        = "bash"
)

func normalizeHostCommandProposalPayload(payload json.RawMessage) (
	HostCommandProposalSpec, json.RawMessage, error,
) {
	spec, err := decodeStructuredPayload[HostCommandProposalSpec](payload)
	if err != nil {
		return HostCommandProposalSpec{}, nil, err
	}
	fields, err := structuredPayloadFields(payload)
	if err != nil {
		return HostCommandProposalSpec{}, nil, err
	}
	spec.Version = strings.TrimSpace(spec.Version)
	spec.Transport = strings.ToLower(strings.TrimSpace(spec.Transport))
	if spec.Transport == "" {
		// Keep durable pre-shell payloads replayable. New callers are directed
		// to send the transport explicitly by the Tool schema.
		if transportPresent, _ := structuredPayloadField(fields, "transport"); transportPresent {
			return HostCommandProposalSpec{}, nil,
				errors.New("host command proposal transport is invalid")
		}
		spec.Transport = HostCommandTransportProcess
	}
	spec.ExecutablePath = strings.TrimSpace(spec.ExecutablePath)
	spec.Shell = strings.ToLower(strings.TrimSpace(spec.Shell))
	spec.Command = strings.TrimSpace(spec.Command)
	spec.WorkingDirectory = strings.TrimSpace(spec.WorkingDirectory)
	spec.Purpose = strings.TrimSpace(redact.String(spec.Purpose))
	if spec.Argv != nil {
		spec.Argv = append([]string{}, spec.Argv...)
	}
	if spec.Version == runner.RiskEscalationProtocolVersion {
		scope, scopeErr := spec.riskEscalationScope()
		if scopeErr != nil {
			return HostCommandProposalSpec{}, nil, scopeErr
		}
		spec.RiskKinds = make([]string, len(scope.Kinds))
		for index, kind := range scope.Kinds {
			spec.RiskKinds[index] = string(kind)
		}
		spec.NetworkTargets = append([]string(nil), scope.NetworkTargets...)
		spec.NetworkPurpose = scope.NetworkPurpose
		spec.CredentialKinds = append([]string(nil), scope.CredentialKinds...)
		spec.HostPaths = append([]string(nil), scope.HostPaths...)
		spec.PolicyCode = scope.PolicyCode
		spec.PolicyReason = scope.PolicyReason
		spec.RequestedTool = scope.RequestedTool
		spec.OtherRiskReason = scope.OtherReason
	}
	executablePresent, executableNonNull := structuredPayloadField(fields, "executable_path")
	argvPresent, argvNonNull := structuredPayloadField(fields, "argv")
	shellPresent, shellNonNull := structuredPayloadField(fields, "shell")
	commandPresent, commandNonNull := structuredPayloadField(fields, "command")
	switch spec.Transport {
	case HostCommandTransportProcess:
		if !executablePresent || !executableNonNull || !argvPresent || !argvNonNull ||
			spec.Argv == nil || shellPresent || commandPresent {
			return HostCommandProposalSpec{}, nil,
				errors.New("process host command proposal fields are invalid")
		}
	case HostCommandTransportShell:
		if executablePresent || argvPresent || !shellPresent || !shellNonNull ||
			!commandPresent || !commandNonNull {
			return HostCommandProposalSpec{}, nil,
				errors.New("shell host command proposal fields are invalid")
		}
	}
	if err := spec.Validate(); err != nil {
		return HostCommandProposalSpec{}, nil, err
	}
	canonical, err := marshalHostCommandProposalSpec(spec)
	if err != nil {
		return HostCommandProposalSpec{}, nil, err
	}
	return spec, canonical, nil
}

func NormalizeHostCommandProposalPayload(payload json.RawMessage) (
	HostCommandProposalSpec, json.RawMessage, error,
) {
	return normalizeHostCommandProposalPayload(payload)
}

func marshalHostCommandProposalSpec(spec HostCommandProposalSpec) (
	json.RawMessage, error,
) {
	if spec.Version == runner.RiskEscalationProtocolVersion {
		type canonicalRiskEscalation struct {
			Version             string   `json:"version"`
			Transport           string   `json:"transport"`
			ExecutablePath      string   `json:"executable_path,omitempty"`
			Argv                []string `json:"argv,omitempty"`
			Shell               string   `json:"shell,omitempty"`
			Command             string   `json:"command,omitempty"`
			WorkingDirectory    string   `json:"working_directory"`
			TimeoutMilliseconds int64    `json:"timeout_milliseconds"`
			Purpose             string   `json:"purpose"`
			RiskKinds           []string `json:"risk_kinds"`
			NetworkTargets      []string `json:"network_targets,omitempty"`
			NetworkPurpose      string   `json:"network_purpose,omitempty"`
			CredentialKinds     []string `json:"credential_kinds,omitempty"`
			HostPaths           []string `json:"host_paths,omitempty"`
			PolicyCode          string   `json:"policy_code,omitempty"`
			PolicyReason        string   `json:"policy_reason,omitempty"`
			RequestedTool       string   `json:"requested_tool,omitempty"`
			OtherRiskReason     string   `json:"other_risk_reason,omitempty"`
		}
		return json.Marshal(canonicalRiskEscalation{
			Version: spec.Version, Transport: spec.Transport,
			ExecutablePath: spec.ExecutablePath, Argv: spec.Argv,
			Shell: spec.Shell, Command: spec.Command,
			WorkingDirectory:    spec.WorkingDirectory,
			TimeoutMilliseconds: spec.TimeoutMilliseconds, Purpose: spec.Purpose,
			RiskKinds: spec.RiskKinds, NetworkTargets: spec.NetworkTargets,
			NetworkPurpose: spec.NetworkPurpose, CredentialKinds: spec.CredentialKinds,
			HostPaths: spec.HostPaths, PolicyCode: spec.PolicyCode,
			PolicyReason: spec.PolicyReason, RequestedTool: spec.RequestedTool,
			OtherRiskReason: spec.OtherRiskReason,
		})
	}
	if spec.Transport == HostCommandTransportProcess {
		return json.Marshal(struct {
			Version             string   `json:"version"`
			Transport           string   `json:"transport"`
			ExecutablePath      string   `json:"executable_path"`
			Argv                []string `json:"argv"`
			WorkingDirectory    string   `json:"working_directory"`
			TimeoutMilliseconds int64    `json:"timeout_milliseconds"`
			Purpose             string   `json:"purpose"`
		}{spec.Version, spec.Transport, spec.ExecutablePath, spec.Argv,
			spec.WorkingDirectory, spec.TimeoutMilliseconds, spec.Purpose})
	}
	return json.Marshal(struct {
		Version             string `json:"version"`
		Transport           string `json:"transport"`
		Shell               string `json:"shell"`
		Command             string `json:"command"`
		WorkingDirectory    string `json:"working_directory"`
		TimeoutMilliseconds int64  `json:"timeout_milliseconds"`
		Purpose             string `json:"purpose"`
	}{spec.Version, spec.Transport, spec.Shell, spec.Command,
		spec.WorkingDirectory, spec.TimeoutMilliseconds, spec.Purpose})
}

func (s HostCommandProposalSpec) Validate() error {
	if (s.Version != runner.HostCommandProposalProtocolVersion &&
		s.Version != runner.RiskEscalationProtocolVersion) ||
		s.WorkingDirectory == "" ||
		!utf8.ValidString(s.WorkingDirectory) ||
		strings.ContainsRune(s.WorkingDirectory, 0) ||
		len([]rune(s.WorkingDirectory)) > MaxWorkspaceRootPathRunes ||
		s.TimeoutMilliseconds < 1 ||
		s.TimeoutMilliseconds > runner.MaxHostCommandTimeout.Milliseconds() ||
		!utf8.ValidString(s.Purpose) || s.Purpose == "" ||
		strings.TrimSpace(s.Purpose) != s.Purpose ||
		strings.ContainsRune(s.Purpose, 0) ||
		utf8.RuneCountInString(s.Purpose) > runner.MaxHostCommandPurposeRunes {
		return errors.New("host command proposal payload is invalid")
	}
	if s.Version == runner.HostCommandProposalProtocolVersion {
		if len(s.RiskKinds) != 0 || len(s.NetworkTargets) != 0 ||
			s.NetworkPurpose != "" || len(s.CredentialKinds) != 0 ||
			len(s.HostPaths) != 0 || s.PolicyCode != "" || s.PolicyReason != "" ||
			s.RequestedTool != "" || s.OtherRiskReason != "" {
			return errors.New("approval-mode host proposal cannot carry Workspace Access escalation fields")
		}
	} else if _, err := s.riskEscalationScope(); err != nil {
		return err
	}
	switch s.Transport {
	case HostCommandTransportProcess:
		if s.ExecutablePath == "" || !utf8.ValidString(s.ExecutablePath) ||
			strings.ContainsRune(s.ExecutablePath, 0) ||
			len([]rune(s.ExecutablePath)) > MaxWorkspaceRootPathRunes ||
			s.Shell != "" || s.Command != "" {
			return errors.New("process host command proposal transport is invalid")
		}
	case HostCommandTransportShell:
		if s.ExecutablePath != "" || len(s.Argv) != 0 ||
			(s.Shell != HostCommandShellPowerShell && s.Shell != HostCommandShellBash) ||
			s.Command == "" || !utf8.ValidString(s.Command) ||
			strings.ContainsRune(s.Command, 0) || strings.ContainsAny(s.Command, "\r\n") ||
			len([]byte(s.Command)) > runner.MaxHostCommandArgumentBytes ||
			redact.String(s.Command) != s.Command {
			return errors.New("shell host command proposal is invalid or contains secret-like material")
		}
	default:
		return errors.New("host command proposal transport is invalid")
	}
	if len(s.Argv) > runner.MaxHostCommandArguments {
		return errors.New("host command proposal argv exceeds its item limit")
	}
	total := 0
	for _, argument := range s.Argv {
		if !utf8.ValidString(argument) || strings.ContainsRune(argument, 0) ||
			len([]byte(argument)) > runner.MaxHostCommandArgumentBytes ||
			redact.String(argument) != argument {
			return errors.New("host command proposal argv is invalid or contains secret-like material")
		}
		total += len([]byte(argument))
		if total > runner.MaxHostCommandArgumentsBytes {
			return errors.New("host command proposal argv exceeds its total limit")
		}
	}
	return nil
}

func (s HostCommandProposalSpec) riskEscalationScope() (
	runner.RiskEscalationScope, error,
) {
	kinds := make([]runner.RiskEscalationKind, len(s.RiskKinds))
	for index, value := range s.RiskKinds {
		kinds[index] = runner.RiskEscalationKind(strings.ToLower(strings.TrimSpace(value)))
	}
	scope, err := runner.NewRiskEscalationScope(runner.RiskEscalationScopeRequest{
		Kinds: kinds, NetworkTargets: s.NetworkTargets,
		NetworkPurpose: s.NetworkPurpose, CredentialKinds: s.CredentialKinds,
		HostPaths: s.HostPaths, PolicyCode: s.PolicyCode,
		PolicyReason: s.PolicyReason, RequestedTool: s.RequestedTool,
		OtherReason: s.OtherRiskReason,
	})
	if err != nil {
		return runner.RiskEscalationScope{}, errors.New("risk escalation scope is invalid or contains secret-like material")
	}
	return scope, nil
}

func (s HostCommandProposalSpec) RiskEscalationScope() (
	runner.RiskEscalationScope, error,
) {
	if s.Version != runner.RiskEscalationProtocolVersion {
		return runner.RiskEscalationScope{}, errors.New("host command payload is not a risk escalation")
	}
	return s.riskEscalationScope()
}

type HostCommandProposalState string

const (
	HostCommandProposalRecorded  HostCommandProposalState = "recorded"
	HostCommandProposalWaiting   HostCommandProposalState = "waiting_approval"
	HostCommandProposalDenied    HostCommandProposalState = "denied"
	HostCommandProposalCompleted HostCommandProposalState = "completed"
	HostCommandProposalFailed    HostCommandProposalState = "failed"
)

type HostCommandProposalResult struct {
	ProposalID      string
	SpecFingerprint string
	Replayed        bool
	State           HostCommandProposalState
	ApprovalID      string
	GrantID         string
	Evidence        string
	ErrorCode       string
	Message         string
	Uncertain       bool
}

func (r HostCommandProposalResult) Validate() error {
	if strings.TrimSpace(r.ProposalID) == "" ||
		strings.TrimSpace(r.ProposalID) != r.ProposalID ||
		len([]rune(r.ProposalID)) > MaxToolIdentityRunes ||
		len(r.SpecFingerprint) != 64 {
		return errors.New("host command proposal result is invalid")
	}
	if r.State == "" {
		r.State = HostCommandProposalRecorded
	}
	switch r.State {
	case HostCommandProposalRecorded:
		if r.ApprovalID != "" || r.GrantID != "" || r.Evidence != "" ||
			r.ErrorCode != "" || r.Message != "" || r.Uncertain {
			return errors.New("legacy host command proposal result contains escalation state")
		}
	case HostCommandProposalWaiting:
		if strings.TrimSpace(r.ApprovalID) == "" || r.Evidence != "" ||
			r.ErrorCode != "" || r.Uncertain {
			return errors.New("waiting risk escalation result is invalid")
		}
	case HostCommandProposalDenied:
		if strings.TrimSpace(r.ApprovalID) == "" || strings.TrimSpace(r.Message) == "" ||
			r.Evidence != "" || r.Uncertain {
			return errors.New("denied risk escalation result is invalid")
		}
	case HostCommandProposalCompleted:
		if strings.TrimSpace(r.ApprovalID) == "" || r.Uncertain {
			return errors.New("completed risk escalation result is invalid")
		}
	case HostCommandProposalFailed:
		if strings.TrimSpace(r.ApprovalID) == "" || strings.TrimSpace(r.ErrorCode) == "" ||
			strings.TrimSpace(r.Message) == "" || (r.Uncertain && r.Evidence != "") {
			return errors.New("failed risk escalation result is invalid")
		}
	default:
		return errors.New("host command proposal result state is invalid")
	}
	return nil
}
