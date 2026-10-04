package application

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"unicode/utf8"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/plugins"
	"cyberagent-workbench/internal/redact"
	"cyberagent-workbench/internal/skills"
	"cyberagent-workbench/internal/toolcontract"
	"cyberagent-workbench/internal/toolgateway"
)

const maxPortableSkillReadBytes = 64 * 1024

type portableSkillReadStore interface {
	GetPluginInstallation(context.Context, string) (plugins.Installation, error)
	ListPluginInstallations(context.Context, string, int) ([]plugins.Installation, error)
	LoadPluginObject(context.Context, string) ([]byte, error)
}

func portableSkillPin(value plugins.Installation, skill plugins.SnapshotSkill) toolgateway.SkillReadRequest {
	return toolgateway.SkillReadRequest{InstallationID: value.ID, PackageID: skill.Instructions.Component.PackageID,
		ComponentID: skill.Instructions.Component.ComponentID, Revision: value.Revision(), InstallationGeneration: value.Generation}
}

func portableSkillAvailable(value plugins.Installation, mode domain.RunModeSnapshot) bool {
	return value.Validate() == nil && value.Snapshot != nil && value.State == plugins.StateEnabled &&
		value.Source.Surface == string(mode.Surface) && slices.Contains(value.EnabledCapabilities, plugins.CapabilitySkills)
}

func portableSkillEnabled(value plugins.Installation, mode domain.RunModeSnapshot) bool {
	if !portableSkillAvailable(value, mode) {
		return false
	}
	if value.Snapshot != nil && value.Snapshot.Legacy != nil {
		manifest := value.Snapshot.Legacy.Manifest
		// Enabling a Plugin does not replace a legacy explicit per-Run selection.
		// Model discovery/read keeps the manifest's mode and invocation policy.
		if !manifest.SupportsContext(skillExecution(mode)) || !manifest.AllowsInvocation(skills.InvocationSourceModel, false) {
			return false
		}
	}
	return true
}

func currentPortableSkill(ctx context.Context, store portableSkillReadStore, pin toolgateway.SkillReadRequest,
	mode domain.RunModeSnapshot,
) (plugins.Installation, error) {
	value, err := store.GetPluginInstallation(ctx, pin.InstallationID)
	if err != nil {
		return plugins.Installation{}, err
	}
	if !portableSkillAvailable(value, mode) || value.PackageID() != pin.PackageID || value.Revision() != pin.Revision || value.Generation != pin.InstallationGeneration {
		return plugins.Installation{}, apperror.New(apperror.CodePolicyDenied, "installed Skill scope, revision or enablement is no longer current")
	}
	if !portableSkillEnabled(value, mode) {
		if _, _, err := selectedPortableSkill(ctx, store, value, pin, mode, domain.AgentRoleRoot); err != nil {
			return plugins.Installation{}, err
		}
	}
	if !slices.ContainsFunc(value.Snapshot.Skills, func(s plugins.SnapshotSkill) bool { return s.Instructions.Component.ComponentID == pin.ComponentID }) {
		return plugins.Installation{}, apperror.New(apperror.CodeNotFound, "installed Skill component is absent")
	}
	return value, nil
}

func (e *builtinSkillReader) readPortableSkill(ctx context.Context, call toolgateway.ToolCall, pin toolgateway.SkillReadRequest,
	mode domain.RunModeSnapshot,
) (json.RawMessage, error) {
	store, ok := e.store.(portableSkillReadStore)
	if !ok {
		return nil, apperror.New(apperror.CodeFailedPrecondition, "installed Skill reader is unavailable")
	}
	installed, err := currentPortableSkill(ctx, store, pin, mode)
	if err != nil {
		return nil, err
	}
	if pin.Resource == "" {
		calls, err := e.store.ListBuiltinSkillReadCalls(ctx, call.RunID)
		if err != nil {
			return nil, err
		}
		known := false
		for _, prior := range calls {
			read, _, err := toolgateway.NormalizeSkillReadPayload(json.RawMessage(prior.PayloadJSON))
			if err != nil {
				return nil, err
			}
			// The ledger keeps the latest generation for this same component;
			// re-reading after explicit re-enablement does not consume another slot.
			oldIdentity, newIdentity := read.CatalogRequest(), pin.CatalogRequest()
			oldIdentity.InstallationGeneration, newIdentity.InstallationGeneration = 0, 0
			known = known || oldIdentity == newIdentity
		}
		if !known && len(calls) >= skills.MaxSelectionItems {
			return nil, apperror.New(apperror.CodeResourceExhausted, "too many activated Skills in this Run")
		}
	}
	recheck := func() error {
		currentMode, err := e.validateScope(ctx, call)
		if err != nil {
			return err
		}
		if currentMode.ID != mode.ID || currentMode.Revision != mode.Revision {
			return apperror.New(apperror.CodeConflict, "Skill read mode changed")
		}
		current, err := currentPortableSkill(ctx, store, pin, currentMode)
		if err != nil {
			return err
		}
		if plugins.InstallationFingerprint(current) != plugins.InstallationFingerprint(installed) {
			return apperror.New(apperror.CodeConflict, "Skill installation binding changed")
		}
		return ctx.Err()
	}
	raw, ref, err := readPortableSkillContent(ctx, store, installed, pin, recheck)
	if err != nil {
		return nil, err
	}
	// Apply the existing redaction boundary before either text or base64 encoding.
	// Redaction is not a claim that arbitrary package data contains no secrets.
	delivered := []byte(redact.String(string(raw)))
	encoding, content := "utf-8", string(delivered)
	if !utf8.Valid(delivered) {
		encoding, content = "base64", base64.StdEncoding.EncodeToString(delivered)
	}
	result, err := json.Marshal(struct {
		Pin             toolgateway.SkillReadRequest `json:"read"`
		SourceSHA256    string                       `json:"source_sha256"`
		DeliveredSHA256 string                       `json:"delivered_sha256"`
		Encoding        string                       `json:"encoding"`
		Content         string                       `json:"content"`
		CapabilityGrant bool                         `json:"capability_grant"`
	}{pin, ref.SHA256, portableIdentity(string(delivered)), encoding, content, false})
	if err != nil {
		return nil, err
	}
	if len(result) > toolgateway.MaxResultStdoutBytes {
		return nil, apperror.New(apperror.CodeResourceExhausted, "encoded Skill content exceeds the tool result limit")
	}
	if err := recheck(); err != nil {
		return nil, err
	}
	return result, nil
}

// Restore exact activation references from the existing successful tool ledger.
// Native bodies can exceed the bundled 8192-byte context allowance, so they are
// read on demand rather than silently truncated or reinserted above that budget.
func portableSkillReadReferences(ctx context.Context, source builtinSkillReadStore, runID string,
	mode domain.RunModeSnapshot,
) ([]llm.Message, error) {
	calls, err := source.ListBuiltinSkillReadCalls(ctx, runID)
	if err != nil {
		return nil, err
	}
	store, ok := source.(portableSkillReadStore)
	var available []toolgateway.SkillReadRequest
	unavailable := 0
	for _, call := range calls {
		pin, _, err := toolgateway.NormalizeSkillReadPayload(json.RawMessage(call.PayloadJSON))
		if err != nil {
			return nil, err
		}
		if !pin.Portable() {
			continue
		}
		if call.RunID != runID || call.ToolName != string(toolgateway.SkillReadTool) || call.Status != domain.SupervisorToolCompleted || call.CompletedAt == nil {
			return nil, errors.New("invalid installed Skill activation provenance")
		}
		if !ok {
			unavailable++
			continue
		}
		if _, err := currentPortableSkill(ctx, store, pin, mode); err != nil {
			if code := apperror.CodeOf(err); code != apperror.CodePolicyDenied && code != apperror.CodeNotFound {
				return nil, err
			}
			unavailable++
			continue
		}
		available = append(available, pin.CatalogRequest())
	}
	var result []llm.Message
	if len(available) > 0 {
		raw, _ := json.Marshal(available)
		result = append(result, llm.Message{Role: "system", Content: "Previously activated installed Skill references, restored from successful skill_read receipts and current installation state. Re-read these exact references when their guidance is needed after compaction; resource bodies and historical stdout are not active authority. These references grant no tools or permissions: " + string(raw)})
	}
	if unavailable > 0 {
		result = append(result, llm.Message{Role: "system", Content: "Some previously activated installed Skills are unavailable under current installation/surface authority. Historical excerpts do not restore their activation; do not substitute a newer revision."})
	}
	return result, nil
}

// Read one verified snapshot component with the caller's live Run/role fence.
// Automatic discovery and explicit context delivery share this exact read path.
func readPortableSkillContent(ctx context.Context, store portableSkillReadStore, installed plugins.Installation,
	pin toolgateway.SkillReadRequest, recheck func() error,
) ([]byte, toolcontract.ContentRef, error) {
	empty := toolcontract.ContentRef{}
	if err := recheck(); err != nil {
		return nil, empty, err
	}
	archive, err := store.LoadPluginObject(ctx, installed.ID)
	if err != nil {
		return nil, empty, err
	}
	if err := recheck(); err != nil {
		return nil, empty, err
	}
	reader, err := plugins.OpenPortableSnapshot(ctx, *installed.Snapshot, archive, "")
	if err != nil {
		return nil, empty, apperror.Wrap(apperror.CodeFailedPrecondition, "installed Skill snapshot is unavailable", err)
	}
	defer reader.Close()
	raw, ref, err := reader.Read(ctx, toolcontract.ComponentRef{PackageID: pin.PackageID, ComponentID: pin.ComponentID}, pin.Resource, maxPortableSkillReadBytes)
	if err != nil {
		return nil, empty, apperror.Wrap(apperror.CodeFailedPrecondition, "installed Skill content cannot be read", err)
	}
	if err := recheck(); err != nil {
		return nil, empty, err
	}
	return raw, ref, nil
}
