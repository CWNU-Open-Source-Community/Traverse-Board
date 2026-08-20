package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/codeintel"
)

const CodeIntelInventoryPath = "/api/v1/code-intel"

type CodeIntelSource interface {
	Inventory() []codeintel.CapabilitySnapshot
	Qualify(context.Context, string, string) []codeintel.Qualification
}

type CodeIntelInventoryView struct {
	ProtocolVersion string                       `json:"protocol_version"`
	Enabled         bool                         `json:"enabled"`
	Servers         []CodeIntelServerView        `json:"servers"`
	Qualifications  []CodeIntelQualificationView `json:"qualifications"`
}

type CodeIntelCapabilitiesView struct {
	WorkspaceSymbols bool `json:"workspace_symbols"`
	DocumentSymbols  bool `json:"document_symbols"`
	Definition       bool `json:"definition"`
	References       bool `json:"references"`
	Implementation   bool `json:"implementation"`
	Hover            bool `json:"hover"`
	SignatureHelp    bool `json:"signature_help"`
	Diagnostics      bool `json:"diagnostics"`
	CallHierarchy    bool `json:"call_hierarchy"`
	TypeHierarchy    bool `json:"type_hierarchy"`
}

type CodeIntelServerView struct {
	ProtocolVersion       string                    `json:"protocol_version"`
	ServerID              string                    `json:"server_id"`
	ServerName            string                    `json:"server_name"`
	WorkspaceID           string                    `json:"workspace_id"`
	Languages             []string                  `json:"languages"`
	SourceKind            string                    `json:"source_kind"`
	SourceLabel           string                    `json:"source_label"`
	SourceSHA256          string                    `json:"source_sha256"`
	DescriptorFingerprint string                    `json:"descriptor_fingerprint"`
	CapabilityFingerprint string                    `json:"capability_fingerprint,omitempty"`
	Generation            string                    `json:"generation,omitempty"`
	Health                string                    `json:"health"`
	Capabilities          CodeIntelCapabilitiesView `json:"capabilities"`
	ModelVisibleTools     []string                  `json:"model_visible_tools"`
	ServerVersion         string                    `json:"server_version,omitempty"`
	LastError             string                    `json:"last_error,omitempty"`
	ProcessOwned          bool                      `json:"process_owned"`
	ReadOnly              bool                      `json:"read_only"`
	NetworkAccessGranted  bool                      `json:"network_access_granted"`
	CredentialsGranted    bool                      `json:"credentials_granted"`
	ShellProfileLoaded    bool                      `json:"shell_profile_loaded"`
	QualifiedAt           string                    `json:"qualified_at,omitempty"`
}

type CodeIntelQualificationView struct {
	ProtocolVersion       string `json:"protocol_version"`
	ServerID              string `json:"server_id"`
	WorkspaceID           string `json:"workspace_id"`
	Eligible              bool   `json:"eligible"`
	Health                string `json:"health"`
	DescriptorFingerprint string `json:"descriptor_fingerprint"`
	ExecutableHashMatched bool   `json:"executable_hash_matched"`
	Reviewed              bool   `json:"reviewed"`
	ProcessOwned          bool   `json:"process_owned"`
	MinimalEnvironment    bool   `json:"minimal_environment"`
	NetworkAccessGranted  bool   `json:"network_access_granted"`
	CredentialsGranted    bool   `json:"credentials_granted"`
	ShellProfileLoaded    bool   `json:"shell_profile_loaded"`
	Reason                string `json:"reason,omitempty"`
}

func (a *API) codeIntelInventory(request *http.Request) (any, *Page, error) {
	if err := validateSingleQueryValues(request.URL.Query(), "workspace_id"); err != nil {
		return nil, nil, err
	}
	workspaceID := ""
	if values, found := request.URL.Query()["workspace_id"]; found {
		if len(values) != 1 || strings.TrimSpace(values[0]) == "" {
			return nil, nil, apperror.New(apperror.CodeInvalidArgument,
				"workspace_id must appear exactly once with a value")
		}
		workspaceID = strings.TrimSpace(values[0])
	}
	result := CodeIntelInventoryView{ProtocolVersion: codeintel.ProtocolVersion,
		Enabled: a.codeIntelSource != nil, Servers: []CodeIntelServerView{},
		Qualifications: []CodeIntelQualificationView{}}
	if a.codeIntelSource == nil {
		return result, nil, nil
	}
	for _, server := range a.codeIntelSource.Inventory() {
		if err := server.Validate(); err != nil {
			return nil, nil, apperror.New(apperror.CodeUnavailable,
				"code-intel runtime returned invalid metadata")
		}
		if workspaceID != "" && server.WorkspaceID != workspaceID {
			continue
		}
		result.Servers = append(result.Servers, codeIntelServerView(server))
	}
	if strings.TrimSpace(workspaceID) != "" {
		if err := validatePathIdentity(workspaceID); err != nil {
			return nil, nil, err
		}
		workspace, err := a.store.GetWorkspaceInfo(request.Context(), workspaceID)
		if err != nil {
			return nil, nil, apperror.Normalize(err)
		}
		for _, qualification := range a.codeIntelSource.Qualify(request.Context(),
			workspace.ID, workspace.RootPath) {
			if err := qualification.Validate(); err != nil {
				return nil, nil, apperror.New(apperror.CodeUnavailable,
					"code-intel runtime returned invalid qualification metadata")
			}
			result.Qualifications = append(result.Qualifications,
				codeIntelQualificationView(qualification))
		}
	}
	return result, nil, nil
}

func codeIntelServerView(value codeintel.CapabilitySnapshot) CodeIntelServerView {
	qualifiedAt := ""
	if value.QualifiedAt != nil {
		qualifiedAt = value.QualifiedAt.UTC().Format(time.RFC3339Nano)
	}
	return CodeIntelServerView{ProtocolVersion: value.ProtocolVersion,
		ServerID: value.ServerID, ServerName: value.ServerName,
		WorkspaceID: value.WorkspaceID, Languages: append([]string(nil), value.Languages...),
		SourceKind: value.Source.Kind, SourceLabel: value.Source.Label,
		SourceSHA256:          value.Source.SHA256,
		DescriptorFingerprint: value.DescriptorFingerprint,
		CapabilityFingerprint: value.CapabilityFingerprint, Generation: value.Generation,
		Health: string(value.Health), Capabilities: CodeIntelCapabilitiesView{
			WorkspaceSymbols: value.Capabilities.WorkspaceSymbols,
			DocumentSymbols:  value.Capabilities.DocumentSymbols,
			Definition:       value.Capabilities.Definition, References: value.Capabilities.References,
			Implementation: value.Capabilities.Implementation, Hover: value.Capabilities.Hover,
			SignatureHelp: value.Capabilities.SignatureHelp,
			Diagnostics:   value.Capabilities.Diagnostics,
			CallHierarchy: value.Capabilities.CallHierarchy,
			TypeHierarchy: value.Capabilities.TypeHierarchy,
		}, ModelVisibleTools: append([]string(nil), value.ModelVisibleTools...),
		ServerVersion: value.ServerVersion, LastError: value.LastError,
		ProcessOwned: value.ProcessOwned, ReadOnly: value.ReadOnly,
		NetworkAccessGranted: value.NetworkAccessGranted,
		CredentialsGranted:   value.CredentialsGranted,
		ShellProfileLoaded:   value.ShellProfileLoaded, QualifiedAt: qualifiedAt}
}

func codeIntelQualificationView(value codeintel.Qualification) CodeIntelQualificationView {
	return CodeIntelQualificationView{ProtocolVersion: value.ProtocolVersion,
		ServerID: value.ServerID, WorkspaceID: value.WorkspaceID, Eligible: value.Eligible,
		Health: string(value.Health), DescriptorFingerprint: value.DescriptorFingerprint,
		ExecutableHashMatched: value.ExecutableHashMatched, Reviewed: value.Reviewed,
		ProcessOwned: value.ProcessOwned, MinimalEnvironment: value.MinimalEnvironment,
		NetworkAccessGranted: value.NetworkAccessGranted,
		CredentialsGranted:   value.CredentialsGranted,
		ShellProfileLoaded:   value.ShellProfileLoaded, Reason: value.Reason}
}
