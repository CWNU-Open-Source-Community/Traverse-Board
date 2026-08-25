// Package packagede2e owns the fixed, offline repositories and adversarial
// matrix used to qualify the packaged Standard Code product path. The fixtures
// are test data, never trusted project instructions or runtime authority.
package packagede2e

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	FixtureManifestProtocol = "standard_code_fixture_manifest.v1"
	AttackMatrixProtocol    = "standard_code_attack_matrix.v1"
	FixtureSetProtocol      = "standard_code_fixture_set.v1"
	PackagedE2EProtocol     = "standard_code_packaged_e2e.v1"

	fixtureManifestAsset = "testdata/fixture-manifest.json"
	attackMatrixAsset    = "testdata/attack-matrix.json"
	maximumFixtureBytes  = 1 << 20
	maximumFileBytes     = 64 << 10
)

//go:embed all:testdata
var embeddedAssets embed.FS

type FixtureManifest struct {
	ProtocolVersion string              `json:"protocol_version"`
	Issue           int                 `json:"issue"`
	SourceDateEpoch int64               `json:"source_date_epoch"`
	Commit          FixtureCommit       `json:"commit"`
	Repositories    []FixtureRepository `json:"repositories"`
}

type FixtureCommit struct {
	AuthorName  string `json:"author_name"`
	AuthorEmail string `json:"author_email"`
	Message     string `json:"message"`
}

type FixtureRepository struct {
	ID           string         `json:"id"`
	Language     string         `json:"language"`
	Goal         string         `json:"goal"`
	Command      FixtureCommand `json:"command"`
	ExpectedHead string         `json:"expected_head"`
	ExpectedTree string         `json:"expected_tree"`
	RepairAsset  string         `json:"repair_asset"`
	RepairSHA256 string         `json:"repair_sha256"`
	Files        []FixtureFile  `json:"files"`
}

type FixtureCommand struct {
	Executable string   `json:"executable"`
	Arguments  []string `json:"arguments"`
}

type FixtureFile struct {
	Path        string `json:"path"`
	Asset       string `json:"asset"`
	Encoding    string `json:"encoding"`
	LineEndings string `json:"line_endings"`
	Role        string `json:"role"`
	SHA256      string `json:"sha256"`
}

type AttackMatrix struct {
	ProtocolVersion    string       `json:"protocol_version"`
	Issue              int          `json:"issue"`
	FailurePolicy      string       `json:"failure_policy"`
	RequiredCategories []string     `json:"required_categories"`
	Cases              []AttackCase `json:"cases"`
}

type AttackCase struct {
	ID               string   `json:"id"`
	Category         string   `json:"category"`
	Phase            string   `json:"phase"`
	Required         bool     `json:"required"`
	Backends         []string `json:"backends"`
	FixtureIDs       []string `json:"fixture_ids"`
	Stimulus         string   `json:"stimulus"`
	ExpectedOutcome  string   `json:"expected_outcome"`
	ExpectedSignal   string   `json:"expected_signal"`
	RequiredEvidence []string `json:"required_evidence"`
}

type Definition struct {
	Manifest       FixtureManifest
	AttackMatrix   AttackMatrix
	ManifestSHA256 string
	MatrixSHA256   string
}

var lowercaseDigestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
var lowercaseObjectPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
var attackIDPattern = regexp.MustCompile(`^[a-z0-9_]{3,80}$`)

var repositoryCommands = map[string]FixtureCommand{
	"go":     {Executable: "go", Arguments: []string{"test", "./..."}},
	"node":   {Executable: "node", Arguments: []string{"--test"}},
	"python": {Executable: "python", Arguments: []string{"-m", "unittest", "discover", "-s", "tests"}},
	"rust":   {Executable: "cargo", Arguments: []string{"test", "--offline"}},
}

var requiredAttackCategories = []string{
	"filesystem_escape", "credential_access", "network_escape", "process_escape",
	"prompt_injection", "authority_replay", "approval_fallback", "output_safety", "recovery",
}

func LoadDefinition() (Definition, error) {
	manifestBytes, err := embeddedAssets.ReadFile(fixtureManifestAsset)
	if err != nil {
		return Definition{}, fmt.Errorf("read fixture manifest: %w", err)
	}
	matrixBytes, err := embeddedAssets.ReadFile(attackMatrixAsset)
	if err != nil {
		return Definition{}, fmt.Errorf("read attack matrix: %w", err)
	}
	var manifest FixtureManifest
	if err := decodeStrict(manifestBytes, &manifest); err != nil {
		return Definition{}, fmt.Errorf("decode fixture manifest: %w", err)
	}
	var matrix AttackMatrix
	if err := decodeStrict(matrixBytes, &matrix); err != nil {
		return Definition{}, fmt.Errorf("decode attack matrix: %w", err)
	}
	definition := Definition{Manifest: manifest, AttackMatrix: matrix,
		ManifestSHA256: digestBytes(manifestBytes), MatrixSHA256: digestBytes(matrixBytes)}
	if err := definition.Validate(); err != nil {
		return Definition{}, err
	}
	return definition, nil
}

func (d Definition) Validate() error {
	if err := validateFixtureManifest(d.Manifest); err != nil {
		return fmt.Errorf("fixture manifest: %w", err)
	}
	if err := validateAttackMatrix(d.AttackMatrix, d.Manifest); err != nil {
		return fmt.Errorf("attack matrix: %w", err)
	}
	if !lowercaseDigestPattern.MatchString(d.ManifestSHA256) ||
		!lowercaseDigestPattern.MatchString(d.MatrixSHA256) {
		return errors.New("definition digests are invalid")
	}
	return nil
}

func validateFixtureManifest(manifest FixtureManifest) error {
	if manifest.ProtocolVersion != FixtureManifestProtocol || manifest.Issue != 140 ||
		manifest.SourceDateEpoch != 946684800 ||
		manifest.Commit != (FixtureCommit{AuthorName: "Traverse Board E2E",
			AuthorEmail: "e2e@traverse-board.invalid",
			Message:     "fixture: packaged standard code baseline"}) {
		return errors.New("identity is not the frozen issue #140 fixture identity")
	}
	if len(manifest.Repositories) != len(repositoryCommands) {
		return fmt.Errorf("repository count is %d, want %d", len(manifest.Repositories),
			len(repositoryCommands))
	}
	seenRepositories := map[string]bool{}
	for index, repository := range manifest.Repositories {
		expectedCommand, known := repositoryCommands[repository.ID]
		if !known || repository.Language != repository.ID || seenRepositories[repository.ID] {
			return fmt.Errorf("repository %d identity is invalid", index)
		}
		seenRepositories[repository.ID] = true
		if repository.Goal != strings.TrimSpace(repository.Goal) ||
			len(repository.Goal) < 16 || len(repository.Goal) > 256 ||
			!reflect.DeepEqual(repository.Command, expectedCommand) ||
			!lowercaseObjectPattern.MatchString(repository.ExpectedHead) ||
			!lowercaseObjectPattern.MatchString(repository.ExpectedTree) ||
			!safeFixturePath(repository.RepairAsset) ||
			!strings.HasPrefix(repository.RepairAsset, "repairs/") ||
			!lowercaseDigestPattern.MatchString(repository.RepairSHA256) {
			return fmt.Errorf("repository %q contract is invalid", repository.ID)
		}
		patch, err := embeddedAssets.ReadFile("testdata/" + repository.RepairAsset)
		if err != nil || len(patch) == 0 || len(patch) > maximumFileBytes ||
			!utf8.Valid(patch) || digestBytes(patch) != repository.RepairSHA256 ||
			bytes.Contains(patch, []byte("../")) || bytes.Contains(patch, []byte(`\\`)) ||
			bytes.Contains(patch, []byte("GIT binary patch")) {
			return fmt.Errorf("repository %q repair asset is invalid", repository.ID)
		}
		if err := validateRepositoryFiles(repository); err != nil {
			return err
		}
	}
	for id := range repositoryCommands {
		if !seenRepositories[id] {
			return fmt.Errorf("repository %q is missing", id)
		}
	}
	return nil
}

func validateRepositoryFiles(repository FixtureRepository) error {
	if len(repository.Files) < 4 || len(repository.Files) > 64 {
		return fmt.Errorf("repository %q file count is invalid", repository.ID)
	}
	seenPaths := map[string]bool{}
	roles := map[string]bool{}
	total := 0
	previous := ""
	for index, file := range repository.Files {
		folded := strings.ToLower(file.Path)
		if !safeFixturePath(file.Path) || !safeFixturePath(file.Asset) ||
			seenPaths[folded] || (index > 0 && file.Path <= previous) ||
			!lowercaseDigestPattern.MatchString(file.SHA256) {
			return fmt.Errorf("repository %q file %d identity is invalid", repository.ID, index)
		}
		seenPaths[folded] = true
		previous = file.Path
		if file.Encoding != "raw" && file.Encoding != "base64" {
			return fmt.Errorf("repository %q file %q encoding is invalid", repository.ID, file.Path)
		}
		if file.LineEndings != "lf" && file.LineEndings != "crlf" &&
			file.LineEndings != "binary" {
			return fmt.Errorf("repository %q file %q line endings are invalid", repository.ID, file.Path)
		}
		switch file.Role {
		case "metadata", "edge", "source", "test", "source_test", "untrusted_instruction":
			roles[file.Role] = true
		default:
			return fmt.Errorf("repository %q file %q role is invalid", repository.ID, file.Path)
		}
		content, err := fixtureFileContent(repository.ID, file)
		if err != nil {
			return fmt.Errorf("repository %q file %q asset: %w", repository.ID, file.Path, err)
		}
		if len(content) == 0 || len(content) > maximumFileBytes {
			return fmt.Errorf("repository %q file %q size is invalid", repository.ID, file.Path)
		}
		if actual := digestBytes(content); actual != file.SHA256 {
			return fmt.Errorf("repository %q file %q digest=%s want=%s", repository.ID,
				file.Path, actual, file.SHA256)
		}
		if !validLineEndings(content, file.LineEndings) {
			return fmt.Errorf("repository %q file %q line endings are invalid",
				repository.ID, file.Path)
		}
		total += len(content)
	}
	if total > maximumFixtureBytes || !roles["untrusted_instruction"] ||
		(!roles["source"] && !roles["source_test"]) ||
		(!roles["test"] && !roles["source_test"]) {
		return fmt.Errorf("repository %q role or size coverage is incomplete", repository.ID)
	}
	return nil
}

func validateAttackMatrix(matrix AttackMatrix, manifest FixtureManifest) error {
	if matrix.ProtocolVersion != AttackMatrixProtocol || matrix.Issue != 140 ||
		matrix.FailurePolicy != "fail_closed_no_waiver" ||
		!reflect.DeepEqual(matrix.RequiredCategories, requiredAttackCategories) ||
		len(matrix.Cases) != 40 {
		return errors.New("matrix header or case count is invalid")
	}
	repositoryIDs := map[string]bool{}
	for _, repository := range manifest.Repositories {
		repositoryIDs[repository.ID] = true
	}
	seenCases := map[string]bool{}
	categoryCoverage := map[string]map[string]bool{}
	for _, category := range requiredAttackCategories {
		categoryCoverage[category] = map[string]bool{}
	}
	allowedPhases := map[string]bool{"standard_code_execution": true, "recovery": true,
		"manual_host": true}
	allowedOutcomes := map[string]bool{"deny": true, "redact": true, "truncate": true,
		"recover": true, "preserve": true, "propose": true}
	allowedSignals := map[string]bool{"invalid_argument": true, "permission_denied": true,
		"failed_precondition": true, "conflict": true, "resource_exhausted": true,
		"redacted": true, "truncated": true, "interrupted": true,
		"recovery_required": true, "approval_required": true}
	allowedEvidence := map[string]bool{"operator_ui": true, "immutable_event": true,
		"workspace_digest": true, "process_receipt": true, "network_observation": true,
		"artifact_digest": true, "thread_transcript": true, "checkpoint": true}
	for index, attack := range matrix.Cases {
		if !attackIDPattern.MatchString(attack.ID) || seenCases[attack.ID] ||
			categoryCoverage[attack.Category] == nil || !allowedPhases[attack.Phase] ||
			!attack.Required || !allowedOutcomes[attack.ExpectedOutcome] ||
			!allowedSignals[attack.ExpectedSignal] ||
			attack.Stimulus != strings.TrimSpace(attack.Stimulus) ||
			len(attack.Stimulus) < 24 || len(attack.Stimulus) > 512 {
			return fmt.Errorf("case %d identity or expectation is invalid", index)
		}
		if attack.Category == "recovery" {
			if (attack.Phase != "recovery" && attack.Phase != "manual_host") ||
				(attack.ExpectedOutcome != "recover" && attack.ExpectedOutcome != "preserve") {
				return fmt.Errorf("case %q recovery contract is invalid", attack.ID)
			}
		} else if attack.Phase != "standard_code_execution" ||
			attack.ExpectedOutcome == "recover" || attack.ExpectedOutcome == "preserve" {
			return fmt.Errorf("case %q execution contract is invalid", attack.ID)
		}
		if attack.Category == "approval_fallback" &&
			(attack.ExpectedOutcome != "propose" || attack.ExpectedSignal != "approval_required") {
			return fmt.Errorf("case %q approval fallback contract is invalid", attack.ID)
		}
		seenCases[attack.ID] = true
		seenBackends := map[string]bool{}
		for _, backend := range attack.Backends {
			if (backend != "local" && backend != "docker") || seenBackends[backend] {
				return fmt.Errorf("case %q backend is invalid", attack.ID)
			}
			seenBackends[backend] = true
			categoryCoverage[attack.Category][backend] = true
		}
		if len(seenBackends) == 0 {
			return fmt.Errorf("case %q has no backend", attack.ID)
		}
		seenFixtures := map[string]bool{}
		for _, id := range attack.FixtureIDs {
			if !repositoryIDs[id] || seenFixtures[id] {
				return fmt.Errorf("case %q fixture is invalid", attack.ID)
			}
			seenFixtures[id] = true
		}
		if len(seenFixtures) == 0 {
			return fmt.Errorf("case %q has no fixture", attack.ID)
		}
		seenEvidence := map[string]bool{}
		for _, evidence := range attack.RequiredEvidence {
			if !allowedEvidence[evidence] || seenEvidence[evidence] {
				return fmt.Errorf("case %q evidence is invalid", attack.ID)
			}
			seenEvidence[evidence] = true
		}
		if len(seenEvidence) == 0 ||
			(attack.ExpectedOutcome == "deny" &&
				(!seenEvidence["operator_ui"] || !seenEvidence["immutable_event"])) {
			return fmt.Errorf("case %q evidence coverage is incomplete", attack.ID)
		}
	}
	for _, category := range requiredAttackCategories {
		coverage := categoryCoverage[category]
		if !coverage["local"] || !coverage["docker"] {
			return fmt.Errorf("category %q backend coverage is incomplete", category)
		}
	}
	return nil
}

func fixtureFileContent(repositoryID string, file FixtureFile) ([]byte, error) {
	content, err := embeddedAssets.ReadFile(path.Join("testdata/repositories", repositoryID,
		file.Asset))
	if err != nil {
		return nil, err
	}
	if file.Encoding == "raw" {
		return content, nil
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(string(content)))
	if err != nil {
		return nil, err
	}
	return decoded, nil
}

func validLineEndings(content []byte, lineEndings string) bool {
	switch lineEndings {
	case "binary":
		return true
	case "lf":
		return utf8.Valid(content) && !bytes.ContainsRune(content, '\r')
	case "crlf":
		if !utf8.Valid(content) || !bytes.Contains(content, []byte("\r\n")) {
			return false
		}
		withoutPairs := bytes.ReplaceAll(content, []byte("\r\n"), nil)
		return !bytes.ContainsRune(withoutPairs, '\r') && !bytes.ContainsRune(withoutPairs, '\n')
	default:
		return false
	}
}

func safeFixturePath(value string) bool {
	if value == "" || value != strings.TrimSpace(value) || len(value) > 240 ||
		!utf8.ValidString(value) || strings.ContainsAny(value, "\\:\x00") ||
		strings.HasPrefix(value, "/") || path.Clean(value) != value || value == "." ||
		value == ".." || strings.HasPrefix(value, "../") {
		return false
	}
	for _, component := range strings.Split(value, "/") {
		if component == "" || component == "." || component == ".." ||
			strings.EqualFold(component, ".git") || strings.HasSuffix(component, " ") ||
			strings.HasSuffix(component, ".") || windowsReservedComponent(component) {
			return false
		}
	}
	return true
}

func windowsReservedComponent(component string) bool {
	base := component
	if index := strings.IndexByte(base, '.'); index >= 0 {
		base = base[:index]
	}
	base = strings.ToUpper(base)
	if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" {
		return true
	}
	if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) {
		return base[3] >= '1' && base[3] <= '9'
	}
	return false
}

func decodeStrict(content []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func digestBytes(content []byte) string {
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:])
}

func sortedRepositoryIDs(repositories []FixtureRepository) []string {
	values := make([]string, 0, len(repositories))
	for _, repository := range repositories {
		values = append(values, repository.ID)
	}
	sort.Strings(values)
	return values
}
