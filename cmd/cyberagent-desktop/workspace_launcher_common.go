//go:build desktop

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"cyberagent-workbench/internal/desktop"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type workspaceLauncherCandidate struct {
	descriptor   desktop.WorkspaceLauncherDescriptor
	executable   string
	passRoot     bool
	allowReparse bool
}

type nativeWorkspaceLauncher struct {
	discover func() ([]workspaceLauncherCandidate, error)
	confirm  func(context.Context, workspaceLauncherCandidate,
		desktop.WorkspaceOpenTarget) (bool, error)
	start func(context.Context, workspaceLauncherCandidate,
		desktop.WorkspaceOpenTarget) error
}

func newNativeWorkspaceLauncher() *nativeWorkspaceLauncher {
	return &nativeWorkspaceLauncher{
		discover: discoverWorkspaceLaunchers,
		confirm:  confirmWorkspaceOpen,
		start:    startWorkspaceLauncher,
	}
}

func (l *nativeWorkspaceLauncher) List(
	ctx context.Context,
) ([]desktop.WorkspaceLauncherDescriptor, error) {
	if l == nil || l.discover == nil || ctx == nil {
		return nil, errors.New("native workspace launcher is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	candidates, err := l.discover()
	if err != nil {
		return nil, err
	}
	out := make([]desktop.WorkspaceLauncherDescriptor, 0, len(candidates))
	for _, candidate := range candidates {
		out = append(out, candidate.descriptor)
	}
	return out, nil
}

func (l *nativeWorkspaceLauncher) Open(ctx context.Context,
	target desktop.WorkspaceOpenTarget, launcherID string) (desktop.NativeWorkspaceOpenResult, error) {
	if l == nil || l.discover == nil || l.confirm == nil || l.start == nil || ctx == nil {
		return desktop.NativeWorkspaceOpenResult{}, errors.New("native workspace launcher is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return desktop.NativeWorkspaceOpenResult{}, err
	}
	candidates, err := l.discover()
	if err != nil {
		return desktop.NativeWorkspaceOpenResult{}, err
	}
	candidate, found := findWorkspaceLauncher(candidates, launcherID)
	if !found {
		return desktop.NativeWorkspaceOpenResult{}, errors.New("native workspace launcher was not found")
	}
	if err := validateWorkspaceDirectory(target.RootPath); err != nil {
		return desktop.NativeWorkspaceOpenResult{}, err
	}
	if err := validateLauncherExecutable(candidate); err != nil {
		return desktop.NativeWorkspaceOpenResult{}, err
	}
	confirmed, err := l.confirm(ctx, candidate, target)
	if err != nil {
		return desktop.NativeWorkspaceOpenResult{}, err
	}
	if !confirmed {
		return desktop.NativeWorkspaceOpenResult{Status: desktop.WorkspaceOpenCancelled}, nil
	}
	if err := ctx.Err(); err != nil {
		return desktop.NativeWorkspaceOpenResult{}, err
	}
	// Revalidate after the native confirmation to reduce replacement races.
	if err := validateWorkspaceDirectory(target.RootPath); err != nil {
		return desktop.NativeWorkspaceOpenResult{}, err
	}
	if err := validateLauncherExecutable(candidate); err != nil {
		return desktop.NativeWorkspaceOpenResult{}, err
	}
	if err := l.start(ctx, candidate, target); err != nil {
		return desktop.NativeWorkspaceOpenResult{}, err
	}
	return desktop.NativeWorkspaceOpenResult{
		Status: desktop.WorkspaceOpenStarted, OperatorConfirmed: true,
		ExternalProcessStarted: true,
	}, nil
}

func confirmWorkspaceOpen(ctx context.Context, candidate workspaceLauncherCandidate,
	target desktop.WorkspaceOpenTarget) (bool, error) {
	answer, err := runtime.MessageDialog(ctx, runtime.MessageDialogOptions{
		Type:  runtime.QuestionDialog,
		Title: "打开工作区",
		Message: fmt.Sprintf("使用 %s 打开 Workspace \"%s\"？\n\n目录：%s\n应用：%s\n\n"+
			"针路簿只传递已登记目录，不执行命令。外部应用可能读取该目录内容。",
			candidate.descriptor.Label, target.Name, target.RootPath, candidate.executable),
		Buttons: []string{"打开", "取消"}, DefaultButton: "取消", CancelButton: "取消",
	})
	if err != nil {
		return false, err
	}
	return answer == "打开", nil
}

func startWorkspaceLauncher(ctx context.Context, candidate workspaceLauncherCandidate,
	target desktop.WorkspaceOpenTarget) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	command, err := workspaceLauncherCommand(candidate, target)
	if err != nil {
		return err
	}
	if err := command.Start(); err != nil {
		return err
	}
	// The external application owns its lifecycle after the confirmed launch.
	_ = command.Process.Release()
	return nil
}

func workspaceLauncher(id, label string, kind desktop.WorkspaceLauncherKind,
	executable string, passRoot, allowReparse bool) workspaceLauncherCandidate {
	return workspaceLauncherCandidate{
		descriptor: desktop.WorkspaceLauncherDescriptor{ID: id, Label: label, Kind: kind},
		// An empty executable stays empty: the macOS Finder candidate opens the
		// registered directory through /usr/bin/open and carries no app path.
		executable: cleanWorkspaceLauncherExecutable(executable),
		passRoot:   passRoot, allowReparse: allowReparse,
	}
}

func cleanWorkspaceLauncherExecutable(value string) string {
	if value == "" {
		return ""
	}
	return filepath.Clean(value)
}

func addWorkspaceLauncher(target map[string]workspaceLauncherCandidate,
	candidate workspaceLauncherCandidate) {
	if _, exists := target[candidate.descriptor.ID]; exists {
		return
	}
	if err := validateLauncherExecutable(candidate); err != nil {
		return
	}
	target[candidate.descriptor.ID] = candidate
}

func findWorkspaceLauncher(candidates []workspaceLauncherCandidate,
	id string) (workspaceLauncherCandidate, bool) {
	for _, candidate := range candidates {
		if candidate.descriptor.ID == id {
			return candidate, true
		}
	}
	return workspaceLauncherCandidate{}, false
}

func orderedWorkspaceLaunchers(candidates map[string]workspaceLauncherCandidate,
	order map[string]int) []workspaceLauncherCandidate {
	out := make([]workspaceLauncherCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		out = append(out, candidate)
	}
	sort.Slice(out, func(left, right int) bool {
		leftOrder, leftKnown := order[out[left].descriptor.ID]
		rightOrder, rightKnown := order[out[right].descriptor.ID]
		if leftKnown != rightKnown {
			return leftKnown
		}
		if leftOrder != rightOrder {
			return leftOrder < rightOrder
		}
		return out[left].descriptor.ID < out[right].descriptor.ID
	})
	return out
}

func validateWorkspaceDirectory(root string) error {
	if root == "" || !filepath.IsAbs(root) || strings.ContainsRune(root, 0) {
		return errors.New("registered workspace directory is invalid")
	}
	if root != filepath.Clean(root) {
		return errors.New("registered workspace directory is not canonical")
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return errors.New("registered workspace directory is unavailable")
	}
	return nil
}
