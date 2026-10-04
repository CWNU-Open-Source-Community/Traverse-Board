package runner

import (
	"errors"
	"path/filepath"
	"strings"
)

const (
	OnceCommandProtocolVersion   = "once_command.v1"
	OnceCommandPolicyVersion     = "once_command_policy.v1"
	OnceExecutionProtocolVersion = "once_execution.v1"
	MaxOnceOutputBytes           = 64 * 1024
)

var ErrOnceCommandBoundary = errors.New("once command boundary is invalid")

func pathWithin(candidate, root string) (bool, error) {
	candidate = filepath.Clean(candidate)
	root = filepath.Clean(root)
	relative, err := filepath.Rel(root, candidate)
	if err != nil {
		return false, err
	}
	if relative == "." {
		return true, nil
	}
	return !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && relative != "..", nil
}
