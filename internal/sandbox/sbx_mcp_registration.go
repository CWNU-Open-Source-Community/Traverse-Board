package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"cyberagent-workbench/internal/sbxmcp"
)

const sbxMCPRegistrationLimit = 64 * 1024

// Validate the complete owned registration. The v0.47 CLI inspect view omits
// Cwd, Env, EnvOverride and SecretEnv, which also bind the daemon's host-process
// environment and directory.
func sbxVerifyHelperRegistration(ctx context.Context, name, executable, arg string) error {
	if ctx == nil || ctx.Err() != nil || !sbxMCPHelperName(name) || arg != sbxmcp.Arg {
		return ErrSBXBoundary
	}
	if _, err := sbxCanonical(executable, false); err != nil {
		return ErrSBXBoundary
	}
	path, err := sbxMCPRegistrationPath(name)
	if err != nil {
		return ErrSBXBoundary
	}
	data, err := sbxReadMCPRegistration(ctx, path)
	if err != nil {
		return err
	}
	return sbxValidateMCPRegistration(data, name, executable, arg)
}

func sbxMCPHelperName(name string) bool {
	const prefix = "traverse-empty-"
	if len(name) != len(prefix)+32 || !strings.HasPrefix(name, prefix) {
		return false
	}
	for _, c := range name[len(prefix):] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func sbxReadMCPRegistration(ctx context.Context, path string) ([]byte, error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, ErrSBXBoundary
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsRune(path, 0) {
		return nil, ErrSBXBoundary
	}
	before, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && sbxMCPRegistrationAbsent(ctx, path) {
			// A fixed missing filename (or store directory) is the only state
			// that authorizes registration. Do not expose the host path.
			return nil, os.ErrNotExist
		}
		return nil, ErrSBXBoundary
	}
	if _, err := sbxCanonical(path, false); err != nil {
		return nil, ErrSBXBoundary
	}
	if before.Size() > sbxMCPRegistrationLimit || sbxValidateWorkspaceEntry(path, before) != nil {
		return nil, ErrSBXBoundary
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, ErrSBXBoundary
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) || !opened.Mode().IsRegular() ||
		opened.Size() > sbxMCPRegistrationLimit || sbxMCPRegistrationHandleValid(file) != nil {
		return nil, ErrSBXBoundary
	}
	data, err := io.ReadAll(io.LimitReader(file, sbxMCPRegistrationLimit+1))
	if err != nil || len(data) > sbxMCPRegistrationLimit || ctx.Err() != nil {
		return nil, ErrSBXBoundary
	}
	if _, err := sbxCanonical(path, false); err != nil {
		return nil, ErrSBXBoundary
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(opened, after) || sbxValidateWorkspaceEntry(path, after) != nil ||
		sbxMCPRegistrationHandleValid(file) != nil {
		return nil, ErrSBXBoundary
	}
	return data, nil
}

// Validate the closest existing ancestor before reporting a missing record.
// In particular, dangling links and redirected parents are invalid boundaries,
// rather than permission to add a server through an alternate store path.
func sbxMCPRegistrationAbsent(ctx context.Context, path string) bool {
	for parent := filepath.Dir(path); ; parent = filepath.Dir(parent) {
		if ctx.Err() != nil {
			return false
		}
		info, err := os.Lstat(parent)
		if err == nil {
			if !info.IsDir() || sbxValidateWorkspaceEntry(parent, info) != nil {
				return false
			}
			if _, err := sbxCanonical(parent, true); err != nil {
				return false
			}
			_, err := os.Lstat(path)
			return ctx.Err() == nil && errors.Is(err, os.ErrNotExist)
		}
		if !errors.Is(err, os.ErrNotExist) || filepath.Dir(parent) == parent {
			return false
		}
	}
}

func sbxValidateMCPRegistration(data []byte, name, executable, arg string) error {
	if len(data) == 0 || len(data) > sbxMCPRegistrationLimit || !utf8.Valid(data) ||
		!sbxMCPHelperName(name) || arg != sbxmcp.Arg || !filepath.IsAbs(executable) || filepath.Clean(executable) != executable ||
		sbxUniqueJSON(data) != nil {
		return ErrSBXBoundary
	}
	envelope, err := sbxMCPRegistrationObject(data, []string{"request", "spec"})
	if err != nil {
		return err
	}
	request, err := sbxMCPRegistrationObject(envelope["request"], []string{
		"Name", "URL", "Local", "Command", "Args", "Cwd", "Env", "EnvOverride", "SecretEnv", "Headers",
		"OAuthOverride", "OAuthResource", "PathOverride", "PortOverride", "TransportOverride", "DisableHTTP2", "SkipSSRFCheck",
	})
	if err != nil {
		return err
	}
	spec, err := sbxMCPRegistrationObject(envelope["spec"], []string{
		"Name", "Type", "URL", "RegistryURL", "Image", "Command", "ResolvedCommand", "Cwd", "Env", "EnvOverride", "SecretEnv", "Headers",
		"RequiresOAuth", "OAuthOverride", "OAuthProviders", "OAuthRequestedScopes", "OAuthResourceOverride", "CallbackPort",
		"HTTPPath", "HTTPPort", "HTTPTransport", "RemoteTransport", "DisableHTTP2", "SSRFCheckFailed", "SSRFCheckReason",
	})
	if err != nil {
		return err
	}
	directory := filepath.Dir(executable)
	for _, check := range []struct {
		object map[string]json.RawMessage
		field  string
		want   string
	}{
		{request, "Name", name}, {request, "Command", executable}, {request, "Cwd", directory},
		{spec, "Name", name}, {spec, "Type", "local"}, {spec, "ResolvedCommand", executable}, {spec, "Cwd", directory},
	} {
		var value string
		if json.Unmarshal(check.object[check.field], &value) != nil || value != check.want {
			return ErrSBXBoundary
		}
	}
	var args, command []string
	if json.Unmarshal(request["Args"], &args) != nil || len(args) != 1 || args[0] != arg ||
		json.Unmarshal(spec["Command"], &command) != nil || len(command) != 2 || command[0] != executable || command[1] != arg {
		return ErrSBXBoundary
	}
	for _, object := range []map[string]json.RawMessage{request, spec} {
		for _, field := range []string{"Env", "EnvOverride", "SecretEnv", "Headers", "OAuthOverride"} {
			if !sbxMCPEmptyCollection(object[field]) {
				return ErrSBXBoundary
			}
		}
	}
	for _, field := range []string{"OAuthProviders", "OAuthRequestedScopes"} {
		if !sbxMCPEmptyCollection(spec[field]) {
			return ErrSBXBoundary
		}
	}
	for _, fields := range []struct {
		object map[string]json.RawMessage
		keys   []string
		want   string
	}{
		{request, []string{"URL", "OAuthResource", "PathOverride", "TransportOverride"}, `""`},
		{request, []string{"Local", "DisableHTTP2", "SkipSSRFCheck"}, "false"},
		{request, []string{"PortOverride"}, "0"},
		{spec, []string{"URL", "RegistryURL", "Image", "OAuthResourceOverride", "HTTPPath", "HTTPTransport", "RemoteTransport", "SSRFCheckReason"}, `""`},
		{spec, []string{"RequiresOAuth", "DisableHTTP2", "SSRFCheckFailed"}, "false"},
		{spec, []string{"CallbackPort", "HTTPPort"}, "0"},
	} {
		for _, key := range fields.keys {
			if string(bytes.TrimSpace(fields.object[key])) != fields.want {
				return ErrSBXBoundary
			}
		}
	}
	return nil
}

func sbxMCPRegistrationObject(raw json.RawMessage, fields []string) (map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || len(object) != len(fields) {
		return nil, ErrSBXBoundary
	}
	// Map lookups are case-sensitive, unlike encoding/json's struct decoder.
	// Requiring every field also rejects aliases and unrecognized additions.
	for _, field := range fields {
		if _, found := object[field]; !found {
			return nil, ErrSBXBoundary
		}
	}
	return object, nil
}

func sbxMCPEmptyCollection(raw json.RawMessage) bool {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	switch collection := value.(type) {
	case nil:
		return true
	case map[string]any:
		return len(collection) == 0
	case []any:
		return len(collection) == 0
	default:
		return false
	}
}
