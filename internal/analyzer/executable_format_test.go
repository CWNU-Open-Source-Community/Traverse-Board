package analyzer

import (
	"encoding/binary"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestInspectExecutableRecognizesBoundedPEAndELFHeaders(t *testing.T) {
	tests := []struct {
		name    string
		content []byte
		format  string
		goarch  string
	}{
		{name: "pe 386", content: testPEExecutable(t, "386"), format: "pe", goarch: "386"},
		{name: "pe amd64", content: testPEExecutable(t, "amd64"), format: "pe", goarch: "amd64"},
		{name: "pe arm", content: testPEExecutable(t, "arm"), format: "pe", goarch: "arm"},
		{name: "pe arm64", content: testPEExecutable(t, "arm64"), format: "pe", goarch: "arm64"},
		{name: "elf 386", content: testELFExecutable(t, "386"), format: "elf", goarch: "386"},
		{name: "elf amd64", content: testELFExecutable(t, "amd64"), format: "elf", goarch: "amd64"},
		{name: "elf arm", content: testELFExecutable(t, "arm"), format: "elf", goarch: "arm"},
		{name: "elf arm64", content: testELFExecutable(t, "arm64"), format: "elf", goarch: "arm64"},
		{name: "elf riscv64", content: testELFExecutable(t, "riscv64"), format: "elf", goarch: "riscv64"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			inspection, ok := inspectExecutable(test.content)
			if !ok || inspection.format != test.format || inspection.goarch != test.goarch ||
				inspection.machine == 0 {
				t.Fatalf("inspection = %#v, ok=%v", inspection, ok)
			}
		})
	}
}

func TestInspectExecutableRejectsMalformedAndNonExecutableImages(t *testing.T) {
	peDLL := testPEExecutable(t, "amd64")
	peOffset := int(binary.LittleEndian.Uint32(peDLL[0x3c:0x40]))
	characteristics := peOffset + 4 + 18
	binary.LittleEndian.PutUint16(peDLL[characteristics:characteristics+2], 0x2002)

	elfNoLoad := testELFExecutable(t, "amd64")
	programOffset := int(binary.LittleEndian.Uint64(elfNoLoad[32:40]))
	binary.LittleEndian.PutUint32(elfNoLoad[programOffset:programOffset+4], 0)

	peBadMagic := testPEExecutable(t, "amd64")
	optionalOffset := peOffset + 4 + 20
	binary.LittleEndian.PutUint16(peBadMagic[optionalOffset:optionalOffset+2], 0x010b)

	for name, content := range map[string][]byte{
		"empty": nil, "text": []byte("not an executable"),
		"truncated pe": []byte("MZ"), "dll": peDLL,
		"pe class mismatch": peBadMagic, "elf without load segment": elfNoLoad,
		"truncated elf": {0x7f, 'E', 'L', 'F'},
	} {
		t.Run(name, func(t *testing.T) {
			if inspection, ok := inspectExecutable(content); ok {
				t.Fatalf("unexpected inspection: %#v", inspection)
			}
		})
	}
}

func TestExecutableFormatEvidenceBindsChainWithoutAuthority(t *testing.T) {
	format := executableFormatForGOOS(runtime.GOOS)
	if format == "" {
		t.Skipf("runtime GOOS %q is not a PE/ELF target", runtime.GOOS)
	}
	executable := testExecutableForTarget(t, runtime.GOOS, runtime.GOARCH)
	raw := testRequestJSON(t)
	candidate := mustInvocationCandidate(t, raw)
	identity, code := BuildExecutableIdentity(candidate, raw, executable)
	if code != "" {
		t.Fatal(code)
	}
	preflight, code := BuildInvocationPreflight(candidate, raw, executable, identity)
	if code != "" {
		t.Fatal(code)
	}
	evidence, code := BuildExecutableFormatEvidence(candidate, raw, executable, identity,
		preflight)
	if code != "" {
		t.Fatal(code)
	}
	if evidence.ProtocolVersion != ExecutableFormatEvidenceProtocolVersion ||
		evidence.ExecutableFormat != format || evidence.TargetGOARCH != runtime.GOARCH ||
		!evidence.CallerBytesOnly || !evidence.CompleteImageBound ||
		!evidence.ExecutableFormatVerified || !evidence.TargetArchitectureVerified ||
		evidence.ExecutableSemanticsVerified || evidence.ImmutableHandleVerified ||
		evidence.ProvenanceVerified || evidence.PathIncluded || evidence.CommandIncluded ||
		evidence.ProcessStartEnabled || evidence.ProductInvocationEnabled {
		t.Fatalf("unsafe or incomplete format evidence: %#v", evidence)
	}
	encoded, code := EncodeExecutableFormatEvidence(evidence, candidate, raw, executable,
		identity, preflight)
	if code != "" {
		t.Fatal(code)
	}
	decoded, code := DecodeExecutableFormatEvidence(encoded, candidate, raw, executable,
		identity, preflight)
	if code != "" || !reflect.DeepEqual(decoded, evidence) {
		t.Fatalf("round trip failed: code=%s value=%#v", code, decoded)
	}
	assertExactObjectKeys(t, encoded, []string{"analyzer", "caller_bytes_only",
		"candidate_sha256", "command_included", "complete_image_bound",
		"executable_bytes", "executable_format", "executable_format_verified",
		"executable_identity_sha256", "executable_semantics_verified", "executable_sha256",
		"immutable_handle_verified", "invocation_preflight_sha256", "machine_code",
		"path_included", "process_start_enabled", "product_invocation_enabled",
		"protocol_version", "provenance_verified", "target_architecture_verified",
		"target_goarch", "target_goos"})
}

func TestExecutableFormatEvidenceRejectsDriftAndSchemaWidening(t *testing.T) {
	if executableFormatForGOOS(runtime.GOOS) == "" {
		t.Skipf("runtime GOOS %q is not a PE/ELF target", runtime.GOOS)
	}
	executable := testExecutableForTarget(t, runtime.GOOS, runtime.GOARCH)
	raw := testRequestJSON(t)
	candidate := mustInvocationCandidate(t, raw)
	identity, _ := BuildExecutableIdentity(candidate, raw, executable)
	preflight, _ := BuildInvocationPreflight(candidate, raw, executable, identity)
	evidence, code := BuildExecutableFormatEvidence(candidate, raw, executable, identity,
		preflight)
	if code != "" {
		t.Fatal(code)
	}

	mutated := evidence
	mutated.ProcessStartEnabled = true
	if got := ValidateExecutableFormatEvidence(mutated, candidate, raw, executable, identity,
		preflight); got != CodeInvalidResult {
		t.Fatalf("authority drift code = %s", got)
	}
	encoded, code := EncodeExecutableFormatEvidence(evidence, candidate, raw, executable,
		identity, preflight)
	if code != "" {
		t.Fatal(code)
	}
	text := string(encoded)
	for name, malformed := range map[string]string{
		"future": strings.Replace(text, ExecutableFormatEvidenceProtocolVersion,
			"analyzer_executable_format.v2", 1),
		"unknown": strings.Replace(text, `"path_included":false`,
			`"path_included":false,"path":"analyzer"`, 1),
		"duplicate": strings.Replace(text, `"process_start_enabled":false`,
			`"process_start_enabled":false,"process_start_enabled":false`, 1),
		"missing false": strings.Replace(text, `,"product_invocation_enabled":false`, "", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, got := DecodeExecutableFormatEvidence([]byte(malformed), candidate, raw,
				executable, identity, preflight); got != CodeInvalidResult {
				t.Fatalf("schema drift code = %s", got)
			}
		})
	}

	otherArch := "386"
	if runtime.GOARCH == otherArch {
		otherArch = "amd64"
	}
	wrongExecutable := testExecutableForTarget(t, runtime.GOOS, otherArch)
	wrongIdentity, code := BuildExecutableIdentity(candidate, raw, wrongExecutable)
	if code != "" {
		t.Fatal(code)
	}
	wrongPreflight, code := BuildInvocationPreflight(candidate, raw, wrongExecutable,
		wrongIdentity)
	if code != "" {
		t.Fatal(code)
	}
	if _, got := BuildExecutableFormatEvidence(candidate, raw, wrongExecutable, wrongIdentity,
		wrongPreflight); got != CodeInvalidContent {
		t.Fatalf("architecture mismatch code = %s", got)
	}
}

func testExecutableForTarget(t *testing.T, goos, goarch string) []byte {
	t.Helper()
	switch executableFormatForGOOS(goos) {
	case executableFormatPE:
		return testPEExecutable(t, goarch)
	case executableFormatELF:
		return testELFExecutable(t, goarch)
	default:
		t.Fatalf("unsupported test GOOS %q", goos)
		return nil
	}
}

func testPEExecutable(t *testing.T, goarch string) []byte {
	t.Helper()
	machines := map[string]uint16{"386": 0x014c, "arm": 0x01c4, "amd64": 0x8664,
		"arm64": 0xaa64}
	machine, ok := machines[goarch]
	if !ok {
		t.Fatalf("unsupported PE architecture %q", goarch)
	}
	optionalBytes, optionalMagic := 0xe0, uint16(0x010b)
	if goarch == "amd64" || goarch == "arm64" {
		optionalBytes, optionalMagic = 0xf0, 0x020b
	}
	content := make([]byte, 0x200)
	copy(content[:2], "MZ")
	binary.LittleEndian.PutUint32(content[0x3c:0x40], 0x80)
	copy(content[0x80:0x84], "PE\x00\x00")
	coff := 0x84
	binary.LittleEndian.PutUint16(content[coff:coff+2], machine)
	binary.LittleEndian.PutUint16(content[coff+2:coff+4], 1)
	binary.LittleEndian.PutUint16(content[coff+16:coff+18], uint16(optionalBytes))
	binary.LittleEndian.PutUint16(content[coff+18:coff+20], 0x0002)
	binary.LittleEndian.PutUint16(content[coff+20:coff+22], optionalMagic)
	return content
}

func testELFExecutable(t *testing.T, goarch string) []byte {
	t.Helper()
	type elfMachine struct {
		machine uint16
		class   byte
	}
	machines := map[string]elfMachine{
		"386": {machine: 3, class: 1}, "arm": {machine: 40, class: 1},
		"amd64": {machine: 62, class: 2}, "arm64": {machine: 183, class: 2},
		"riscv64": {machine: 243, class: 2},
	}
	machine, ok := machines[goarch]
	if !ok {
		t.Fatalf("unsupported ELF architecture %q", goarch)
	}
	headerBytes, programBytes := 52, 32
	if machine.class == 2 {
		headerBytes, programBytes = 64, 56
	}
	content := make([]byte, headerBytes+programBytes)
	copy(content[:4], []byte{0x7f, 'E', 'L', 'F'})
	content[4], content[5], content[6] = machine.class, 1, 1
	binary.LittleEndian.PutUint16(content[16:18], 2)
	binary.LittleEndian.PutUint16(content[18:20], machine.machine)
	binary.LittleEndian.PutUint32(content[20:24], 1)
	if machine.class == 1 {
		binary.LittleEndian.PutUint32(content[28:32], uint32(headerBytes))
		binary.LittleEndian.PutUint16(content[40:42], uint16(headerBytes))
		binary.LittleEndian.PutUint16(content[42:44], uint16(programBytes))
		binary.LittleEndian.PutUint16(content[44:46], 1)
	} else {
		binary.LittleEndian.PutUint64(content[32:40], uint64(headerBytes))
		binary.LittleEndian.PutUint16(content[52:54], uint16(headerBytes))
		binary.LittleEndian.PutUint16(content[54:56], uint16(programBytes))
		binary.LittleEndian.PutUint16(content[56:58], 1)
	}
	binary.LittleEndian.PutUint32(content[headerBytes:headerBytes+4], 1)
	if machine.class == 1 {
		binary.LittleEndian.PutUint32(content[headerBytes+16:headerBytes+20], uint32(len(content)))
		binary.LittleEndian.PutUint32(content[headerBytes+20:headerBytes+24], uint32(len(content)))
	} else {
		binary.LittleEndian.PutUint64(content[headerBytes+32:headerBytes+40], uint64(len(content)))
		binary.LittleEndian.PutUint64(content[headerBytes+40:headerBytes+48], uint64(len(content)))
	}
	return content
}
