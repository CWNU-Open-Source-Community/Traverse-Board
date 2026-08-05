package analyzer

import (
	"encoding/json"
	"reflect"
)

const (
	AnalyzerEmbeddedWASIProfileProtocolVersion  = "analyzer_embedded_wasi_isolation_profile.v1"
	AnalyzerEmbeddedWASIBackend                 = "wazero_interpreter_wasip1.v1"
	AnalyzerEmbeddedWASIRuntimeModule           = "github.com/tetratelabs/wazero"
	AnalyzerEmbeddedWASIRuntimeVersion          = "v1.12.0"
	AnalyzerEmbeddedWASITarget                  = "wasm32-wasip1"
	AnalyzerEmbeddedWASIStartFunction           = "_start"
	AnalyzerEmbeddedWASIMemoryLimitPages        = 4096
	AnalyzerEmbeddedWASIModuleLimitBytes        = 16 * 1024 * 1024
	MaxAnalyzerEmbeddedWASIProfileEnvelopeBytes = 16 * 1024
)

// AnalyzerEmbeddedWASIProfile is a non-executing, default-deny candidate for
// replacing the native analyzer subprocess boundary. It contains no module
// bytes, path, command, runtime instance, or product capability.
type AnalyzerEmbeddedWASIProfile struct {
	ProtocolVersion             string                          `json:"protocol_version"`
	Backend                     string                          `json:"backend"`
	RuntimeModule               string                          `json:"runtime_module"`
	RuntimeVersion              string                          `json:"runtime_version"`
	GuestTarget                 string                          `json:"guest_target"`
	Engine                      string                          `json:"engine"`
	CoreFeatures                string                          `json:"core_features"`
	StartFunction               string                          `json:"start_function"`
	MemoryLimitPages            uint32                          `json:"memory_limit_pages"`
	ModuleLimitBytes            int                             `json:"module_limit_bytes"`
	RuntimePerInvocation        bool                            `json:"runtime_per_invocation"`
	ModuleInstancePerInvocation bool                            `json:"module_instance_per_invocation"`
	CloseOnContextDone          bool                            `json:"close_on_context_done"`
	CustomSectionsEnabled       bool                            `json:"custom_sections_enabled"`
	DebugInfoEnabled            bool                            `json:"debug_info_enabled"`
	CompilerEngineEnabled       bool                            `json:"compiler_engine_enabled"`
	CompilationCacheEnabled     bool                            `json:"compilation_cache_enabled"`
	InheritedArguments          bool                            `json:"inherited_arguments"`
	InheritedEnvironment        bool                            `json:"inherited_environment"`
	SyntheticArgumentsOnly      bool                            `json:"synthetic_arguments_only"`
	EmptyEnvironmentOnly        bool                            `json:"empty_environment_only"`
	BoundedMemoryStdioOnly      bool                            `json:"bounded_memory_stdio_only"`
	DeterministicRandomOnly     bool                            `json:"deterministic_random_only"`
	FilesystemMounted           bool                            `json:"filesystem_mounted"`
	HostClocksEnabled           bool                            `json:"host_clocks_enabled"`
	HostRandomEnabled           bool                            `json:"host_random_enabled"`
	SocketHostModuleEnabled     bool                            `json:"socket_host_module_enabled"`
	CustomHostModulesEnabled    bool                            `json:"custom_host_modules_enabled"`
	NativeProcessEnabled        bool                            `json:"native_process_enabled"`
	HostPathIncluded            bool                            `json:"host_path_included"`
	RawModuleIncluded           bool                            `json:"raw_module_included"`
	CandidateOnly               bool                            `json:"candidate_only"`
	DefaultDeny                 bool                            `json:"default_deny"`
	StartBlocked                bool                            `json:"start_blocked"`
	ProductInvocationAuthorized bool                            `json:"product_invocation_authorized"`
	Authority                   AnalyzerProductAdapterAuthority `json:"authority"`
}

func BuildAnalyzerEmbeddedWASIProfile() AnalyzerEmbeddedWASIProfile {
	return AnalyzerEmbeddedWASIProfile{
		ProtocolVersion:             AnalyzerEmbeddedWASIProfileProtocolVersion,
		Backend:                     AnalyzerEmbeddedWASIBackend,
		RuntimeModule:               AnalyzerEmbeddedWASIRuntimeModule,
		RuntimeVersion:              AnalyzerEmbeddedWASIRuntimeVersion,
		GuestTarget:                 AnalyzerEmbeddedWASITarget,
		Engine:                      "interpreter",
		CoreFeatures:                "webassembly_core_v2",
		StartFunction:               AnalyzerEmbeddedWASIStartFunction,
		MemoryLimitPages:            AnalyzerEmbeddedWASIMemoryLimitPages,
		ModuleLimitBytes:            AnalyzerEmbeddedWASIModuleLimitBytes,
		RuntimePerInvocation:        true,
		ModuleInstancePerInvocation: true,
		CloseOnContextDone:          true,
		SyntheticArgumentsOnly:      true,
		EmptyEnvironmentOnly:        true,
		BoundedMemoryStdioOnly:      true,
		DeterministicRandomOnly:     true,
		CandidateOnly:               true,
		DefaultDeny:                 true,
		StartBlocked:                true,
	}
}

func ValidateAnalyzerEmbeddedWASIProfile(value AnalyzerEmbeddedWASIProfile) ErrorCode {
	if !reflect.DeepEqual(value, BuildAnalyzerEmbeddedWASIProfile()) {
		return CodeInvalidResult
	}
	return ""
}

func EncodeAnalyzerEmbeddedWASIProfile(value AnalyzerEmbeddedWASIProfile) ([]byte, ErrorCode) {
	if code := ValidateAnalyzerEmbeddedWASIProfile(value); code != "" {
		return nil, code
	}
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) == 0 || len(encoded) > MaxAnalyzerEmbeddedWASIProfileEnvelopeBytes {
		return nil, CodeInternal
	}
	return encoded, ""
}

func DecodeAnalyzerEmbeddedWASIProfile(raw []byte) (AnalyzerEmbeddedWASIProfile, ErrorCode) {
	var value AnalyzerEmbeddedWASIProfile
	if !strictDecode(raw, MaxAnalyzerEmbeddedWASIProfileEnvelopeBytes, &value) {
		return AnalyzerEmbeddedWASIProfile{}, CodeInvalidResult
	}
	expectedRaw, err := json.Marshal(value)
	if err != nil {
		return AnalyzerEmbeddedWASIProfile{}, CodeInternal
	}
	var actual, expected any
	if json.Unmarshal(raw, &actual) != nil || json.Unmarshal(expectedRaw, &expected) != nil ||
		!sameAnalyzerStartJSONShape(actual, expected) {
		return AnalyzerEmbeddedWASIProfile{}, CodeInvalidResult
	}
	if code := ValidateAnalyzerEmbeddedWASIProfile(value); code != "" {
		return AnalyzerEmbeddedWASIProfile{}, code
	}
	return value, ""
}
