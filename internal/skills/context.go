package skills

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/redact"
	"cyberagent-workbench/internal/runmutation"
)

const ContextProtocolVersion = "skill_context.v1"

// ContextItem is an in-memory, selection-bound delivery unit. Content must
// never be persisted by the Store or copied into Run events.
type ContextItem struct {
	Ordinal               int
	Name                  string
	Version               string
	SourceSHA256          string
	SourceBytes           int
	SourceTokenUpperBound int
	DeliveredSHA256       string
	DeliveredBytes        int
	TokenUpperBound       int
	RedactionCount        int
	Content               string
}

// ContextAssembly contains the exact redacted Skill text delivered to one
// root Supervisor turn. Its fingerprint binds the content without exposing it.
type ContextAssembly struct {
	ProtocolVersion      string
	SelectionID          string
	RunID                string
	MissionID            string
	Surface              domain.ExecutionSurface
	Phase                domain.ExecutionPhase
	Profile              domain.Profile
	Role                 domain.AgentRole
	ModeBound            bool
	SelectionFingerprint string
	Fingerprint          string
	SelectionItemCount   int
	TokenBudget          int
	TokenUpperBound      int
	ItemCount            int
	RedactionCount       int
	Items                []ContextItem
}

type RootContextPreparationRequest struct {
	RunID                string
	MissionID            string
	RootAgentID          string
	SupervisorAttemptID  string
	Turn                 int
	SelectionID          string
	ProtocolVersion      string
	Profile              domain.Profile
	ModeSnapshotID       string
	ModeRevision         int64
	Surface              domain.ExecutionSurface
	Phase                domain.ExecutionPhase
	SelectionFingerprint string
	ContextFingerprint   string
	SelectionItemCount   int
	ItemCount            int
	TokenBudget          int
	TokenUpperBound      int
	RedactionCount       int
}

type RootContextPreparation struct {
	ID string
	RootContextPreparationRequest
	PreparedAt time.Time
	Recovered  bool
}

type RootContextCommit struct {
	PreparationID       string
	RunID               string
	SupervisorAttemptID string
	ModelAttempt        int
	CommittedAt         time.Time
}

// AssembleContext is the only Registry API that exposes Skill content. It
// requires a complete persisted selection and revalidates every pinned tuple
// against the immutable Registry before returning redacted in-memory text.
func (r *Registry) AssembleContext(selection Selection) (ContextAssembly, error) {
	return r.AssembleContextFor(selection, ExecutionContext{
		Surface: domain.ExecutionSurfaceCode, Phase: domain.ExecutionPhaseDeliver,
		Profile: selection.Profile, Role: domain.AgentRoleRoot,
	})
}

// AssembleContextFor revalidates every pinned Registry tuple, then delivers
// only the subset compatible with the active surface, phase, Profile, and role.
func (r *Registry) AssembleContextFor(selection Selection,
	execution ExecutionContext,
) (ContextAssembly, error) {
	if r == nil {
		return ContextAssembly{}, errors.New("skill registry is required")
	}
	if err := selection.Validate(); err != nil {
		return ContextAssembly{}, fmt.Errorf("invalid persisted Skill selection: %w", err)
	}
	if err := r.Validate(); err != nil {
		return ContextAssembly{}, err
	}
	if err := execution.Validate(); err != nil {
		return ContextAssembly{}, err
	}
	if execution.Profile != selection.Profile {
		return ContextAssembly{}, errors.New("skill execution context does not match the persisted selection Profile")
	}

	items := make([]ContextItem, 0, len(selection.Items))
	totalTokens := 0
	totalRedactions := 0
	modeBound := false
	for _, selected := range selection.Items {
		entry, found := r.version(selected.Name, selected.Version)
		if !found {
			return ContextAssembly{}, fmt.Errorf("selected Skill %q version %q is unavailable in the embedded Registry",
				selected.Name, selected.Version)
		}
		if err := entry.manifest.Validate(entry.content); err != nil {
			return ContextAssembly{}, fmt.Errorf("selected Skill %q failed delivery validation: %w", selected.Name, err)
		}
		manifest := entry.manifest
		if manifest.Name != selected.Name || manifest.Version != selected.Version ||
			manifest.ContentSHA256 != selected.ContentSHA256 ||
			manifest.ContentBytes != selected.ContentBytes ||
			manifest.ContentTokenUpperBound != selected.TokenUpperBound {
			return ContextAssembly{}, fmt.Errorf("selected Skill %q no longer matches its pinned version, hash, or size", selected.Name)
		}
		if !containsProfile(manifest.Profiles, selection.Profile) {
			return ContextAssembly{}, fmt.Errorf("selected Skill %q no longer supports profile %q", selected.Name, selection.Profile)
		}
		modeBound = modeBound || manifest.HasModeMetadata()
		if !manifest.SupportsContext(execution) {
			continue
		}

		redacted := redact.Text(string(entry.content))
		delivered := []byte(redacted.Text)
		if err := validateContextContent(delivered); err != nil {
			return ContextAssembly{}, fmt.Errorf("selected Skill %q produced invalid redacted context: %w", selected.Name, err)
		}
		tokens := ContentTokenUpperBound(delivered)
		if tokens > selected.TokenUpperBound {
			return ContextAssembly{}, fmt.Errorf("selected Skill %q redacted context exceeds its pinned token bound", selected.Name)
		}
		redactionCount := 0
		for _, finding := range redacted.Findings {
			redactionCount += finding.Count
		}
		digest := sha256.Sum256(delivered)
		items = append(items, ContextItem{
			Ordinal: len(items) + 1, Name: selected.Name, Version: selected.Version,
			SourceSHA256: selected.ContentSHA256, SourceBytes: selected.ContentBytes,
			SourceTokenUpperBound: selected.TokenUpperBound,
			DeliveredSHA256:       hex.EncodeToString(digest[:]), DeliveredBytes: len(delivered),
			TokenUpperBound: tokens, RedactionCount: redactionCount, Content: redacted.Text,
		})
		totalTokens += tokens
		totalRedactions += redactionCount
	}
	if totalTokens > selection.TokenBudget {
		return ContextAssembly{}, errors.New("redacted Skill context exceeds the persisted selection budget")
	}
	assembly := ContextAssembly{
		ProtocolVersion: ContextProtocolVersion, SelectionID: selection.ID,
		RunID: selection.RunID, MissionID: selection.MissionID, Surface: execution.Surface,
		Phase: execution.Phase, Profile: selection.Profile, Role: execution.Role,
		ModeBound:            modeBound,
		SelectionFingerprint: selection.Fingerprint, SelectionItemCount: selection.ItemCount,
		TokenBudget:     selection.TokenBudget,
		TokenUpperBound: totalTokens, ItemCount: len(items), RedactionCount: totalRedactions,
		Items: items,
	}
	assembly.Fingerprint = ContextFingerprint(assembly)
	if err := assembly.Validate(); err != nil {
		return ContextAssembly{}, err
	}
	return assembly, nil
}

func (a ContextAssembly) Validate() error {
	if a.ProtocolVersion != ContextProtocolVersion {
		return fmt.Errorf("unsupported Skill context protocol %q", a.ProtocolVersion)
	}
	if !validSelectionIdentity(a.SelectionID) || !validSelectionIdentity(a.RunID) ||
		!validSelectionIdentity(a.MissionID) || !validSHA256(a.SelectionFingerprint) {
		return errors.New("skill context selection and Run provenance is invalid")
	}
	profile, err := domain.ParseProfile(string(a.Profile))
	if err != nil || profile != a.Profile {
		return fmt.Errorf("invalid Skill context profile %q", a.Profile)
	}
	execution := ExecutionContext{Surface: a.Surface, Phase: a.Phase, Profile: a.Profile, Role: a.Role}
	if err := execution.Validate(); err != nil {
		return err
	}
	if a.SelectionItemCount <= 0 || a.SelectionItemCount > MaxSelectionItems ||
		a.TokenBudget <= 0 || a.TokenBudget > MaxSelectionTokenBudget ||
		a.TokenUpperBound < 0 || a.TokenUpperBound > a.TokenBudget {
		return errors.New("skill context token accounting is invalid")
	}
	if len(a.Items) > a.SelectionItemCount || a.ItemCount != len(a.Items) ||
		a.RedactionCount < 0 || a.RedactionCount > a.TokenBudget {
		return errors.New("skill context item or redaction accounting is invalid")
	}
	if len(a.Items) == 0 {
		if !a.ModeBound || a.TokenUpperBound != 0 || a.RedactionCount != 0 {
			return errors.New("empty Skill context must be mode-bound with zero accounting")
		}
	} else if a.TokenUpperBound == 0 {
		return errors.New("non-empty Skill context requires a positive token bound")
	}
	totalTokens := 0
	totalRedactions := 0
	previousName := ""
	for index, item := range a.Items {
		if item.Ordinal != index+1 || !validName(item.Name) || !validCoreVersion(item.Version) ||
			!validSHA256(item.SourceSHA256) || !validSHA256(item.DeliveredSHA256) ||
			item.SourceBytes <= 0 || item.SourceBytes > MaxContentBytes ||
			item.SourceTokenUpperBound <= 0 || item.SourceTokenUpperBound > MaxContentTokenUpperBound ||
			item.SourceTokenUpperBound != item.SourceBytes || item.DeliveredBytes <= 0 ||
			item.TokenUpperBound <= 0 || item.TokenUpperBound != item.DeliveredBytes ||
			item.TokenUpperBound > item.SourceTokenUpperBound || item.RedactionCount < 0 {
			return fmt.Errorf("skill context item %d is invalid", index+1)
		}
		if previousName != "" && previousName >= item.Name {
			return errors.New("skill context items must be unique and sorted")
		}
		content := []byte(item.Content)
		if err := validateContextContent(content); err != nil || len(content) != item.DeliveredBytes {
			return fmt.Errorf("skill context item %d content is invalid", index+1)
		}
		digest := sha256.Sum256(content)
		if item.DeliveredSHA256 != hex.EncodeToString(digest[:]) {
			return fmt.Errorf("skill context item %d delivery hash is invalid", index+1)
		}
		totalTokens += item.TokenUpperBound
		totalRedactions += item.RedactionCount
		previousName = item.Name
	}
	if totalTokens != a.TokenUpperBound || totalRedactions != a.RedactionCount {
		return errors.New("skill context aggregate accounting is inconsistent")
	}
	if !validSHA256(a.Fingerprint) || a.Fingerprint != ContextFingerprint(a) {
		return errors.New("skill context fingerprint is invalid")
	}
	return nil
}

func (a ContextAssembly) Preparation(rootAgentID string, supervisorAttemptID string,
	turn int,
) RootContextPreparationRequest {
	return RootContextPreparationRequest{
		RunID: a.RunID, MissionID: a.MissionID, RootAgentID: rootAgentID,
		SupervisorAttemptID: supervisorAttemptID, Turn: turn, SelectionID: a.SelectionID,
		ProtocolVersion: a.ProtocolVersion, Profile: a.Profile,
		SelectionFingerprint: a.SelectionFingerprint, ContextFingerprint: a.Fingerprint,
		ItemCount: a.ItemCount, TokenBudget: a.TokenBudget,
		TokenUpperBound: a.TokenUpperBound, RedactionCount: a.RedactionCount,
	}
}

// PreparationForMode binds a mode-aware root delivery, including an empty
// compatible subset, to the exact immutable Run mode snapshot used to build it.
func (a ContextAssembly) PreparationForMode(mode domain.RunModeSnapshot,
	rootAgentID string, supervisorAttemptID string, turn int,
) (RootContextPreparationRequest, error) {
	if err := mode.Validate(); err != nil {
		return RootContextPreparationRequest{}, err
	}
	if a.RunID != mode.RunID || a.MissionID != mode.MissionID ||
		a.Surface != mode.Surface || a.Phase != mode.Phase || a.Profile != mode.Profile ||
		a.Role != domain.AgentRoleRoot {
		return RootContextPreparationRequest{}, errors.New(
			"root Skill context does not match the immutable Run mode snapshot")
	}
	request := a.Preparation(rootAgentID, supervisorAttemptID, turn)
	request.ModeSnapshotID = mode.ID
	request.ModeRevision = mode.Revision
	request.Surface = mode.Surface
	request.Phase = mode.Phase
	request.SelectionItemCount = a.SelectionItemCount
	if err := request.Validate(); err != nil {
		return RootContextPreparationRequest{}, err
	}
	return request, nil
}

func (r RootContextPreparationRequest) ModeBound() bool {
	return r.ModeSnapshotID != "" || r.ModeRevision != 0 || r.Surface != "" ||
		r.Phase != "" || r.SelectionItemCount != 0
}

func (r RootContextPreparationRequest) Validate() error {
	for _, value := range []string{
		r.RunID, r.MissionID, r.RootAgentID, r.SupervisorAttemptID, r.SelectionID,
	} {
		if !validSelectionIdentity(value) {
			return errors.New("root Skill context identities must be normalized and bounded")
		}
	}
	if r.Turn <= 0 || r.ProtocolVersion != ContextProtocolVersion {
		return errors.New("root Skill context turn or protocol is invalid")
	}
	profile, err := domain.ParseProfile(string(r.Profile))
	if err != nil || profile != r.Profile {
		return fmt.Errorf("invalid root Skill context profile %q", r.Profile)
	}
	if !validSHA256(r.SelectionFingerprint) || !validSHA256(r.ContextFingerprint) {
		return errors.New("root Skill context fingerprints are invalid")
	}
	if r.TokenBudget <= 0 || r.TokenBudget > MaxSelectionTokenBudget ||
		r.RedactionCount < 0 || r.RedactionCount > r.TokenBudget {
		return errors.New("root Skill context bounds are invalid")
	}
	if !r.ModeBound() {
		if r.ItemCount <= 0 || r.ItemCount > MaxSelectionItems ||
			r.TokenUpperBound <= 0 || r.TokenUpperBound > r.TokenBudget {
			return errors.New("legacy root Skill context bounds are invalid")
		}
		return nil
	}
	if !validSelectionIdentity(r.ModeSnapshotID) || r.ModeRevision <= 0 ||
		!r.Surface.Valid() || !r.Phase.Valid() ||
		r.SelectionItemCount <= 0 || r.SelectionItemCount > MaxSelectionItems ||
		r.ItemCount < 0 || r.ItemCount > r.SelectionItemCount ||
		r.TokenUpperBound < 0 || r.TokenUpperBound > r.TokenBudget ||
		(r.ItemCount == 0 && (r.TokenUpperBound != 0 || r.RedactionCount != 0)) ||
		(r.ItemCount > 0 && r.TokenUpperBound == 0) {
		return errors.New("mode-bound root Skill context bounds are invalid")
	}
	return nil
}

func (p RootContextPreparation) Validate() error {
	if !validSelectionIdentity(p.ID) || p.PreparedAt.IsZero() {
		return errors.New("root Skill context preparation identity and timestamp are required")
	}
	return p.RootContextPreparationRequest.Validate()
}

func (c RootContextCommit) Validate() error {
	if !validSelectionIdentity(c.PreparationID) || !validSelectionIdentity(c.RunID) ||
		!validSelectionIdentity(c.SupervisorAttemptID) || c.ModelAttempt <= 0 ||
		c.CommittedAt.IsZero() {
		return errors.New("root Skill context commit is invalid")
	}
	return nil
}

func ContextFingerprint(assembly ContextAssembly) string {
	parts := []string{
		ContextProtocolVersion, assembly.SelectionID, assembly.RunID, assembly.MissionID,
		string(assembly.Profile), assembly.SelectionFingerprint,
		strconv.Itoa(assembly.TokenBudget), strconv.Itoa(len(assembly.Items)),
	}
	if assembly.ModeBound {
		parts = append(parts, "skill_mode.v1", string(assembly.Surface),
			string(assembly.Phase), string(assembly.Role))
	}
	for _, item := range assembly.Items {
		parts = append(parts, strconv.Itoa(item.Ordinal), item.Name, item.Version,
			item.SourceSHA256, strconv.Itoa(item.SourceBytes),
			strconv.Itoa(item.SourceTokenUpperBound), item.DeliveredSHA256,
			strconv.Itoa(item.DeliveredBytes), strconv.Itoa(item.TokenUpperBound),
			strconv.Itoa(item.RedactionCount))
	}
	return runmutation.Fingerprint(parts...)
}

func validateContextContent(content []byte) error {
	if len(content) == 0 || len(content) > MaxContentBytes || !utf8.Valid(content) {
		return fmt.Errorf("content must be valid UTF-8 between 1 and %d bytes", MaxContentBytes)
	}
	for _, current := range string(content) {
		if current == 0 || (unicode.IsControl(current) && current != '\n' && current != '\r' && current != '\t') {
			return errors.New("content contains a forbidden control character")
		}
	}
	if strings.TrimSpace(string(content)) == "" {
		return errors.New("content cannot contain only whitespace")
	}
	return nil
}
