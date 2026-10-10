package sandbox

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
)

// The CLI version is immutable while its bytes still match the backend's
// generation. Cache only a successful version check, rehash on every use, and
// read mutable daemon settings/inventory afresh through the fixed local API.
func (b *SBXBackend) compatibleCLI(ctx context.Context) (string, error) {
	select {
	case b.versionGate <- struct{}{}:
		defer func() { <-b.versionGate }()
	case <-ctx.Done():
		return "", ctx.Err()
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	digest, err := sbxFileDigest(b.config.ExecutablePath)
	if err != nil || digest != b.executableSHA || digest == "" {
		return "", ErrSBXBoundary
	}
	if b.versionOutput != "" {
		return b.versionOutput, nil
	}
	result, err := b.call(ctx, []string{"version"}, nil, 64*1024)
	if err != nil || result.ExitCode != 0 {
		return "", ErrSBXUnavailable
	}
	version := string(result.Stdout)
	if sbxCompatibleVersion(version) {
		b.versionOutput = version
	}
	return version, nil
}

func (b *SBXBackend) isolationSetting(ctx context.Context, key string) (bool, error) {
	data, err := b.daemon.IsolationSetting(ctx, key)
	if err != nil || len(data) > sbxDaemonBodyLimit || sbxUniqueJSON(data) != nil {
		return false, ErrSBXBoundary
	}
	var setting struct {
		Key   string  `json:"key"`
		Type  string  `json:"type"`
		Value *bool   `json:"value"`
		Error *string `json:"error"`
	}
	if json.Unmarshal(data, &setting) != nil || setting.Key != key || setting.Type != "bool" || setting.Value == nil ||
		(setting.Error != nil && *setting.Error != "") {
		return false, ErrSBXBoundary
	}
	return *setting.Value, nil
}

// Conditional removal and the static gateway contract are validated against
// this installed CLI line. A new daemon protocol requires compatibility tests.
var sbxSupportedVersion = regexp.MustCompile(`^sbx version:? v0\.47\.0(?: [0-9a-f]{40})?$`)

func sbxCompatibleVersion(value string) bool {
	return sbxSupportedVersion.MatchString(strings.TrimSpace(value))
}
