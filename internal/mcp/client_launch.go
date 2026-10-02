package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"

	"cyberagent-workbench/internal/toolcontract"
)

// ResolveLaunch resolves only the declaration and the host's explicit context.
// It never reads ambient environment, invokes a shell, creates directories, or
// grants authority. The caller must authorize the returned frozen configuration.
func ResolveLaunch(declaration toolcontract.LaunchDeclaration, host toolcontract.LaunchContext) (toolcontract.ResolvedLaunch, error) {
	if declaration.Validate() != nil || host.Validate() != nil {
		return toolcontract.ResolvedLaunch{}, errors.New("invalid MCP declaration or host launch context")
	}
	root, err := canonicalLaunchDirectory(host.InstallRoot)
	if err != nil {
		return toolcontract.ResolvedLaunch{}, err
	}
	data, err := canonicalLaunchDirectory(host.DataRoot)
	if err != nil {
		return toolcontract.ResolvedLaunch{}, err
	}
	base, err := normalizeLaunchEnvironment(host.BaseEnv)
	if err != nil {
		return toolcontract.ResolvedLaunch{}, err
	}
	variables := maps.Clone(base)
	variables["PLUGIN_ROOT"], variables["PLUGIN_DATA"] = root, data
	versions := slices.Clone(declaration.ProtocolVersions)
	if len(versions) == 0 {
		versions = slices.Clone(host.ProtocolVersions)
	}
	for _, version := range versions {
		if !supportedClientProtocol(version) || !slices.Contains(host.ProtocolVersions, version) {
			return toolcontract.ResolvedLaunch{}, errors.New("MCP profile is unsupported or outside the host's permitted versions")
		}
	}
	resolved := toolcontract.ResolvedLaunch{Component: declaration.Component, InstanceID: host.InstanceID,
		Format: declaration.Format, FormatVersion: declaration.FormatVersion, ProtocolVersions: versions, Transport: declaration.Transport}
	switch declaration.Transport {
	case toolcontract.TransportStdio:
		local := *declaration.Stdio
		local.Env, err = normalizeLaunchEnvironment(local.Env)
		if err != nil {
			return toolcontract.ResolvedLaunch{}, err
		}
		for key, value := range local.Env {
			value, err = expandLaunchValue(value, variables)
			if err != nil {
				return toolcontract.ResolvedLaunch{}, err
			}
			base[key] = value
		}
		local.Env = base
		// os/exec would otherwise silently inherit SystemRoot on Windows.
		if runtime.GOOS == "windows" && local.Env["SYSTEMROOT"] == "" {
			return toolcontract.ResolvedLaunch{}, errors.New("Windows stdio launch requires host-supplied SystemRoot")
		}
		local.Cwd, err = expandLaunchValue(local.Cwd, variables)
		if err != nil {
			return toolcontract.ResolvedLaunch{}, err
		}
		if local.Cwd == "" {
			local.Cwd = root
		} else if !filepath.IsAbs(local.Cwd) {
			local.Cwd = filepath.Join(root, local.Cwd)
		}
		local.Cwd, err = canonicalLaunchDirectory(local.Cwd)
		if err != nil {
			return toolcontract.ResolvedLaunch{}, err
		}
		local.Command, err = expandLaunchValue(local.Command, variables)
		if err != nil {
			return toolcontract.ResolvedLaunch{}, err
		}
		local.Command, err = resolveLaunchExecutable(local.Command, local.Cwd, local.Env)
		if err != nil {
			return toolcontract.ResolvedLaunch{}, err
		}
		local.ExecutableSHA256, err = launchExecutableDigest(local.Command)
		if err != nil {
			return toolcontract.ResolvedLaunch{}, err
		}
		local.Args = slices.Clone(local.Args)
		for i, arg := range local.Args {
			local.Args[i], err = expandLaunchValue(arg, variables)
			if err != nil {
				return toolcontract.ResolvedLaunch{}, err
			}
		}
		resolved.Stdio = &local
	case toolcontract.TransportStreamableHTTP:
		remote := *declaration.HTTP
		remote.Endpoint, err = expandLaunchValue(remote.Endpoint, variables)
		if err != nil {
			return toolcontract.ResolvedLaunch{}, err
		}
		remote.Headers = make(map[string]string, len(declaration.HTTP.Headers))
		for name, value := range declaration.HTTP.Headers {
			value, err = expandLaunchValue(value, variables)
			if err != nil {
				return toolcontract.ResolvedLaunch{}, err
			}
			remote.Headers[http.CanonicalHeaderKey(name)] = value
		}
		if remote.Credential != nil {
			credential := *remote.Credential
			remote.Credential = &credential
		}
		resolved.HTTP = &remote
	}
	if err := resolved.Validate(); err != nil {
		return toolcontract.ResolvedLaunch{}, err
	}
	return resolved, nil
}

func expandLaunchValue(value string, variables map[string]string) (string, error) {
	var result strings.Builder
	for {
		start := strings.Index(value, "${")
		if start < 0 {
			result.WriteString(value)
			break
		}
		result.WriteString(value[:start])
		value = value[start+2:]
		end := strings.IndexByte(value, '}')
		if end < 0 {
			return "", errors.New("MCP launch has an unterminated variable")
		}
		key := value[:end]
		if runtime.GOOS == "windows" {
			key = strings.ToUpper(key)
		}
		replacement, exists := variables[key]
		if !exists || key == "" {
			return "", errors.New("MCP launch references a variable absent from its trusted context")
		}
		result.WriteString(replacement) // one expansion pass; never interpret replacement text
		value = value[end+1:]
	}
	if result.Len() > 65536 {
		return "", errors.New("expanded MCP launch field exceeds its limit")
	}
	return result.String(), nil
}

func normalizeLaunchEnvironment(env map[string]string) (map[string]string, error) {
	result := make(map[string]string, len(env))
	for key, value := range env {
		if runtime.GOOS == "windows" {
			key = strings.ToUpper(key)
		}
		if _, exists := result[key]; exists {
			return nil, errors.New("MCP environment contains ambiguous case aliases")
		}
		result[key] = value
	}
	return result, nil
}

func canonicalLaunchDirectory(value string) (string, error) {
	value, err := filepath.EvalSymlinks(value)
	if err != nil {
		return "", errors.New("MCP launch directory is unavailable")
	}
	info, err := os.Stat(value)
	if err != nil || !info.IsDir() || !filepath.IsAbs(value) {
		return "", errors.New("MCP launch directory is invalid")
	}
	return filepath.Clean(value), nil
}

func resolveLaunchExecutable(command, cwd string, env map[string]string) (string, error) {
	candidates := []string{command}
	if !filepath.IsAbs(command) {
		if strings.ContainsAny(command, "/\\") {
			candidates = []string{filepath.Join(cwd, command)}
		} else {
			candidates = nil
			for _, dir := range filepath.SplitList(env["PATH"]) {
				// Never pick an implicit executable from cwd or an ambient PATH.
				if !filepath.IsAbs(dir) {
					continue
				}
				candidates = append(candidates, filepath.Join(dir, command))
			}
		}
	}
	for _, candidate := range candidates {
		paths := []string{candidate}
		if runtime.GOOS == "windows" && filepath.Ext(candidate) == "" {
			paths = nil
			extensions := env["PATHEXT"]
			if extensions == "" {
				extensions = ".COM;.EXE"
			}
			for _, ext := range strings.Split(extensions, ";") {
				if strings.EqualFold(ext, ".exe") || strings.EqualFold(ext, ".com") {
					paths = append(paths, candidate+ext)
				}
			}
		}
		for _, path := range paths {
			if runtime.GOOS == "windows" && !strings.EqualFold(filepath.Ext(path), ".exe") && !strings.EqualFold(filepath.Ext(path), ".com") {
				continue // shell scripts require an explicitly declared interpreter
			}
			info, err := os.Stat(path)
			if err != nil || !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode()&0111 == 0) {
				continue
			}
			canonical, err := filepath.EvalSymlinks(path)
			if err == nil {
				return filepath.Clean(canonical), nil
			}
		}
	}
	return "", errors.New("MCP executable was not found in the explicit launch context")
}

func launchExecutableDigest(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("MCP executable is unavailable")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 512*1024*1024 {
		return "", errors.New("MCP executable is not a bounded regular file")
	}
	hash := sha256.New()
	if n, err := io.Copy(hash, io.LimitReader(file, 512*1024*1024+1)); err != nil || n > 512*1024*1024 {
		return "", errors.New("MCP executable could not be fingerprinted")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func cloneResolvedLaunch(value toolcontract.ResolvedLaunch) toolcontract.ResolvedLaunch {
	value.ProtocolVersions = slices.Clone(value.ProtocolVersions)
	if value.Stdio != nil {
		local := *value.Stdio
		local.Args, local.Env = slices.Clone(local.Args), maps.Clone(local.Env)
		value.Stdio = &local
	}
	if value.HTTP != nil {
		remote := *value.HTTP
		remote.Headers = maps.Clone(remote.Headers)
		if remote.Credential != nil {
			credential := *remote.Credential
			remote.Credential = &credential
		}
		value.HTTP = &remote
	}
	return value
}

func launchEnvironmentEntries(env map[string]string) []string {
	entries := make([]string, 0, len(env))
	for key, value := range env {
		entries = append(entries, key+"="+value)
	}
	sort.Strings(entries)
	return entries
}
