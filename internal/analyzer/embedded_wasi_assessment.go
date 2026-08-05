package analyzer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

const (
	AnalyzerEmbeddedWASIAssessmentProtocolVersion  = "analyzer_embedded_wasi_module_assessment.v1"
	MaxAnalyzerEmbeddedWASIAssessmentEnvelopeBytes = 32 * 1024
	maxAnalyzerEmbeddedWASIImports                 = 64
	maxAnalyzerEmbeddedWASIImportNameBytes         = 128
	maxAnalyzerEmbeddedWASIExports                 = 64
	maxAnalyzerEmbeddedWASIExportNameBytes         = 128

	AnalyzerEmbeddedWASIFailureNonFunctionImport = "non_function_import_denied"
	AnalyzerEmbeddedWASIFailureImportInventory   = "import_inventory_mismatch"
	AnalyzerEmbeddedWASIFailureFunctionImport    = "function_import_denied"
	AnalyzerEmbeddedWASIFailureExportSurface     = "function_export_surface_denied"
	AnalyzerEmbeddedWASIFailureMemoryImport      = "memory_import_denied"
	AnalyzerEmbeddedWASIFailureMemoryExport      = "memory_export_missing"
	AnalyzerEmbeddedWASIFailureMemoryLimit       = "memory_limit_exceeded"
	AnalyzerEmbeddedWASIFailureStartExport       = "start_export_missing"
)

type AnalyzerEmbeddedWASIImport struct {
	Module    string `json:"module"`
	Name      string `json:"name"`
	Signature string `json:"signature"`
}

// AnalyzerEmbeddedWASIAssessment records compile-time validation only. The
// guest is never instantiated and no WASI host module is registered here.
type AnalyzerEmbeddedWASIAssessment struct {
	ProtocolVersion              string                          `json:"protocol_version"`
	ProfileSHA256                string                          `json:"profile_sha256"`
	ModuleSHA256                 string                          `json:"module_sha256"`
	ModuleBytes                  int                             `json:"module_bytes"`
	Imports                      []AnalyzerEmbeddedWASIImport    `json:"imports"`
	Exports                      []string                        `json:"exports"`
	ImportedFunctionCount        int                             `json:"imported_function_count"`
	ImportedNonFunctionCount     int                             `json:"imported_non_function_count"`
	ImportedMemoryCount          int                             `json:"imported_memory_count"`
	ExportedMemoryCount          int                             `json:"exported_memory_count"`
	InitialMemoryPages           uint32                          `json:"initial_memory_pages"`
	DeclaredMaxMemoryPages       uint32                          `json:"declared_max_memory_pages"`
	DeclaredMaxMemoryPresent     bool                            `json:"declared_max_memory_present"`
	EffectiveMaxMemoryPages      uint32                          `json:"effective_max_memory_pages"`
	ImportInventoryMatches       bool                            `json:"import_inventory_matches"`
	AllFunctionImportsAllowed    bool                            `json:"all_function_imports_allowed"`
	FunctionExportSurfaceAllowed bool                            `json:"function_export_surface_allowed"`
	MemoryPolicyPassed           bool                            `json:"memory_policy_passed"`
	StartExportPresent           bool                            `json:"start_export_present"`
	CompiledOnly                 bool                            `json:"compiled_only"`
	Instantiated                 bool                            `json:"instantiated"`
	GuestExecuted                bool                            `json:"guest_executed"`
	CandidateOnly                bool                            `json:"candidate_only"`
	DefaultDeny                  bool                            `json:"default_deny"`
	StartBlocked                 bool                            `json:"start_blocked"`
	ProductInvocationAuthorized  bool                            `json:"product_invocation_authorized"`
	Passed                       bool                            `json:"passed"`
	FailureCode                  string                          `json:"failure_code"`
	Authority                    AnalyzerProductAdapterAuthority `json:"authority"`
	Fingerprint                  string                          `json:"fingerprint"`
}

var analyzerEmbeddedWASIAllowedImports = map[string]string{
	"args_get":          "i32,i32->i32",
	"args_sizes_get":    "i32,i32->i32",
	"environ_get":       "i32,i32->i32",
	"environ_sizes_get": "i32,i32->i32",
	"fd_fdstat_get":     "i32,i32->i32",
	"fd_read":           "i32,i32,i32,i32->i32",
	"fd_write":          "i32,i32,i32,i32->i32",
	"proc_exit":         "i32->",
	"random_get":        "i32,i32->i32",
}

var analyzerEmbeddedWASIAllowedFunctionExports = map[string]struct{}{
	"__main_void": {},
	"_start":      {},
}

func AssessAnalyzerEmbeddedWASIModule(
	ctx context.Context,
	module []byte,
	profile AnalyzerEmbeddedWASIProfile,
) (AnalyzerEmbeddedWASIAssessment, ErrorCode) {
	if ctx == nil || ValidateAnalyzerEmbeddedWASIProfile(profile) != "" {
		return AnalyzerEmbeddedWASIAssessment{}, CodeInvalidRequest
	}
	if len(module) == 0 {
		return AnalyzerEmbeddedWASIAssessment{}, CodeInvalidContent
	}
	if len(module) > profile.ModuleLimitBytes {
		return AnalyzerEmbeddedWASIAssessment{}, CodeInputLimitExceeded
	}

	runtimeConfig := wazero.NewRuntimeConfigInterpreter().
		WithCoreFeatures(api.CoreFeaturesV2).
		WithMemoryLimitPages(profile.MemoryLimitPages).
		WithCloseOnContextDone(profile.CloseOnContextDone).
		WithDebugInfoEnabled(profile.DebugInfoEnabled).
		WithCustomSections(profile.CustomSectionsEnabled)
	runtime := wazero.NewRuntimeWithConfig(ctx, runtimeConfig)
	compiled, err := runtime.CompileModule(ctx, module)
	if err != nil {
		_ = runtime.Close(context.Background())
		if ctx.Err() != nil {
			return AnalyzerEmbeddedWASIAssessment{}, CodeDeadlineExceeded
		}
		return AnalyzerEmbeddedWASIAssessment{}, CodeInvalidContent
	}

	assessment := analyzerEmbeddedWASIAssessmentBase(module, profile)
	assessment.CompiledOnly = true
	assessment.Imports = collectAnalyzerEmbeddedWASIImports(compiled)
	assessment.Exports, assessment.FunctionExportSurfaceAllowed =
		collectAnalyzerEmbeddedWASIExports(compiled, profile.StartFunction)
	assessment.ImportedFunctionCount = len(assessment.Imports)
	assessment.ImportedMemoryCount = len(compiled.ImportedMemories())
	assessment.ExportedMemoryCount, assessment.InitialMemoryPages,
		assessment.DeclaredMaxMemoryPages, assessment.DeclaredMaxMemoryPresent =
		collectAnalyzerEmbeddedWASIMemory(compiled)
	assessment.EffectiveMaxMemoryPages = profile.MemoryLimitPages
	assessment.StartExportPresent = containsSortedString(assessment.Exports, profile.StartFunction)

	parsedImports, nonFunctionCount, parseErr := parseAnalyzerEmbeddedWASIImports(module)
	assessment.ImportedNonFunctionCount = nonFunctionCount
	assessment.ImportInventoryMatches = parseErr == nil &&
		analyzerEmbeddedWASIImportInventoryMatches(parsedImports, assessment.Imports)
	assessment.AllFunctionImportsAllowed = analyzerEmbeddedWASIImportsAllowed(assessment.Imports)
	assessment.MemoryPolicyPassed = assessment.ImportedMemoryCount == 0 &&
		assessment.ExportedMemoryCount > 0 && assessment.InitialMemoryPages <= profile.MemoryLimitPages &&
		(!assessment.DeclaredMaxMemoryPresent || assessment.DeclaredMaxMemoryPages <= profile.MemoryLimitPages)
	assessment.FailureCode = analyzerEmbeddedWASIAssessmentFailure(assessment)
	assessment.Passed = assessment.FailureCode == ""
	assessment.Fingerprint = analyzerStartFingerprint(assessment)

	closeErr := errors.Join(compiled.Close(context.Background()), runtime.Close(context.Background()))
	if closeErr != nil {
		return AnalyzerEmbeddedWASIAssessment{}, CodeInternal
	}
	if parseErr != nil && nonFunctionCount == 0 {
		return AnalyzerEmbeddedWASIAssessment{}, CodeInvalidContent
	}
	if code := ValidateAnalyzerEmbeddedWASIAssessment(assessment, profile); code != "" {
		return AnalyzerEmbeddedWASIAssessment{}, CodeInternal
	}
	if !assessment.Passed {
		return assessment, CodeCapabilityDenied
	}
	return assessment, ""
}

func ValidateAnalyzerEmbeddedWASIAssessment(
	value AnalyzerEmbeddedWASIAssessment,
	profile AnalyzerEmbeddedWASIProfile,
) ErrorCode {
	profileDigest, profileOK := canonicalSHA256(profile)
	if ValidateAnalyzerEmbeddedWASIProfile(profile) != "" || !profileOK ||
		value.ProtocolVersion != AnalyzerEmbeddedWASIAssessmentProtocolVersion ||
		value.ProfileSHA256 != profileDigest || !validDigest(value.ModuleSHA256) ||
		value.ModuleBytes <= 0 || value.ModuleBytes > profile.ModuleLimitBytes ||
		value.ImportedFunctionCount != len(value.Imports) || value.ImportedNonFunctionCount < 0 ||
		value.ImportedMemoryCount < 0 || value.ExportedMemoryCount < 0 ||
		value.EffectiveMaxMemoryPages != profile.MemoryLimitPages ||
		!sortedUniqueAnalyzerEmbeddedWASIImports(value.Imports) || !sort.StringsAreSorted(value.Exports) ||
		hasDuplicateStrings(value.Exports) || !value.CompiledOnly || value.Instantiated ||
		value.GuestExecuted || !value.CandidateOnly || !value.DefaultDeny || !value.StartBlocked ||
		value.ProductInvocationAuthorized || value.Authority != (AnalyzerProductAdapterAuthority{}) ||
		value.AllFunctionImportsAllowed != analyzerEmbeddedWASIImportsAllowed(value.Imports) ||
		value.FunctionExportSurfaceAllowed != analyzerEmbeddedWASIExportsAllowed(value.Exports, profile.StartFunction) ||
		value.StartExportPresent != containsSortedString(value.Exports, profile.StartFunction) {
		return CodeInvalidResult
	}
	memoryPassed := value.ImportedMemoryCount == 0 && value.ExportedMemoryCount > 0 &&
		value.InitialMemoryPages <= profile.MemoryLimitPages &&
		(!value.DeclaredMaxMemoryPresent || value.DeclaredMaxMemoryPages <= profile.MemoryLimitPages)
	if value.MemoryPolicyPassed != memoryPassed ||
		value.FailureCode != analyzerEmbeddedWASIAssessmentFailure(value) ||
		value.Passed != (value.FailureCode == "") ||
		analyzerStartFingerprint(value) != value.Fingerprint {
		return CodeInvalidResult
	}
	return ""
}

func EncodeAnalyzerEmbeddedWASIAssessment(
	value AnalyzerEmbeddedWASIAssessment,
	profile AnalyzerEmbeddedWASIProfile,
) ([]byte, ErrorCode) {
	if code := ValidateAnalyzerEmbeddedWASIAssessment(value, profile); code != "" {
		return nil, code
	}
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) == 0 || len(encoded) > MaxAnalyzerEmbeddedWASIAssessmentEnvelopeBytes {
		return nil, CodeInternal
	}
	return encoded, ""
}

func DecodeAnalyzerEmbeddedWASIAssessment(
	raw []byte,
	profile AnalyzerEmbeddedWASIProfile,
) (AnalyzerEmbeddedWASIAssessment, ErrorCode) {
	var value AnalyzerEmbeddedWASIAssessment
	if !strictDecode(raw, MaxAnalyzerEmbeddedWASIAssessmentEnvelopeBytes, &value) {
		return AnalyzerEmbeddedWASIAssessment{}, CodeInvalidResult
	}
	expectedRaw, err := json.Marshal(value)
	if err != nil {
		return AnalyzerEmbeddedWASIAssessment{}, CodeInternal
	}
	var actual, expected any
	if json.Unmarshal(raw, &actual) != nil || json.Unmarshal(expectedRaw, &expected) != nil ||
		!sameAnalyzerStartJSONShape(actual, expected) {
		return AnalyzerEmbeddedWASIAssessment{}, CodeInvalidResult
	}
	if code := ValidateAnalyzerEmbeddedWASIAssessment(value, profile); code != "" {
		return AnalyzerEmbeddedWASIAssessment{}, code
	}
	return value, ""
}

func analyzerEmbeddedWASIAssessmentBase(
	module []byte,
	profile AnalyzerEmbeddedWASIProfile,
) AnalyzerEmbeddedWASIAssessment {
	profileDigest, _ := canonicalSHA256(profile)
	moduleDigest := sha256.Sum256(module)
	return AnalyzerEmbeddedWASIAssessment{
		ProtocolVersion: AnalyzerEmbeddedWASIAssessmentProtocolVersion,
		ProfileSHA256:   profileDigest,
		ModuleSHA256:    hex.EncodeToString(moduleDigest[:]),
		ModuleBytes:     len(module),
		Imports:         []AnalyzerEmbeddedWASIImport{},
		Exports:         []string{},
		CandidateOnly:   true,
		DefaultDeny:     true,
		StartBlocked:    true,
	}
}

func collectAnalyzerEmbeddedWASIImports(compiled wazero.CompiledModule) []AnalyzerEmbeddedWASIImport {
	imports := make([]AnalyzerEmbeddedWASIImport, 0, len(compiled.ImportedFunctions()))
	for _, definition := range compiled.ImportedFunctions() {
		module, name, imported := definition.Import()
		if !imported {
			continue
		}
		imports = append(imports, AnalyzerEmbeddedWASIImport{
			Module: module, Name: name, Signature: analyzerEmbeddedWASIFunctionSignature(definition),
		})
	}
	sort.Slice(imports, func(left, right int) bool {
		return analyzerEmbeddedWASIImportKey(imports[left]) < analyzerEmbeddedWASIImportKey(imports[right])
	})
	return imports
}

func collectAnalyzerEmbeddedWASIExports(compiled wazero.CompiledModule, startFunction string) ([]string, bool) {
	exports := make([]string, 0, len(compiled.ExportedFunctions()))
	for name := range compiled.ExportedFunctions() {
		if len(exports) == maxAnalyzerEmbeddedWASIExports || len(name) > maxAnalyzerEmbeddedWASIExportNameBytes {
			return nil, false
		}
		exports = append(exports, name)
	}
	sort.Strings(exports)
	return exports, analyzerEmbeddedWASIExportsAllowed(exports, startFunction)
}

func collectAnalyzerEmbeddedWASIMemory(compiled wazero.CompiledModule) (int, uint32, uint32, bool) {
	memories := compiled.ExportedMemories()
	var initial, maximum uint32
	var maximumPresent bool
	for _, definition := range memories {
		if definition.Min() > initial {
			initial = definition.Min()
		}
		if current, present := definition.Max(); present && (!maximumPresent || current < maximum) {
			maximum, maximumPresent = current, true
		}
	}
	return len(memories), initial, maximum, maximumPresent
}

func analyzerEmbeddedWASIFunctionSignature(definition api.FunctionDefinition) string {
	parameters := make([]string, len(definition.ParamTypes()))
	for index, valueType := range definition.ParamTypes() {
		parameters[index] = api.ValueTypeName(valueType)
	}
	results := make([]string, len(definition.ResultTypes()))
	for index, valueType := range definition.ResultTypes() {
		results[index] = api.ValueTypeName(valueType)
	}
	return strings.Join(parameters, ",") + "->" + strings.Join(results, ",")
}

func analyzerEmbeddedWASIImportsAllowed(imports []AnalyzerEmbeddedWASIImport) bool {
	for _, imported := range imports {
		if imported.Module != "wasi_snapshot_preview1" ||
			analyzerEmbeddedWASIAllowedImports[imported.Name] != imported.Signature {
			return false
		}
	}
	return true
}

func analyzerEmbeddedWASIAssessmentFailure(value AnalyzerEmbeddedWASIAssessment) string {
	switch {
	case value.ImportedNonFunctionCount > 0:
		return AnalyzerEmbeddedWASIFailureNonFunctionImport
	case !value.ImportInventoryMatches:
		return AnalyzerEmbeddedWASIFailureImportInventory
	case !value.AllFunctionImportsAllowed:
		return AnalyzerEmbeddedWASIFailureFunctionImport
	case !value.FunctionExportSurfaceAllowed:
		return AnalyzerEmbeddedWASIFailureExportSurface
	case value.ImportedMemoryCount > 0:
		return AnalyzerEmbeddedWASIFailureMemoryImport
	case value.ExportedMemoryCount == 0:
		return AnalyzerEmbeddedWASIFailureMemoryExport
	case !value.MemoryPolicyPassed:
		return AnalyzerEmbeddedWASIFailureMemoryLimit
	case !value.StartExportPresent:
		return AnalyzerEmbeddedWASIFailureStartExport
	default:
		return ""
	}
}

func parseAnalyzerEmbeddedWASIImports(module []byte) ([]AnalyzerEmbeddedWASIImport, int, error) {
	if len(module) < 8 || !bytes.Equal(module[:8], []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}) {
		return nil, 0, errors.New("invalid WebAssembly header")
	}
	imports := []AnalyzerEmbeddedWASIImport{}
	nonFunctionCount := 0
	for offset := 8; offset < len(module); {
		sectionID := module[offset]
		offset++
		sectionSize, consumed, ok := readAnalyzerEmbeddedWASIULEB32(module[offset:])
		if !ok {
			return nil, nonFunctionCount, errors.New("invalid WebAssembly section size")
		}
		offset += consumed
		if uint64(sectionSize) > uint64(len(module)-offset) {
			return nil, nonFunctionCount, errors.New("truncated WebAssembly section")
		}
		section := module[offset : offset+int(sectionSize)]
		offset += int(sectionSize)
		if sectionID != 2 {
			continue
		}
		count, cursor, ok := readAnalyzerEmbeddedWASIULEB32(section)
		if !ok || count > maxAnalyzerEmbeddedWASIImports {
			return nil, nonFunctionCount, errors.New("invalid WebAssembly import count")
		}
		for index := uint32(0); index < count; index++ {
			moduleName, used, nameOK := readAnalyzerEmbeddedWASIName(section[cursor:])
			if !nameOK {
				return nil, nonFunctionCount, errors.New("invalid WebAssembly import module")
			}
			cursor += used
			name, used, nameOK := readAnalyzerEmbeddedWASIName(section[cursor:])
			if !nameOK || cursor+used >= len(section) {
				return nil, nonFunctionCount, errors.New("invalid WebAssembly import name")
			}
			cursor += used
			kind := section[cursor]
			cursor++
			if kind != 0 {
				nonFunctionCount++
				return imports, nonFunctionCount, errors.New("non-function WebAssembly import denied")
			}
			_, used, ok = readAnalyzerEmbeddedWASIULEB32(section[cursor:])
			if !ok {
				return nil, nonFunctionCount, errors.New("invalid WebAssembly import type")
			}
			cursor += used
			imports = append(imports, AnalyzerEmbeddedWASIImport{Module: moduleName, Name: name})
		}
		if cursor != len(section) {
			return nil, nonFunctionCount, errors.New("trailing WebAssembly import data")
		}
	}
	sort.Slice(imports, func(left, right int) bool {
		return analyzerEmbeddedWASIImportKey(imports[left]) < analyzerEmbeddedWASIImportKey(imports[right])
	})
	return imports, nonFunctionCount, nil
}

func readAnalyzerEmbeddedWASIName(raw []byte) (string, int, bool) {
	length, consumed, ok := readAnalyzerEmbeddedWASIULEB32(raw)
	if !ok || length > maxAnalyzerEmbeddedWASIImportNameBytes || int(length) > len(raw)-consumed {
		return "", 0, false
	}
	value := raw[consumed : consumed+int(length)]
	if !utf8.Valid(value) {
		return "", 0, false
	}
	return string(value), consumed + int(length), true
}

func readAnalyzerEmbeddedWASIULEB32(raw []byte) (uint32, int, bool) {
	var value uint32
	for index := 0; index < len(raw) && index < 5; index++ {
		current := raw[index]
		if index == 4 && current&0xf0 != 0 {
			return 0, 0, false
		}
		value |= uint32(current&0x7f) << (7 * index)
		if current&0x80 == 0 {
			return value, index + 1, true
		}
	}
	return 0, 0, false
}

func analyzerEmbeddedWASIImportKey(value AnalyzerEmbeddedWASIImport) string {
	return value.Module + "\x00" + value.Name + "\x00" + value.Signature
}

func analyzerEmbeddedWASIExportsAllowed(exports []string, startFunction string) bool {
	if startFunction != AnalyzerEmbeddedWASIStartFunction || len(exports) > maxAnalyzerEmbeddedWASIExports {
		return false
	}
	for _, name := range exports {
		if _, allowed := analyzerEmbeddedWASIAllowedFunctionExports[name]; !allowed || len(name) > maxAnalyzerEmbeddedWASIExportNameBytes {
			return false
		}
	}
	return true
}

func analyzerEmbeddedWASIImportInventoryMatches(
	parsed []AnalyzerEmbeddedWASIImport,
	compiled []AnalyzerEmbeddedWASIImport,
) bool {
	if len(parsed) != len(compiled) {
		return false
	}
	for index := range parsed {
		if parsed[index].Module != compiled[index].Module || parsed[index].Name != compiled[index].Name {
			return false
		}
	}
	return true
}

func sortedUniqueAnalyzerEmbeddedWASIImports(values []AnalyzerEmbeddedWASIImport) bool {
	for index := 1; index < len(values); index++ {
		if analyzerEmbeddedWASIImportKey(values[index-1]) >= analyzerEmbeddedWASIImportKey(values[index]) {
			return false
		}
	}
	return true
}

func containsSortedString(values []string, target string) bool {
	index := sort.SearchStrings(values, target)
	return index < len(values) && values[index] == target
}

func hasDuplicateStrings(values []string) bool {
	for index := 1; index < len(values); index++ {
		if values[index-1] == values[index] {
			return true
		}
	}
	return false
}
