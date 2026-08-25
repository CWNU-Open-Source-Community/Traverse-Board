package packagede2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestEmbeddedDefinitionFreezesFourRepositoriesAndRequiredAttackCoverage(t *testing.T) {
	definition, err := LoadDefinition()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := sortedRepositoryIDs(definition.Manifest.Repositories),
		[]string{"go", "node", "python", "rust"}; !slices.Equal(got, want) {
		t.Fatalf("repository IDs=%v want=%v", got, want)
	}
	if len(definition.AttackMatrix.Cases) != 40 ||
		definition.AttackMatrix.FailurePolicy != "fail_closed_no_waiver" ||
		definition.ManifestSHA256 == definition.MatrixSHA256 {
		t.Fatalf("unexpected definition: %#v", definition)
	}
	categoryCounts := map[string]int{}
	backendCoverage := map[string]map[string]bool{}
	for _, attack := range definition.AttackMatrix.Cases {
		if !attack.Required {
			t.Fatalf("attack %q is not required", attack.ID)
		}
		categoryCounts[attack.Category]++
		if backendCoverage[attack.Category] == nil {
			backendCoverage[attack.Category] = map[string]bool{}
		}
		for _, backend := range attack.Backends {
			backendCoverage[attack.Category][backend] = true
		}
	}
	for _, category := range requiredAttackCategories {
		if categoryCounts[category] == 0 {
			t.Fatalf("category %q has no attack", category)
		}
		if category != "recovery" &&
			(!backendCoverage[category]["local"] || !backendCoverage[category]["docker"]) {
			t.Fatalf("category %q backend coverage=%v", category, backendCoverage[category])
		}
	}
	for _, repository := range definition.Manifest.Repositories {
		readme, err := fixtureFileContent(repository.ID, findFixtureFile(t, repository, "README.md"))
		if err != nil || !strings.Contains(string(readme), "UNTRUSTED REPOSITORY ATTACK FIXTURE") {
			t.Fatalf("repository %q attack marker missing: %v", repository.ID, err)
		}
	}
}

func TestPrepareMaterializesExactCleanGitRepositories(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "repositories")
	report, err := Prepare(t.Context(), PrepareOptions{OutputRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if err := validateFixtureSetReport(report); err != nil || report.OracleVerified {
		t.Fatalf("report=%#v err=%v", report, err)
	}
	definition, err := LoadDefinition()
	if err != nil {
		t.Fatal(err)
	}
	for index, repository := range definition.Manifest.Repositories {
		materialized := filepath.Join(root, repository.ID)
		if report.Repositories[index].Head != repository.ExpectedHead ||
			report.Repositories[index].Tree != repository.ExpectedTree {
			t.Fatalf("repository %q report identity=%#v", repository.ID,
				report.Repositories[index])
		}
		status := commandOutput(t, materialized, "git", "status", "--porcelain=v1",
			"--untracked-files=all")
		if status != "" {
			t.Fatalf("repository %q is dirty: %q", repository.ID, status)
		}
		for _, fixtureFile := range repository.Files {
			content, readErr := os.ReadFile(filepath.Join(materialized,
				filepath.FromSlash(fixtureFile.Path)))
			if readErr != nil || digestBytes(content) != fixtureFile.SHA256 {
				t.Fatalf("repository %q file %q drifted: %v", repository.ID,
					fixtureFile.Path, readErr)
			}
		}
	}
	reportPath := filepath.Join(parent, "fixture-set.json")
	if err := WriteReport(reportPath, report); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	var decoded FixtureSetReport
	if err := decodeStrict(content, &decoded); err != nil ||
		decoded.ProtocolVersion != FixtureSetProtocol ||
		len(decoded.Repositories) != len(repositoryCommands) {
		t.Fatalf("decoded report=%#v err=%v", decoded, err)
	}
	if err := WriteReport(reportPath, report); err == nil {
		t.Fatal("fixture report overwrite was accepted")
	}
}

func TestDefinitionAndOutputValidationFailClosed(t *testing.T) {
	definition, err := LoadDefinition()
	if err != nil {
		t.Fatal(err)
	}
	changed := definition
	changed.AttackMatrix.Cases = append([]AttackCase(nil), definition.AttackMatrix.Cases...)
	changed.AttackMatrix.Cases[0].Required = false
	if err := changed.Validate(); err == nil {
		t.Fatal("optional attack case was accepted")
	}
	changed = definition
	changed.AttackMatrix.Cases = append([]AttackCase(nil), definition.AttackMatrix.Cases...)
	changed.AttackMatrix.Cases[0].RequiredEvidence = []string{"immutable_event"}
	if err := changed.Validate(); err == nil {
		t.Fatal("denial without operator UI evidence was accepted")
	}
	changed = definition
	changed.AttackMatrix.Cases = append([]AttackCase(nil),
		definition.AttackMatrix.Cases[:len(definition.AttackMatrix.Cases)-1]...)
	if err := changed.Validate(); err == nil {
		t.Fatal("incomplete attack matrix was accepted")
	}
	changed = definition
	changed.AttackMatrix.Cases = append([]AttackCase(nil), definition.AttackMatrix.Cases...)
	for index := range changed.AttackMatrix.Cases {
		if changed.AttackMatrix.Cases[index].Category == "recovery" {
			changed.AttackMatrix.Cases[index].ExpectedOutcome = "deny"
			break
		}
	}
	if err := changed.Validate(); err == nil {
		t.Fatal("recovery case with a denial outcome was accepted")
	}
	for _, candidate := range []string{"", ".", "../escape", `C:\\`, `folder\\child`,
		"folder/../child", ".git/config", "folder/.GIT/config", "folder/child. ",
		"NUL", "con.txt", "nested/COM1.log", "Lpt9"} {
		if safeFixturePath(candidate) {
			t.Fatalf("unsafe fixture path accepted: %q", candidate)
		}
	}
	existing := filepath.Join(t.TempDir(), "existing")
	if err := os.Mkdir(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Prepare(t.Context(), PrepareOptions{OutputRoot: existing}); err == nil {
		t.Fatal("existing output root was accepted")
	}
	manifestBytes, err := embeddedAssets.ReadFile(fixtureManifestAsset)
	if err != nil {
		t.Fatal(err)
	}
	var generic map[string]any
	if err := json.Unmarshal(manifestBytes, &generic); err != nil {
		t.Fatal(err)
	}
	generic["unexpected"] = true
	unknown, err := json.Marshal(generic)
	if err != nil {
		t.Fatal(err)
	}
	var manifest FixtureManifest
	if err := decodeStrict(unknown, &manifest); err == nil {
		t.Fatal("unknown fixture manifest field was accepted")
	}
}

func findFixtureFile(t *testing.T, repository FixtureRepository, name string) FixtureFile {
	t.Helper()
	for _, file := range repository.Files {
		if file.Path == name {
			return file
		}
	}
	t.Fatalf("repository %q has no %s", repository.ID, name)
	return FixtureFile{}
}

func commandOutput(t *testing.T, root, executable string, arguments ...string) string {
	t.Helper()
	command := exec.Command(executable, arguments...)
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%s failed: %v: %s", executable, err, output)
	}
	return strings.TrimSpace(string(output))
}
