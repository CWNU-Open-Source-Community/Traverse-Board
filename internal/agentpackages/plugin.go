package agentpackages

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"
	"unicode/utf8"
)

func inspectPlugin(ctx context.Context, source fs.FS) (pluginDescription, error) {
	if err := ctx.Err(); err != nil {
		return pluginDescription{}, err
	}
	entries, err := directoryEntries(ctx, source, ".")
	if err != nil {
		return pluginDescription{}, errors.New(loadFailureCode(err, "plugin_root_unreadable"))
	}
	present := make(map[string]bool, len(entries))
	for _, entry := range entries {
		present[entry.Name()] = true
	}
	if !present["plugin.json"] {
		return pluginDescription{}, errors.New("plugin_manifest_missing")
	}
	raw, err := readBounded(ctx, source, "plugin.json", maxDocumentBytes)
	if err != nil {
		return pluginDescription{}, errors.New(loadFailureCode(err, "plugin_manifest_unreadable"))
	}
	fields, err := jsonObject(raw)
	if err != nil {
		return pluginDescription{}, errors.New("plugin_manifest_invalid")
	}
	schema, err := jsonString(fields["$schema"])
	if err != nil || schema != pluginSchema {
		return pluginDescription{}, errors.New("plugin_schema_unsupported")
	}
	name, err := jsonString(fields["name"])
	if err != nil || !validPluginName(name) {
		return pluginDescription{}, errors.New("plugin_name_invalid")
	}
	result := pluginDescription{name: name, manifest: reference("plugin.json", raw)}
	for _, key := range sortedJSONKeys(fields) {
		value := fields[key]
		switch key {
		case "$schema", "name":
		case "version", "description", "homepage", "repository", "license":
			text, err := jsonString(value)
			if err != nil {
				return pluginDescription{}, errors.New("plugin_metadata_invalid")
			}
			if key == "version" {
				result.version = text
			}
		case "keywords":
			if !jsonStringArray(value) {
				return pluginDescription{}, errors.New("plugin_keywords_invalid")
			}
		case "author":
			author, err := jsonObject(value)
			if err != nil {
				return pluginDescription{}, errors.New("plugin_author_invalid")
			}
			for key, value := range author {
				if key != "name" && key != "email" && key != "url" {
					return pluginDescription{}, errors.New("plugin_author_invalid")
				}
				if _, err := jsonString(value); err != nil {
					return pluginDescription{}, errors.New("plugin_author_invalid")
				}
			}
		case "extensions":
			// No client extension namespaces are implemented here. Their values
			// are deliberately not validated or interpreted, even if non-object.
			if _, err := jsonObject(value); err != nil {
				result.diagnostics = append(result.diagnostics, componentDiagnostic{boundary: "manifest", path: "plugin.json", code: "extensions_ignored"})
			}
		default:
			result.diagnostics = append(result.diagnostics, componentDiagnostic{boundary: "manifest", path: "plugin.json", code: "unknown_field_ignored", component: key})
		}
	}
	if present["skills"] {
		discoverSkills(ctx, source, &result)
	}
	if present["mcp.json"] {
		discoverServers(ctx, source, &result)
	}
	if err := ctx.Err(); err != nil {
		return pluginDescription{}, err
	}
	return result, nil
}

func discoverSkills(ctx context.Context, source fs.FS, result *pluginDescription) {
	entries, err := directoryEntries(ctx, source, "skills")
	if err != nil {
		result.diagnostics = append(result.diagnostics, componentDiagnostic{boundary: "skills", path: "skills", code: loadFailureCode(err, "skills_location_invalid")})
		return
	}
	documents, retainedBytes := 0, 0
	for _, entry := range entries {
		if ctx.Err() != nil {
			return
		}
		directory := path.Join("skills", entry.Name())
		info, err := fs.Stat(source, directory)
		if err != nil {
			result.diagnostics = append(result.diagnostics, componentDiagnostic{boundary: "skill", component: entry.Name(), path: directory, code: "skill_location_invalid"})
			continue
		}
		if !info.IsDir() {
			continue
		}
		documents++
		if documents > maxSkillComponents {
			result.diagnostics = append(result.diagnostics, componentDiagnostic{boundary: "skills", path: "skills", code: "resource_limit_exceeded"})
			break
		}
		skill, err := inspectSkill(ctx, source, directory)
		if errors.Is(err, errMissingSkill) {
			continue
		}
		if err != nil {
			result.diagnostics = append(result.diagnostics, componentDiagnostic{boundary: "skill", component: entry.Name(), path: path.Join(directory, "SKILL.md"), code: loadFailureCode(err, "skill_invalid")})
			continue
		}
		retainedBytes += skill.instructions.bytes
		if retainedBytes > maxDiscoveryBytes {
			result.diagnostics = append(result.diagnostics, componentDiagnostic{boundary: "skills", path: "skills", code: "resource_limit_exceeded"})
			break
		}
		result.skills = append(result.skills, skill)
	}
}

func discoverServers(ctx context.Context, source fs.FS, result *pluginDescription) {
	invalidDocument := func() {
		result.diagnostics = append(result.diagnostics, componentDiagnostic{boundary: "mcp", path: "mcp.json", code: "mcp_configuration_invalid"})
	}
	raw, err := readBounded(ctx, source, "mcp.json", maxDocumentBytes)
	if err != nil {
		result.diagnostics = append(result.diagnostics, componentDiagnostic{boundary: "mcp", path: "mcp.json", code: loadFailureCode(err, "mcp_configuration_invalid")})
		return
	}
	fields, err := jsonObject(raw)
	if err != nil || len(fields) != 2 {
		invalidDocument()
		return
	}
	result.mcp = reference("mcp.json", raw)
	schema, err := jsonString(fields["$schema"])
	if err != nil || schema != mcpSchema {
		invalidDocument()
		return
	}
	servers, err := jsonObject(fields["mcpServers"])
	if err != nil {
		invalidDocument()
		return
	}
	for _, key := range sortedJSONKeys(servers) {
		if ctx.Err() != nil {
			return
		}
		declaration := servers[key]
		transport, err := validateServer(declaration)
		if err != nil {
			result.diagnostics = append(result.diagnostics, componentDiagnostic{boundary: "server", component: key, path: "mcp.json", code: "mcp_server_invalid"})
			continue
		}
		// Preserve exact declarations until B's sole runtime resolver receives
		// the C-owned contract. This digest is not an authorization fingerprint.
		result.servers = append(result.servers, serverDescription{key: key, transport: transport, declaration: append(json.RawMessage(nil), declaration...), sha256: digestBytes(declaration)})
	}
}

func validPluginName(name string) bool {
	if len(name) < 1 || len(name) > 64 || strings.Contains(name, "--") || strings.Contains(name, "..") {
		return false
	}
	alphanumeric := func(r byte) bool { return r >= 'a' && r <= 'z' || r >= '0' && r <= '9' }
	if !alphanumeric(name[0]) || !alphanumeric(name[len(name)-1]) {
		return false
	}
	for i := range name {
		if !alphanumeric(name[i]) && name[i] != '-' && name[i] != '.' {
			return false
		}
	}
	return true
}

func jsonObject(raw []byte) (map[string]json.RawMessage, error) {
	if !utf8.Valid(raw) {
		return nil, errors.New("json_object_required")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return nil, errors.New("json_object_required")
	}
	result := make(map[string]json.RawMessage)
	for decoder.More() {
		keyToken, err := decoder.Token()
		key, ok := keyToken.(string)
		if err != nil || !ok {
			return nil, errors.New("json_object_required")
		}
		if _, exists := result[key]; exists {
			return nil, errors.New("json_duplicate_key")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, errors.New("json_object_required")
		}
		result[key] = value
	}
	if closing, err := decoder.Token(); err != nil || closing != json.Delim('}') {
		return nil, errors.New("json_object_required")
	}
	if err := decoder.Decode(new(json.RawMessage)); err != io.EOF {
		return nil, errors.New("json_trailing_data")
	}
	return result, nil
}

func jsonString(raw []byte) (string, error) {
	var result string
	if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &result) != nil {
		return "", errors.New("json_string_required")
	}
	return result, nil
}

func jsonStringArray(raw []byte) bool {
	var values []json.RawMessage
	if len(raw) == 0 || raw[0] != '[' || json.Unmarshal(raw, &values) != nil {
		return false
	}
	for _, value := range values {
		if _, err := jsonString(value); err != nil {
			return false
		}
	}
	return true
}

func sortedJSONKeys(values map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
