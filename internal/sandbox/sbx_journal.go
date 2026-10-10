package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type sbxInventoryEntry struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Workspaces []string `json:"workspaces"`
}

// The envelope and fields are the local CLI contract consumed by Docker's
// first-party client, not the unrelated experimental hosted Sandbox API:
// https://github.com/docker/docker-agent/blob/main/pkg/sandbox/sandbox.go
func (b *SBXBackend) inventory(ctx context.Context) ([]sbxInventoryEntry, error) {
	result, err := b.call(ctx, []string{"ls", "--json"}, nil, 1024*1024)
	if err != nil || result.ExitCode != 0 {
		return nil, errors.Join(err, ErrSBXCLI)
	}
	if err := sbxUniqueJSON(result.Stdout); err != nil {
		return nil, err
	}
	var value struct {
		Sandboxes *[]sbxInventoryEntry `json:"sandboxes"`
	}
	if err := json.Unmarshal(result.Stdout, &value); err != nil || value.Sandboxes == nil {
		return nil, ErrSBXOwnership
	}
	ids, names := map[string]bool{}, map[string]bool{}
	for _, entry := range *value.Sandboxes {
		if !sbxIdentifier(entry.ID) || !sbxIdentifier(entry.Name) || ids[entry.ID] || names[entry.Name] || len(entry.Workspaces) > 128 {
			return nil, ErrSBXOwnership
		}
		ids[entry.ID], names[entry.Name] = true, true
		for _, workspace := range entry.Workspaces {
			if workspace == "" || strings.ContainsRune(workspace, 0) || len(workspace) > 4096 {
				return nil, ErrSBXOwnership
			}
		}
	}
	return *value.Sandboxes, nil
}

type sbxRecord struct {
	AppName            string `json:"app_name"`
	Version            string `json:"version"`
	OperationDigest    string `json:"operation_digest"`
	RequestFingerprint string `json:"request_fingerprint"`
	Name               string `json:"name"`
	ID                 string `json:"id"`
	Workspace          string `json:"workspace"`
	Phase              string `json:"phase"`
	Removed            bool   `json:"removed"`
}

func (r sbxRecord) valid() bool {
	return r.AppName == SBXAppName && r.Version == SBXPolicyVersion && sbxSHA(r.OperationDigest) && sbxSHA(r.RequestFingerprint) &&
		strings.HasPrefix(r.Name, "traverse-sbx-") && len(r.Name) == 45 &&
		sbxSHA(r.Name[13:]+r.Name[13:]) &&
		(r.ID == "" || sbxIdentifier(r.ID)) && filepath.IsAbs(r.Workspace) && filepath.Clean(r.Workspace) == r.Workspace &&
		(r.Phase == "reserved" || r.Phase == "unused" || r.Phase == "created" || r.Phase == "dispatching" || r.Phase == "removed") &&
		(!r.Removed || r.Phase == "removed")
}

func sbxIdentifier(value string) bool {
	if value == "" || len(value) > 256 {
		return false
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	return value[0] != '-'
}
func sbxFind(entries []sbxInventoryEntry, name string) (sbxInventoryEntry, bool, error) {
	for _, entry := range entries {
		if entry.Name == name {
			return entry, true, nil
		}
	}
	return sbxInventoryEntry{}, false, nil
}
func sbxMountsMatch(entry sbxInventoryEntry, record sbxRecord) bool {
	return len(entry.Workspaces) == 2 && entry.Workspaces[0] == record.Workspace &&
		entry.Workspaces[1] == filepath.Join(record.Workspace, ".git")+":ro"
}

func (b *SBXBackend) save(record sbxRecord) error {
	if !record.valid() {
		return ErrSBXOwnership
	}
	if _, err := sbxCanonical(b.config.JournalRoot, true); err != nil {
		return err
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(b.config.JournalRoot, ".sbx-record-*")
	if err != nil {
		return err
	}
	temp := file.Name()
	defer os.Remove(temp)
	if err := file.Chmod(0600); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	target := filepath.Join(b.config.JournalRoot, record.OperationDigest+".json")
	if info, err := os.Lstat(target); err == nil && !info.Mode().IsRegular() {
		return ErrSBXOwnership
	}
	if err := os.Rename(temp, target); err != nil {
		return err
	}
	return sbxSyncJournalDirectory(b.config.JournalRoot)
}

func (b *SBXBackend) removeOwned(ctx context.Context, record *sbxRecord) error {
	all, err := b.inventory(ctx)
	if err != nil {
		return errors.Join(ErrSBXCleanup, err)
	}
	entry, found, err := sbxFind(all, record.Name)
	if err != nil {
		return err
	}
	if !found {
		// An ID can survive under a new name: absence of the reserved name alone
		// does not prove this owned VM has gone.
		for _, candidate := range all {
			if record.ID != "" && candidate.ID == record.ID {
				return ErrSBXOwnership
			}
		}
		if record.ID == "" {
			return ErrSBXOwnership
		}
		record.Phase, record.Removed = "removed", true
		return b.save(*record)
	}
	if record.ID == "" || entry.ID != record.ID || !sbxMountsMatch(entry, *record) {
		return ErrSBXOwnership
	}
	stopped, stopErr := b.call(ctx, []string{"stop", record.Name}, nil, 4096)
	if stopErr != nil || stopped.ExitCode != 0 {
		return errors.Join(ErrSBXCleanup, stopErr)
	}
	all, err = b.inventory(ctx)
	if err != nil {
		return errors.Join(ErrSBXCleanup, err)
	}
	entry, found, err = sbxFind(all, record.Name)
	if err != nil || !found || entry.ID != record.ID || !sbxMountsMatch(entry, *record) {
		return ErrSBXOwnership
	}
	removed, rmErr := b.call(ctx, []string{"rm", "--force", record.Name}, nil, 4096)
	if rmErr != nil || removed.ExitCode != 0 {
		return errors.Join(ErrSBXCleanup, rmErr)
	}
	all, err = b.inventory(ctx)
	if err != nil {
		return errors.Join(ErrSBXCleanup, err)
	}
	for _, candidate := range all {
		if candidate.ID == record.ID || candidate.Name == record.Name {
			return ErrSBXCleanup
		}
	}
	record.Phase, record.Removed = "removed", true
	return b.save(*record)
}

// RecoverStartup does not replay a command. It only removes resources with a
// previously persisted immutable ID and the same exact local mount identity.
// A create interrupted before its ID was journaled remains unresolved rather
// than adopting a VM merely because its name shares our prefix.
func (b *SBXBackend) RecoverStartup(ctx context.Context) error {
	if b == nil || ctx == nil {
		return ErrSBXBoundary
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.lock == nil || b.closed {
		return ErrSBXUnavailable
	}
	return b.recoverLocked(ctx)
}

func (b *SBXBackend) recoverLocked(ctx context.Context) error {
	entries, err := os.ReadDir(b.config.JournalRoot)
	if err != nil {
		return err
	}
	var failures []error
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		file := filepath.Join(b.config.JournalRoot, entry.Name())
		if _, err := sbxCanonical(file, false); err != nil {
			failures = append(failures, ErrSBXOwnership)
			continue
		}
		data, err := sbxReadRecord(file)
		if err != nil || len(data) > 64*1024 || sbxUniqueJSON(data) != nil {
			failures = append(failures, ErrSBXOwnership)
			continue
		}
		var record sbxRecord
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&record) != nil || !record.valid() || entry.Name() != record.OperationDigest+".json" {
			failures = append(failures, ErrSBXOwnership)
			continue
		}
		if record.Removed || record.Phase == "unused" {
			continue
		}
		if err := b.removeOwned(ctx, &record); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func sbxReadRecord(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(io.LimitReader(file, 64*1024+1))
}

func (b *SBXBackend) Close() error {
	if b == nil {
		return nil
	}
	// Cancellation terminates the foreground client; Run still owns the VM
	// and retains the journal lock while its independent cleanup confirms rm.
	b.cancel()
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	b.closed = true
	if b.lock != nil {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		cleanupErr := b.recoverLocked(cleanup)
		cancel()
		err := errors.Join(cleanupErr, b.lock.Close())
		b.lock = nil
		return err
	}
	return nil
}

// Reject ambiguous authority input even when encoding/json would select the
// last duplicate field. Unknown additive inventory fields remain compatible.
func sbxUniqueJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var walk func(int) error
	walk = func(depth int) error {
		if depth > 32 {
			return ErrSBXOwnership
		}
		token, err := decoder.Token()
		if err != nil {
			return ErrSBXOwnership
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		if delim == '{' {
			keys := map[string]bool{}
			for decoder.More() {
				key, err := decoder.Token()
				if err != nil {
					return ErrSBXOwnership
				}
				s, ok := key.(string)
				if !ok || keys[s] {
					return ErrSBXOwnership
				}
				keys[s] = true
				if err := walk(depth + 1); err != nil {
					return err
				}
			}
		} else if delim == '[' {
			for decoder.More() {
				if err := walk(depth + 1); err != nil {
					return err
				}
			}
		} else {
			return ErrSBXOwnership
		}
		_, err = decoder.Token()
		return err
	}
	if err := walk(0); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return ErrSBXOwnership
	}
	return nil
}
