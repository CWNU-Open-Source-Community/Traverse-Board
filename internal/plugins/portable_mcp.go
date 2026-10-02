package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/mcp"
	"cyberagent-workbench/internal/toolcontract"
)

func (s *Service) stagePortableMCPServers(ctx context.Context, installation Installation, scope mcp.ScopeKind, runID, workspaceID string, stager MCPStager) ([]mcp.ServerRecord, error) {
	objects, ok := s.store.(interface {
		LoadPluginObject(context.Context, string) ([]byte, error)
	})
	if !ok {
		return nil, errors.New("native MCP object reader is unavailable")
	}
	raw, err := objects.LoadPluginObject(ctx, installation.ID)
	if err != nil {
		return nil, err
	}
	reader, err := OpenPortableSnapshot(ctx, *installation.Snapshot, raw, "")
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	launches, err := reader.Launches()
	if err != nil {
		return nil, err
	}
	if len(launches) == 0 {
		return nil, apperror.New(apperror.CodeFailedPrecondition, "native package has no supported MCP declarations; inspect its component diagnostics")
	}
	values := make([]mcp.ServerRecord, 0, len(launches))
	for index, launch := range launches {
		ref := mcp.NativeSourceRef{InstallationID: installation.ID, Component: launch.Component, Revision: installation.Revision(), InstallationGeneration: installation.Generation, Surface: installation.Source.Surface}
		encoded, _ := json.Marshal(struct {
			Source             mcp.NativeSourceRef
			Scope              mcp.ScopeKind
			RunID, WorkspaceID string
		}{ref, scope, runID, workspaceID})
		descriptor := mcp.ServerDescriptor{ProtocolVersion: mcp.NativeClientProtocolVersion, ID: "native-mcp-" + digest(encoded), Name: fmt.Sprintf("%s MCP %d", installation.DisplayName(), index+1),
			NativeSource: &ref, Transport: mcp.TransportKind(launch.Transport), Scope: scope, RunID: runID, WorkspaceID: workspaceID,
			DeclaredCapabilities: []mcp.CapabilityKind{mcp.CapabilityTools, mcp.CapabilityResources, mcp.CapabilityPrompts}, CallTimeoutMillis: 30_000, MaxResultBytes: mcp.MaxClientResultBytes,
			Source: mcp.Source{Kind: "plugin", URI: installation.Source.URI, Version: installation.Snapshot.AuthorVersion, Commit: installation.Source.Commit,
				SHA256: installation.ArchiveSHA256, PluginID: installation.PackageID(), Fingerprint: installation.PackageFingerprint}}
		record, _, err := stager.Stage(ctx, descriptor)
		if err != nil {
			return values, err
		}
		values = append(values, record)
	}
	return values, nil
}

// Launches returns only the already-validated declarations from the acquired
// package. It does not read the import source again or activate a contribution.
func (r *PortableReader) Launches() ([]toolcontract.LaunchDeclaration, error) {
	if r == nil || r.pkg == nil {
		return nil, errors.New("portable reader is closed")
	}
	return r.pkg.Launches(), nil
}

// RuntimeRoot is transient and remains owned by this reader. A host must keep
// the reader open until its process/session closes, and check live installation
// authority separately. The path and its cwd are not a process sandbox.
func (r *PortableReader) RuntimeRoot() (string, error) {
	if r == nil || r.pkg == nil {
		return "", errors.New("portable reader is closed")
	}
	return r.directory, nil
}

// PrepareRuntimeFiles restores only owner executable bits recorded at capture.
// Source acquisition and ordinary Skill reads never make files executable. This
// is called only after explicit host approval of native MCP discovery/launch.
func (r *PortableReader) PrepareRuntimeFiles(ctx context.Context) error {
	if err := r.VerifyRuntimeFiles(ctx); err != nil {
		return err
	}
	for _, entry := range r.snapshot.Inventory {
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.Kind == "file" && entry.Mode&0o100 != 0 {
			name, err := filepath.Localize(entry.Path)
			if err != nil {
				return err
			}
			if err := os.Chmod(filepath.Join(r.directory, name), 0o700); err != nil {
				return err
			}
		}
	}
	return r.VerifyRuntimeFiles(ctx)
}

// VerifyRuntimeFiles rejects replacement, extra files, links and changed bytes
// in the transient source tree. Permission mode differs intentionally from the
// captured source: extraction starts private/inert. This check cannot make an
// in-flight untrusted process atomic with filesystem changes.
func (r *PortableReader) VerifyRuntimeFiles(ctx context.Context) error {
	if r == nil || r.pkg == nil {
		return errors.New("portable reader is closed")
	}
	root, err := os.OpenRoot(r.directory)
	if err != nil {
		return err
	}
	defer root.Close()
	if _, err := root.Lstat(".git"); !errors.Is(err, os.ErrNotExist) {
		return errors.New("runtime source tree contains unexpected repository administration")
	}
	actual, _, err := captureSnapshot(ctx, root)
	if err != nil {
		return err
	}
	if !slices.EqualFunc(actual, r.snapshot.Inventory, func(a, b SnapshotEntry) bool {
		return a.Path == b.Path && a.Kind == b.Kind && a.SHA256 == b.SHA256 && a.Bytes == b.Bytes
	}) {
		return errors.New("MCP source tree changed after snapshot acquisition")
	}
	return nil
}
