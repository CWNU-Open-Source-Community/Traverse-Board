package codeintel

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

const MaxConfigBytes = 512 * 1024

type Config struct {
	ProtocolVersion string             `json:"protocol_version"`
	Servers         []ServerDescriptor `json:"servers"`
}

// LoadConfig reads an explicitly selected process-owned configuration file.
// The configuration path is never inferred from the Workspace and symlinked
// config files are rejected so a repository cannot swap an operator review.
func LoadConfig(path string) (Config, string, error) {
	path = strings.TrimSpace(path)
	if path == "" || !filepath.IsAbs(path) || !utf8.ValidString(path) ||
		strings.ContainsRune(path, 0) {
		return Config{}, "", errors.New("code-intel config path must be an absolute normalized path")
	}
	clean := filepath.Clean(path)
	if clean != path {
		return Config{}, "", errors.New("code-intel config path must be canonical")
	}
	info, err := os.Lstat(clean)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Size() < 2 || info.Size() > MaxConfigBytes {
		return Config{}, "", errors.New("code-intel config must be a bounded real file")
	}
	file, err := os.Open(clean)
	if err != nil {
		return Config{}, "", fmt.Errorf("open code-intel config: %w", err)
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return Config{}, "", errors.New("code-intel config changed while it was opened")
	}
	raw, err := io.ReadAll(io.LimitReader(file, MaxConfigBytes+1))
	if err != nil || len(raw) > MaxConfigBytes || !utf8.Valid(raw) {
		return Config{}, "", errors.New("code-intel config must be bounded UTF-8 JSON")
	}
	completed, err := file.Stat()
	if err != nil || !os.SameFile(opened, completed) || opened.Size() != completed.Size() ||
		!opened.ModTime().Equal(completed.ModTime()) {
		return Config{}, "", errors.New("code-intel config changed while it was read")
	}

	var config Config
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return Config{}, "", errors.New("code-intel config does not match code-intel-config.v1")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Config{}, "", errors.New("code-intel config contains trailing data")
	}
	if config.ProtocolVersion != ConfigProtocolVersion || len(config.Servers) == 0 ||
		len(config.Servers) > MaxServers {
		return Config{}, "", errors.New("code-intel config version or server count is invalid")
	}
	digest := sha256.Sum256(raw)
	configDigest := hex.EncodeToString(digest[:])
	label := filepath.Base(clean)
	if !validDisplayText(label, 256, false) || !redactionInvariant(label) {
		return Config{}, "", errors.New(
			"code-intel config filename is unsafe for metadata projection")
	}
	seen := make(map[string]struct{}, len(config.Servers))
	for index := range config.Servers {
		descriptor := &config.Servers[index]
		if descriptor.Source != (Source{}) {
			return Config{}, "", errors.New("code-intel source metadata is process-generated")
		}
		if descriptor.RequestTimeoutMillis == 0 {
			descriptor.RequestTimeoutMillis = DefaultRequestTimeout.Milliseconds()
		}
		descriptor.Source = Source{Kind: "operator_config", Label: label, SHA256: configDigest}
		if err := descriptor.Validate(); err != nil {
			return Config{}, "", fmt.Errorf("code-intel server %q: %w", descriptor.ID, err)
		}
		key := descriptor.WorkspaceID + "\x00" + descriptor.ID
		if _, exists := seen[key]; exists {
			return Config{}, "", errors.New("code-intel config repeats a Workspace/server identity")
		}
		seen[key] = struct{}{}
	}
	sort.Slice(config.Servers, func(i, j int) bool {
		if config.Servers[i].WorkspaceID == config.Servers[j].WorkspaceID {
			return config.Servers[i].ID < config.Servers[j].ID
		}
		return config.Servers[i].WorkspaceID < config.Servers[j].WorkspaceID
	})
	return config, configDigest, nil
}

func executableDigest(path string) (string, bool, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", false, errors.New("reviewed LSP executable is unavailable or redirected")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", false, errors.New("reviewed LSP executable could not be opened")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return "", false, errors.New("reviewed LSP executable changed while it was opened")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", false, errors.New("reviewed LSP executable could not be hashed")
	}
	completed, err := file.Stat()
	if err != nil || !os.SameFile(opened, completed) || opened.Size() != completed.Size() ||
		!opened.ModTime().Equal(completed.ModTime()) {
		return "", false, errors.New("reviewed LSP executable changed while it was hashed")
	}
	return hex.EncodeToString(hash.Sum(nil)), true, nil
}
