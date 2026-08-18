package runner

import (
	"crypto/sha256"
	"encoding/hex"
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
	"unicode/utf8"

	"cyberagent-workbench/internal/redact"
)

const (
	CommandRuntimeProtocolVersion = "command-runtime.v2"
	CommandRuntimePolicyVersion   = "command-runtime-policy.v2"
	CommandRuntimeResultVersion   = "command-runtime-result.v2"

	MaxCommandRuntimeArguments        = 128
	MaxCommandRuntimeArgumentBytes    = 16 * 1024
	MaxCommandRuntimeEnvironment      = 32
	MaxCommandRuntimeEnvironmentBytes = 64 * 1024
	MaxCommandRuntimePurposeRunes     = 1200
	MaxCommandRuntimeScriptBytes      = 64 * 1024
	MaxCommandRuntimeTimeout          = 30 * time.Minute
	MaxCommandRuntimeInlineBytes      = 512 * 1024
	MinCommandRuntimeInlineBytes      = 4 * 1024
	MaxCommandRuntimeArtifactBytes    = 4 * 1024 * 1024
	MaxCommandRuntimeStdinBytes       = 64 * 1024
)

var (
	ErrCommandRuntimeBoundary    = errors.New("command runtime boundary is invalid")
	ErrCommandRuntimeUnavailable = errors.New("command runtime profile is unavailable")
)

type CommandRuntimeProfile string

const (
	CommandRuntimePowerShell CommandRuntimeProfile = "powershell"
	CommandRuntimeBash       CommandRuntimeProfile = "bash"
	CommandRuntimeProcess    CommandRuntimeProfile = "process"
)

func (p CommandRuntimeProfile) Valid() bool {
	return p == CommandRuntimePowerShell || p == CommandRuntimeBash ||
		p == CommandRuntimeProcess
}

type CommandRuntimeStdinPolicy string

const (
	CommandRuntimeStdinClosed CommandRuntimeStdinPolicy = "closed"
	CommandRuntimeStdinPipe   CommandRuntimeStdinPolicy = "pipe"
)

func (p CommandRuntimeStdinPolicy) Valid() bool {
	return p == CommandRuntimeStdinClosed || p == CommandRuntimeStdinPipe
}

type CommandRuntimeNetwork string

const (
	// Native commands do not have a portable OS network sandbox. The ordinary
	// runtime therefore accepts only disabled intent and sends network-looking
	// commands through Policy, where they fail closed or require the separate
	// reviewed host-command path.
	CommandRuntimeNetworkDisabled CommandRuntimeNetwork = "disabled"
)

type CommandRuntimeEnvironment struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type CommandRuntimeOutputPolicy struct {
	InlineBytes   int `json:"inline_bytes"`
	ArtifactBytes int `json:"artifact_bytes"`
}

type CommandRuntimeSpec struct {
	Version             string                      `json:"version"`
	Profile             CommandRuntimeProfile       `json:"profile"`
	Executable          string                      `json:"executable,omitempty"`
	Arguments           []string                    `json:"arguments,omitempty"`
	Script              string                      `json:"script,omitempty"`
	WorkingDirectory    string                      `json:"working_directory"`
	Environment         []CommandRuntimeEnvironment `json:"environment"`
	StdinPolicy         CommandRuntimeStdinPolicy   `json:"stdin_policy"`
	InitialStdin        string                      `json:"initial_stdin,omitempty"`
	CloseInitialStdin   bool                        `json:"close_initial_stdin"`
	TimeoutMilliseconds int64                       `json:"timeout_milliseconds"`
	Output              CommandRuntimeOutputPolicy  `json:"output"`
	Network             CommandRuntimeNetwork       `json:"network"`
	Purpose             string                      `json:"purpose"`
}

// CommandRuntimeResolvedSpec is the canonical launch contract. The absolute
// cwd and environment values remain process-local; the executable digest and
// canonical argv make the exact launch reviewable and auditable.
type CommandRuntimeResolvedSpec struct {
	Spec                 CommandRuntimeSpec
	ExecutablePath       string
	ExecutableSHA256     string
	CanonicalArgv        []string
	AbsoluteDirectory    string
	Environment          []string
	EnvironmentSHA256    string
	WorkspaceRootSHA256  string
	ExecutablePinned     bool
	ProfileStartupFiles  bool
	EnvironmentInherited bool
}

func NormalizeCommandRuntimeSpec(spec CommandRuntimeSpec,
	workspaceRoot string,
) (CommandRuntimeResolvedSpec, error) {
	spec.Version = strings.TrimSpace(spec.Version)
	spec.Profile = CommandRuntimeProfile(strings.ToLower(strings.TrimSpace(string(spec.Profile))))
	spec.Executable = strings.TrimSpace(spec.Executable)
	spec.WorkingDirectory = filepath.ToSlash(strings.TrimSpace(spec.WorkingDirectory))
	spec.Purpose = strings.TrimSpace(redact.String(spec.Purpose))
	if spec.Version != CommandRuntimeProtocolVersion || !spec.Profile.Valid() {
		return CommandRuntimeResolvedSpec{}, ErrCommandRuntimeBoundary
	}
	if spec.WorkingDirectory == "" {
		spec.WorkingDirectory = "."
	}
	if spec.StdinPolicy == "" {
		spec.StdinPolicy = CommandRuntimeStdinClosed
	}
	if spec.StdinPolicy == CommandRuntimeStdinClosed && spec.InitialStdin == "" {
		spec.CloseInitialStdin = true
	}
	if spec.Network == "" {
		spec.Network = CommandRuntimeNetworkDisabled
	}
	if spec.Output.InlineBytes == 0 {
		spec.Output.InlineBytes = 64 * 1024
	}
	if spec.Output.ArtifactBytes == 0 {
		spec.Output.ArtifactBytes = MaxCommandRuntimeArtifactBytes
	}
	if !spec.StdinPolicy.Valid() || spec.Network != CommandRuntimeNetworkDisabled ||
		spec.TimeoutMilliseconds < 1 ||
		spec.TimeoutMilliseconds > MaxCommandRuntimeTimeout.Milliseconds() ||
		spec.Output.InlineBytes < MinCommandRuntimeInlineBytes ||
		spec.Output.InlineBytes > MaxCommandRuntimeInlineBytes ||
		spec.Output.ArtifactBytes < spec.Output.InlineBytes ||
		spec.Output.ArtifactBytes > MaxCommandRuntimeArtifactBytes ||
		spec.Purpose == "" || !utf8.ValidString(spec.Purpose) ||
		strings.ContainsRune(spec.Purpose, 0) ||
		utf8.RuneCountInString(spec.Purpose) > MaxCommandRuntimePurposeRunes {
		return CommandRuntimeResolvedSpec{}, ErrCommandRuntimeBoundary
	}
	if !utf8.ValidString(spec.InitialStdin) || strings.ContainsRune(spec.InitialStdin, 0) ||
		len([]byte(spec.InitialStdin)) > MaxCommandRuntimeStdinBytes ||
		redact.String(spec.InitialStdin) != spec.InitialStdin {
		return CommandRuntimeResolvedSpec{}, fmt.Errorf("%w: stdin is invalid or contains secret-like material", ErrCommandRuntimeBoundary)
	}
	if spec.StdinPolicy == CommandRuntimeStdinClosed && spec.InitialStdin != "" {
		return CommandRuntimeResolvedSpec{}, fmt.Errorf("%w: closed stdin cannot carry input", ErrCommandRuntimeBoundary)
	}
	if spec.StdinPolicy == CommandRuntimeStdinPipe && spec.CloseInitialStdin &&
		spec.InitialStdin == "" {
		// Closing an initially empty pipe is meaningful and accepted.
	}

	root, directory, relative, err := resolveCommandRuntimeDirectory(workspaceRoot,
		spec.WorkingDirectory)
	if err != nil {
		return CommandRuntimeResolvedSpec{}, err
	}
	spec.WorkingDirectory = relative
	arguments, err := normalizeCommandRuntimeArguments(spec.Arguments)
	if err != nil {
		return CommandRuntimeResolvedSpec{}, err
	}
	spec.Arguments = arguments

	var executablePath string
	var canonicalArgv []string
	switch spec.Profile {
	case CommandRuntimePowerShell, CommandRuntimeBash:
		if spec.Executable != "" || len(spec.Arguments) != 0 || spec.Script == "" ||
			!utf8.ValidString(spec.Script) || strings.ContainsRune(spec.Script, 0) ||
			len([]byte(spec.Script)) > MaxCommandRuntimeScriptBytes ||
			redact.String(spec.Script) != spec.Script {
			return CommandRuntimeResolvedSpec{}, fmt.Errorf("%w: shell profile requires one bounded secret-free script", ErrCommandRuntimeBoundary)
		}
		executablePath, err = resolveCommandRuntimeShell(spec.Profile)
		if err != nil {
			return CommandRuntimeResolvedSpec{}, err
		}
		if spec.Profile == CommandRuntimePowerShell {
			canonicalArgv = []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command", spec.Script}
		} else {
			canonicalArgv = []string{"--noprofile", "--norc", "-c", spec.Script}
		}
	case CommandRuntimeProcess:
		if spec.Executable == "" || spec.Script != "" {
			return CommandRuntimeResolvedSpec{}, fmt.Errorf("%w: process profile requires an executable and literal argv", ErrCommandRuntimeBoundary)
		}
		executablePath, err = resolveCommandRuntimeProcess(spec.Executable)
		if err != nil {
			return CommandRuntimeResolvedSpec{}, err
		}
		if !commandRuntimeNativeExecutableAllowed(executablePath) {
			return CommandRuntimeResolvedSpec{}, fmt.Errorf("%w: process profile cannot select a shell or script interpreter", ErrCommandRuntimeBoundary)
		}
		if within, withinErr := pathWithin(filepath.Dir(executablePath), root); withinErr != nil || within {
			return CommandRuntimeResolvedSpec{}, fmt.Errorf("%w: process executable must be outside the workspace", ErrCommandRuntimeBoundary)
		}
		canonicalArgv = append([]string(nil), spec.Arguments...)
	}

	executableSHA, err := commandRuntimeFileSHA256(executablePath)
	if err != nil {
		return CommandRuntimeResolvedSpec{}, err
	}
	environmentSpec, environment, environmentSHA, err :=
		normalizeCommandRuntimeEnvironment(spec.Environment)
	if err != nil {
		return CommandRuntimeResolvedSpec{}, err
	}
	spec.Environment = environmentSpec
	rootDigest := sha256.Sum256([]byte(root))
	return CommandRuntimeResolvedSpec{
		Spec: spec, ExecutablePath: executablePath,
		ExecutableSHA256: executableSHA, CanonicalArgv: canonicalArgv,
		AbsoluteDirectory: directory, Environment: environment,
		EnvironmentSHA256:   hex.EncodeToString(environmentSHA[:]),
		WorkspaceRootSHA256: hex.EncodeToString(rootDigest[:]),
		ExecutablePinned:    true, ProfileStartupFiles: false,
		EnvironmentInherited: false,
	}, nil
}

func CommandRuntimeSpecFingerprint(spec CommandRuntimeResolvedSpec) string {
	value := struct {
		Version             string                      `json:"version"`
		Profile             CommandRuntimeProfile       `json:"profile"`
		ExecutablePath      string                      `json:"executable_path"`
		ExecutableSHA256    string                      `json:"executable_sha256"`
		Argv                []string                    `json:"argv"`
		WorkingDirectory    string                      `json:"working_directory"`
		Environment         []CommandRuntimeEnvironment `json:"environment"`
		EnvironmentSHA256   string                      `json:"environment_sha256"`
		StdinPolicy         CommandRuntimeStdinPolicy   `json:"stdin_policy"`
		InitialStdinSHA256  string                      `json:"initial_stdin_sha256"`
		CloseInitialStdin   bool                        `json:"close_initial_stdin"`
		TimeoutMilliseconds int64                       `json:"timeout_milliseconds"`
		Output              CommandRuntimeOutputPolicy  `json:"output"`
		Network             CommandRuntimeNetwork       `json:"network"`
		Purpose             string                      `json:"purpose"`
		WorkspaceRootSHA256 string                      `json:"workspace_root_sha256"`
	}{
		Version: spec.Spec.Version, Profile: spec.Spec.Profile,
		ExecutablePath: spec.ExecutablePath, ExecutableSHA256: spec.ExecutableSHA256,
		Argv:                append([]string(nil), spec.CanonicalArgv...),
		WorkingDirectory:    spec.Spec.WorkingDirectory,
		Environment:         append([]CommandRuntimeEnvironment(nil), spec.Spec.Environment...),
		EnvironmentSHA256:   spec.EnvironmentSHA256,
		StdinPolicy:         spec.Spec.StdinPolicy,
		InitialStdinSHA256:  commandRuntimeStringSHA256(spec.Spec.InitialStdin),
		CloseInitialStdin:   spec.Spec.CloseInitialStdin,
		TimeoutMilliseconds: spec.Spec.TimeoutMilliseconds,
		Output:              spec.Spec.Output, Network: spec.Spec.Network,
		Purpose: spec.Spec.Purpose, WorkspaceRootSHA256: spec.WorkspaceRootSHA256,
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func resolveCommandRuntimeDirectory(workspaceRoot string,
	relative string,
) (string, string, string, error) {
	root, err := filepath.Abs(strings.TrimSpace(workspaceRoot))
	if err != nil || !filepath.IsAbs(root) {
		return "", "", "", ErrCommandRuntimeBoundary
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", "", "", fmt.Errorf("%w: workspace root is unavailable", ErrCommandRuntimeBoundary)
	}
	if filepath.IsAbs(relative) || strings.ContainsRune(relative, 0) {
		return "", "", "", fmt.Errorf("%w: working directory must be workspace-relative", ErrCommandRuntimeBoundary)
	}
	cleanRelative := filepath.Clean(filepath.FromSlash(relative))
	if cleanRelative == "" {
		cleanRelative = "."
	}
	if cleanRelative == ".." || strings.HasPrefix(cleanRelative, ".."+string(filepath.Separator)) {
		return "", "", "", fmt.Errorf("%w: working directory escapes the workspace", ErrCommandRuntimeBoundary)
	}
	directory, err := filepath.EvalSymlinks(filepath.Join(root, cleanRelative))
	if err != nil {
		return "", "", "", fmt.Errorf("%w: working directory is unavailable", ErrCommandRuntimeBoundary)
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", "", "", fmt.Errorf("%w: working directory must be an existing non-link directory", ErrCommandRuntimeBoundary)
	}
	within, err := pathWithin(directory, root)
	if err != nil || !within {
		return "", "", "", fmt.Errorf("%w: working directory escapes the workspace", ErrCommandRuntimeBoundary)
	}
	relativePath, err := filepath.Rel(root, directory)
	if err != nil {
		return "", "", "", ErrCommandRuntimeBoundary
	}
	return filepath.Clean(root), filepath.Clean(directory), filepath.ToSlash(relativePath), nil
}

func normalizeCommandRuntimeArguments(values []string) ([]string, error) {
	if len(values) > MaxCommandRuntimeArguments {
		return nil, fmt.Errorf("%w: too many command arguments", ErrCommandRuntimeBoundary)
	}
	result := append([]string(nil), values...)
	total := 0
	for _, value := range result {
		if !utf8.ValidString(value) || strings.ContainsRune(value, 0) ||
			len([]byte(value)) > MaxCommandRuntimeArgumentBytes ||
			redact.String(value) != value {
			return nil, fmt.Errorf("%w: argument is invalid or contains secret-like material", ErrCommandRuntimeBoundary)
		}
		total += len([]byte(value))
	}
	if total > MaxCommandRuntimeEnvironmentBytes {
		return nil, fmt.Errorf("%w: command arguments are too large", ErrCommandRuntimeBoundary)
	}
	return result, nil
}

func normalizeCommandRuntimeEnvironment(values []CommandRuntimeEnvironment) (
	[]CommandRuntimeEnvironment, []string, [sha256.Size]byte, error,
) {
	if len(values) > MaxCommandRuntimeEnvironment {
		return nil, nil, [sha256.Size]byte{}, fmt.Errorf("%w: too many environment entries", ErrCommandRuntimeBoundary)
	}
	result := append([]CommandRuntimeEnvironment(nil), values...)
	sort.Slice(result, func(left int, right int) bool {
		return strings.ToLower(result[left].Name) < strings.ToLower(result[right].Name)
	})
	seen := make(map[string]struct{}, len(result))
	total := 0
	for index := range result {
		entry := &result[index]
		entry.Name = strings.TrimSpace(entry.Name)
		if !validCommandRuntimeEnvironmentName(entry.Name) ||
			!utf8.ValidString(entry.Value) || strings.ContainsRune(entry.Value, 0) ||
			redact.String(entry.Name+"="+entry.Value) != entry.Name+"="+entry.Value {
			return nil, nil, [sha256.Size]byte{}, fmt.Errorf("%w: environment entry is invalid or secret-like", ErrCommandRuntimeBoundary)
		}
		key := strings.ToLower(entry.Name)
		if _, found := seen[key]; found {
			return nil, nil, [sha256.Size]byte{}, fmt.Errorf("%w: duplicate environment entry", ErrCommandRuntimeBoundary)
		}
		seen[key] = struct{}{}
		total += len(entry.Name) + len(entry.Value) + 1
	}
	if total > MaxCommandRuntimeEnvironmentBytes {
		return nil, nil, [sha256.Size]byte{}, fmt.Errorf("%w: environment is too large", ErrCommandRuntimeBoundary)
	}
	merged := commandRuntimeBaseEnvironment()
	for _, entry := range result {
		merged = replaceCommandRuntimeEnvironment(merged, entry.Name, entry.Value)
	}
	sort.Slice(merged, func(left int, right int) bool {
		leftKey, _, _ := strings.Cut(merged[left], "=")
		rightKey, _, _ := strings.Cut(merged[right], "=")
		return strings.ToLower(leftKey) < strings.ToLower(rightKey)
	})
	encoded, err := json.Marshal(merged)
	if err != nil {
		return nil, nil, [sha256.Size]byte{}, ErrCommandRuntimeBoundary
	}
	return result, merged, sha256.Sum256(encoded), nil
}

func validCommandRuntimeEnvironmentName(value string) bool {
	if value == "" || len(value) > 128 || strings.ContainsAny(value, "=\x00") {
		return false
	}
	for index, current := range []byte(value) {
		if current >= 'A' && current <= 'Z' || current >= 'a' && current <= 'z' ||
			index > 0 && current >= '0' && current <= '9' || current == '_' {
			continue
		}
		return false
	}
	lower := strings.ToLower(value)
	for _, fragment := range []string{
		"api_key", "apikey", "auth", "cookie", "credential", "password",
		"passwd", "private_key", "secret", "token", "askpass",
	} {
		if strings.Contains(lower, fragment) {
			return false
		}
	}
	switch lower {
	case "path", "pathext", "comspec", "systemroot", "windir", "home",
		"userprofile", "bash_env", "env", "shellopts", "prompt_command",
		"ld_preload", "ld_library_path", "dyld_insert_libraries",
		"node_options", "pythonstartup", "git_config_global",
		"git_config_system", "git_config_count", "ssh_command":
		return false
	}
	return !strings.HasPrefix(lower, "git_config_key_") &&
		!strings.HasPrefix(lower, "git_config_value_") &&
		!strings.HasPrefix(lower, "dyld_")
}

func replaceCommandRuntimeEnvironment(values []string, name string, value string) []string {
	key := strings.ToLower(name)
	for index, entry := range values {
		current, _, _ := strings.Cut(entry, "=")
		if strings.ToLower(current) == key {
			values[index] = name + "=" + value
			return values
		}
	}
	return append(values, name+"="+value)
}

func resolveCommandRuntimeProcess(value string) (string, error) {
	if strings.ContainsRune(value, 0) || strings.ContainsAny(value, "\r\n") {
		return "", ErrCommandRuntimeBoundary
	}
	path := value
	var err error
	if !filepath.IsAbs(path) {
		if filepath.Base(path) != path {
			return "", fmt.Errorf("%w: executable must be an absolute path or bare name", ErrCommandRuntimeBoundary)
		}
		path, err = exec.LookPath(path)
		if err != nil {
			return "", fmt.Errorf("%w: executable was not found", ErrCommandRuntimeUnavailable)
		}
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", ErrCommandRuntimeBoundary
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("%w: executable is unavailable", ErrCommandRuntimeUnavailable)
	}
	return filepath.Clean(path), nil
}

func commandRuntimeFileSHA256(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Size() < 1 || info.Size() > 1<<30 {
		return "", fmt.Errorf("%w: executable must be a bounded regular non-link file", ErrCommandRuntimeBoundary)
	}
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("%w: executable cannot be opened", ErrCommandRuntimeUnavailable)
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", fmt.Errorf("%w: executable cannot be hashed", ErrCommandRuntimeUnavailable)
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func commandRuntimeStringSHA256(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func commandRuntimeBaseEnvironment() []string {
	allowed := commandRuntimeInheritedEnvironmentNames()
	values := make([]string, 0, len(allowed)+16)
	for _, name := range allowed {
		if value, found := os.LookupEnv(name); found && value != "" &&
			utf8.ValidString(value) && !strings.ContainsRune(value, 0) &&
			redact.String(name+"="+value) == name+"="+value {
			values = append(values, name+"="+value)
		}
	}
	fixed := commandRuntimeFixedEnvironment()
	for _, entry := range fixed {
		name, value, _ := strings.Cut(entry, "=")
		values = replaceCommandRuntimeEnvironment(values, name, value)
	}
	return values
}

func commandRuntimeIntentJSON(spec CommandRuntimeResolvedSpec) string {
	value := struct {
		Version             string                      `json:"version"`
		PolicyVersion       string                      `json:"policy_version"`
		Profile             CommandRuntimeProfile       `json:"profile"`
		ExecutablePath      string                      `json:"executable_path"`
		ExecutableSHA256    string                      `json:"executable_sha256"`
		Argv                []string                    `json:"argv"`
		WorkingDirectory    string                      `json:"working_directory"`
		Environment         []CommandRuntimeEnvironment `json:"environment"`
		EnvironmentSHA256   string                      `json:"environment_sha256"`
		StdinPolicy         CommandRuntimeStdinPolicy   `json:"stdin_policy"`
		InitialStdinBytes   int                         `json:"initial_stdin_bytes"`
		InitialStdinSHA256  string                      `json:"initial_stdin_sha256"`
		CloseInitialStdin   bool                        `json:"close_initial_stdin"`
		TimeoutMilliseconds int64                       `json:"timeout_milliseconds"`
		Output              CommandRuntimeOutputPolicy  `json:"output"`
		Network             CommandRuntimeNetwork       `json:"network"`
		Purpose             string                      `json:"purpose"`
	}{
		Version: spec.Spec.Version, PolicyVersion: CommandRuntimePolicyVersion,
		Profile: spec.Spec.Profile, ExecutablePath: spec.ExecutablePath,
		ExecutableSHA256:    spec.ExecutableSHA256,
		Argv:                append([]string(nil), spec.CanonicalArgv...),
		WorkingDirectory:    spec.Spec.WorkingDirectory,
		Environment:         append([]CommandRuntimeEnvironment(nil), spec.Spec.Environment...),
		EnvironmentSHA256:   spec.EnvironmentSHA256,
		StdinPolicy:         spec.Spec.StdinPolicy,
		InitialStdinBytes:   len([]byte(spec.Spec.InitialStdin)),
		InitialStdinSHA256:  commandRuntimeStringSHA256(spec.Spec.InitialStdin),
		CloseInitialStdin:   spec.Spec.CloseInitialStdin,
		TimeoutMilliseconds: spec.Spec.TimeoutMilliseconds,
		Output:              spec.Spec.Output, Network: spec.Spec.Network, Purpose: spec.Spec.Purpose,
	}
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
