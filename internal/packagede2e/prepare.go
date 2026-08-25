package packagede2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	commandOutputLimit = 64 << 10
	commandTimeout     = 90 * time.Second
)

type PrepareOptions struct {
	OutputRoot       string
	VerifyToolchains bool
}

type FixtureSetReport struct {
	ProtocolVersion     string                    `json:"protocol_version"`
	ManifestSHA256      string                    `json:"manifest_sha256"`
	AttackMatrixSHA256  string                    `json:"attack_matrix_sha256"`
	RepositoryCount     int                       `json:"repository_count"`
	AttackCaseCount     int                       `json:"attack_case_count"`
	RequiredCategories  []string                  `json:"required_categories"`
	OracleVerified      bool                      `json:"oracle_verified"`
	AllAttackCasesBound bool                      `json:"all_attack_cases_bound"`
	Repositories        []FixtureRepositoryReport `json:"repositories"`
}

type FixtureRepositoryReport struct {
	ID                      string `json:"id"`
	Language                string `json:"language"`
	Head                    string `json:"head"`
	Tree                    string `json:"tree"`
	ContentSHA256           string `json:"content_sha256"`
	FileCount               int    `json:"file_count"`
	Clean                   bool   `json:"clean"`
	BaselineFailureObserved bool   `json:"baseline_failure_observed"`
	RepairPassVerified      bool   `json:"repair_pass_verified"`
}

func Prepare(ctx context.Context, options PrepareOptions) (report FixtureSetReport, err error) {
	if ctx == nil {
		return FixtureSetReport{}, errors.New("fixture context is required")
	}
	definition, err := LoadDefinition()
	if err != nil {
		return FixtureSetReport{}, err
	}
	root, err := validateNewOutputRoot(options.OutputRoot)
	if err != nil {
		return FixtureSetReport{}, err
	}
	if err := os.Mkdir(root, 0o755); err != nil {
		return FixtureSetReport{}, fmt.Errorf("create fixture root: %w", err)
	}
	owned := true
	defer func() {
		if err != nil && owned {
			_ = os.RemoveAll(root)
		}
	}()

	report = FixtureSetReport{ProtocolVersion: FixtureSetProtocol,
		ManifestSHA256: definition.ManifestSHA256, AttackMatrixSHA256: definition.MatrixSHA256,
		RepositoryCount:    len(definition.Manifest.Repositories),
		AttackCaseCount:    len(definition.AttackMatrix.Cases),
		RequiredCategories: append([]string(nil), definition.AttackMatrix.RequiredCategories...),
		OracleVerified:     options.VerifyToolchains, AllAttackCasesBound: true}
	for _, repository := range definition.Manifest.Repositories {
		if ctx.Err() != nil {
			return FixtureSetReport{}, ctx.Err()
		}
		repositoryRoot := filepath.Join(root, repository.ID)
		if err := materializeRepository(ctx, definition.Manifest, repository,
			repositoryRoot); err != nil {
			return FixtureSetReport{}, err
		}
		head, err := gitValue(ctx, repositoryRoot, "rev-parse", "HEAD")
		if err != nil {
			return FixtureSetReport{}, err
		}
		tree, err := gitValue(ctx, repositoryRoot, "show", "-s", "--format=%T", "HEAD")
		if err != nil {
			return FixtureSetReport{}, err
		}
		status, err := gitValue(ctx, repositoryRoot, "status", "--porcelain=v1",
			"--untracked-files=all")
		if err != nil {
			return FixtureSetReport{}, err
		}
		if head != repository.ExpectedHead || tree != repository.ExpectedTree || status != "" {
			return FixtureSetReport{}, fmt.Errorf("repository %q identity drifted", repository.ID)
		}
		current := FixtureRepositoryReport{ID: repository.ID, Language: repository.Language,
			Head: head, Tree: tree, ContentSHA256: repositoryContentDigest(repository),
			FileCount: len(repository.Files), Clean: true}
		if options.VerifyToolchains {
			baseline, repaired, verifyErr := verifyRepositoryOracle(ctx, root, repositoryRoot,
				repository)
			if verifyErr != nil {
				return FixtureSetReport{}, verifyErr
			}
			current.BaselineFailureObserved = baseline
			current.RepairPassVerified = repaired
		}
		report.Repositories = append(report.Repositories, current)
	}
	owned = false
	return report, nil
}

func WriteReport(path string, report FixtureSetReport) error {
	if err := validateFixtureSetReport(report); err != nil {
		return err
	}
	target, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil || strings.TrimSpace(path) == "" || filepath.Clean(target) == filepath.VolumeName(target)+string(filepath.Separator) {
		return errors.New("fixture report path is invalid")
	}
	parent := filepath.Dir(target)
	info, err := os.Lstat(parent)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("fixture report parent must be an existing regular directory")
	}
	content, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	content = append(content, '\n')
	file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("create fixture report: %w", err)
	}
	if _, err = file.Write(content); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func validateFixtureSetReport(report FixtureSetReport) error {
	if report.ProtocolVersion != FixtureSetProtocol ||
		!lowercaseDigestPattern.MatchString(report.ManifestSHA256) ||
		!lowercaseDigestPattern.MatchString(report.AttackMatrixSHA256) ||
		report.RepositoryCount != len(repositoryCommands) ||
		report.AttackCaseCount != 40 ||
		!reflectStringSliceEqual(report.RequiredCategories, requiredAttackCategories) ||
		!report.AllAttackCasesBound || len(report.Repositories) != report.RepositoryCount {
		return errors.New("fixture set report is invalid")
	}
	seen := map[string]bool{}
	for _, repository := range report.Repositories {
		if seen[repository.ID] || repository.Language != repository.ID ||
			!lowercaseObjectPattern.MatchString(repository.Head) ||
			!lowercaseObjectPattern.MatchString(repository.Tree) ||
			!lowercaseDigestPattern.MatchString(repository.ContentSHA256) ||
			repository.FileCount < 4 || !repository.Clean ||
			(report.OracleVerified &&
				(!repository.BaselineFailureObserved || !repository.RepairPassVerified)) ||
			(!report.OracleVerified &&
				(repository.BaselineFailureObserved || repository.RepairPassVerified)) {
			return fmt.Errorf("fixture report repository %q is invalid", repository.ID)
		}
		seen[repository.ID] = true
	}
	return nil
}

func validateNewOutputRoot(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", errors.New("fixture output root is required")
	}
	root, err := filepath.Abs(trimmed)
	if err != nil || filepath.Clean(root) != root || root == filepath.VolumeName(root)+string(filepath.Separator) {
		return "", errors.New("fixture output root is invalid")
	}
	if _, err := os.Lstat(root); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return "", errors.New("fixture output root already exists")
		}
		return "", fmt.Errorf("inspect fixture output root: %w", err)
	}
	parent := filepath.Dir(root)
	info, err := os.Lstat(parent)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("fixture output parent must be an existing regular directory")
	}
	return root, nil
}

func materializeRepository(ctx context.Context, manifest FixtureManifest,
	repository FixtureRepository, root string,
) error {
	if err := os.Mkdir(root, 0o755); err != nil {
		return fmt.Errorf("create repository %q: %w", repository.ID, err)
	}
	for _, fixtureFile := range repository.Files {
		content, err := fixtureFileContent(repository.ID, fixtureFile)
		if err != nil {
			return fmt.Errorf("read repository %q file %q: %w", repository.ID,
				fixtureFile.Path, err)
		}
		target := filepath.Join(root, filepath.FromSlash(fixtureFile.Path))
		relative, relErr := filepath.Rel(root, target)
		if relErr != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("repository %q file escaped its root", repository.ID)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return err
		}
		_, writeErr := file.Write(content)
		closeErr := file.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	commands := [][]string{
		{"init", "--quiet", "--object-format=sha1", "--initial-branch=main"},
		{"config", "core.autocrlf", "false"},
		{"config", "core.filemode", "false"},
		{"config", "core.symlinks", "false"},
		{"config", "commit.gpgsign", "false"},
		{"add", "--all"},
	}
	for _, arguments := range commands {
		if _, err := runGit(ctx, root, nil, arguments...); err != nil {
			return fmt.Errorf("prepare repository %q: %w", repository.ID, err)
		}
	}
	commitEnvironment := map[string]string{
		"GIT_AUTHOR_NAME": manifest.Commit.AuthorName, "GIT_AUTHOR_EMAIL": manifest.Commit.AuthorEmail,
		"GIT_AUTHOR_DATE":    formatGitDate(manifest.SourceDateEpoch),
		"GIT_COMMITTER_NAME": manifest.Commit.AuthorName, "GIT_COMMITTER_EMAIL": manifest.Commit.AuthorEmail,
		"GIT_COMMITTER_DATE": formatGitDate(manifest.SourceDateEpoch),
	}
	if _, err := runGitWithEnvironment(ctx, root, nil, commitEnvironment,
		"commit", "--quiet", "--no-gpg-sign", "-m", manifest.Commit.Message); err != nil {
		return fmt.Errorf("commit repository %q: %w", repository.ID, err)
	}
	return nil
}

func verifyRepositoryOracle(ctx context.Context, outputRoot, repositoryRoot string,
	repository FixtureRepository,
) (bool, bool, error) {
	verificationRoot := filepath.Join(outputRoot, ".oracle-"+repository.ID)
	if _, err := runGit(ctx, outputRoot, nil, "clone", "--quiet", "--no-hardlinks",
		"--local", repositoryRoot, verificationRoot); err != nil {
		return false, false, fmt.Errorf("clone repository %q oracle: %w", repository.ID, err)
	}
	defer os.RemoveAll(verificationRoot)
	baselineErr := runToolchain(ctx, verificationRoot, repository.Command)
	if baselineErr == nil {
		return false, false, fmt.Errorf("repository %q baseline unexpectedly passed", repository.ID)
	}
	var exitErr *exec.ExitError
	if !errors.As(baselineErr, &exitErr) || exitErr.ExitCode() == 0 {
		return false, false, fmt.Errorf("repository %q baseline could not execute: %w",
			repository.ID, baselineErr)
	}
	patch, err := embeddedAssets.ReadFile("testdata/" + repository.RepairAsset)
	if err != nil {
		return false, false, err
	}
	if _, err := runGit(ctx, verificationRoot, patch, "apply", "--check", "--unidiff-zero",
		"--whitespace=nowarn", "-"); err != nil {
		return false, false, fmt.Errorf("repository %q repair check failed: %w", repository.ID, err)
	}
	if _, err := runGit(ctx, verificationRoot, patch, "apply", "--unidiff-zero",
		"--whitespace=nowarn", "-"); err != nil {
		return false, false, fmt.Errorf("repository %q repair failed: %w", repository.ID, err)
	}
	if err := runToolchain(ctx, verificationRoot, repository.Command); err != nil {
		return true, false, fmt.Errorf("repository %q repaired verification failed: %w",
			repository.ID, err)
	}
	if _, err := runGit(ctx, verificationRoot, nil, "diff", "--check"); err != nil {
		return true, false, fmt.Errorf("repository %q repaired diff is invalid: %w",
			repository.ID, err)
	}
	return true, true, nil
}

func runToolchain(parent context.Context, root string, command FixtureCommand) error {
	ctx, cancel := context.WithTimeout(parent, commandTimeout)
	defer cancel()
	process := exec.CommandContext(ctx, command.Executable, command.Arguments...)
	process.Dir = root
	cacheRoot := filepath.Join(root, ".e2e-cache")
	process.Env = append(filteredEnvironment(),
		"GOPROXY=off", "GOSUMDB=off", "GOTOOLCHAIN=local",
		"GOCACHE="+filepath.Join(cacheRoot, "go-build"),
		"GOMODCACHE="+filepath.Join(cacheRoot, "go-mod"),
		"CARGO_NET_OFFLINE=true", "RUSTUP_TOOLCHAIN=1.97.1",
		"CARGO_TARGET_DIR="+filepath.Join(cacheRoot, "cargo-target"),
		"CARGO_HOME="+filepath.Join(cacheRoot, "cargo-home"),
		"PYTHONDONTWRITEBYTECODE=1", "PYTHONNOUSERSITE=1", "NODE_DISABLE_COLORS=1",
		"NO_COLOR=1",
	)
	output := &boundedBuffer{limit: commandOutputLimit}
	process.Stdout, process.Stderr = output, output
	err := process.Run()
	if ctx.Err() != nil {
		return fmt.Errorf("toolchain command timed out: %w", ctx.Err())
	}
	return err
}

func gitValue(ctx context.Context, root string, arguments ...string) (string, error) {
	output, err := runGit(ctx, root, nil, arguments...)
	return strings.TrimSpace(string(output)), err
}

func runGit(ctx context.Context, root string, stdin []byte, arguments ...string) ([]byte, error) {
	return runGitWithEnvironment(ctx, root, stdin, nil, arguments...)
}

func runGitWithEnvironment(ctx context.Context, root string, stdin []byte,
	extra map[string]string, arguments ...string,
) ([]byte, error) {
	command := exec.CommandContext(ctx, "git", append([]string{
		"-c", "core.autocrlf=false", "-c", "core.filemode=false",
		"-c", "core.symlinks=false", "-c", "commit.gpgsign=false",
	}, arguments...)...)
	command.Dir = root
	command.Env = append(filteredEnvironment(), "GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=Never")
	keys := make([]string, 0, len(extra))
	for key := range extra {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		command.Env = append(command.Env, key+"="+extra[key])
	}
	if stdin != nil {
		command.Stdin = bytes.NewReader(stdin)
	}
	output := &boundedBuffer{limit: commandOutputLimit}
	command.Stdout, command.Stderr = output, output
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", arguments[0], err,
			strings.TrimSpace(output.String()))
	}
	return output.Bytes(), nil
}

func filteredEnvironment() []string {
	blocked := map[string]bool{
		"GIT_AUTHOR_NAME": true, "GIT_AUTHOR_EMAIL": true, "GIT_AUTHOR_DATE": true,
		"GIT_COMMITTER_NAME": true, "GIT_COMMITTER_EMAIL": true,
		"GIT_COMMITTER_DATE": true, "GIT_CONFIG_GLOBAL": true,
		"GIT_CONFIG_SYSTEM": true, "GIT_CONFIG_COUNT": true,
		"AWS_ACCESS_KEY_ID": true, "AWS_SECRET_ACCESS_KEY": true,
		"AWS_SESSION_TOKEN": true, "AZURE_CLIENT_SECRET": true,
		"GOOGLE_APPLICATION_CREDENTIALS": true, "GH_TOKEN": true,
		"GITHUB_TOKEN": true, "OPENAI_API_KEY": true, "DEEPSEEK_API_KEY": true,
		"SSH_AUTH_SOCK": true, "HTTP_PROXY": true, "HTTPS_PROXY": true,
		"ALL_PROXY": true, "NO_PROXY": true,
	}
	values := make([]string, 0, len(os.Environ()))
	for _, value := range os.Environ() {
		key, _, found := strings.Cut(value, "=")
		if found && !blocked[strings.ToUpper(key)] {
			values = append(values, value)
		}
	}
	return values
}

func repositoryContentDigest(repository FixtureRepository) string {
	var content bytes.Buffer
	for _, file := range repository.Files {
		content.WriteString(file.Path)
		content.WriteByte(0)
		content.WriteString(file.SHA256)
		content.WriteByte(0)
	}
	return digestBytes(content.Bytes())
}

func formatGitDate(epoch int64) string {
	return time.Unix(epoch, 0).UTC().Format(time.RFC3339)
}

func reflectStringSliceEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

type boundedBuffer struct {
	buffer bytes.Buffer
	limit  int
}

func (b *boundedBuffer) Write(value []byte) (int, error) {
	original := len(value)
	remaining := b.limit - b.buffer.Len()
	if remaining > 0 {
		if len(value) > remaining {
			value = value[:remaining]
		}
		_, _ = b.buffer.Write(value)
	}
	return original, nil
}

func (b *boundedBuffer) Bytes() []byte  { return append([]byte(nil), b.buffer.Bytes()...) }
func (b *boundedBuffer) String() string { return b.buffer.String() }

var _ io.Writer = (*boundedBuffer)(nil)
