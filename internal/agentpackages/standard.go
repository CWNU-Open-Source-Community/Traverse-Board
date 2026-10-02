// Package agentpackages reads portable Agent Skills and Agent Plugins as data.
// It uses toolcontract identities and never installs, authorizes, or executes.
package agentpackages

import (
	"encoding/json"
)

const pluginSchema = "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json"
const mcpSchema = "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json"

// Acquisition limits are host implementation bounds, not format constraints or
// model token budgets. They do not inherit the legacy Skill's 4096-byte bound.
const maxDocumentBytes = 1 << 20
const maxResourceBytes = 16 << 20
const maxDirectoryEntries = 4096
const maxSkillComponents = 256
const maxDiscoveryBytes = 8 << 20

type contentReference struct {
	path   string
	sha256 string
	bytes  int
}

type skillDescription struct {
	name, description, license, compatibility, allowedTools string
	metadata                                                map[string]string
	frontmatter                                             []byte
	instructions                                            contentReference
	root                                                    string
}

type componentDiagnostic struct {
	boundary, component, path, code string
}

type serverDescription struct {
	key, transport string
	declaration    json.RawMessage
	sha256         string
}

type pluginDescription struct {
	name, version string
	manifest      contentReference
	mcp           contentReference
	skills        []skillDescription
	servers       []serverDescription
	diagnostics   []componentDiagnostic
}
