package analyzer

import (
	"bytes"
	"reflect"
	"testing"
)

func TestAnalyzerEmbeddedWASIProfileIsDefaultDenyAndStrict(t *testing.T) {
	profile := BuildAnalyzerEmbeddedWASIProfile()
	if profile.Engine != "interpreter" || !profile.RuntimePerInvocation ||
		!profile.ModuleInstancePerInvocation || !profile.CloseOnContextDone ||
		!profile.SyntheticArgumentsOnly || !profile.EmptyEnvironmentOnly ||
		!profile.BoundedMemoryStdioOnly || !profile.DeterministicRandomOnly ||
		!profile.CandidateOnly || !profile.DefaultDeny || !profile.StartBlocked {
		t.Fatalf("embedded WASI isolation invariants drifted: %#v", profile)
	}
	if profile.CompilerEngineEnabled || profile.CompilationCacheEnabled ||
		profile.InheritedArguments || profile.InheritedEnvironment ||
		profile.FilesystemMounted || profile.HostClocksEnabled ||
		profile.HostRandomEnabled || profile.SocketHostModuleEnabled ||
		profile.CustomHostModulesEnabled || profile.NativeProcessEnabled ||
		profile.HostPathIncluded || profile.RawModuleIncluded ||
		profile.ProductInvocationAuthorized || profile.Authority != (AnalyzerProductAdapterAuthority{}) {
		t.Fatalf("embedded WASI profile widened authority: %#v", profile)
	}

	raw, code := EncodeAnalyzerEmbeddedWASIProfile(profile)
	if code != "" {
		t.Fatalf("encode profile: %s", code)
	}
	decoded, code := DecodeAnalyzerEmbeddedWASIProfile(raw)
	if code != "" || !reflect.DeepEqual(decoded, profile) {
		t.Fatalf("round trip drifted: code=%s decoded=%#v", code, decoded)
	}

	unknown := bytes.Replace(raw, []byte(`"authority":`), []byte(`"unknown":false,"authority":`), 1)
	if _, code = DecodeAnalyzerEmbeddedWASIProfile(unknown); code != CodeInvalidResult {
		t.Fatalf("unknown field accepted: %s", code)
	}
	missing := bytes.Replace(raw, []byte(`"start_blocked":true,`), nil, 1)
	if _, code = DecodeAnalyzerEmbeddedWASIProfile(missing); code != CodeInvalidResult {
		t.Fatalf("missing field accepted: %s", code)
	}
	widened := bytes.Replace(raw, []byte(`"process_start":false`), []byte(`"process_start":true`), 1)
	if _, code = DecodeAnalyzerEmbeddedWASIProfile(widened); code != CodeInvalidResult {
		t.Fatalf("authority widening accepted: %s", code)
	}
}
