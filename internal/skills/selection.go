package skills

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/runmutation"
)

const (
	SelectionProtocolVersion    = "skill_selection.v1"
	MaxSelectionItems           = 8
	DefaultSelectionTokenBudget = 4096
	MaxSelectionTokenBudget     = 8192
)

type SelectionItem struct {
	SelectionID     string
	Ordinal         int
	Name            string
	Version         string
	ContentSHA256   string
	ContentBytes    int
	TokenUpperBound int
}

type Selection struct {
	ID              string
	RunID           string
	MissionID       string
	ProtocolVersion string
	Profile         domain.Profile
	TokenBudget     int
	TokenUpperBound int
	ItemCount       int
	Fingerprint     string
	RequestedBy     string
	Items           []SelectionItem
	CreatedAt       time.Time
}

type SelectionOperation struct {
	KeyDigest          string
	RequestFingerprint string
	SelectionID        string
	RunID              string
	RequestedBy        string
	CreatedAt          time.Time
}

type ResolveSelectionRequest struct {
	SelectionID string
	RunID       string
	MissionID   string
	Surface     domain.ExecutionSurface
	Phase       domain.ExecutionPhase
	Profile     domain.Profile
	Names       []string
	TokenBudget int
	Invocation  InvocationSource
	Explicit    bool
	RequestedBy string
	CreatedAt   time.Time
}

func (r *Registry) ResolveSelection(request ResolveSelectionRequest) (Selection, error) {
	if r == nil {
		return Selection{}, errors.New("skill registry is required")
	}
	if err := r.Validate(); err != nil {
		return Selection{}, err
	}
	request.SelectionID = strings.TrimSpace(request.SelectionID)
	request.RunID = strings.TrimSpace(request.RunID)
	request.MissionID = strings.TrimSpace(request.MissionID)
	request.RequestedBy = strings.TrimSpace(request.RequestedBy)
	request.CreatedAt = request.CreatedAt.UTC()
	if request.Surface == "" {
		request.Surface = domain.ExecutionSurfaceCode
	}
	if request.Phase == "" {
		request.Phase = domain.ExecutionPhaseDeliver
	}
	if request.Invocation == "" {
		request.Invocation = InvocationSourceUser
		request.Explicit = true
	}
	if !request.Surface.Valid() || !request.Phase.Valid() {
		return Selection{}, errors.New("skill selection surface or phase is invalid")
	}
	if !request.Invocation.Valid() {
		return Selection{}, fmt.Errorf("invalid Skill invocation source %q", request.Invocation)
	}
	profile, err := domain.ParseProfile(string(request.Profile))
	if err != nil || profile != request.Profile {
		return Selection{}, fmt.Errorf("invalid Skill selection profile %q", request.Profile)
	}
	if len(request.Names) == 0 || len(request.Names) > MaxSelectionItems {
		return Selection{}, fmt.Errorf("skill selection requires between 1 and %d names", MaxSelectionItems)
	}
	if request.TokenBudget <= 0 || request.TokenBudget > MaxSelectionTokenBudget {
		return Selection{}, fmt.Errorf("skill selection token budget must be between 1 and %d", MaxSelectionTokenBudget)
	}
	names := make([]string, len(request.Names))
	for index, name := range request.Names {
		names[index] = strings.TrimSpace(name)
		if !validName(names[index]) {
			return Selection{}, fmt.Errorf("invalid selected Skill name %q", name)
		}
	}
	sort.Strings(names)
	items := make([]SelectionItem, 0, len(names))
	manifests := make([]Manifest, 0, len(names))
	tokenUpperBound := 0
	for index, name := range names {
		if index > 0 && name == names[index-1] {
			return Selection{}, fmt.Errorf("selected Skill %q is duplicated", name)
		}
		manifest, ok := r.Get(name)
		if !ok {
			return Selection{}, fmt.Errorf("selected Skill %q was not found", name)
		}
		if !manifestSupportsRootRun(manifest, request.Surface, profile) {
			return Selection{}, fmt.Errorf("selected Skill %q is incompatible with surface %q and profile %q",
				name, request.Surface, profile)
		}
		if !manifest.AllowsInvocation(request.Invocation, request.Explicit) {
			return Selection{}, fmt.Errorf("selected Skill %q does not allow %s invocation under the requested explicitness policy",
				name, request.Invocation)
		}
		item := SelectionItem{
			SelectionID: request.SelectionID, Ordinal: index + 1,
			Name: name, Version: manifest.Version,
			ContentSHA256: manifest.ContentSHA256, ContentBytes: manifest.ContentBytes,
			TokenUpperBound: manifest.ContentTokenUpperBound,
		}
		items = append(items, item)
		manifests = append(manifests, manifest)
		tokenUpperBound += item.TokenUpperBound
	}
	if err := validateSelectionModeCoverage(manifests, request.Surface, profile); err != nil {
		return Selection{}, err
	}
	if tokenUpperBound > request.TokenBudget {
		return Selection{}, fmt.Errorf("selected Skills require token upper bound %d, budget is %d", tokenUpperBound, request.TokenBudget)
	}
	selection := Selection{
		ID: request.SelectionID, RunID: request.RunID, MissionID: request.MissionID,
		ProtocolVersion: SelectionProtocolVersion, Profile: profile,
		TokenBudget: request.TokenBudget, TokenUpperBound: tokenUpperBound,
		ItemCount: len(items), RequestedBy: request.RequestedBy,
		Items: items, CreatedAt: request.CreatedAt,
	}
	selection.Fingerprint = SelectionFingerprint(selection)
	if err := selection.Validate(); err != nil {
		return Selection{}, err
	}
	return selection, nil
}

func manifestSupportsRootRun(manifest Manifest, surface domain.ExecutionSurface,
	profile domain.Profile,
) bool {
	for _, phase := range []domain.ExecutionPhase{
		domain.ExecutionPhasePlan, domain.ExecutionPhaseDeliver,
	} {
		if manifest.SupportsContext(ExecutionContext{
			Surface: surface, Phase: phase, Profile: profile, Role: domain.AgentRoleRoot,
		}) {
			return true
		}
	}
	return false
}

// validateSelectionModeCoverage permits phase-specific root subsets while
// retaining the single-guide Specialist bound in each phase.
func validateSelectionModeCoverage(manifests []Manifest, surface domain.ExecutionSurface,
	profile domain.Profile,
) error {
	for _, manifest := range manifests {
		if !manifestSupportsRootRun(manifest, surface, profile) {
			return fmt.Errorf("selected Skill %q must support root delivery in at least one phase",
				manifest.Name)
		}
	}
	for _, phase := range []domain.ExecutionPhase{
		domain.ExecutionPhasePlan, domain.ExecutionPhaseDeliver,
	} {
		specialistCount := 0
		for _, manifest := range manifests {
			if manifest.SupportsContext(ExecutionContext{
				Surface: surface, Phase: phase, Profile: profile,
				Role: domain.AgentRoleSpecialist,
			}) {
				specialistCount++
			}
		}
		if specialistCount > MaxSpecialistContextItems {
			return fmt.Errorf("selected Skills expose %d Specialist guides in %q phase, maximum is %d",
				specialistCount, phase, MaxSpecialistContextItems)
		}
	}
	return nil
}

func (s Selection) Validate() error {
	if !validSelectionIdentity(s.ID) || !validSelectionIdentity(s.RunID) ||
		!validSelectionIdentity(s.MissionID) || !validSelectionIdentity(s.RequestedBy) {
		return errors.New("skill selection identities must be normalized and bounded UTF-8")
	}
	if s.ProtocolVersion != SelectionProtocolVersion {
		return fmt.Errorf("unsupported Skill selection protocol %q", s.ProtocolVersion)
	}
	profile, err := domain.ParseProfile(string(s.Profile))
	if err != nil || profile != s.Profile {
		return fmt.Errorf("invalid Skill selection profile %q", s.Profile)
	}
	if s.TokenBudget <= 0 || s.TokenBudget > MaxSelectionTokenBudget {
		return fmt.Errorf("skill selection token budget must be between 1 and %d", MaxSelectionTokenBudget)
	}
	if len(s.Items) == 0 || len(s.Items) > MaxSelectionItems || s.ItemCount != len(s.Items) {
		return fmt.Errorf("skill selection must contain between 1 and %d consistent items", MaxSelectionItems)
	}
	total := 0
	previous := ""
	for index, item := range s.Items {
		if item.SelectionID != s.ID || item.Ordinal != index+1 || !validName(item.Name) ||
			!validCoreVersion(item.Version) || !validSHA256(item.ContentSHA256) ||
			item.ContentBytes <= 0 || item.ContentBytes > MaxContentBytes ||
			item.TokenUpperBound <= 0 || item.TokenUpperBound > MaxContentTokenUpperBound ||
			item.TokenUpperBound != item.ContentBytes {
			return fmt.Errorf("skill selection item %d is invalid", index+1)
		}
		if previous != "" && previous >= item.Name {
			return errors.New("skill selection items must be unique and sorted")
		}
		previous = item.Name
		total += item.TokenUpperBound
	}
	if total != s.TokenUpperBound || total > s.TokenBudget {
		return errors.New("skill selection token accounting is inconsistent")
	}
	if !validSHA256(s.Fingerprint) || s.Fingerprint != SelectionFingerprint(s) {
		return errors.New("skill selection fingerprint is invalid")
	}
	if s.CreatedAt.IsZero() {
		return errors.New("skill selection creation time is required")
	}
	return nil
}

func (o SelectionOperation) Validate() error {
	if !validSHA256(o.KeyDigest) || !validSHA256(o.RequestFingerprint) ||
		!validSelectionIdentity(o.SelectionID) || !validSelectionIdentity(o.RunID) ||
		!validSelectionIdentity(o.RequestedBy) || o.CreatedAt.IsZero() {
		return errors.New("skill selection operation is invalid")
	}
	return nil
}

func SelectionFingerprint(selection Selection) string {
	parts := []string{
		SelectionProtocolVersion, selection.RunID, selection.MissionID,
		string(selection.Profile), strconv.Itoa(selection.TokenBudget),
		strconv.Itoa(len(selection.Items)), selection.RequestedBy,
	}
	for _, item := range selection.Items {
		parts = append(parts, strconv.Itoa(item.Ordinal), item.Name, item.Version,
			item.ContentSHA256, strconv.Itoa(item.ContentBytes),
			strconv.Itoa(item.TokenUpperBound))
	}
	return runmutation.Fingerprint(parts...)
}

func SelectionRequestFingerprint(selection Selection) string {
	names := make([]string, len(selection.Items))
	for index, item := range selection.Items {
		names[index] = item.Name
	}
	return SelectionIntentFingerprint(selection.RunID, selection.MissionID,
		selection.Profile, selection.TokenBudget, names, selection.RequestedBy)
}

// SelectionIntentFingerprint binds an idempotent operation to operator intent,
// independent of the Registry version that resolves that intent into pinned content.
func SelectionIntentFingerprint(runID, missionID string, profile domain.Profile,
	tokenBudget int, names []string, requestedBy string,
) string {
	orderedNames := append([]string(nil), names...)
	sort.Strings(orderedNames)
	parts := []string{
		"skill_selection_request.v1", runID, missionID, string(profile),
		strconv.Itoa(tokenBudget), strconv.Itoa(len(orderedNames)), requestedBy,
	}
	parts = append(parts, orderedNames...)
	return runmutation.Fingerprint(parts...)
}

func CloneSelection(selection Selection) Selection {
	selection.Items = append([]SelectionItem(nil), selection.Items...)
	return selection
}

func validSelectionIdentity(value string) bool {
	if !domain.ValidAgentID(value) || !utf8.ValidString(value) {
		return false
	}
	for _, current := range value {
		if unicode.IsControl(current) {
			return false
		}
	}
	return true
}
