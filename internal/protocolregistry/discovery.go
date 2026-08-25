package protocolregistry

import (
	"bytes"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	maxScannedFileBytes = 16 << 20
	runtimeImportPath   = "cyberagent-workbench/internal/protocolregistry"
)

var discoveredProtocolPattern = regexp.MustCompile(`[A-Za-z][A-Za-z0-9_.-]*\.v[0-9]+`)

type Inventory map[string][]string

func Discover(root string, policy ScanPolicy) (Inventory, error) {
	extensions := make(map[string]struct{}, len(policy.Extensions))
	for _, extension := range policy.Extensions {
		extensions[extension] = struct{}{}
	}
	files := make(map[string]string)
	for _, scanRoot := range policy.Roots {
		full, err := joinRepositoryPath(root, scanRoot)
		if err != nil {
			return nil, err
		}
		info, err := os.Lstat(full)
		if err != nil {
			return nil, fmt.Errorf("inspect scan root %q: %w", scanRoot, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("scan root %q is a symbolic link", scanRoot)
		}
		if !info.IsDir() {
			if _, ok := extensions[strings.ToLower(filepath.Ext(full))]; ok {
				files[scanRoot] = full
			}
			continue
		}
		err = filepath.WalkDir(full, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.Type()&os.ModeSymlink != 0 {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if entry.IsDir() {
				return nil
			}
			if _, ok := extensions[strings.ToLower(filepath.Ext(path))]; !ok {
				return nil
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			files[filepath.ToSlash(relative)] = path
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("walk scan root %q: %w", scanRoot, err)
		}
	}

	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	inventorySets := make(map[string]map[string]struct{})
	for _, relative := range paths {
		info, err := os.Stat(files[relative])
		if err != nil {
			return nil, fmt.Errorf("stat scanned file %q: %w", relative, err)
		}
		if info.Size() > maxScannedFileBytes {
			return nil, fmt.Errorf("scanned file %q exceeds %d bytes", relative, maxScannedFileBytes)
		}
		raw, err := os.ReadFile(files[relative])
		if err != nil {
			return nil, fmt.Errorf("read scanned file %q: %w", relative, err)
		}
		if !utf8.Valid(raw) {
			return nil, fmt.Errorf("scanned file %q is not valid UTF-8", relative)
		}
		for _, identifier := range discoverIdentifiers(raw) {
			if inventorySets[identifier] == nil {
				inventorySets[identifier] = make(map[string]struct{})
			}
			inventorySets[identifier][relative] = struct{}{}
		}
	}
	inventory := make(Inventory, len(inventorySets))
	for identifier, sources := range inventorySets {
		for source := range sources {
			inventory[identifier] = append(inventory[identifier], source)
		}
		sort.Strings(inventory[identifier])
	}
	return inventory, nil
}

func CompareInventory(registry Registry, inventory Inventory) error {
	active := make(map[string]string)
	retired := make(map[string]string)
	for _, family := range registry.Families {
		for _, identifier := range family.ActiveIdentifiers {
			active[identifier] = family.ID
		}
		for _, identifier := range family.RetiredIdentifiers {
			retired[identifier.Identifier] = family.ID
		}
	}
	allowlist := make(map[string]AllowlistEntry)
	for _, entry := range registry.TestAndGoldenAllowlist {
		allowlist[entry.Identifier] = entry
	}
	var problems []string
	for identifier, sources := range inventory {
		if _, ok := active[identifier]; ok {
			continue
		}
		if _, ok := retired[identifier]; ok {
			continue
		}
		if entry, ok := allowlist[identifier]; ok {
			if !equalStrings(entry.Sources, sources) {
				problems = append(problems, fmt.Sprintf("allowlisted identifier %q source drift: registry=%v discovered=%v", identifier, entry.Sources, sources))
			}
			continue
		}
		problems = append(problems, fmt.Sprintf("unregistered protocol identifier %q in %s", identifier, strings.Join(sources, ", ")))
	}
	for identifier, family := range active {
		if _, ok := inventory[identifier]; !ok {
			problems = append(problems, fmt.Sprintf("active protocol identifier %q in family %q was deleted or renamed without retirement", identifier, family))
		}
	}
	for identifier, entry := range allowlist {
		if _, ok := inventory[identifier]; !ok {
			problems = append(problems, fmt.Sprintf("allowlisted %s identifier %q is stale", entry.Kind, identifier))
		}
	}
	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	return errors.New("protocol registry drift:\n- " + strings.Join(problems, "\n- "))
}

// CheckRuntimeAuthorityBoundary ensures the governance package cannot silently
// become an Application/runtime dependency.
func CheckRuntimeAuthorityBoundary(root string) error {
	var consumers []string
	for _, scanRoot := range []string{"cmd", "internal"} {
		full, err := joinRepositoryPath(root, scanRoot)
		if err != nil {
			return err
		}
		err = filepath.WalkDir(full, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			relative = filepath.ToSlash(relative)
			if strings.HasPrefix(relative, "cmd/protocolregistry/") || strings.HasPrefix(relative, "internal/protocolregistry/") {
				return nil
			}
			parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				return fmt.Errorf("parse imports in %q: %w", relative, err)
			}
			for _, imported := range parsed.Imports {
				if strings.Trim(imported.Path.Value, `"`) == runtimeImportPath {
					consumers = append(consumers, relative)
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	if len(consumers) != 0 {
		sort.Strings(consumers)
		return fmt.Errorf("protocol registry is governance-only but runtime imports were found: %s", strings.Join(consumers, ", "))
	}
	return nil
}

func discoverIdentifiers(raw []byte) []string {
	indices := discoveredProtocolPattern.FindAllIndex(raw, -1)
	seen := make(map[string]struct{})
	for _, index := range indices {
		if index[0] > 0 && isProtocolIdentifierByte(raw[index[0]-1]) {
			continue
		}
		if index[1] < len(raw) && isProtocolIdentifierByte(raw[index[1]]) {
			continue
		}
		identifier := string(raw[index[0]:index[1]])
		if protocolIDPattern.MatchString(identifier) {
			seen[identifier] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for identifier := range seen {
		result = append(result, identifier)
	}
	sort.Strings(result)
	return result
}

func isProtocolIdentifierByte(value byte) bool {
	// A period immediately after vN is commonly the length/domain separator in
	// fingerprints (for example, a version token followed by a period), so it
	// terminates the identifier.
	// Letters, digits, underscore, and hyphen still reject partial matches such as
	// a version token followed by a suffix or preview marker.
	return bytes.IndexByte([]byte("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_-"), value) >= 0
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
