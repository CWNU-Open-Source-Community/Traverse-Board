package githubreview

import (
	"encoding/json"
	"slices"
	"strconv"

	"cyberagent-workbench/internal/toolcontract"
)

// ReviewWriteOperation describes the frozen native request, not a grant. A
// review reply, state change or reviewer request remains a shared remote write.
func ReviewWriteOperation(spec WriteSpec, preview WritePreview) (toolcontract.Operation, error) {
	spec.Reviewers = slices.Clone(spec.Reviewers)
	if err := validateWriteBinding(spec, preview); err != nil {
		return toolcontract.Operation{}, err
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		return toolcontract.Operation{}, err
	}
	target := "https://api.github.com/graphql"
	pull := "https://api.github.com" + repositoryAPIPath(spec.Identity.Repository) + "/pulls/" + strconv.FormatInt(spec.Identity.Number, 10)
	if spec.Operation == WriteSubmitReview {
		target = pull + "/reviews"
	} else if spec.Operation == WriteRequestReviewer {
		target = pull + "/requested_reviewers"
	}
	op := toolcontract.Operation{ID: preview.ID, Kind: toolcontract.OperationToolCall,
		ToolID: ApprovalToolName, Component: toolcontract.ComponentRef{PackageID: "traverse-board", ComponentID: "native-github"},
		AdapterID: "native-github", AdapterRevision: "1",
		InputFingerprint:      Fingerprint("review-write-input", string(raw), preview.ID, preview.ApprovalFingerprint, preview.IdempotencyMarker),
		CapabilityFingerprint: spec.CapabilityGeneration,
		Targets:               []toolcontract.Target{{Kind: "endpoint", Locator: target}},
		Effects:               []toolcontract.Effect{toolcontract.EffectPublicNetwork, toolcontract.EffectRemoteWrite}}
	return op, op.Validate()
}
