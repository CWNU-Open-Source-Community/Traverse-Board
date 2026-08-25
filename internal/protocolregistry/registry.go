// Package protocolregistry implements repository governance for versioned protocol
// identifiers. It is deliberately not a runtime source of authority or capability.
package protocolregistry

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	Schema            = "protocol-family-registry.v1"
	RegistryPath      = "protocols/registry.json"
	GeneratedDocument = "docs/convergence/protocol-registry.md"
	maxRegistryBytes  = 4 << 20
	ClassExternal     = "external-durable"
	ClassInternal     = "internal-durable"
	ClassProjection   = "projection"
	ClassEphemeral    = "ephemeral"
	ReaderActive      = "active"
	ReaderRetired     = "retired"
	WriterCurrent     = "write-current"
	WriterNew         = "write-new"
	GateMigration     = "migration-or-retention"
	GateRebuild       = "rebuild-from-source"
	GateRestart       = "restart-non-persistence"
)

var (
	familyIDPattern   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	protocolIDPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]*\.v([0-9]+)$`)
)

type Registry struct {
	Schema                 string                 `json:"schema"`
	Authority              AuthorityPolicy        `json:"authority"`
	Scan                   ScanPolicy             `json:"scan"`
	Families               []Family               `json:"families"`
	TestAndGoldenAllowlist []AllowlistEntry       `json:"test_and_golden_allowlist"`
	CompatibilityExamples  []CompatibilityExample `json:"compatibility_examples"`
}

type AuthorityPolicy struct {
	Scope                      string `json:"scope"`
	RuntimeAuthority           string `json:"runtime_authority"`
	CapabilityGrant            string `json:"capability_grant"`
	AutomaticProtocolDeletion  string `json:"automatic_protocol_deletion"`
	AutomaticDataMigration     string `json:"automatic_data_migration"`
	HistoricalChecksumMutation string `json:"historical_checksum_mutation"`
}

type ScanPolicy struct {
	Roots      []string `json:"roots"`
	Extensions []string `json:"extensions"`
}

type Family struct {
	ID                      string              `json:"id"`
	Class                   string              `json:"class"`
	Owner                   string              `json:"owner"`
	SourceOfTruth           []string            `json:"source_of_truth"`
	Writers                 []Writer            `json:"writers"`
	Readers                 []Reader            `json:"readers"`
	PersistedOrExported     bool                `json:"persisted_or_exported"`
	PersistenceBoundary     string              `json:"persistence_boundary"`
	CompatibilityRule       string              `json:"compatibility_rule"`
	RetirementGate          RetirementGate      `json:"retirement_gate"`
	RebuildSource           string              `json:"rebuild_source,omitempty"`
	RestartPersistenceProof string              `json:"restart_persistence_proof,omitempty"`
	ActiveIdentifiers       []string            `json:"active_identifiers"`
	RetiredIdentifiers      []RetiredIdentifier `json:"retired_identifiers"`
}

type Writer struct {
	ID       string `json:"id"`
	Source   string `json:"source"`
	Versions []int  `json:"versions"`
	Mode     string `json:"mode"`
}

type Reader struct {
	ID         string            `json:"id"`
	Source     string            `json:"source"`
	Versions   []int             `json:"versions"`
	Status     string            `json:"status"`
	Retirement *ReaderRetirement `json:"retirement,omitempty"`
}

type ReaderRetirement struct {
	Decision          string `json:"decision"`
	MigrationEvidence string `json:"migration_evidence"`
	Rollback          string `json:"rollback"`
}

type RetirementGate struct {
	Mode         string   `json:"mode"`
	Requirements []string `json:"requirements"`
}

type RetiredIdentifier struct {
	Identifier        string `json:"identifier"`
	Decision          string `json:"decision"`
	MigrationEvidence string `json:"migration_evidence,omitempty"`
	RebuildEvidence   string `json:"rebuild_evidence,omitempty"`
	RestartEvidence   string `json:"restart_evidence,omitempty"`
	Rollback          string `json:"rollback"`
}

type AllowlistEntry struct {
	Identifier string   `json:"identifier"`
	Kind       string   `json:"kind"`
	Sources    []string `json:"sources"`
	Reason     string   `json:"reason"`
}

type CompatibilityExample struct {
	ID                     string `json:"id"`
	Description            string `json:"description"`
	OldFixture             string `json:"old_fixture"`
	ReaderVersions         []int  `json:"reader_versions"`
	WriterVersion          int    `json:"writer_version"`
	UnknownVersionBehavior string `json:"unknown_version_behavior"`
	ReaderRetirementCheck  string `json:"reader_retirement_check"`
}

func LoadFile(path string) (Registry, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Registry{}, fmt.Errorf("stat protocol registry: %w", err)
	}
	if info.Size() > maxRegistryBytes {
		return Registry{}, fmt.Errorf("protocol registry exceeds %d bytes", maxRegistryBytes)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Registry{}, fmt.Errorf("read protocol registry: %w", err)
	}
	return Decode(raw)
}

func Decode(raw []byte) (Registry, error) {
	if len(raw) == 0 || len(raw) > maxRegistryBytes {
		return Registry{}, errors.New("protocol registry has an invalid size")
	}
	if err := rejectDuplicateJSONFields(raw); err != nil {
		return Registry{}, fmt.Errorf("decode protocol registry: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var registry Registry
	if err := decoder.Decode(&registry); err != nil {
		return Registry{}, fmt.Errorf("decode protocol registry: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return Registry{}, fmt.Errorf("decode protocol registry: %w", err)
	}
	if err := ValidateStructure(registry); err != nil {
		return Registry{}, err
	}
	return registry, nil
}

func ValidateStructure(registry Registry) error {
	if registry.Schema != Schema {
		return fmt.Errorf("protocol registry schema must be %q", Schema)
	}
	if registry.Authority != (AuthorityPolicy{
		Scope:                      "governance-and-tests-only",
		RuntimeAuthority:           "never",
		CapabilityGrant:            "never",
		AutomaticProtocolDeletion:  "never",
		AutomaticDataMigration:     "never",
		HistoricalChecksumMutation: "never",
	}) {
		return errors.New("protocol registry authority boundary is missing or was widened")
	}
	if err := validateSortedPaths("scan.roots", registry.Scan.Roots); err != nil {
		return err
	}
	if len(registry.Scan.Roots) == 0 {
		return errors.New("protocol registry scan roots are empty")
	}
	if err := validateExtensions(registry.Scan.Extensions); err != nil {
		return err
	}
	if len(registry.Families) == 0 {
		return errors.New("protocol registry contains no families")
	}
	if !sort.SliceIsSorted(registry.Families, func(i, j int) bool {
		return registry.Families[i].ID < registry.Families[j].ID
	}) {
		return errors.New("protocol registry families must be sorted by id")
	}

	claimed := make(map[string]string)
	familyIDs := make(map[string]struct{})
	for i := range registry.Families {
		family := &registry.Families[i]
		if err := validateFamily(*family); err != nil {
			return fmt.Errorf("family %q: %w", family.ID, err)
		}
		if _, duplicate := familyIDs[family.ID]; duplicate {
			return fmt.Errorf("duplicate protocol family %q", family.ID)
		}
		familyIDs[family.ID] = struct{}{}
		for _, identifier := range family.ActiveIdentifiers {
			if previous, duplicate := claimed[identifier]; duplicate {
				return fmt.Errorf("protocol identifier %q is claimed by both %q and %q", identifier, previous, family.ID)
			}
			claimed[identifier] = family.ID
		}
		for _, retired := range family.RetiredIdentifiers {
			if previous, duplicate := claimed[retired.Identifier]; duplicate {
				return fmt.Errorf("protocol identifier %q is claimed by both %q and %q", retired.Identifier, previous, family.ID)
			}
			claimed[retired.Identifier] = family.ID
		}
	}

	if !sort.SliceIsSorted(registry.TestAndGoldenAllowlist, func(i, j int) bool {
		return registry.TestAndGoldenAllowlist[i].Identifier < registry.TestAndGoldenAllowlist[j].Identifier
	}) {
		return errors.New("test and golden allowlist must be sorted by identifier")
	}
	allowlisted := make(map[string]struct{})
	for _, entry := range registry.TestAndGoldenAllowlist {
		if err := validateProtocolIdentifier(entry.Identifier); err != nil {
			return fmt.Errorf("allowlist identifier %q: %w", entry.Identifier, err)
		}
		if _, duplicate := allowlisted[entry.Identifier]; duplicate {
			return fmt.Errorf("duplicate allowlist identifier %q", entry.Identifier)
		}
		if family, collision := claimed[entry.Identifier]; collision {
			return fmt.Errorf("allowlist identifier %q is already registered to family %q", entry.Identifier, family)
		}
		allowlisted[entry.Identifier] = struct{}{}
		if strings.TrimSpace(entry.Kind) == "" || strings.TrimSpace(entry.Reason) == "" {
			return fmt.Errorf("allowlist identifier %q requires kind and reason", entry.Identifier)
		}
		switch entry.Kind {
		case "compatibility-example", "conformance-test", "golden-vector", "negative-version-fixture", "test-fixture":
		default:
			return fmt.Errorf("allowlist identifier %q has unsupported kind %q", entry.Identifier, entry.Kind)
		}
		if err := validateSortedPaths("allowlist sources", entry.Sources); err != nil {
			return fmt.Errorf("allowlist identifier %q: %w", entry.Identifier, err)
		}
		if len(entry.Sources) == 0 {
			return fmt.Errorf("allowlist identifier %q has no exact source", entry.Identifier)
		}
	}

	if len(registry.CompatibilityExamples) == 0 {
		return errors.New("protocol registry contains no compatibility example")
	}
	if !sort.SliceIsSorted(registry.CompatibilityExamples, func(i, j int) bool {
		return registry.CompatibilityExamples[i].ID < registry.CompatibilityExamples[j].ID
	}) {
		return errors.New("compatibility examples must be sorted by id")
	}
	for _, example := range registry.CompatibilityExamples {
		if !familyIDPattern.MatchString(example.ID) || strings.TrimSpace(example.Description) == "" {
			return fmt.Errorf("compatibility example %q is invalid", example.ID)
		}
		if err := validateRepositoryPath(example.OldFixture); err != nil {
			return fmt.Errorf("compatibility example %q old fixture: %w", example.ID, err)
		}
		if err := validateVersions("compatibility reader versions", example.ReaderVersions); err != nil {
			return fmt.Errorf("compatibility example %q: %w", example.ID, err)
		}
		if !containsInt(example.ReaderVersions, 1) || !containsInt(example.ReaderVersions, 2) ||
			example.WriterVersion != 2 || example.UnknownVersionBehavior != "fail-closed" ||
			strings.TrimSpace(example.ReaderRetirementCheck) == "" {
			return fmt.Errorf("compatibility example %q must prove dual-read v1/v2, write-new v2, unknown-version fail-closed, and reader retirement", example.ID)
		}
	}
	return nil
}

func ValidateRepositoryPaths(root string, registry Registry) error {
	paths := append([]string(nil), registry.Scan.Roots...)
	for _, family := range registry.Families {
		paths = append(paths, family.SourceOfTruth...)
		for _, writer := range family.Writers {
			paths = append(paths, writer.Source)
		}
		for _, reader := range family.Readers {
			paths = append(paths, reader.Source)
		}
	}
	for _, entry := range registry.TestAndGoldenAllowlist {
		paths = append(paths, entry.Sources...)
	}
	for _, example := range registry.CompatibilityExamples {
		paths = append(paths, example.OldFixture)
	}
	seen := make(map[string]struct{})
	for _, path := range paths {
		if _, duplicate := seen[path]; duplicate {
			continue
		}
		seen[path] = struct{}{}
		full, err := joinRepositoryPath(root, path)
		if err != nil {
			return err
		}
		info, err := os.Lstat(full)
		if err != nil {
			return fmt.Errorf("registered source %q is unavailable: %w", path, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("registered source %q is a symbolic link", path)
		}
	}
	return nil
}

func validateFamily(family Family) error {
	if !familyIDPattern.MatchString(family.ID) {
		return errors.New("id must be a lowercase kebab-case identifier")
	}
	switch family.Class {
	case ClassExternal, ClassInternal, ClassProjection, ClassEphemeral:
	default:
		return fmt.Errorf("unknown class %q", family.Class)
	}
	if strings.TrimSpace(family.Owner) == "" || strings.TrimSpace(family.PersistenceBoundary) == "" ||
		strings.TrimSpace(family.CompatibilityRule) == "" {
		return errors.New("owner, persistence boundary, and compatibility rule are required")
	}
	if err := validateSortedPaths("source_of_truth", family.SourceOfTruth); err != nil {
		return err
	}
	if len(family.SourceOfTruth) == 0 || len(family.Writers) == 0 || len(family.Readers) == 0 {
		return errors.New("source of truth, writer, and reader records are required")
	}
	if err := validateSortedStrings("retirement requirements", family.RetirementGate.Requirements); err != nil {
		return err
	}
	if len(family.RetirementGate.Requirements) == 0 {
		return errors.New("retirement gate requires explicit evidence")
	}
	switch family.Class {
	case ClassExternal, ClassInternal:
		if !family.PersistedOrExported || family.RetirementGate.Mode != GateMigration {
			return errors.New("durable families must be persisted/exported and use a migration-or-retention retirement gate")
		}
		if family.RebuildSource != "" || family.RestartPersistenceProof != "" {
			return errors.New("durable families cannot use projection or ephemeral proof fields")
		}
	case ClassProjection:
		if family.RetirementGate.Mode != GateRebuild || strings.TrimSpace(family.RebuildSource) == "" {
			return errors.New("projection families must name a rebuild source and use its retirement gate")
		}
		if family.RestartPersistenceProof != "" {
			return errors.New("projection families cannot use an ephemeral restart proof")
		}
	case ClassEphemeral:
		if family.PersistedOrExported || family.RetirementGate.Mode != GateRestart ||
			strings.TrimSpace(family.RestartPersistenceProof) == "" {
			return errors.New("ephemeral families must be non-persisted and prove restart non-persistence")
		}
		if family.RebuildSource != "" {
			return errors.New("ephemeral families cannot declare a durable rebuild source")
		}
	}
	if err := validateSortedProtocolIDs("active identifiers", family.ActiveIdentifiers); err != nil {
		return err
	}
	if len(family.ActiveIdentifiers) == 0 {
		return errors.New("family contains no active identifiers")
	}
	if !sort.SliceIsSorted(family.RetiredIdentifiers, func(i, j int) bool {
		return family.RetiredIdentifiers[i].Identifier < family.RetiredIdentifiers[j].Identifier
	}) {
		return errors.New("retired identifiers must be sorted")
	}
	versions := make(map[int]struct{})
	for _, identifier := range family.ActiveIdentifiers {
		version, _ := protocolVersion(identifier)
		versions[version] = struct{}{}
	}
	for _, retired := range family.RetiredIdentifiers {
		if err := validateProtocolIdentifier(retired.Identifier); err != nil {
			return fmt.Errorf("retired identifier %q: %w", retired.Identifier, err)
		}
		if strings.TrimSpace(retired.Decision) == "" || strings.TrimSpace(retired.Rollback) == "" {
			return fmt.Errorf("retired identifier %q requires decision and rollback evidence", retired.Identifier)
		}
		switch family.Class {
		case ClassExternal, ClassInternal:
			if strings.TrimSpace(retired.MigrationEvidence) == "" {
				return fmt.Errorf("retired durable identifier %q requires migration or retention evidence", retired.Identifier)
			}
		case ClassProjection:
			if strings.TrimSpace(retired.RebuildEvidence) == "" {
				return fmt.Errorf("retired projection identifier %q requires rebuild evidence", retired.Identifier)
			}
		case ClassEphemeral:
			if strings.TrimSpace(retired.RestartEvidence) == "" {
				return fmt.Errorf("retired ephemeral identifier %q requires restart evidence", retired.Identifier)
			}
		}
	}
	if err := validateWriters(family.Writers, versions); err != nil {
		return err
	}
	if err := validateReaders(family.Readers, versions, family.Class); err != nil {
		return err
	}
	return nil
}

func validateWriters(writers []Writer, versions map[int]struct{}) error {
	seen := make(map[string]struct{})
	maxVersion := -1
	for version := range versions {
		if version > maxVersion {
			maxVersion = version
		}
	}
	writesNewest := false
	for _, writer := range writers {
		if !familyIDPattern.MatchString(writer.ID) {
			return fmt.Errorf("writer id %q is invalid", writer.ID)
		}
		if _, duplicate := seen[writer.ID]; duplicate {
			return fmt.Errorf("duplicate writer %q", writer.ID)
		}
		seen[writer.ID] = struct{}{}
		if err := validateRepositoryPath(writer.Source); err != nil {
			return fmt.Errorf("writer %q source: %w", writer.ID, err)
		}
		if err := validateVersions("writer versions", writer.Versions); err != nil {
			return fmt.Errorf("writer %q: %w", writer.ID, err)
		}
		if writer.Mode != WriterCurrent && writer.Mode != WriterNew {
			return fmt.Errorf("writer %q has invalid mode %q", writer.ID, writer.Mode)
		}
		if containsInt(writer.Versions, maxVersion) {
			writesNewest = true
		}
	}
	if !writesNewest {
		return fmt.Errorf("no writer records newest version v%d", maxVersion)
	}
	return nil
}

func validateReaders(readers []Reader, versions map[int]struct{}, class string) error {
	seen := make(map[string]struct{})
	covered := make(map[int]struct{})
	for _, reader := range readers {
		if !familyIDPattern.MatchString(reader.ID) {
			return fmt.Errorf("reader id %q is invalid", reader.ID)
		}
		if _, duplicate := seen[reader.ID]; duplicate {
			return fmt.Errorf("duplicate reader %q", reader.ID)
		}
		seen[reader.ID] = struct{}{}
		if err := validateRepositoryPath(reader.Source); err != nil {
			return fmt.Errorf("reader %q source: %w", reader.ID, err)
		}
		if err := validateVersions("reader versions", reader.Versions); err != nil {
			return fmt.Errorf("reader %q: %w", reader.ID, err)
		}
		switch reader.Status {
		case ReaderActive:
			if reader.Retirement != nil {
				return fmt.Errorf("active reader %q has retirement evidence", reader.ID)
			}
			for _, version := range reader.Versions {
				covered[version] = struct{}{}
			}
		case ReaderRetired:
			if reader.Retirement == nil || strings.TrimSpace(reader.Retirement.Decision) == "" ||
				strings.TrimSpace(reader.Retirement.Rollback) == "" {
				return fmt.Errorf("retired reader %q lacks decision and rollback evidence", reader.ID)
			}
			if (class == ClassExternal || class == ClassInternal) &&
				strings.TrimSpace(reader.Retirement.MigrationEvidence) == "" {
				return fmt.Errorf("retired durable reader %q lacks migration or retention evidence", reader.ID)
			}
		default:
			return fmt.Errorf("reader %q has invalid status %q", reader.ID, reader.Status)
		}
	}
	for version := range versions {
		if _, ok := covered[version]; !ok {
			return fmt.Errorf("version v%d has no active reader", version)
		}
	}
	return nil
}

func validateExtensions(extensions []string) error {
	if err := validateSortedStrings("scan extensions", extensions); err != nil {
		return err
	}
	if len(extensions) == 0 {
		return errors.New("protocol registry scan extensions are empty")
	}
	for _, extension := range extensions {
		if extension == "" || extension[0] != '.' || strings.ToLower(extension) != extension ||
			strings.ContainsAny(extension, `/\\`) {
			return fmt.Errorf("invalid scan extension %q", extension)
		}
	}
	return nil
}

func validateSortedProtocolIDs(label string, identifiers []string) error {
	if err := validateSortedStrings(label, identifiers); err != nil {
		return err
	}
	for _, identifier := range identifiers {
		if err := validateProtocolIdentifier(identifier); err != nil {
			return fmt.Errorf("%s %q: %w", label, identifier, err)
		}
	}
	return nil
}

func validateProtocolIdentifier(identifier string) error {
	version, err := protocolVersion(identifier)
	if err != nil {
		return err
	}
	if version > 9999 {
		return errors.New("version exceeds the governance bound")
	}
	return nil
}

func protocolVersion(identifier string) (int, error) {
	match := protocolIDPattern.FindStringSubmatch(identifier)
	if match == nil {
		return 0, errors.New("must be an exact *.vN identifier")
	}
	version, err := strconv.Atoi(match[1])
	if err != nil {
		return 0, errors.New("version is invalid")
	}
	return version, nil
}

func validateVersions(label string, versions []int) error {
	if len(versions) == 0 {
		return fmt.Errorf("%s are empty", label)
	}
	for i, version := range versions {
		if version < 0 || version > 9999 || (i > 0 && versions[i-1] >= version) {
			return fmt.Errorf("%s must be sorted, unique, and bounded", label)
		}
	}
	return nil
}

func validateSortedPaths(label string, paths []string) error {
	if err := validateSortedStrings(label, paths); err != nil {
		return err
	}
	for _, path := range paths {
		if err := validateRepositoryPath(path); err != nil {
			return fmt.Errorf("%s: %w", label, err)
		}
	}
	return nil
}

func validateSortedStrings(label string, values []string) error {
	for i, value := range values {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s contains an empty value", label)
		}
		if i > 0 && values[i-1] >= value {
			return fmt.Errorf("%s must be sorted and unique", label)
		}
	}
	return nil
}

func validateRepositoryPath(path string) error {
	if path == "" || path == "." || filepath.IsAbs(path) || strings.Contains(path, "\\") ||
		strings.Contains(path, ":") || filepath.ToSlash(filepath.Clean(path)) != path ||
		path == ".." || strings.HasPrefix(path, "../") {
		return fmt.Errorf("repository path %q is not a clean relative slash path", path)
	}
	return nil
}

func joinRepositoryPath(root, path string) (string, error) {
	if err := validateRepositoryPath(path); err != nil {
		return "", err
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve repository root: %w", err)
	}
	full, err := filepath.Abs(filepath.Join(absRoot, filepath.FromSlash(path)))
	if err != nil {
		return "", fmt.Errorf("resolve repository path %q: %w", path, err)
	}
	prefix := absRoot + string(filepath.Separator)
	if full != absRoot && !strings.HasPrefix(strings.ToLower(full), strings.ToLower(prefix)) {
		return "", fmt.Errorf("repository path %q escaped the root", path)
	}
	return full, nil
}

func containsInt(values []int, target int) bool {
	index := sort.SearchInts(values, target)
	return index < len(values) && values[index] == target
}

func rejectDuplicateJSONFields(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := consumeJSONValue(decoder); err != nil {
		return err
	}
	return ensureJSONEOF(decoder)
}

func consumeJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("JSON object key is not a string")
			}
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("duplicate field %q", key)
			}
			seen[key] = struct{}{}
			if err := consumeJSONValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil {
			return err
		}
		if end != json.Delim('}') {
			return errors.New("JSON object is not closed")
		}
	case '[':
		for decoder.More() {
			if err := consumeJSONValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil {
			return err
		}
		if end != json.Delim(']') {
			return errors.New("JSON array is not closed")
		}
	default:
		return errors.New("unexpected JSON delimiter")
	}
	return nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("multiple JSON values are not allowed")
		}
		return err
	}
	return nil
}
