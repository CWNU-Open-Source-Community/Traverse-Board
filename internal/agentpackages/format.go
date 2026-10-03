package agentpackages

import (
	"context"
	"errors"
	"io/fs"
	"strings"
)

const (
	FormatAgentPlugin  = "agent-plugins"
	FormatAgentSkill   = "agent-skills"
	FormatLegacyPlugin = "traverse-plugin"
	FormatLegacySkill  = "traverse-skill"
)

// ErrLegacyFormat tells the existing import service to use its unchanged
// legacy codec, signature/trust checks and recovery records. It is never
// returned for a malformed standard package as a fallback to weaker parsing.
var ErrLegacyFormat = errors.New("legacy_package_requires_existing_codec")

// DetectDirectory selects an existing format, not an installation policy.
// No package scripts, network fetches, archives or MCP runtimes are invoked.
func DetectDirectory(ctx context.Context, directory string) (string, error) {
	root, err := openDirectory(directory)
	if err != nil {
		return "", err
	}
	defer root.Close()
	return detectFormat(ctx, root)
}

func detectFormat(ctx context.Context, source fs.FS) (string, error) {
	entries, err := directoryEntries(ctx, source, ".")
	if err != nil {
		return "", err
	}
	present := make(map[string]bool, len(entries))
	for _, entry := range entries {
		present[entry.Name()] = true
	}
	if present["plugin.json"] {
		raw, err := readBounded(ctx, source, "plugin.json", maxDocumentBytes)
		if err != nil {
			return "", err
		}
		fields, err := jsonObject(raw)
		if err != nil {
			return "", errors.New("plugin_manifest_invalid")
		}
		if _, explicit := fields["$schema"]; explicit {
			return FormatAgentPlugin, nil
		}
		protocol, _ := jsonString(fields["protocol"])
		if protocol == "plugin.v1" {
			return FormatLegacyPlugin, nil
		}
		return "", errors.New("plugin_format_unsupported")
	}
	if present["manifest.json"] {
		raw, err := readBounded(ctx, source, "manifest.json", maxDocumentBytes)
		if err == nil {
			fields, parseErr := jsonObject(raw)
			if parseErr == nil {
				protocol, _ := jsonString(fields["protocol"])
				if protocol == "skill.v1" {
					return FormatLegacySkill, nil
				}
				if strings.HasPrefix(protocol, "skill.") || strings.HasPrefix(protocol, "skill_package.") {
					return "", errors.New("skill_format_unsupported")
				}
			}
		}
		// A standard skill may carry an unrelated manifest.json resource.
		// Only the explicit legacy marker selects the old codec.
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if present["SKILL.md"] {
		return FormatAgentSkill, nil
	}
	return "", errors.New("package_format_unrecognized")
}
