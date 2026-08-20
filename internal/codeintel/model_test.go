package codeintel

import (
	"strings"
	"testing"
)

func TestCapabilitySnapshotValidationRequiresCanonicalNegotiatedProvenance(t *testing.T) {
	digest := strings.Repeat("a", 64)
	base := CapabilitySnapshot{ProtocolVersion: ProtocolVersion, ServerID: "gopls",
		ServerName: "gopls", WorkspaceID: "workspace-1", Languages: []string{"go"},
		Source:                Source{Kind: "operator_config", Label: "code-intel.json", SHA256: digest},
		DescriptorFingerprint: digest, Health: HealthConfigured,
		ProcessOwned: true, ReadOnly: true, ModelVisibleTools: []string{}}
	if err := base.Validate(); err != nil {
		t.Fatalf("configured snapshot was rejected: %v", err)
	}
	withInvalidGeneration := base
	withInvalidGeneration.Generation = "not-a-generation"
	if err := withInvalidGeneration.Validate(); err == nil {
		t.Fatal("invalid optional generation was accepted")
	}
	withoutNegotiatedBinding := base
	withoutNegotiatedBinding.Capabilities.Hover = true
	withoutNegotiatedBinding.ModelVisibleTools = []string{ToolHover}
	if err := withoutNegotiatedBinding.Validate(); err == nil {
		t.Fatal("negotiated capability without generation and fingerprint was accepted")
	}
}

func TestResultValidationRejectsUnsupportedToolsAndControlText(t *testing.T) {
	digest := strings.Repeat("a", 64)
	base := Result{ProtocolVersion: ProtocolVersion, Tool: ToolWorkspaceSymbols,
		State: EvidenceCurrent, EvidenceLevel: "semantic_language_server",
		Provenance: Provenance{ProtocolVersion: ProtocolVersion,
			WorkspaceID: "workspace-1", RootFingerprint: digest, DirtyDigest: digest,
			ServerID: "gopls", ServerGeneration: digest,
			CapabilityFingerprint: digest, QueryFingerprint: digest},
		Items: []EvidenceItem{}, Page: Page{Limit: 20}}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid empty result was rejected: %v", err)
	}
	unsupported := base
	unsupported.Tool = "code_rename"
	if err := unsupported.Validate(); err == nil {
		t.Fatal("unsupported semantic tool result was accepted")
	}
	controlText := base
	controlText.Content = "unsafe\x07content"
	if err := controlText.Validate(); err == nil {
		t.Fatal("semantic result containing control text was accepted")
	}
}
