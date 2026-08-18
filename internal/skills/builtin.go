package skills

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
)

//go:embed builtins archives
var builtinFiles embed.FS

func BuiltinRegistry() (*Registry, error) {
	registry, err := LoadFS(builtinFiles, "builtins")
	if err != nil {
		return nil, err
	}
	archives, err := fs.ReadDir(builtinFiles, "archives")
	if err != nil {
		return nil, err
	}
	for _, archive := range archives {
		if !archive.IsDir() || !validCoreVersion(archive.Name()) {
			return nil, fmt.Errorf("invalid embedded Skill archive %q", archive.Name())
		}
		history, err := LoadFS(builtinFiles, path.Join("archives", archive.Name()))
		if err != nil {
			return nil, err
		}
		if err := registry.mergeHistory(history); err != nil {
			return nil, err
		}
	}
	return registry, nil
}
