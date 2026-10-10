package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"
	"unicode/utf8"
)

// The accepted v0.47 generated sandboxapi.NewListSandboxesRequest uses GET
// /sandbox. SandboxInfo uses workspace/additional_workspaces, while the CLI
// writeListJSON projects these into workspaces and appends :ro for read-only
// additional mounts. Preserve that ordered projection for the owner validator.
func (transport *sbxDaemonHTTPTransport) Inventory(ctx context.Context) ([]byte, error) {
	data, err := transport.readJSON(ctx, "/sandbox", 1024*1024)
	if err != nil {
		return nil, err
	}
	var native []struct {
		ID                   string `json:"id"`
		Name                 string `json:"name"`
		Workspace            string `json:"workspace"`
		AdditionalWorkspaces []struct {
			Directory string `json:"dir"`
			ReadOnly  bool   `json:"read_only"`
		} `json:"additional_workspaces"`
	}
	if json.Unmarshal(data, &native) != nil || native == nil {
		return nil, ErrSBXBoundary
	}
	entries := make([]sbxInventoryEntry, 0, len(native))
	for _, entry := range native {
		mounts := make([]string, 0, len(entry.AdditionalWorkspaces)+1)
		if entry.Workspace != "" {
			mounts = append(mounts, entry.Workspace)
		}
		for _, mount := range entry.AdditionalWorkspaces {
			if mount.Directory == "" {
				continue
			}
			directory := mount.Directory
			if mount.ReadOnly {
				directory += ":ro"
			}
			mounts = append(mounts, directory)
		}
		entries = append(entries, sbxInventoryEntry{ID: entry.ID, Name: entry.Name, Workspaces: mounts})
	}
	projected, err := json.Marshal(entries)
	if err != nil {
		return nil, ErrSBXBoundary
	}
	if len(projected) > 1024*1024 {
		return nil, ErrSBXOutputLimit
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return projected, nil
}

// NewGetDaemonSettingRequest uses GET /daemon/settings/<key> and returns a
// sandboxapi.Setting record (key, source, type, value and optional metadata).
// Only these two compiled isolation observations are reachable here.
func (transport *sbxDaemonHTTPTransport) IsolationSetting(ctx context.Context, key string) ([]byte, error) {
	switch key {
	case "ssh.agentForwardingEnabled", "mcp.forceLocalGateway":
	default:
		return nil, ErrSBXBoundary
	}
	return transport.readJSON(ctx, "/daemon/settings/"+key, sbxDaemonBodyLimit)
}

func (transport *sbxDaemonHTTPTransport) readJSON(ctx context.Context, path string, limit int64) ([]byte, error) {
	if ctx == nil {
		return nil, ErrSBXBoundary
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bounded, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	status, body, err := transport.requestBounded(bounded, http.MethodGet, path, nil, limit)
	if err != nil {
		return nil, errors.Join(ErrSBXUnavailable, err)
	}
	if status != http.StatusOK {
		return nil, ErrSBXUnavailable
	}
	if !utf8.Valid(body) || sbxUniqueJSON(body) != nil {
		return nil, ErrSBXBoundary
	}
	if err := bounded.Err(); err != nil {
		return nil, err
	}
	return body, nil
}
