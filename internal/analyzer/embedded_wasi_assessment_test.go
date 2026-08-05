package analyzer

import (
	"bytes"
	"context"
	"os"
	"reflect"
	"testing"
)

func TestAnalyzerEmbeddedWASIAssessmentCompilesWithoutInstantiation(t *testing.T) {
	profile := BuildAnalyzerEmbeddedWASIProfile()
	assessment, code := AssessAnalyzerEmbeddedWASIModule(context.Background(), minimalAnalyzerWASM(), profile)
	if code != "" || !assessment.Passed {
		t.Fatalf("assess minimal module: code=%s assessment=%#v", code, assessment)
	}
	if !assessment.CompiledOnly || assessment.Instantiated || assessment.GuestExecuted ||
		!assessment.StartBlocked || assessment.ProductInvocationAuthorized ||
		assessment.Authority != (AnalyzerProductAdapterAuthority{}) {
		t.Fatalf("compile-only boundary widened: %#v", assessment)
	}
	raw, code := EncodeAnalyzerEmbeddedWASIAssessment(assessment, profile)
	if code != "" {
		t.Fatalf("encode assessment: %s", code)
	}
	decoded, code := DecodeAnalyzerEmbeddedWASIAssessment(raw, profile)
	if code != "" || !reflect.DeepEqual(decoded, assessment) {
		t.Fatalf("assessment round trip drifted: code=%s decoded=%#v", code, decoded)
	}
	missing := bytes.Replace(raw, []byte(`"guest_executed":false,`), nil, 1)
	if _, code = DecodeAnalyzerEmbeddedWASIAssessment(missing, profile); code != CodeInvalidResult {
		t.Fatalf("missing field accepted: %s", code)
	}
	widened := bytes.Replace(raw, []byte(`"execution":false`), []byte(`"execution":true`), 1)
	if _, code = DecodeAnalyzerEmbeddedWASIAssessment(widened, profile); code != CodeInvalidResult {
		t.Fatalf("authority widening accepted: %s", code)
	}
}

func TestAnalyzerEmbeddedWASIAssessmentRejectsUnexpectedAndNonFunctionImports(t *testing.T) {
	profile := BuildAnalyzerEmbeddedWASIProfile()
	assessment, code := AssessAnalyzerEmbeddedWASIModule(context.Background(), analyzerWASMWithEvilFunctionImport(), profile)
	if code != CodeCapabilityDenied || assessment.FailureCode != AnalyzerEmbeddedWASIFailureFunctionImport || assessment.Passed {
		t.Fatalf("unexpected function import outcome: code=%s assessment=%#v", code, assessment)
	}
	assessment, code = AssessAnalyzerEmbeddedWASIModule(context.Background(), analyzerWASMWithImportedMemory(), profile)
	if code != CodeCapabilityDenied || assessment.FailureCode != AnalyzerEmbeddedWASIFailureNonFunctionImport ||
		assessment.ImportedNonFunctionCount != 1 || assessment.Passed {
		t.Fatalf("non-function import outcome: code=%s assessment=%#v", code, assessment)
	}
}

func TestAnalyzerEmbeddedWASIAssessmentRejectsMalformedAndMissingStart(t *testing.T) {
	profile := BuildAnalyzerEmbeddedWASIProfile()
	if _, code := AssessAnalyzerEmbeddedWASIModule(context.Background(), []byte("not-wasm"), profile); code != CodeInvalidContent {
		t.Fatalf("malformed module accepted: %s", code)
	}
	assessment, code := AssessAnalyzerEmbeddedWASIModule(context.Background(), analyzerWASMWithoutStart(), profile)
	if code != CodeCapabilityDenied || assessment.FailureCode != AnalyzerEmbeddedWASIFailureStartExport || assessment.Passed {
		t.Fatalf("missing start outcome: code=%s assessment=%#v", code, assessment)
	}
}

func TestAnalyzerEmbeddedWASIAssessmentRejectsAdditionalFunctionExport(t *testing.T) {
	profile := BuildAnalyzerEmbeddedWASIProfile()
	assessment, code := AssessAnalyzerEmbeddedWASIModule(context.Background(), analyzerWASMWithExtraExport(), profile)
	if code != CodeCapabilityDenied || assessment.FailureCode != AnalyzerEmbeddedWASIFailureExportSurface || assessment.Passed {
		t.Fatalf("additional export outcome: code=%s assessment=%#v", code, assessment)
	}
}

func TestAnalyzerEmbeddedWASIProductionFixtureAssessment(t *testing.T) {
	path := os.Getenv("CYBERAGENT_WASI_FIXTURE")
	if path == "" {
		t.Skip("CYBERAGENT_WASI_FIXTURE is not set")
	}
	module, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read WASI fixture: %v", err)
	}
	assessment, code := AssessAnalyzerEmbeddedWASIModule(context.Background(), module, BuildAnalyzerEmbeddedWASIProfile())
	if code != "" || !assessment.Passed {
		t.Fatalf("assess production fixture: code=%s assessment=%#v", code, assessment)
	}
	expectedNames := []string{
		"args_get", "args_sizes_get", "environ_get", "environ_sizes_get", "fd_fdstat_get",
		"fd_read", "fd_write", "proc_exit", "random_get",
	}
	actualNames := make([]string, len(assessment.Imports))
	for index, imported := range assessment.Imports {
		if imported.Module != "wasi_snapshot_preview1" {
			t.Fatalf("unexpected import module: %#v", imported)
		}
		actualNames[index] = imported.Name
	}
	if !reflect.DeepEqual(actualNames, expectedNames) {
		t.Fatalf("fixture import surface drifted: got=%v want=%v", actualNames, expectedNames)
	}
}

func minimalAnalyzerWASM() []byte {
	return []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
		0x01, 0x04, 0x01, 0x60, 0x00, 0x00,
		0x03, 0x02, 0x01, 0x00,
		0x05, 0x03, 0x01, 0x00, 0x01,
		0x07, 0x13, 0x02,
		0x06, 'm', 'e', 'm', 'o', 'r', 'y', 0x02, 0x00,
		0x06, '_', 's', 't', 'a', 'r', 't', 0x00, 0x00,
		0x0a, 0x04, 0x01, 0x02, 0x00, 0x0b,
	}
}

func analyzerWASMWithEvilFunctionImport() []byte {
	return []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
		0x01, 0x04, 0x01, 0x60, 0x00, 0x00,
		0x02, 0x0c, 0x01, 0x04, 'e', 'v', 'i', 'l', 0x03, 'r', 'u', 'n', 0x00, 0x00,
		0x05, 0x03, 0x01, 0x00, 0x01,
		0x07, 0x13, 0x02,
		0x06, 'm', 'e', 'm', 'o', 'r', 'y', 0x02, 0x00,
		0x06, '_', 's', 't', 'a', 'r', 't', 0x00, 0x00,
	}
}

func analyzerWASMWithImportedMemory() []byte {
	return []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
		0x01, 0x04, 0x01, 0x60, 0x00, 0x00,
		0x02, 0x10, 0x01, 0x04, 'e', 'v', 'i', 'l', 0x06, 'm', 'e', 'm', 'o', 'r', 'y', 0x02, 0x00, 0x01,
		0x03, 0x02, 0x01, 0x00,
		0x07, 0x13, 0x02,
		0x06, 'm', 'e', 'm', 'o', 'r', 'y', 0x02, 0x00,
		0x06, '_', 's', 't', 'a', 'r', 't', 0x00, 0x00,
		0x0a, 0x04, 0x01, 0x02, 0x00, 0x0b,
	}
}

func analyzerWASMWithoutStart() []byte {
	return []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
		0x05, 0x03, 0x01, 0x00, 0x01,
		0x07, 0x0a, 0x01, 0x06, 'm', 'e', 'm', 'o', 'r', 'y', 0x02, 0x00,
	}
}

func analyzerWASMWithExtraExport() []byte {
	return []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
		0x01, 0x04, 0x01, 0x60, 0x00, 0x00,
		0x03, 0x03, 0x02, 0x00, 0x00,
		0x05, 0x03, 0x01, 0x00, 0x01,
		0x07, 0x1a, 0x03,
		0x06, 'm', 'e', 'm', 'o', 'r', 'y', 0x02, 0x00,
		0x06, '_', 's', 't', 'a', 'r', 't', 0x00, 0x00,
		0x04, 'e', 'v', 'i', 'l', 0x00, 0x01,
		0x0a, 0x07, 0x02, 0x02, 0x00, 0x0b, 0x02, 0x00, 0x0b,
	}
}
