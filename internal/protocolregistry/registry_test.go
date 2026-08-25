package protocolregistry

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepositoryRegistryIsValidSynchronizedAndNonAuthorizing(t *testing.T) {
	root := repositoryRoot(t)
	registry := loadRepositoryRegistry(t, root)
	if err := ValidateRepositoryPaths(root, registry); err != nil {
		t.Fatal(err)
	}
	inventory, err := Discover(root, registry.Scan)
	if err != nil {
		t.Fatal(err)
	}
	if err := CompareInventory(registry, inventory); err != nil {
		t.Fatal(err)
	}
	if err := CheckRuntimeAuthorityBoundary(root); err != nil {
		t.Fatal(err)
	}
	generated, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(GeneratedDocument)))
	if err != nil {
		t.Fatal(err)
	}
	if expected := RenderMarkdown(registry); !bytes.Equal(generated, expected) {
		t.Fatalf("%s is stale; run go run ./cmd/protocolregistry -write", GeneratedDocument)
	}
}

func TestCompatibilityExampleDualReadsWritesNewAndFailsClosed(t *testing.T) {
	oldFixture, err := os.ReadFile("testdata/compatibility_v1.json")
	if err != nil {
		t.Fatal(err)
	}
	old, err := decodeCompatibilityEnvelope(oldFixture)
	if err != nil {
		t.Fatalf("old v1 fixture is no longer readable: %v", err)
	}
	if old.Value != "legacy" {
		t.Fatalf("old fixture value = %q", old.Value)
	}

	written, err := encodeCompatibilityEnvelope("new")
	if err != nil {
		t.Fatal(err)
	}
	var wire compatibilityEnvelope
	if err := json.Unmarshal(written, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.Protocol != compatibilityProtocolV2 {
		t.Fatalf("writer protocol = %q, want write-new %q", wire.Protocol, compatibilityProtocolV2)
	}
	if _, err := decodeCompatibilityEnvelope(written); err != nil {
		t.Fatalf("v2 reader failed: %v", err)
	}

	unknown := []byte(`{"protocol":"protocol_registry_example.v3","value":"future"}`)
	if _, err := decodeCompatibilityEnvelope(unknown); err == nil || !strings.Contains(err.Error(), "unsupported protocol") {
		t.Fatalf("unknown version did not fail closed: %v", err)
	}
}

func TestEvolutionRetainsDurableReaderHistory(t *testing.T) {
	previous := loadRepositoryRegistry(t, repositoryRoot(t))
	current := cloneRegistry(t, previous)
	index := firstFamilyWithClass(t, current, ClassExternal)
	oldReader := current.Families[index].Readers[0]
	current.Families[index].Readers = append(current.Families[index].Readers, Reader{
		ID:       oldReader.ID + "-replacement",
		Source:   oldReader.Source,
		Versions: append([]int(nil), oldReader.Versions...),
		Status:   ReaderActive,
	})
	current.Families[index].Readers = current.Families[index].Readers[1:]
	if err := ValidateStructure(current); err != nil {
		t.Fatalf("test registry is structurally invalid: %v", err)
	}
	if err := ValidateEvolution(previous, current); err == nil || !strings.Contains(err.Error(), "deleted reader history") {
		t.Fatalf("durable reader deletion was not rejected: %v", err)
	}
}

func TestEvolutionAllowsEvidenceBackedReaderRetirementWithReplacement(t *testing.T) {
	previous := loadRepositoryRegistry(t, repositoryRoot(t))
	current := cloneRegistry(t, previous)
	index := firstFamilyWithVersions(t, current, ClassInternal, 1, 2)
	oldReader := current.Families[index].Readers[0]
	var activeV2 []string
	for _, identifier := range current.Families[index].ActiveIdentifiers {
		version, err := protocolVersion(identifier)
		if err != nil {
			t.Fatal(err)
		}
		if version == 1 {
			current.Families[index].RetiredIdentifiers = append(
				current.Families[index].RetiredIdentifiers,
				RetiredIdentifier{
					Identifier:        identifier,
					Decision:          "ADR-test-v1-retirement",
					MigrationEvidence: "All v1 rows migrated transactionally; old fixtures retained.",
					Rollback:          "Restore the v1 reader and the pre-migration backup.",
				},
			)
			continue
		}
		activeV2 = append(activeV2, identifier)
	}
	current.Families[index].ActiveIdentifiers = activeV2
	current.Families[index].Readers[0].Status = ReaderRetired
	current.Families[index].Readers[0].Retirement = &ReaderRetirement{
		Decision:          "ADR-test-v1-retirement",
		MigrationEvidence: "All v1 rows migrated under a transaction; old fixtures retained.",
		Rollback:          "Restore the reader and the pre-migration backup.",
	}
	current.Families[index].Readers = append(current.Families[index].Readers, Reader{
		ID:       oldReader.ID + "-replacement",
		Source:   oldReader.Source,
		Versions: []int{2},
		Status:   ReaderActive,
	})
	current.Families[index].Writers[0].Versions = []int{2}
	current.Families[index].Writers[0].Mode = WriterNew
	if err := ValidateStructure(current); err != nil {
		t.Fatalf("evidence-backed retirement is structurally invalid: %v", err)
	}
	if err := ValidateEvolution(previous, current); err != nil {
		t.Fatalf("evidence-backed retirement was rejected: %v", err)
	}
}

func TestClassSpecificProofsAreMandatory(t *testing.T) {
	registry := loadRepositoryRegistry(t, repositoryRoot(t))
	tests := []struct {
		name  string
		class string
		edit  func(*Family)
		want  string
	}{
		{
			name:  "projection rebuild source",
			class: ClassProjection,
			edit:  func(family *Family) { family.RebuildSource = "" },
			want:  "rebuild source",
		},
		{
			name:  "ephemeral restart persistence",
			class: ClassEphemeral,
			edit:  func(family *Family) { family.PersistedOrExported = true },
			want:  "non-persisted",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := cloneRegistry(t, registry)
			index := firstFamilyWithClass(t, candidate, test.class)
			test.edit(&candidate.Families[index])
			if err := ValidateStructure(candidate); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("missing class proof was accepted: %v", err)
			}
		})
	}
}

func TestDecodeRejectsDuplicateAndUnknownFields(t *testing.T) {
	for _, test := range []struct {
		name string
		raw  string
		want string
	}{
		{name: "duplicate", raw: `{"schema":"protocol-family-registry.v1","schema":"protocol-family-registry.v1"}`, want: "duplicate field"},
		{name: "unknown", raw: `{"schema":"protocol-family-registry.v1","unexpected":true}`, want: "unknown field"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Decode([]byte(test.raw)); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("invalid registry was accepted: %v", err)
			}
		})
	}
}

func TestIdentifierDiscoveryUsesExactTokenBoundaries(t *testing.T) {
	identifiers := discoverIdentifiers([]byte("ok.v1 bad.v2suffix prefixbad.v3x next.good.v4"))
	if len(identifiers) != 2 || identifiers[0] != "next.good.v4" || identifiers[1] != "ok.v1" {
		t.Fatalf("identifiers = %v", identifiers)
	}
}

func TestInventoryDriftRejectsAdditionDeletionUpgradeAndAllowlistMove(t *testing.T) {
	root := repositoryRoot(t)
	registry := loadRepositoryRegistry(t, root)
	discovered, err := Discover(root, registry.Scan)
	if err != nil {
		t.Fatal(err)
	}
	cloneInventory := func() Inventory {
		clone := make(Inventory, len(discovered))
		for identifier, sources := range discovered {
			clone[identifier] = append([]string(nil), sources...)
		}
		return clone
	}
	active := registry.Families[0].ActiveIdentifiers[0]
	allowlisted := registry.TestAndGoldenAllowlist[0].Identifier
	unregisteredUpgrade := strings.Join([]string{"unregistered_protocol", ".v999"}, "")
	tests := []struct {
		name string
		edit func(Inventory)
		want string
	}{
		{
			name: "delete active protocol",
			edit: func(inventory Inventory) { delete(inventory, active) },
			want: "deleted or renamed without retirement",
		},
		{
			name: "add or upgrade protocol",
			edit: func(inventory Inventory) { inventory[unregisteredUpgrade] = []string{"internal/domain/example.go"} },
			want: "unregistered protocol identifier",
		},
		{
			name: "move allowlisted fixture",
			edit: func(inventory Inventory) { inventory[allowlisted] = []string{"internal/domain/example_test.go"} },
			want: "source drift",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := cloneInventory()
			test.edit(candidate)
			if err := CompareInventory(registry, candidate); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("inventory drift was accepted: %v", err)
			}
		})
	}
}

const (
	compatibilityProtocolV1 = "protocol_registry_example.v1"
	compatibilityProtocolV2 = "protocol_registry_example.v2"
)

type compatibilityEnvelope struct {
	Protocol string `json:"protocol"`
	Value    string `json:"value"`
}

func decodeCompatibilityEnvelope(raw []byte) (compatibilityEnvelope, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var envelope compatibilityEnvelope
	if err := decoder.Decode(&envelope); err != nil {
		return compatibilityEnvelope{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return compatibilityEnvelope{}, errors.New("compatibility envelope contains trailing data")
	}
	switch envelope.Protocol {
	case compatibilityProtocolV1, compatibilityProtocolV2:
	default:
		return compatibilityEnvelope{}, errors.New("unsupported protocol version")
	}
	if envelope.Value == "" {
		return compatibilityEnvelope{}, errors.New("compatibility envelope value is empty")
	}
	return envelope, nil
}

func encodeCompatibilityEnvelope(value string) ([]byte, error) {
	if value == "" {
		return nil, errors.New("compatibility envelope value is empty")
	}
	return json.Marshal(compatibilityEnvelope{Protocol: compatibilityProtocolV2, Value: value})
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func loadRepositoryRegistry(t *testing.T, root string) Registry {
	t.Helper()
	registry, err := LoadFile(filepath.Join(root, filepath.FromSlash(RegistryPath)))
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func cloneRegistry(t *testing.T, registry Registry) Registry {
	t.Helper()
	raw, err := json.Marshal(registry)
	if err != nil {
		t.Fatal(err)
	}
	clone, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	return clone
}

func firstFamilyWithClass(t *testing.T, registry Registry, class string) int {
	t.Helper()
	for index := range registry.Families {
		if registry.Families[index].Class == class {
			return index
		}
	}
	t.Fatalf("registry has no %s family", class)
	return -1
}

func firstFamilyWithVersions(t *testing.T, registry Registry, class string, versions ...int) int {
	t.Helper()
	for index := range registry.Families {
		family := registry.Families[index]
		if family.Class != class {
			continue
		}
		seen := make(map[int]struct{})
		for _, identifier := range family.ActiveIdentifiers {
			version, err := protocolVersion(identifier)
			if err != nil {
				t.Fatal(err)
			}
			seen[version] = struct{}{}
		}
		matched := true
		for _, version := range versions {
			if _, ok := seen[version]; !ok {
				matched = false
			}
		}
		if matched {
			return index
		}
	}
	t.Fatalf("registry has no %s family with versions %v", class, versions)
	return -1
}
