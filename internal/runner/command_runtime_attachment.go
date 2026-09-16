package runner

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const CommandRuntimeAttachmentEnvironment = "TRAVERSE_ATTACHMENTS_DIR"

// CommandRuntimeAttachmentInput is populated only by the application after
// validating sent attachment identities and materializing their original bytes.
// It is not part of the model-facing command spec and grants no execution mode.
type CommandRuntimeAttachmentInput struct {
	Root           string
	ManifestSHA256 string
}

func BindCommandRuntimeAttachmentInput(spec CommandRuntimeResolvedSpec, input CommandRuntimeAttachmentInput) (CommandRuntimeResolvedSpec, error) {
	if spec.AttachmentInput != nil || spec.Spec.Version != CommandRuntimeProtocolVersion {
		return CommandRuntimeResolvedSpec{}, ErrCommandRuntimeBoundary
	}
	input.Root = filepath.Clean(input.Root)
	copy := input
	spec.AttachmentInput = &copy
	for _, entry := range spec.Spec.Environment {
		if strings.EqualFold(entry.Name, CommandRuntimeAttachmentEnvironment) {
			return CommandRuntimeResolvedSpec{}, ErrCommandRuntimeBoundary
		}
	}
	spec.Environment = append([]string(nil), spec.Environment...)
	for _, entry := range spec.Environment {
		name, _, _ := strings.Cut(entry, "=")
		if strings.EqualFold(name, CommandRuntimeAttachmentEnvironment) {
			return CommandRuntimeResolvedSpec{}, ErrCommandRuntimeBoundary
		}
	}
	spec.Environment = append(spec.Environment, CommandRuntimeAttachmentEnvironment+"="+input.Root)
	sort.Slice(spec.Environment, func(i, j int) bool {
		left, _, _ := strings.Cut(spec.Environment[i], "=")
		right, _, _ := strings.Cut(spec.Environment[j], "=")
		return strings.ToLower(left) < strings.ToLower(right)
	})
	encoded, _ := json.Marshal(spec.Environment)
	digest := sha256.Sum256(encoded)
	spec.EnvironmentSHA256 = hex.EncodeToString(digest[:])
	if err := validateCommandRuntimeAttachmentInput(spec); err != nil {
		return CommandRuntimeResolvedSpec{}, err
	}
	return spec, nil
}

func commandRuntimeAttachmentManifest(spec CommandRuntimeResolvedSpec) string {
	if spec.AttachmentInput == nil {
		return ""
	}
	return spec.AttachmentInput.ManifestSHA256
}

func validateCommandRuntimeAttachmentInput(spec CommandRuntimeResolvedSpec) error {
	input := spec.AttachmentInput
	if input == nil {
		return nil
	}
	decoded, err := hex.DecodeString(input.ManifestSHA256)
	if err != nil || len(decoded) != sha256.Size || strings.ToLower(input.ManifestSHA256) != input.ManifestSHA256 ||
		!filepath.IsAbs(input.Root) || filepath.Clean(input.Root) != input.Root || !validCommandRuntimeText(input.Root, false) {
		return ErrCommandRuntimeBoundary
	}
	root, err := filepath.EvalSymlinks(input.Root)
	if err != nil || !commandRuntimePathEqual(root, input.Root) {
		return ErrCommandRuntimeBoundary
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ErrCommandRuntimeBoundary
	}
	for _, pair := range [][2]string{{root, spec.WorkspaceRoot}, {spec.WorkspaceRoot, root}} {
		outside, err := commandRuntimeExecutableOutsideWorkspace(pair[0], pair[1])
		if err != nil || !outside {
			return ErrCommandRuntimeBoundary
		}
	}
	count := 0
	for _, entry := range spec.Environment {
		name, value, _ := strings.Cut(entry, "=")
		if strings.EqualFold(name, CommandRuntimeAttachmentEnvironment) {
			if value != input.Root {
				return ErrCommandRuntimeBoundary
			}
			count++
		}
	}
	encoded, _ := json.Marshal(spec.Environment)
	digest := sha256.Sum256(encoded)
	if count != 1 || hex.EncodeToString(digest[:]) != spec.EnvironmentSHA256 {
		return ErrCommandRuntimeBoundary
	}
	return nil
}
