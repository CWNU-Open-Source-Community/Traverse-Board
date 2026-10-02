package agentpackages

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// directorySource adapts absolute, contained links (notably Windows junctions)
// to the relative names accepted by os.Root. Resolving a path alone never
// authorizes an ordinary os.Open: the actual open always goes through the held
// root, which rechecks traversal if a link changes between resolution and open.
// The host owns the immutable installation snapshot and its expected digests.
type directorySource struct {
	root      *os.Root
	canonical string
}

func openDirectory(name string) (*directorySource, error) {
	root, err := os.OpenRoot(name)
	if err != nil {
		return nil, fmt.Errorf("open package root: %w", err)
	}
	canonical, err := directoryCanonical(root, name)
	if err != nil {
		_ = root.Close()
		return nil, fmt.Errorf("resolve package root: %w", err)
	}
	return &directorySource{root: root, canonical: canonical}, nil
}

func (s *directorySource) Open(name string) (fs.File, error) {
	relative, err := s.resolve(name)
	if err != nil {
		return nil, err
	}
	return openRootReadOnly(s.root, relative)
}

func (s *directorySource) Stat(name string) (fs.FileInfo, error) {
	relative, err := s.resolve(name)
	if err != nil {
		return nil, err
	}
	return s.root.Stat(relative)
}

func (s *directorySource) resolve(name string) (string, error) {
	local, err := filepath.Localize(name)
	if err != nil {
		return "", &fs.PathError{Op: "resolve", Path: name, Err: fs.ErrInvalid}
	}
	return resolveDirectoryPath(s.root, s.canonical, local)
}

func (s *directorySource) Close() error { return s.root.Close() }
