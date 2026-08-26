// Package surfacegovernance validates the non-authorizing product support
// registry defined by ADR 0135. It is build-time governance tooling only; the
// runtime must never consult this package to grant a capability or authority.
package surfacegovernance

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
)

const RegistryVersion = "surface-tier-registry.v1"

var requiredTierIDs = []string{
	"active",
	"maintenance-only",
	"extension-only",
	"deferred",
}

var requiredEntryCriteria = []string{
	"owner",
	"user-problem",
	"shared-go-application-contract",
	"authority-impact",
	"supported-platforms",
	"release-test-evidence",
	"compatibility-strategy",
	"deprecation-window",
	"removal-rollback-plan",
	"independently-removable-slice",
}

type Registry struct {
	Version          string         `json:"version"`
	Authority        string         `json:"authority"`
	EvidenceBaseline string         `json:"evidence_baseline"`
	ReassessBy       string         `json:"reassess_by"`
	Governance       Governance     `json:"governance"`
	Owners           []Owner        `json:"owners"`
	Tiers            []TierPolicy   `json:"tiers"`
	Entries          []SurfaceEntry `json:"entries"`
}

type Governance struct {
	EntryCriteria            []EntryCriterion `json:"entry_criteria"`
	DefaultDeprecationDays   int              `json:"default_deprecation_days"`
	MinimumTaggedPrereleases int              `json:"minimum_tagged_prereleases"`
	AuthorityStatement       string           `json:"authority_statement"`
	RollbackStatement        string           `json:"rollback_statement"`
}

type EntryCriterion struct {
	ID          string `json:"id"`
	Description string `json:"description"`
}

type Owner struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Responsibilities []string `json:"responsibilities"`
}

type TierPolicy struct {
	ID                 string   `json:"id"`
	ProductCommitment  string   `json:"product_commitment"`
	AllowedChanges     []string `json:"allowed_changes"`
	NewWorkflowPolicy  string   `json:"new_workflow_policy"`
	CoreReleaseBlocker bool     `json:"core_release_blocker"`
}

type SurfaceEntry struct {
	ID                     string    `json:"id"`
	Name                   string    `json:"name"`
	Kind                   string    `json:"kind"`
	Category               string    `json:"category"`
	CountsAsProductSurface bool      `json:"counts_as_product_surface"`
	Tier                   string    `json:"tier"`
	Owner                  string    `json:"owner"`
	SupportedPlatforms     []string  `json:"supported_platforms"`
	ApplicationContract    string    `json:"application_contract"`
	AuthorityImpact        string    `json:"authority_impact"`
	ReleaseBlockingChecks  []string  `json:"release_blocking_checks"`
	ContractChecks         []string  `json:"contract_checks"`
	RecoveryEvidence       []string  `json:"recovery_evidence"`
	CompatibilityStrategy  string    `json:"compatibility_strategy"`
	DeprecationWindow      string    `json:"deprecation_window"`
	RemovalRollbackPlan    string    `json:"removal_rollback_plan"`
	CoreReleaseBlocker     bool      `json:"core_release_blocker"`
	GoOwnedControls        []string  `json:"go_owned_controls"`
	Boundary               string    `json:"boundary"`
	Lifecycle              Lifecycle `json:"lifecycle"`
}

type Lifecycle struct {
	RegisteredTier string       `json:"registered_tier"`
	RegisteredBy   string       `json:"registered_by"`
	Status         string       `json:"status"`
	Transitions    []Transition `json:"transitions"`
}

type Transition struct {
	From                   string   `json:"from"`
	To                     string   `json:"to"`
	Decision               string   `json:"decision"`
	EntryReview            string   `json:"entry_review"`
	EffectiveOn            string   `json:"effective_on"`
	ReleaseNotes           string   `json:"release_notes"`
	RollbackPlan           string   `json:"rollback_plan"`
	Replacement            string   `json:"replacement"`
	DeprecationNoticeOn    string   `json:"deprecation_notice_on"`
	RemovalEligibleOn      string   `json:"removal_eligible_on"`
	TaggedPrereleases      []string `json:"tagged_prereleases"`
	ExportRecoveryEvidence []string `json:"export_recovery_evidence"`
	OldClientFixtures      []string `json:"old_client_fixtures"`
	OldStoreFixtures       []string `json:"old_store_fixtures"`
	EmergencyAdvisory      string   `json:"emergency_advisory"`
}

func LoadFile(path string) (Registry, error) {
	file, err := os.Open(path)
	if err != nil {
		return Registry{}, err
	}
	defer file.Close()
	return Decode(file)
}

func Decode(reader io.Reader) (Registry, error) {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	var registry Registry
	if err := decoder.Decode(&registry); err != nil {
		return Registry{}, fmt.Errorf("decode Surface registry: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Registry{}, fmt.Errorf("decode Surface registry: unexpected trailing JSON value")
		}
		return Registry{}, fmt.Errorf("decode Surface registry trailer: %w", err)
	}
	return registry, nil
}

func Validate(registry Registry) error {
	var problems []string
	add := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}

	if registry.Version != RegistryVersion {
		add("version = %q, want %q", registry.Version, RegistryVersion)
	}
	requireText(add, "authority", registry.Authority)
	requireText(add, "evidence_baseline", registry.EvidenceBaseline)
	if _, err := time.Parse("2006-01-02", registry.ReassessBy); err != nil {
		add("reassess_by must be an ISO date: %v", err)
	}
	validateGovernance(registry.Governance, add)

	ownerIDs := make(map[string]struct{}, len(registry.Owners))
	for i, owner := range registry.Owners {
		prefix := fmt.Sprintf("owners[%d]", i)
		requireText(add, prefix+".id", owner.ID)
		requireText(add, prefix+".name", owner.Name)
		requireList(add, prefix+".responsibilities", owner.Responsibilities)
		if _, exists := ownerIDs[owner.ID]; exists {
			add("duplicate owner id %q", owner.ID)
		}
		ownerIDs[owner.ID] = struct{}{}
	}

	tiers := make(map[string]TierPolicy, len(registry.Tiers))
	for i, tier := range registry.Tiers {
		prefix := fmt.Sprintf("tiers[%d]", i)
		requireText(add, prefix+".id", tier.ID)
		requireText(add, prefix+".product_commitment", tier.ProductCommitment)
		requireList(add, prefix+".allowed_changes", tier.AllowedChanges)
		requireText(add, prefix+".new_workflow_policy", tier.NewWorkflowPolicy)
		if _, exists := tiers[tier.ID]; exists {
			add("duplicate tier id %q", tier.ID)
		}
		tiers[tier.ID] = tier
	}
	validateTierPolicies(tiers, add)

	entryIDs := make(map[string]struct{}, len(registry.Entries))
	for i := range registry.Entries {
		entry := registry.Entries[i]
		prefix := fmt.Sprintf("entries[%d] (%s)", i, entry.ID)
		requireText(add, prefix+".id", entry.ID)
		requireText(add, prefix+".name", entry.Name)
		requireText(add, prefix+".kind", entry.Kind)
		validateCategory(entry, prefix, add)
		if _, exists := entryIDs[entry.ID]; exists {
			add("duplicate entry id %q", entry.ID)
		}
		entryIDs[entry.ID] = struct{}{}
		if _, exists := tiers[entry.Tier]; !exists {
			add("%s.tier %q is not registered", prefix, entry.Tier)
		}
		if _, exists := ownerIDs[entry.Owner]; !exists {
			add("%s.owner %q is not registered", prefix, entry.Owner)
		}
		requireList(add, prefix+".supported_platforms", entry.SupportedPlatforms)
		requireText(add, prefix+".application_contract", entry.ApplicationContract)
		requireText(add, prefix+".authority_impact", entry.AuthorityImpact)
		requireList(add, prefix+".contract_checks", entry.ContractChecks)
		requireList(add, prefix+".recovery_evidence", entry.RecoveryEvidence)
		requireText(add, prefix+".compatibility_strategy", entry.CompatibilityStrategy)
		requireText(add, prefix+".deprecation_window", entry.DeprecationWindow)
		requireText(add, prefix+".removal_rollback_plan", entry.RemovalRollbackPlan)
		requireText(add, prefix+".boundary", entry.Boundary)
		validateUniqueStrings(entry.SupportedPlatforms, prefix+".supported_platforms", add)
		validateUniqueStrings(entry.ReleaseBlockingChecks, prefix+".release_blocking_checks", add)
		validateUniqueStrings(entry.ContractChecks, prefix+".contract_checks", add)
		validateUniqueStrings(entry.RecoveryEvidence, prefix+".recovery_evidence", add)
		validateEntryTier(entry, prefix, add)
		validateLifecycle(entry, registry.Governance, prefix, add)
	}
	if len(registry.Entries) == 0 {
		add("entries must not be empty")
	}

	if len(problems) != 0 {
		sort.Strings(problems)
		return fmt.Errorf("Surface registry is invalid:\n- %s", strings.Join(problems, "\n- "))
	}
	return nil
}

func validateGovernance(governance Governance, add func(string, ...any)) {
	if governance.DefaultDeprecationDays < 90 {
		add("governance.default_deprecation_days = %d, must be at least 90",
			governance.DefaultDeprecationDays)
	}
	if governance.MinimumTaggedPrereleases < 2 {
		add("governance.minimum_tagged_prereleases = %d, must be at least 2",
			governance.MinimumTaggedPrereleases)
	}
	requireText(add, "governance.authority_statement", governance.AuthorityStatement)
	requireText(add, "governance.rollback_statement", governance.RollbackStatement)
	criteria := make(map[string]struct{}, len(governance.EntryCriteria))
	for i, criterion := range governance.EntryCriteria {
		prefix := fmt.Sprintf("governance.entry_criteria[%d]", i)
		requireText(add, prefix+".id", criterion.ID)
		requireText(add, prefix+".description", criterion.Description)
		if _, exists := criteria[criterion.ID]; exists {
			add("duplicate entry criterion %q", criterion.ID)
		}
		criteria[criterion.ID] = struct{}{}
	}
	for _, id := range requiredEntryCriteria {
		if _, exists := criteria[id]; !exists {
			add("governance.entry_criteria is missing %q", id)
		}
	}
}

func validateTierPolicies(tiers map[string]TierPolicy, add func(string, ...any)) {
	for _, id := range requiredTierIDs {
		if _, exists := tiers[id]; !exists {
			add("missing tier policy %q", id)
		}
	}
	if len(tiers) != len(requiredTierIDs) {
		add("tier registry contains %d policies, want exactly %d", len(tiers), len(requiredTierIDs))
	}

	expectTier(tiers, "active",
		[]string{"features", "security", "compatibility", "accessibility", "recovery", "release"},
		"allowed", true, add)
	expectTier(tiers, "maintenance-only",
		[]string{"security", "compatibility", "data-loss", "severe-defect"},
		"requires-active-promotion", false, add)
	expectTier(tiers, "extension-only",
		[]string{"contract", "security", "compatibility", "independently-approved-extension"},
		"requires-independent-approval", false, add)
	expectTier(tiers, "deferred",
		[]string{"research", "adr"},
		"requires-entry-review", false, add)
}

func expectTier(
	tiers map[string]TierPolicy,
	id string,
	allowed []string,
	workflowPolicy string,
	coreReleaseBlocker bool,
	add func(string, ...any),
) {
	tier, exists := tiers[id]
	if !exists {
		return
	}
	if !sameStringSet(tier.AllowedChanges, allowed) {
		add("tier %q allowed_changes = %v, want exactly %v", id, tier.AllowedChanges, allowed)
	}
	if tier.NewWorkflowPolicy != workflowPolicy {
		add("tier %q new_workflow_policy = %q, want %q", id, tier.NewWorkflowPolicy, workflowPolicy)
	}
	if tier.CoreReleaseBlocker != coreReleaseBlocker {
		add("tier %q core_release_blocker = %t, want %t", id, tier.CoreReleaseBlocker, coreReleaseBlocker)
	}
}

func validateCategory(entry SurfaceEntry, prefix string, add func(string, ...any)) {
	allowed := map[string]bool{
		"user-surface":          true,
		"control-surface":       true,
		"automation-surface":    true,
		"compatibility-surface": true,
		"extension-seam":        true,
		"backend":               true,
		"capability":            true,
		"product-surface":       true,
		"distribution":          true,
	}
	if !allowed[entry.Category] {
		add("%s.category %q is not supported", prefix, entry.Category)
	}
	if (entry.Category == "backend" || entry.Category == "capability" ||
		entry.Category == "extension-seam") && entry.CountsAsProductSurface {
		add("%s cannot count category %q as an independent product Surface", prefix, entry.Category)
	}
}

func validateEntryTier(entry SurfaceEntry, prefix string, add func(string, ...any)) {
	switch entry.Tier {
	case "active":
		requireList(add, prefix+".release_blocking_checks", entry.ReleaseBlockingChecks)
		if !entry.CoreReleaseBlocker {
			add("%s active entry must bind release-blocking evidence", prefix)
		}
	case "maintenance-only":
		if entry.CoreReleaseBlocker {
			add("%s maintenance-only entry cannot be a core release blocker", prefix)
		}
		if len(entry.ReleaseBlockingChecks) != 0 {
			add("%s maintenance-only entry must use contract_checks, not release_blocking_checks", prefix)
		}
	case "extension-only":
		if entry.CoreReleaseBlocker {
			add("%s extension-only entry cannot be a core release blocker", prefix)
		}
		if len(entry.ReleaseBlockingChecks) != 0 {
			add("%s extension-only entry must not define release_blocking_checks", prefix)
		}
		for _, control := range []string{"policy", "approval", "scope"} {
			if !contains(entry.GoOwnedControls, control) {
				add("%s extension-only entry must retain Go-owned %s control", prefix, control)
			}
		}
	case "deferred":
		if entry.CoreReleaseBlocker {
			add("%s deferred entry cannot be a core release blocker", prefix)
		}
		if len(entry.ReleaseBlockingChecks) != 0 {
			add("%s deferred entry must not define release_blocking_checks", prefix)
		}
	}
}

func validateLifecycle(
	entry SurfaceEntry,
	governance Governance,
	prefix string,
	add func(string, ...any),
) {
	lifecycle := entry.Lifecycle
	if !contains(requiredTierIDs, lifecycle.RegisteredTier) {
		add("%s.lifecycle.registered_tier %q is invalid", prefix, lifecycle.RegisteredTier)
	}
	requireText(add, prefix+".lifecycle.registered_by", lifecycle.RegisteredBy)
	if lifecycle.RegisteredBy != "" && !strings.HasPrefix(lifecycle.RegisteredBy, "docs/adr/") {
		add("%s.lifecycle.registered_by must identify a bounded ADR under docs/adr/", prefix)
	}
	if lifecycle.Status != "supported" && lifecycle.Status != "removed" {
		add("%s.lifecycle.status %q must be supported or removed", prefix, lifecycle.Status)
	}
	current := lifecycle.RegisteredTier
	for i, transition := range lifecycle.Transitions {
		transitionPrefix := fmt.Sprintf("%s.lifecycle.transitions[%d]", prefix, i)
		if transition.From != current {
			add("%s.from = %q, want previous state %q", transitionPrefix, transition.From, current)
		}
		if !transitionAllowed(transition.From, transition.To, transition.EmergencyAdvisory != "") {
			add("%s transition %q -> %q is not allowed", transitionPrefix, transition.From, transition.To)
		}
		requireText(add, transitionPrefix+".decision", transition.Decision)
		if transition.Decision != "" && !strings.HasPrefix(transition.Decision, "docs/adr/") {
			add("%s.decision must identify a bounded ADR under docs/adr/", transitionPrefix)
		}
		requireText(add, transitionPrefix+".effective_on", transition.EffectiveOn)
		requireText(add, transitionPrefix+".release_notes", transition.ReleaseNotes)
		requireText(add, transitionPrefix+".rollback_plan", transition.RollbackPlan)
		if _, err := time.Parse("2006-01-02", transition.EffectiveOn); err != nil {
			add("%s.effective_on must be an ISO date: %v", transitionPrefix, err)
		}
		if transition.To == "active" || transition.From == "deferred" {
			requireText(add, transitionPrefix+".entry_review", transition.EntryReview)
		}
		if transition.From == "active" && transition.To == "maintenance-only" {
			requireText(add, transitionPrefix+".replacement", transition.Replacement)
			requireText(add, transitionPrefix+".deprecation_notice_on", transition.DeprecationNoticeOn)
			if _, err := time.Parse("2006-01-02", transition.DeprecationNoticeOn); err != nil {
				add("%s.deprecation_notice_on must be an ISO date: %v", transitionPrefix, err)
			}
		}
		if transition.To == "removed" {
			validateRemovalTransition(transition, governance, transitionPrefix, add)
		}
		current = transition.To
	}
	if lifecycle.Status == "removed" {
		if current != "removed" {
			add("%s.lifecycle.status is removed without a terminal removal transition", prefix)
		}
	} else if current != entry.Tier {
		add("%s.tier = %q but lifecycle ends at %q", prefix, entry.Tier, current)
	}
}

func validateRemovalTransition(
	transition Transition,
	governance Governance,
	prefix string,
	add func(string, ...any),
) {
	requireList(add, prefix+".export_recovery_evidence", transition.ExportRecoveryEvidence)
	requireList(add, prefix+".old_client_fixtures", transition.OldClientFixtures)
	requireList(add, prefix+".old_store_fixtures", transition.OldStoreFixtures)
	if transition.EmergencyAdvisory != "" {
		return
	}
	requireText(add, prefix+".deprecation_notice_on", transition.DeprecationNoticeOn)
	requireText(add, prefix+".removal_eligible_on", transition.RemovalEligibleOn)
	notice, noticeErr := time.Parse("2006-01-02", transition.DeprecationNoticeOn)
	eligible, eligibleErr := time.Parse("2006-01-02", transition.RemovalEligibleOn)
	if noticeErr != nil {
		add("%s.deprecation_notice_on must be an ISO date: %v", prefix, noticeErr)
	}
	if eligibleErr != nil {
		add("%s.removal_eligible_on must be an ISO date: %v", prefix, eligibleErr)
	}
	if noticeErr == nil && eligibleErr == nil {
		days := int(eligible.Sub(notice).Hours() / 24)
		if days < governance.DefaultDeprecationDays {
			add("%s removal window is %d days, must be at least %d",
				prefix, days, governance.DefaultDeprecationDays)
		}
		if effective, err := time.Parse("2006-01-02", transition.EffectiveOn); err == nil && effective.Before(eligible) {
			add("%s.effective_on precedes removal_eligible_on", prefix)
		}
	}
	if len(uniqueNonempty(transition.TaggedPrereleases)) < governance.MinimumTaggedPrereleases {
		add("%s has %d distinct tagged prereleases, must have at least %d",
			prefix, len(uniqueNonempty(transition.TaggedPrereleases)), governance.MinimumTaggedPrereleases)
	}
}

func transitionAllowed(from, to string, emergency bool) bool {
	if to == "removed" {
		return from == "maintenance-only" || from == "extension-only" ||
			from == "deferred" || (from == "active" && emergency)
	}
	switch from {
	case "active":
		return to == "maintenance-only"
	case "maintenance-only":
		return to == "active"
	case "extension-only":
		return to == "active"
	case "deferred":
		return to == "active" || to == "extension-only"
	default:
		return false
	}
}

func requireText(add func(string, ...any), field, value string) {
	if strings.TrimSpace(value) == "" {
		add("%s must not be empty", field)
	}
}

func requireList(add func(string, ...any), field string, values []string) {
	if len(uniqueNonempty(values)) == 0 {
		add("%s must contain at least one non-empty value", field)
	}
}

func validateUniqueStrings(values []string, field string, add func(string, ...any)) {
	seen := make(map[string]struct{}, len(values))
	for i, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			add("%s[%d] must not be empty", field, i)
			continue
		}
		if _, exists := seen[trimmed]; exists {
			add("%s contains duplicate %q", field, trimmed)
		}
		seen[trimmed] = struct{}{}
	}
}

func uniqueNonempty(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			result[trimmed] = struct{}{}
		}
	}
	return result
}

func sameStringSet(got, want []string) bool {
	gotSet := uniqueNonempty(got)
	wantSet := uniqueNonempty(want)
	if len(gotSet) != len(wantSet) {
		return false
	}
	for value := range wantSet {
		if _, exists := gotSet[value]; !exists {
			return false
		}
	}
	return true
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
