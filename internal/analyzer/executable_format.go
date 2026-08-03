package analyzer

import (
	"encoding/binary"
	"encoding/json"
	"reflect"
)

const (
	ExecutableFormatEvidenceProtocolVersion  = "analyzer_executable_format.v1"
	MaxExecutableFormatEvidenceEnvelopeBytes = 4 * 1024

	executableFormatPE  = "pe"
	executableFormatELF = "elf"
)

// ExecutableFormatEvidence records deterministic inspection of caller-owned
// bytes. It proves only the image format and target architecture; it does not
// prove provenance, immutable-handle identity, runtime behavior, or authority.
type ExecutableFormatEvidence struct {
	ProtocolVersion             string `json:"protocol_version"`
	CandidateSHA256             string `json:"candidate_sha256"`
	ExecutableIdentitySHA256    string `json:"executable_identity_sha256"`
	InvocationPreflightSHA256   string `json:"invocation_preflight_sha256"`
	Analyzer                    string `json:"analyzer"`
	TargetGOOS                  string `json:"target_goos"`
	TargetGOARCH                string `json:"target_goarch"`
	ExecutableFormat            string `json:"executable_format"`
	MachineCode                 uint32 `json:"machine_code"`
	ExecutableBytes             int    `json:"executable_bytes"`
	ExecutableSHA256            string `json:"executable_sha256"`
	CallerBytesOnly             bool   `json:"caller_bytes_only"`
	CompleteImageBound          bool   `json:"complete_image_bound"`
	ExecutableFormatVerified    bool   `json:"executable_format_verified"`
	TargetArchitectureVerified  bool   `json:"target_architecture_verified"`
	ExecutableSemanticsVerified bool   `json:"executable_semantics_verified"`
	ImmutableHandleVerified     bool   `json:"immutable_handle_verified"`
	ProvenanceVerified          bool   `json:"provenance_verified"`
	PathIncluded                bool   `json:"path_included"`
	CommandIncluded             bool   `json:"command_included"`
	ProcessStartEnabled         bool   `json:"process_start_enabled"`
	ProductInvocationEnabled    bool   `json:"product_invocation_enabled"`
}

type executableInspection struct {
	format  string
	goarch  string
	machine uint32
}

func BuildExecutableFormatEvidence(candidate InvocationCandidate, rawRequest, executable []byte,
	identity ExecutableIdentity, preflight InvocationPreflight,
) (ExecutableFormatEvidence, ErrorCode) {
	if code := ValidateInvocationPreflight(preflight, candidate, rawRequest, executable,
		identity); code != "" {
		return ExecutableFormatEvidence{}, CodeInvalidResult
	}
	inspection, ok := inspectExecutable(executable)
	if !ok || inspection.format != executableFormatForGOOS(identity.TargetGOOS) ||
		inspection.goarch != identity.TargetGOARCH {
		return ExecutableFormatEvidence{}, CodeInvalidContent
	}
	candidateDigest, candidateOK := invocationCandidateSHA256(candidate)
	identityDigest, identityOK := canonicalSHA256(identity)
	preflightDigest, preflightOK := canonicalSHA256(preflight)
	if !candidateOK || !identityOK || !preflightOK {
		return ExecutableFormatEvidence{}, CodeInternal
	}
	evidence := ExecutableFormatEvidence{
		ProtocolVersion: ExecutableFormatEvidenceProtocolVersion,
		CandidateSHA256: candidateDigest, ExecutableIdentitySHA256: identityDigest,
		InvocationPreflightSHA256: preflightDigest, Analyzer: candidate.Analyzer,
		TargetGOOS: identity.TargetGOOS, TargetGOARCH: identity.TargetGOARCH,
		ExecutableFormat: inspection.format, MachineCode: inspection.machine,
		ExecutableBytes: identity.ExecutableBytes, ExecutableSHA256: identity.ExecutableSHA256,
		CallerBytesOnly: true, CompleteImageBound: true,
		ExecutableFormatVerified: true, TargetArchitectureVerified: true,
	}
	if !validateExecutableFormatEvidenceStructure(candidate, identity, preflight, evidence) {
		return ExecutableFormatEvidence{}, CodeInternal
	}
	return evidence, ""
}

func ValidateExecutableFormatEvidence(evidence ExecutableFormatEvidence,
	candidate InvocationCandidate, rawRequest, executable []byte, identity ExecutableIdentity,
	preflight InvocationPreflight,
) ErrorCode {
	expected, code := BuildExecutableFormatEvidence(candidate, rawRequest, executable, identity,
		preflight)
	if code != "" {
		return code
	}
	if !reflect.DeepEqual(evidence, expected) {
		return CodeInvalidResult
	}
	return ""
}

func EncodeExecutableFormatEvidence(evidence ExecutableFormatEvidence,
	candidate InvocationCandidate, rawRequest, executable []byte, identity ExecutableIdentity,
	preflight InvocationPreflight,
) ([]byte, ErrorCode) {
	if code := ValidateExecutableFormatEvidence(evidence, candidate, rawRequest, executable,
		identity, preflight); code != "" {
		return nil, code
	}
	encoded, err := json.Marshal(evidence)
	if err != nil || len(encoded) == 0 || len(encoded) > MaxExecutableFormatEvidenceEnvelopeBytes {
		return nil, CodeInternal
	}
	return encoded, ""
}

func DecodeExecutableFormatEvidence(rawEvidence []byte, candidate InvocationCandidate,
	rawRequest, executable []byte, identity ExecutableIdentity, preflight InvocationPreflight,
) (ExecutableFormatEvidence, ErrorCode) {
	var wire executableFormatEvidenceWire
	if !strictDecode(rawEvidence, MaxExecutableFormatEvidenceEnvelopeBytes, &wire) ||
		!wire.complete() {
		return ExecutableFormatEvidence{}, CodeInvalidResult
	}
	evidence := wire.value()
	if code := ValidateExecutableFormatEvidence(evidence, candidate, rawRequest, executable,
		identity, preflight); code != "" {
		return ExecutableFormatEvidence{}, CodeInvalidResult
	}
	return evidence, ""
}

func validateExecutableFormatEvidenceStructure(candidate InvocationCandidate,
	identity ExecutableIdentity, preflight InvocationPreflight,
	evidence ExecutableFormatEvidence,
) bool {
	candidateDigest, candidateOK := invocationCandidateSHA256(candidate)
	identityDigest, identityOK := canonicalSHA256(identity)
	preflightDigest, preflightOK := canonicalSHA256(preflight)
	return candidateOK && identityOK && preflightOK &&
		evidence.ProtocolVersion == ExecutableFormatEvidenceProtocolVersion &&
		evidence.CandidateSHA256 == candidateDigest &&
		evidence.ExecutableIdentitySHA256 == identityDigest &&
		evidence.InvocationPreflightSHA256 == preflightDigest &&
		evidence.Analyzer == candidate.Analyzer && evidence.TargetGOOS == identity.TargetGOOS &&
		evidence.TargetGOARCH == identity.TargetGOARCH &&
		evidence.ExecutableFormat == executableFormatForGOOS(identity.TargetGOOS) &&
		evidence.MachineCode != 0 && evidence.ExecutableBytes == identity.ExecutableBytes &&
		evidence.ExecutableSHA256 == identity.ExecutableSHA256 && evidence.CallerBytesOnly &&
		evidence.CompleteImageBound && evidence.ExecutableFormatVerified &&
		evidence.TargetArchitectureVerified && !evidence.ExecutableSemanticsVerified &&
		!evidence.ImmutableHandleVerified && !evidence.ProvenanceVerified &&
		!evidence.PathIncluded && !evidence.CommandIncluded && !evidence.ProcessStartEnabled &&
		!evidence.ProductInvocationEnabled
}

func executableFormatForGOOS(goos string) string {
	switch goos {
	case "windows":
		return executableFormatPE
	case "android", "dragonfly", "freebsd", "linux", "netbsd", "openbsd", "solaris":
		return executableFormatELF
	default:
		return ""
	}
}

func inspectExecutable(executable []byte) (executableInspection, bool) {
	if len(executable) >= 4 && executable[0] == 0x7f &&
		string(executable[1:4]) == "ELF" {
		return inspectELF(executable)
	}
	if len(executable) >= 2 && string(executable[:2]) == "MZ" {
		return inspectPE(executable)
	}
	return executableInspection{}, false
}

func inspectPE(executable []byte) (executableInspection, bool) {
	const (
		minimumDOSHeader      = 64
		peSignatureBytes      = 4
		coffHeaderBytes       = 20
		peExecutableImage     = 0x0002
		peDynamicLinkLibrary  = 0x2000
		pe32OptionalMagic     = 0x010b
		pe32PlusOptionalMagic = 0x020b
	)
	if len(executable) < minimumDOSHeader {
		return executableInspection{}, false
	}
	peOffset64 := uint64(binary.LittleEndian.Uint32(executable[0x3c:0x40]))
	minimumPEBytes := uint64(peSignatureBytes + coffHeaderBytes)
	if peOffset64 < minimumDOSHeader || peOffset64 > uint64(len(executable)) ||
		minimumPEBytes > uint64(len(executable))-peOffset64 {
		return executableInspection{}, false
	}
	peOffset := int(peOffset64)
	if string(executable[peOffset:peOffset+peSignatureBytes]) != "PE\x00\x00" {
		return executableInspection{}, false
	}
	coff := peOffset + peSignatureBytes
	machine := binary.LittleEndian.Uint16(executable[coff : coff+2])
	sectionCount := binary.LittleEndian.Uint16(executable[coff+2 : coff+4])
	optionalBytes := int(binary.LittleEndian.Uint16(executable[coff+16 : coff+18]))
	characteristics := binary.LittleEndian.Uint16(executable[coff+18 : coff+20])
	optionalOffset := coff + coffHeaderBytes
	if sectionCount == 0 || optionalBytes < 2 || optionalOffset > len(executable) ||
		optionalBytes > len(executable)-optionalOffset ||
		characteristics&peExecutableImage == 0 || characteristics&peDynamicLinkLibrary != 0 {
		return executableInspection{}, false
	}
	sectionTableOffset := optionalOffset + optionalBytes
	sectionTableBytes := uint64(sectionCount) * 40
	if sectionTableOffset > len(executable) ||
		sectionTableBytes > uint64(len(executable)-sectionTableOffset) {
		return executableInspection{}, false
	}
	optionalMagic := binary.LittleEndian.Uint16(executable[optionalOffset : optionalOffset+2])
	goarch, expectedMagic := peArchitecture(machine)
	if goarch == "" || optionalMagic != expectedMagic {
		return executableInspection{}, false
	}
	return executableInspection{format: executableFormatPE, goarch: goarch,
		machine: uint32(machine)}, true
}

func peArchitecture(machine uint16) (string, uint16) {
	switch machine {
	case 0x014c:
		return "386", 0x010b
	case 0x01c4:
		return "arm", 0x010b
	case 0x8664:
		return "amd64", 0x020b
	case 0xaa64:
		return "arm64", 0x020b
	default:
		return "", 0
	}
}

func inspectELF(executable []byte) (executableInspection, bool) {
	const (
		elfClass32     = 1
		elfClass64     = 2
		elfDataLSB     = 1
		elfDataMSB     = 2
		elfVersion     = 1
		elfTypeExec    = 2
		elfTypeDyn     = 3
		elfProgramLoad = 1
	)
	if len(executable) < 16 || executable[6] != elfVersion {
		return executableInspection{}, false
	}
	var order binary.ByteOrder
	switch executable[5] {
	case elfDataLSB:
		order = binary.LittleEndian
	case elfDataMSB:
		order = binary.BigEndian
	default:
		return executableInspection{}, false
	}
	class := executable[4]
	headerBytes, programHeaderBytes := 0, 0
	var programOffset uint64
	var programEntryBytes, programCount uint16
	switch class {
	case elfClass32:
		headerBytes, programHeaderBytes = 52, 32
		if len(executable) < headerBytes {
			return executableInspection{}, false
		}
		programOffset = uint64(order.Uint32(executable[28:32]))
		programEntryBytes = order.Uint16(executable[42:44])
		programCount = order.Uint16(executable[44:46])
		if order.Uint16(executable[40:42]) < uint16(headerBytes) {
			return executableInspection{}, false
		}
	case elfClass64:
		headerBytes, programHeaderBytes = 64, 56
		if len(executable) < headerBytes {
			return executableInspection{}, false
		}
		programOffset = order.Uint64(executable[32:40])
		programEntryBytes = order.Uint16(executable[54:56])
		programCount = order.Uint16(executable[56:58])
		if order.Uint16(executable[52:54]) < uint16(headerBytes) {
			return executableInspection{}, false
		}
	default:
		return executableInspection{}, false
	}
	fileType := order.Uint16(executable[16:18])
	machine := order.Uint16(executable[18:20])
	if (fileType != elfTypeExec && fileType != elfTypeDyn) ||
		order.Uint32(executable[20:24]) != elfVersion || programCount == 0 ||
		programEntryBytes < uint16(programHeaderBytes) || programOffset < uint64(headerBytes) ||
		programOffset > uint64(len(executable)) {
		return executableInspection{}, false
	}
	tableBytes := uint64(programEntryBytes) * uint64(programCount)
	if tableBytes > uint64(len(executable))-programOffset {
		return executableInspection{}, false
	}
	hasLoad := false
	for index := uint16(0); index < programCount; index++ {
		offset := programOffset + uint64(index)*uint64(programEntryBytes)
		if order.Uint32(executable[int(offset):int(offset)+4]) != elfProgramLoad {
			continue
		}
		var fileOffset, fileBytes, memoryBytes uint64
		if class == elfClass32 {
			fileOffset = uint64(order.Uint32(executable[int(offset)+4 : int(offset)+8]))
			fileBytes = uint64(order.Uint32(executable[int(offset)+16 : int(offset)+20]))
			memoryBytes = uint64(order.Uint32(executable[int(offset)+20 : int(offset)+24]))
		} else {
			fileOffset = order.Uint64(executable[int(offset)+8 : int(offset)+16])
			fileBytes = order.Uint64(executable[int(offset)+32 : int(offset)+40])
			memoryBytes = order.Uint64(executable[int(offset)+40 : int(offset)+48])
		}
		if fileBytes == 0 || memoryBytes < fileBytes || fileOffset > uint64(len(executable)) ||
			fileBytes > uint64(len(executable))-fileOffset {
			return executableInspection{}, false
		}
		hasLoad = true
	}
	goarch, expectedClass := elfArchitecture(machine)
	if !hasLoad || goarch == "" || class != expectedClass {
		return executableInspection{}, false
	}
	return executableInspection{format: executableFormatELF, goarch: goarch,
		machine: uint32(machine)}, true
}

func elfArchitecture(machine uint16) (string, byte) {
	switch machine {
	case 3:
		return "386", 1
	case 40:
		return "arm", 1
	case 62:
		return "amd64", 2
	case 183:
		return "arm64", 2
	case 243:
		return "riscv64", 2
	default:
		return "", 0
	}
}

type executableFormatEvidenceWire struct {
	ProtocolVersion             *string `json:"protocol_version"`
	CandidateSHA256             *string `json:"candidate_sha256"`
	ExecutableIdentitySHA256    *string `json:"executable_identity_sha256"`
	InvocationPreflightSHA256   *string `json:"invocation_preflight_sha256"`
	Analyzer                    *string `json:"analyzer"`
	TargetGOOS                  *string `json:"target_goos"`
	TargetGOARCH                *string `json:"target_goarch"`
	ExecutableFormat            *string `json:"executable_format"`
	MachineCode                 *uint32 `json:"machine_code"`
	ExecutableBytes             *int    `json:"executable_bytes"`
	ExecutableSHA256            *string `json:"executable_sha256"`
	CallerBytesOnly             *bool   `json:"caller_bytes_only"`
	CompleteImageBound          *bool   `json:"complete_image_bound"`
	ExecutableFormatVerified    *bool   `json:"executable_format_verified"`
	TargetArchitectureVerified  *bool   `json:"target_architecture_verified"`
	ExecutableSemanticsVerified *bool   `json:"executable_semantics_verified"`
	ImmutableHandleVerified     *bool   `json:"immutable_handle_verified"`
	ProvenanceVerified          *bool   `json:"provenance_verified"`
	PathIncluded                *bool   `json:"path_included"`
	CommandIncluded             *bool   `json:"command_included"`
	ProcessStartEnabled         *bool   `json:"process_start_enabled"`
	ProductInvocationEnabled    *bool   `json:"product_invocation_enabled"`
}

func (wire executableFormatEvidenceWire) complete() bool {
	return wire.ProtocolVersion != nil && wire.CandidateSHA256 != nil &&
		wire.ExecutableIdentitySHA256 != nil && wire.InvocationPreflightSHA256 != nil &&
		wire.Analyzer != nil && wire.TargetGOOS != nil && wire.TargetGOARCH != nil &&
		wire.ExecutableFormat != nil && wire.MachineCode != nil &&
		wire.ExecutableBytes != nil && wire.ExecutableSHA256 != nil &&
		wire.CallerBytesOnly != nil && wire.CompleteImageBound != nil &&
		wire.ExecutableFormatVerified != nil && wire.TargetArchitectureVerified != nil &&
		wire.ExecutableSemanticsVerified != nil && wire.ImmutableHandleVerified != nil &&
		wire.ProvenanceVerified != nil && wire.PathIncluded != nil &&
		wire.CommandIncluded != nil && wire.ProcessStartEnabled != nil &&
		wire.ProductInvocationEnabled != nil
}

func (wire executableFormatEvidenceWire) value() ExecutableFormatEvidence {
	return ExecutableFormatEvidence{
		ProtocolVersion: *wire.ProtocolVersion, CandidateSHA256: *wire.CandidateSHA256,
		ExecutableIdentitySHA256:  *wire.ExecutableIdentitySHA256,
		InvocationPreflightSHA256: *wire.InvocationPreflightSHA256, Analyzer: *wire.Analyzer,
		TargetGOOS: *wire.TargetGOOS, TargetGOARCH: *wire.TargetGOARCH,
		ExecutableFormat: *wire.ExecutableFormat, MachineCode: *wire.MachineCode,
		ExecutableBytes: *wire.ExecutableBytes, ExecutableSHA256: *wire.ExecutableSHA256,
		CallerBytesOnly:             *wire.CallerBytesOnly,
		CompleteImageBound:          *wire.CompleteImageBound,
		ExecutableFormatVerified:    *wire.ExecutableFormatVerified,
		TargetArchitectureVerified:  *wire.TargetArchitectureVerified,
		ExecutableSemanticsVerified: *wire.ExecutableSemanticsVerified,
		ImmutableHandleVerified:     *wire.ImmutableHandleVerified,
		ProvenanceVerified:          *wire.ProvenanceVerified, PathIncluded: *wire.PathIncluded,
		CommandIncluded: *wire.CommandIncluded, ProcessStartEnabled: *wire.ProcessStartEnabled,
		ProductInvocationEnabled: *wire.ProductInvocationEnabled,
	}
}
