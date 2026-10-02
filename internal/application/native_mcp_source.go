package application

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/mcp"
	"cyberagent-workbench/internal/plugins"
	"cyberagent-workbench/internal/toolcontract"
)

type nativeMCPSourceStore interface {
	GetPluginInstallation(context.Context, string) (plugins.Installation, error)
	LoadPluginObject(context.Context, string) ([]byte, error)
	GetRunMode(context.Context, string) (domain.RunModeSnapshot, error)
}

type NativeMCPSourceResolver struct {
	store nativeMCPSourceStore
	root  string
}

func NewNativeMCPSourceResolver(store nativeMCPSourceStore, stateRoot string) (*NativeMCPSourceResolver, error) {
	root, err := filepath.Abs(stateRoot)
	if err != nil || stateRoot == "" || store == nil {
		return nil, errors.New("native MCP requires an existing host store and state root")
	}
	return &NativeMCPSourceResolver{store: store, root: root}, nil
}

func (r *NativeMCPSourceResolver) Check(ctx context.Context, ref mcp.NativeSourceRef, runID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if ref.Validate() != nil {
		return errors.New("native MCP source is invalid")
	}
	installed, err := r.store.GetPluginInstallation(ctx, ref.InstallationID)
	if err != nil {
		return err
	}
	if installed.Validate() != nil || installed.Snapshot == nil || installed.State != plugins.StateEnabled ||
		!slices.Contains(installed.EnabledCapabilities, plugins.CapabilityMCP) || installed.PackageID() != ref.Component.PackageID ||
		installed.Revision() != ref.Revision || installed.Generation != ref.InstallationGeneration || installed.Source.Surface != ref.Surface {
		return apperror.New(apperror.CodePolicyDenied, "native MCP installation, revision, surface or enablement changed")
	}
	if runID != "" {
		mode, err := r.store.GetRunMode(ctx, runID)
		if err != nil {
			return err
		}
		if string(mode.Surface) != ref.Surface {
			return apperror.New(apperror.CodePolicyDenied, "native MCP installation is not enabled for this Run surface")
		}
	}
	return nil
}

func (r *NativeMCPSourceResolver) Resolve(ctx context.Context, ref mcp.NativeSourceRef, runID string) (toolcontract.ResolvedLaunch, []string, func(context.Context) error, func(), error) {
	failed := func(err error) (toolcontract.ResolvedLaunch, []string, func(context.Context) error, func(), error) {
		return toolcontract.ResolvedLaunch{}, nil, nil, nil, err
	}
	if err := r.Check(ctx, ref, runID); err != nil {
		return failed(err)
	}
	installed, err := r.store.GetPluginInstallation(ctx, ref.InstallationID)
	if err != nil {
		return failed(err)
	}
	raw, err := r.store.LoadPluginObject(ctx, ref.InstallationID)
	if err != nil {
		return failed(err)
	}
	scratch := filepath.Join(r.root, "runtime")
	data := filepath.Join(r.root, "data", portableIdentity(ref.InstallationID+"\x00"+ref.Component.ComponentID))
	for _, directory := range []string{scratch, data} {
		if err := prepareNativeMCPDirectory(directory); err != nil {
			return failed(err)
		}
	}
	reader, err := plugins.OpenPortableSnapshot(ctx, *installed.Snapshot, raw, scratch)
	if err != nil {
		return failed(err)
	}
	closeSource := func() { _ = reader.Close() }
	success := false
	defer func() {
		if !success {
			closeSource()
		}
	}()
	launches, err := reader.Launches()
	if err != nil {
		return failed(err)
	}
	index := slices.IndexFunc(launches, func(d toolcontract.LaunchDeclaration) bool { return d.Component == ref.Component })
	if index < 0 {
		return failed(apperror.New(apperror.CodeFailedPrecondition, "native MCP component is absent or unsupported"))
	}
	declaration := launches[index]
	if declaration.Stdio != nil {
		if err := reader.PrepareRuntimeFiles(ctx); err != nil {
			return failed(err)
		}
	}
	root, err := reader.RuntimeRoot()
	if err != nil {
		return failed(err)
	}
	rootInfo, err := os.Lstat(root)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return failed(errors.New("native MCP source root is not an ordinary directory"))
	}
	baseEnv := map[string]string{"PYTHONDONTWRITEBYTECODE": "1"}
	for _, name := range []string{"PATH", "SystemRoot", "WINDIR", "PATHEXT", "TEMP", "TMP"} {
		if value, present := os.LookupEnv(name); present {
			baseEnv[name] = value
		}
	}
	launch, err := mcp.ResolveLaunch(declaration, toolcontract.LaunchContext{InstanceID: portableIdentity(ref.InstallationID + "\x00" + ref.Revision), InstallRoot: root,
		DataRoot: data, BaseEnv: baseEnv, ProtocolVersions: []string{"2026-07-28", "2025-06-18", "2024-11-05"}})
	if err != nil {
		return failed(err)
	}
	recheck := func(ctx context.Context) error {
		if err := r.Check(ctx, ref, runID); err != nil {
			return err
		}
		currentRoot, err := os.Lstat(root)
		if err != nil || !os.SameFile(rootInfo, currentRoot) || !currentRoot.IsDir() || currentRoot.Mode()&os.ModeSymlink != 0 {
			return errors.New("native MCP source root was replaced")
		}
		return reader.VerifyRuntimeFiles(ctx)
	}
	if err := recheck(ctx); err != nil {
		return failed(err)
	}
	var secrets []string
	if launch.HTTP != nil {
		for _, value := range launch.HTTP.Headers {
			if value != "" {
				secrets = append(secrets, value)
			}
		}
		endpoint, _ := url.Parse(launch.HTTP.Endpoint)
		for _, values := range endpoint.Query() {
			for _, value := range values {
				if value != "" {
					secrets = append(secrets, value)
				}
			}
		}
	}
	if launch.Stdio != nil {
		for key := range declaration.Stdio.Env {
			if runtime.GOOS == "windows" {
				key = strings.ToUpper(key)
			}
			value := launch.Stdio.Env[key]
			if value != "" {
				secrets = append(secrets, value)
			}
		}
	}
	success = true
	return launch, secrets, recheck, closeSource, nil
}

// Reject pre-existing links before creating descendants. Held parent handles
// keep creation beneath the directory inspected here; launch resolution checks
// the resulting path again. This is not an OS process sandbox or an atomic
// guarantee against a same-user filesystem mutation after these checks.
func prepareNativeMCPDirectory(directory string) error {
	parentPath := filepath.Dir(directory)
	if parentPath == directory {
		return nil
	}
	if err := prepareNativeMCPDirectory(parentPath); err != nil {
		return err
	}
	parent, err := os.OpenRoot(parentPath)
	if err != nil {
		return err
	}
	defer parent.Close()
	name := filepath.Base(directory)
	info, err := parent.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		if err := parent.Mkdir(name, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		info, err = parent.Lstat(name)
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("native MCP state path contains a link or non-directory")
	}
	return nil
}
