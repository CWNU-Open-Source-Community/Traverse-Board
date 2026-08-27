[CmdletBinding()]
param(
    [string]$MsixManifestPath = "build/desktop/msix-manifest.json",
    [string]$DirectSigningEvidencePath = "build/desktop/direct-exe-signing.json",
    [string]$DirectArtifactPath = "build/desktop/TraverseBoard.direct.exe",
    [Parameter(Mandatory = $true)]
    [string]$StoreReadbackPath,
    [Parameter(Mandatory = $true)]
    [string]$LifecycleEvidencePath,
    [Parameter(Mandatory = $true)]
    [string]$StoreListingZhCNPath,
    [Parameter(Mandatory = $true)]
    [string]$StoreListingEnUSPath,
    [Parameter(Mandatory = $true)]
    [string]$PrivacyPolicyPath,
    [Parameter(Mandatory = $true)]
    [string]$StoreIconPath,
    [Parameter(Mandatory = $true)]
    [string]$StoreListingReadbackPath,
    [Parameter(Mandatory = $true)]
    [string]$GitHubReleaseReadbackPath,
    [Parameter(Mandatory = $true)]
    [string]$ArtifactAttestationIndexPath,
    [Parameter(Mandatory = $true)]
    [string]$ExpectedDirectSignerSubject,
    [Parameter(Mandatory = $true)]
    [string]$ExpectedDirectSignerThumbprint,
    [string]$GitHubRepository = "CWNU-Open-Source-Community/Traverse-Board",
    [string]$OutputDirectory = "build/release-completion"
)

<#
.SYNOPSIS
Promotes externally obtained Windows release facts into immutable completion evidence.

.DESCRIPTION
Packaging evidence deliberately remains `lifecycle_validation_status=not_run`.
This finalizer consumes the live Microsoft Store-installed package, Partner Center
readback, bilingual listing/privacy/icon readback, trusted direct-EXE signing
evidence, and the Windows 10/11 lifecycle matrix. It independently rechecks the
candidate hashes and emits windows_release_completion.v1 only when every gate
is passed. It never signs, uploads, installs, or changes the original manifest.
#>

$ErrorActionPreference = "Stop"
if ([System.Environment]::OSVersion.Platform -ne [System.PlatformID]::Win32NT) {
    throw "Windows release finalization requires Windows"
}
Add-Type -AssemblyName System.IO.Compression.FileSystem

function Get-SHA256 {
    param([string]$Path)
    return (Get-FileHash -Algorithm SHA256 -LiteralPath $Path).Hash.ToLowerInvariant()
}

function Get-ZipEntrySHA256 {
    param([object]$Entry)
    $stream = $Entry.Open()
    $sha = [System.Security.Cryptography.SHA256]::Create()
    try {
        return ([System.BitConverter]::ToString($sha.ComputeHash($stream))).Replace(
            "-", "").ToLowerInvariant()
    }
    finally {
        $sha.Dispose()
        $stream.Dispose()
    }
}

function Assert-BooleanField {
    param([object]$Document, [string]$Name, [string]$Label)
    $property = $Document.PSObject.Properties[$Name]
    if ($null -eq $property -or $property.Value -isnot [bool]) {
        throw "$Label field must be a JSON boolean: $Name"
    }
}

function Assert-SHA256 {
    param([string]$Value, [string]$Label)
    if ($Value -notmatch '^[0-9a-f]{64}$') {
        throw "$Label must be a lowercase SHA-256 digest"
    }
}

function Assert-HTTPSURL {
    param([string]$Value, [string]$Label)
    $uri = $null
    if (-not [System.Uri]::TryCreate($Value, [System.UriKind]::Absolute, [ref]$uri) -or
        $uri.Scheme -cne "https" -or [string]::IsNullOrWhiteSpace($uri.Host) -or
        -not [string]::IsNullOrEmpty($uri.UserInfo)) {
        throw "$Label must be an absolute HTTPS URL without embedded credentials"
    }
    return $uri
}

$repositoryRoot = [System.IO.Path]::GetFullPath((Split-Path -Parent $PSScriptRoot))
$repositoryPrefix = $repositoryRoot.TrimEnd('\', '/') +
    [System.IO.Path]::DirectorySeparatorChar

function Resolve-PathFromRepository {
    param([string]$Path)
    $candidate = if ([System.IO.Path]::IsPathRooted($Path)) {
        $Path
    }
    else {
        Join-Path $repositoryRoot $Path
    }
    return [System.IO.Path]::GetFullPath($candidate)
}

function Assert-NoRepositoryReparsePoint {
    param([string]$ResolvedPath, [string]$Label)
    $currentPath = $ResolvedPath
    while ($currentPath -ine $repositoryRoot) {
        if ($currentPath -ine $repositoryRoot -and
            -not $currentPath.StartsWith($repositoryPrefix,
                [System.StringComparison]::OrdinalIgnoreCase)) {
            throw "$Label escapes the repository while resolving path components"
        }
        if (Test-Path -LiteralPath $currentPath) {
            $item = Get-Item -LiteralPath $currentPath -Force
            if (($item.Attributes -band [System.IO.FileAttributes]::ReparsePoint) -ne 0) {
                throw "$Label cannot traverse a reparse point: $currentPath"
            }
        }
        $parent = Split-Path -Parent $currentPath
        if ([string]::IsNullOrWhiteSpace($parent) -or $parent -ieq $currentPath) {
            throw "$Label cannot be resolved beneath the repository"
        }
        $currentPath = [System.IO.Path]::GetFullPath($parent)
    }
}

function Resolve-RepositoryFile {
    param([string]$Path, [string]$Label)
    $resolved = Resolve-PathFromRepository $Path
    if (-not $resolved.StartsWith($repositoryPrefix,
            [System.StringComparison]::OrdinalIgnoreCase) -or
        -not (Test-Path -LiteralPath $resolved -PathType Leaf)) {
        throw "$Label must be a regular file inside the repository"
    }
    Assert-NoRepositoryReparsePoint $resolved $Label
    return $resolved
}

function Resolve-EvidenceSibling {
    param([string]$Directory, [string]$Name, [string]$Label)
    if ([string]::IsNullOrWhiteSpace($Name) -or
        [System.IO.Path]::GetFileName($Name) -cne $Name) {
        throw "$Label must use one safe evidence filename"
    }
    $resolved = [System.IO.Path]::GetFullPath((Join-Path $Directory $Name))
    $directoryPrefix = [System.IO.Path]::GetFullPath($Directory).TrimEnd('\', '/') +
        [System.IO.Path]::DirectorySeparatorChar
    if (-not $resolved.StartsWith($directoryPrefix,
            [System.StringComparison]::OrdinalIgnoreCase) -or
        -not $resolved.StartsWith($repositoryPrefix,
            [System.StringComparison]::OrdinalIgnoreCase) -or
        -not (Test-Path -LiteralPath $resolved -PathType Leaf)) {
        throw "$Label is missing or escapes its evidence directory"
    }
    Assert-NoRepositoryReparsePoint $resolved $Label
    return $resolved
}

function Get-RoundTripTimestamp {
    param([string]$Value, [string]$Label)
    $result = [System.DateTimeOffset]::MinValue
    if (-not [System.DateTimeOffset]::TryParseExact(
            $Value, "o", [System.Globalization.CultureInfo]::InvariantCulture,
            [System.Globalization.DateTimeStyles]::RoundtripKind, [ref]$result) -or
        $result.Offset -ne [System.TimeSpan]::Zero -or
        $result -gt [System.DateTimeOffset]::UtcNow.AddMinutes(5)) {
        throw "$Label must be a valid non-future UTC round-trip timestamp"
    }
    return $result.ToUniversalTime()
}

function Get-ISO8601Timestamp {
    param([string]$Value, [string]$Label)
    $result = [System.DateTimeOffset]::MinValue
    if (-not [System.DateTimeOffset]::TryParse(
            $Value, [System.Globalization.CultureInfo]::InvariantCulture,
            [System.Globalization.DateTimeStyles]::RoundtripKind, [ref]$result) -or
        $result.Offset -ne [System.TimeSpan]::Zero -or
        $result -gt [System.DateTimeOffset]::UtcNow.AddMinutes(5)) {
        throw "$Label must be a valid non-future UTC ISO-8601 timestamp"
    }
    return $result.ToUniversalTime()
}

function Get-StoreVersion {
    param([string]$Value, [string]$Label)
    $parts = @($Value -split '\.')
    if ($parts.Count -ne 4) {
        throw "$Label must have exactly four numeric components"
    }
    $numbers = [System.Collections.Generic.List[int]]::new()
    foreach ($part in $parts) {
        $number = [uint32]0
        if ($part -notmatch '^(0|[1-9][0-9]{0,4})$' -or
            -not [uint32]::TryParse($part, [ref]$number) -or $number -gt 65535) {
            throw "$Label contains a component outside 0..65535"
        }
        $numbers.Add([int]$number)
    }
    return [System.Version]::new(
        $numbers[0], $numbers[1], $numbers[2], $numbers[3])
}

function Test-JSONNumber {
    param([object]$Value)
    return $Value -is [byte] -or $Value -is [sbyte] -or
        $Value -is [int16] -or $Value -is [uint16] -or
        $Value -is [int32] -or $Value -is [uint32] -or
        $Value -is [int64] -or $Value -is [uint64] -or
        $Value -is [single] -or $Value -is [double] -or $Value -is [decimal]
}

function Assert-JSONEquivalent {
    param(
        [object]$Expected,
        [object]$Actual,
        [string]$Path = '$'
    )
    if ($null -eq $Expected -or $null -eq $Actual) {
        if ($null -ne $Expected -or $null -ne $Actual) {
            throw "JSON value differs at $Path"
        }
        return
    }
    if ($Expected -is [System.Management.Automation.PSCustomObject]) {
        if ($Actual -isnot [System.Management.Automation.PSCustomObject]) {
            throw "JSON object type differs at $Path"
        }
        $expectedNames = @($Expected.PSObject.Properties.Name | Sort-Object)
        $actualNames = @($Actual.PSObject.Properties.Name | Sort-Object)
        if ($expectedNames.Count -ne $actualNames.Count) {
            throw "JSON object property count differs at $Path"
        }
        for ($index = 0; $index -lt $expectedNames.Count; $index++) {
            if ([string]$expectedNames[$index] -cne [string]$actualNames[$index]) {
                throw "JSON object properties differ at $Path"
            }
            $name = [string]$expectedNames[$index]
            Assert-JSONEquivalent $Expected.PSObject.Properties[$name].Value `
                $Actual.PSObject.Properties[$name].Value "$Path.$name"
        }
        return
    }
    $expectedEnumerable = $Expected -is [System.Collections.IEnumerable] -and
        $Expected -isnot [string]
    $actualEnumerable = $Actual -is [System.Collections.IEnumerable] -and
        $Actual -isnot [string]
    if ($expectedEnumerable -or $actualEnumerable) {
        if (-not $expectedEnumerable -or -not $actualEnumerable) {
            throw "JSON array type differs at $Path"
        }
        $expectedItems = @($Expected)
        $actualItems = @($Actual)
        if ($expectedItems.Count -ne $actualItems.Count) {
            throw "JSON array length differs at $Path"
        }
        for ($index = 0; $index -lt $expectedItems.Count; $index++) {
            Assert-JSONEquivalent $expectedItems[$index] $actualItems[$index] `
                "$Path[$index]"
        }
        return
    }
    if (Test-JSONNumber $Expected) {
        if (-not (Test-JSONNumber $Actual) -or
            [decimal]$Expected -ne [decimal]$Actual) {
            throw "JSON number differs at $Path"
        }
        return
    }
    if ($Expected -is [bool]) {
        if ($Actual -isnot [bool] -or [bool]$Expected -ne [bool]$Actual) {
            throw "JSON boolean differs at $Path"
        }
        return
    }
    if ($Expected -isnot [string] -or $Actual -isnot [string] -or
        [string]$Expected -cne [string]$Actual) {
        throw "JSON scalar differs at $Path"
    }
}

function Get-AttestationStatement {
    param([string]$BundlePath)
    $bundle = Get-Content -LiteralPath $BundlePath -Raw | ConvertFrom-Json
    $payload = [string]$bundle.dsseEnvelope.payload
    if ([string]$bundle.dsseEnvelope.payloadType -cne
            "application/vnd.in-toto+json" -or
        [string]::IsNullOrWhiteSpace($payload)) {
        throw "Attestation bundle has no DSSE payload"
    }
    try {
        $statementJSON = [System.Text.Encoding]::UTF8.GetString(
            [System.Convert]::FromBase64String($payload))
        $statement = $statementJSON | ConvertFrom-Json
        if ([string]$statement._type -cne "https://in-toto.io/Statement/v1") {
            throw "Attestation bundle does not contain an in-toto Statement v1"
        }
        return $statement
    }
    catch {
        throw "Attestation bundle DSSE statement cannot be decoded: $($_.Exception.Message)"
    }
}

$manifestFile = Resolve-RepositoryFile $MsixManifestPath "MSIX manifest"
$directEvidenceFile = Resolve-RepositoryFile $DirectSigningEvidencePath "Direct EXE evidence"
$directArtifactFile = Resolve-RepositoryFile $DirectArtifactPath "Direct EXE artifact"
$storeReadbackFile = Resolve-RepositoryFile $StoreReadbackPath "Partner Center readback"
$lifecycleFile = Resolve-RepositoryFile $LifecycleEvidencePath "Lifecycle evidence"
$listingZhCNFile = Resolve-RepositoryFile $StoreListingZhCNPath "Chinese Store listing"
$listingEnUSFile = Resolve-RepositoryFile $StoreListingEnUSPath "English Store listing"
$privacyPolicyFile = Resolve-RepositoryFile $PrivacyPolicyPath "Privacy policy"
$storeIconFile = Resolve-RepositoryFile $StoreIconPath "Store icon"
$listingReadbackFile = Resolve-RepositoryFile $StoreListingReadbackPath "Store listing readback"
$githubReleaseReadbackFile = Resolve-RepositoryFile $GitHubReleaseReadbackPath `
    "GitHub Release readback"
$attestationIndexFile = Resolve-RepositoryFile $ArtifactAttestationIndexPath `
    "Artifact attestation index"
if ($GitHubRepository -cne "CWNU-Open-Source-Community/Traverse-Board") {
    throw "Windows release finalization is restricted to the canonical GitHub repository"
}
$ghCommand = Get-Command gh -CommandType Application -ErrorAction Stop |
    Select-Object -First 1
if ($null -eq $ghCommand -or -not (Test-Path -LiteralPath $ghCommand.Source -PathType Leaf)) {
    throw "GitHub CLI executable was not found"
}
$completionRoot = [System.IO.Path]::GetFullPath(
    (Join-Path $repositoryRoot "build/release-completion"))
$outputRoot = Resolve-PathFromRepository $OutputDirectory
$completionPrefix = $completionRoot.TrimEnd('\', '/') +
    [System.IO.Path]::DirectorySeparatorChar
if ($outputRoot -ine $completionRoot -and
    -not $outputRoot.StartsWith($completionPrefix,
        [System.StringComparison]::OrdinalIgnoreCase)) {
    throw "Windows completion output must remain under build/release-completion"
}
Assert-NoRepositoryReparsePoint $outputRoot "Windows completion output"

$manifest = Get-Content -LiteralPath $manifestFile -Raw | ConvertFrom-Json
$directEvidence = Get-Content -LiteralPath $directEvidenceFile -Raw | ConvertFrom-Json
$readback = Get-Content -LiteralPath $storeReadbackFile -Raw | ConvertFrom-Json
$lifecycle = Get-Content -LiteralPath $lifecycleFile -Raw | ConvertFrom-Json
$listingReadback = Get-Content -LiteralPath $listingReadbackFile -Raw | ConvertFrom-Json
$attestationIndex = Get-Content -LiteralPath $attestationIndexFile -Raw | ConvertFrom-Json
foreach ($field in @("store_submission_candidate", "contract_test",
        "release_gate_complete", "lifecycle_validation_required",
        "direct_exe_payload_binding_verified")) {
    Assert-BooleanField $manifest $field "MSIX manifest"
}
foreach ($field in @("trusted_release", "prerelease", "payload_binding_verified",
        "timestamp_present", "signtool_verified")) {
    Assert-BooleanField $directEvidence $field "Direct EXE evidence"
}
foreach ($field in @("microsoft_resigned", "run_full_trust_approved",
        "certification_passed", "listing_published")) {
    Assert-BooleanField $readback $field "Partner Center readback"
}
Assert-BooleanField $listingReadback "published" "Store listing readback"

if ([string]$manifest.protocol_version -cne "msix_manifest.v2" -or
    -not $manifest.store_submission_candidate -or $manifest.contract_test -or
    -not $manifest.release_gate_complete -or
    -not $manifest.lifecycle_validation_required -or
    [string]$manifest.lifecycle_validation_status -cne "not_run" -or
    -not [string]::IsNullOrEmpty([string]$manifest.lifecycle_evidence_sha256) -or
    -not $manifest.direct_exe_payload_binding_verified) {
    throw "MSIX manifest is not an immutable real Store submission candidate"
}
if ([string]$manifest.release_revision -notmatch '^[0-9a-f]{40,64}$') {
    throw "MSIX manifest release revision must be a lowercase Git object digest"
}
$currentStoreVersion = Get-StoreVersion ([string]$manifest.version) `
    "MSIX manifest Store version"
if ([string]$directEvidence.protocol_version -cne "direct_exe_signing.v1" -or
    -not $directEvidence.trusted_release -or $directEvidence.prerelease -or
    -not $directEvidence.payload_binding_verified -or
    -not $directEvidence.timestamp_present -or -not $directEvidence.signtool_verified -or
    [string]$directEvidence.payload_binding -cne "pe_authenticode_normalized.v1" -or
    [string]$directEvidence.signature_digest_algorithm -cne "sha256" -or
    [string]$directEvidence.timestamp_protocol -cne "rfc3161" -or
    [string]$directEvidence.timestamp_digest_algorithm -cne "sha256") {
    throw "Direct EXE evidence is not a trusted stable signing result"
}
if ((Get-SHA256 $directArtifactFile) -cne [string]$directEvidence.artifact_sha256 -or
    [string]$directEvidence.payload_sha256 -cne [string]$manifest.binary_sha256 -or
    [string]$directEvidence.artifact_sha256 -cne
        [string]$manifest.direct_download_sha256 -or
    (Get-SHA256 $directEvidenceFile) -cne
        [string]$manifest.direct_exe_signing_evidence_sha256 -or
    [string]$directEvidence.signing_request_name -cne
        [string]$manifest.direct_exe_signing_request_name -or
    [string]$directEvidence.signing_request_sha256 -cne
        [string]$manifest.direct_exe_signing_request_sha256 -or
    [string]$directEvidence.signing_handoff_name -cne
        [string]$manifest.direct_exe_signing_handoff_name -or
    [string]$directEvidence.signing_handoff_sha256 -cne
        [string]$manifest.direct_exe_signing_handoff_sha256 -or
    [string]$directEvidence.release_version -cne [string]$manifest.marketing_version -or
    [string]$directEvidence.release_revision -cne [string]$manifest.release_revision) {
    throw "Direct EXE, signing evidence, and Store manifest are not the same candidate"
}
$manifestDirectory = Split-Path -Parent $manifestFile
$manifestDirectoryRelative = if ($manifestDirectory -ieq $repositoryRoot) {
    "."
}
else {
    $manifestDirectory.Substring($repositoryPrefix.Length).Replace('\', '/')
}
if ($directArtifactFile -ine (Join-Path $manifestDirectory "TraverseBoard.direct.exe") -or
    $directEvidenceFile -ine (Join-Path $manifestDirectory "direct-exe-signing.json")) {
    throw "Finalization requires the direct EXE evidence beside msix-manifest.json"
}
& (Join-Path $PSScriptRoot "stage-direct-exe.ps1") `
    -OutputDirectory $manifestDirectoryRelative `
    -ExpectedVersion ([string]$manifest.marketing_version) `
    -ExpectedRevision ([string]$manifest.release_revision) `
    -ExpectedSignerSubject $ExpectedDirectSignerSubject `
    -ExpectedSignerThumbprint $ExpectedDirectSignerThumbprint `
    -VerifyOnly

$manifestHash = Get-SHA256 $manifestFile
$candidateUploadHash = [string]$manifest.msixupload_sha256
foreach ($hashAndLabel in @(
        @($manifestHash, "MSIX manifest hash"),
        @([string]$manifest.msix_sha256, "candidate MSIX hash"),
        @($candidateUploadHash, "candidate upload hash"),
        @([string]$manifest.binary_sha256, "Store payload hash"),
        @([string]$directEvidence.artifact_sha256, "direct EXE hash")
    )) {
    Assert-SHA256 ([string]$hashAndLabel[0]) ([string]$hashAndLabel[1])
}

$candidateMsixName = [string]$manifest.msix_name
$candidateUploadName = [string]$manifest.msixupload_name
$candidateUploadFile = Resolve-EvidenceSibling $manifestDirectory `
    $candidateUploadName "Store MSIX upload"
if ([System.IO.Path]::GetFileName($candidateMsixName) -cne $candidateMsixName -or
    (Get-SHA256 $candidateUploadFile) -cne $candidateUploadHash) {
    throw "Store upload name or hash differs from the immutable MSIX manifest"
}
$uploadArchive = [System.IO.Compression.ZipFile]::OpenRead($candidateUploadFile)
try {
    $uploadEntries = @($uploadArchive.Entries)
    if ($uploadEntries.Count -ne 1 -or
        [string]$uploadEntries[0].FullName -cne $candidateMsixName -or
        (Get-ZipEntrySHA256 $uploadEntries[0]) -cne [string]$manifest.msix_sha256) {
        throw "Store upload does not contain exactly the manifest-bound MSIX candidate"
    }
}
finally {
    $uploadArchive.Dispose()
}

$portableManifestFile = Resolve-EvidenceSibling $manifestDirectory `
    "portable-zip-manifest.json" "Portable release manifest"
$portableManifest = Get-Content -LiteralPath $portableManifestFile -Raw | ConvertFrom-Json
$portableZipName = [string]$portableManifest.zip_name
if ([System.IO.Path]::GetFileName($portableZipName) -cne $portableZipName) {
    throw "Portable release manifest contains an unsafe archive name"
}
$expectedReleaseEvidenceNames = @(
    $portableZipName,
    "portable-zip-manifest.json",
    "standard-code-packaged-e2e.json",
    "standard-code-product-e2e.json",
    "standard-code-security-evidence.json",
    "standard-code-release-gate.json",
    "sbom.json",
    "NOTICE",
    "windows-compatibility.json",
    "direct-exe-signing-request.json",
    "direct-exe-signing-handoff.json",
    "direct-exe-signing.json",
    "SHA256SUMS",
    "verification-SHA256SUMS"
)
$releaseEvidenceItems = @($manifest.release_evidence)
if ($releaseEvidenceItems.Count -ne $expectedReleaseEvidenceNames.Count) {
    throw "Store release evidence inventory is incomplete"
}
$releaseEvidenceHashes = [System.Collections.Generic.Dictionary[string, string]]::new(
    [System.StringComparer]::Ordinal)
for ($index = 0; $index -lt $expectedReleaseEvidenceNames.Count; $index++) {
    $name = [string]$releaseEvidenceItems[$index].name
    $hash = [string]$releaseEvidenceItems[$index].sha256
    if ($name -cne $expectedReleaseEvidenceNames[$index]) {
        throw "Store release evidence inventory differs at entry $index"
    }
    Assert-SHA256 $hash "Store release evidence hash for $name"
    $evidenceFile = Resolve-EvidenceSibling $manifestDirectory $name `
        "Store release evidence $name"
    if ((Get-SHA256 $evidenceFile) -cne $hash) {
        throw "Store release evidence file differs from its manifest hash: $name"
    }
    $releaseEvidenceHashes.Add($name, $hash)
}
if ($releaseEvidenceHashes["direct-exe-signing.json"] -cne
        [string]$manifest.direct_exe_signing_evidence_sha256 -or
    $releaseEvidenceHashes["direct-exe-signing-request.json"] -cne
        [string]$manifest.direct_exe_signing_request_sha256 -or
    $releaseEvidenceHashes["direct-exe-signing-handoff.json"] -cne
        [string]$manifest.direct_exe_signing_handoff_sha256) {
    throw "Store release evidence does not preserve the direct-signing chain"
}

if ([string]$readback.protocol_version -cne "windows_store_readback.v1" -or
    [string]$readback.release_revision -cne [string]$manifest.release_revision -or
    [string]$readback.marketing_version -cne [string]$manifest.marketing_version -or
    [string]$readback.store_package_version -cne [string]$manifest.version -or
    [string]$readback.processor_architecture -cne
        [string]$manifest.processor_architecture -or
    [string]$readback.msix_manifest_sha256 -cne $manifestHash -or
    [string]$readback.submitted_upload_sha256 -cne $candidateUploadHash -or
    [string]$readback.package_identity_name -cne
        [string]$manifest.package_identity_name -or
    [string]$readback.package_publisher -cne [string]$manifest.package_publisher -or
    [string]$readback.publisher_display_name -cne
        [string]$manifest.publisher_display_name -or
    -not $readback.microsoft_resigned -or -not $readback.run_full_trust_approved -or
    -not $readback.certification_passed -or -not $readback.listing_published -or
    [string]::IsNullOrWhiteSpace([string]$readback.partner_center_submission_id) -or
    [string]::IsNullOrWhiteSpace([string]$readback.age_rating) -or
    [string]$readback.visible_store_version -cne [string]$manifest.version) {
    throw "Partner Center readback does not prove the exact Store candidate completed"
}
$storeProductURI = Assert-HTTPSURL ([string]$readback.store_product_url) `
    "Microsoft Store product URL"
if ($storeProductURI.Host -ine "apps.microsoft.com" -and
    $storeProductURI.Host -ine "www.microsoft.com") {
    throw "Microsoft Store product URL must use an official Microsoft Store host"
}
$privacyPolicyURI = Assert-HTTPSURL ([string]$readback.privacy_policy_url) `
    "Published privacy policy URL"
$submissionID = [string]$readback.partner_center_submission_id
if ($submissionID -notmatch '^[0-9A-Za-z._-]{1,128}$') {
    throw "Partner Center submission ID cannot be used as an immutable evidence key"
}
$outputFile = Join-Path $outputRoot (
    "windows-release-completion-$([string]$manifest.release_revision)-$submissionID.json")
$inputFiles = @(
    $manifestFile, $directEvidenceFile, $directArtifactFile, $storeReadbackFile,
    $lifecycleFile, $listingZhCNFile, $listingEnUSFile, $privacyPolicyFile,
    $storeIconFile, $listingReadbackFile, $githubReleaseReadbackFile,
    $attestationIndexFile
)
if ($inputFiles -icontains $outputFile -or
    (Test-Path -LiteralPath $outputFile)) {
    throw "Windows completion evidence is content-addressed and cannot overwrite any file"
}
$acceptedAt = Get-RoundTripTimestamp ([string]$readback.accepted_at_utc) `
    "Partner Center acceptance timestamp"
$directSignedAt = Get-RoundTripTimestamp ([string]$directEvidence.signed_at_utc) `
    "Direct EXE signed timestamp"
if ($acceptedAt -lt $directSignedAt) {
    throw "Partner Center acceptance timestamp is invalid"
}
$listingZhCNHash = Get-SHA256 $listingZhCNFile
$listingEnUSHash = Get-SHA256 $listingEnUSFile
$privacyPolicyHash = Get-SHA256 $privacyPolicyFile
$storeIconHash = Get-SHA256 $storeIconFile
$listingReadbackHash = Get-SHA256 $listingReadbackFile
$githubReleaseReadbackHash = Get-SHA256 $githubReleaseReadbackFile
foreach ($hashAndLabel in @(
        @([string]$readback.listing_zh_cn_sha256, "Chinese listing readback hash"),
        @([string]$readback.listing_en_us_sha256, "English listing readback hash"),
        @([string]$readback.privacy_policy_sha256, "privacy-policy readback hash"),
        @([string]$readback.store_icon_sha256, "Store icon readback hash"),
        @([string]$readback.listing_readback_sha256, "listing readback document hash"),
        @([string]$readback.github_release_readback_sha256,
            "GitHub Release readback document hash")
    )) {
    Assert-SHA256 ([string]$hashAndLabel[0]) ([string]$hashAndLabel[1])
}
if ($listingZhCNHash -cne [string]$readback.listing_zh_cn_sha256 -or
    $listingEnUSHash -cne [string]$readback.listing_en_us_sha256 -or
    $privacyPolicyHash -cne [string]$readback.privacy_policy_sha256 -or
    $storeIconHash -cne [string]$readback.store_icon_sha256 -or
    $listingReadbackHash -cne
        [string]$readback.listing_readback_sha256 -or
    $githubReleaseReadbackHash -cne
        [string]$readback.github_release_readback_sha256) {
    throw "Published listing, privacy policy, age rating, icon, or readback has drifted"
}
$listingCapturedAt = Get-RoundTripTimestamp `
    ([string]$listingReadback.captured_at_utc) "Store listing readback timestamp"
if ([string]$listingReadback.protocol_version -cne
        "windows_store_listing_readback.v1" -or
    -not [bool]$listingReadback.published -or
    [string]$listingReadback.release_revision -cne
        [string]$manifest.release_revision -or
    [string]$listingReadback.marketing_version -cne
        [string]$manifest.marketing_version -or
    [string]$listingReadback.store_package_version -cne
        [string]$manifest.version -or
    [string]$listingReadback.partner_center_submission_id -cne $submissionID -or
    [string]$listingReadback.store_product_url -cne
        [string]$readback.store_product_url -or
    [string]$listingReadback.privacy_policy_url -cne
        [string]$readback.privacy_policy_url -or
    [string]$listingReadback.age_rating -cne [string]$readback.age_rating -or
    [string]$listingReadback.listing_zh_cn_sha256 -cne $listingZhCNHash -or
    [string]$listingReadback.listing_en_us_sha256 -cne $listingEnUSHash -or
    [string]$listingReadback.privacy_policy_sha256 -cne $privacyPolicyHash -or
    [string]$listingReadback.store_icon_sha256 -cne $storeIconHash -or
    $listingCapturedAt -lt $acceptedAt) {
    throw "Store listing readback is not bound to the published bilingual candidate"
}
$githubReleaseReadback = [System.IO.File]::ReadAllText(
    $githubReleaseReadbackFile).Replace("`r`n", "`n").Replace("`r", "`n").TrimEnd()
$githubReleaseJSON = & $ghCommand.Source release view `
    ([string]$manifest.marketing_version) `
    --repo $GitHubRepository `
    --json tagName,isDraft,isPrerelease,body,assets,url,publishedAt
if ($LASTEXITCODE -ne 0) {
    throw "Live GitHub Release readback failed"
}
$githubRelease = ($githubReleaseJSON | Out-String) | ConvertFrom-Json
$githubReleaseURI = Assert-HTTPSURL ([string]$githubRelease.url) `
    "Live GitHub Release URL"
if ($githubReleaseURI.Host -ine "github.com") {
    throw "Live GitHub Release URL must use github.com"
}
$githubReleasePublishedAt = Get-ISO8601Timestamp `
    ([string]$githubRelease.publishedAt) "Live GitHub Release publication timestamp"
$tagCommitOutput = & $ghCommand.Source api `
    "repos/$GitHubRepository/commits/$([string]$manifest.marketing_version)" `
    --jq '.sha'
if ($LASTEXITCODE -ne 0) {
    throw "Live GitHub Release tag commit readback failed"
}
$githubTagRevision = (($tagCommitOutput | Out-String).Trim())
$liveReleaseBody = ([string]$githubRelease.body).Replace(
    "`r`n", "`n").Replace("`r", "`n").TrimEnd()
if ([string]$githubRelease.tagName -cne [string]$manifest.marketing_version -or
    $githubTagRevision -cne [string]$manifest.release_revision -or
    [bool]$githubRelease.isDraft -or [bool]$githubRelease.isPrerelease -or
    $liveReleaseBody -cne $githubReleaseReadback -or
    $liveReleaseBody -notmatch 'windows-two-deliverable-contract\.v1' -or
    $liveReleaseBody -notmatch 'TraverseBoard\.exe' -or
    $liveReleaseBody -notmatch 'Microsoft Store' -or
    $liveReleaseBody -match '(?i)Start-[^\r\n]*\.cmd') {
    throw "Live GitHub Release does not preserve the exact stable two-product contract"
}
$githubReleaseAssets = @($githubRelease.assets)

$installedPackages = @(Get-AppxPackage `
        -Name ([string]$manifest.package_identity_name) `
        -PackageTypeFilter Main | Where-Object {
            [string]$_.Name -ceq [string]$manifest.package_identity_name -and
            [string]$_.Publisher -ceq [string]$manifest.package_publisher -and
            [string]$_.Version -ceq [string]$manifest.version -and
            [string]$_.Architecture -ieq [string]$manifest.processor_architecture -and
            [string]$_.SignatureKind -ceq "Store" -and
            [string]$_.Status -ceq "Ok"
        })
if ($installedPackages.Count -ne 1) {
    throw "Exactly one healthy Microsoft Store-signed package for this candidate must be installed"
}
$installedPackage = $installedPackages[0]
if ([string]$installedPackage.PackageFullName -cne
        [string]$readback.installed_package_full_name -or
    [string]$installedPackage.PackageFamilyName -cne
        [string]$readback.installed_package_family_name -or
    [string]$readback.store_signature_kind -cne "Store") {
    throw "Live Store package identity differs from Partner Center/install readback"
}
if ([string]::IsNullOrWhiteSpace([string]$installedPackage.InstallLocation) -or
    -not (Test-Path -LiteralPath ([string]$installedPackage.InstallLocation) `
        -PathType Container)) {
    throw "Live Store package has no readable install location"
}
$installedBinary = Join-Path ([string]$installedPackage.InstallLocation) "TraverseBoard.exe"
$installedPayloadHash = if (Test-Path -LiteralPath $installedBinary -PathType Leaf) {
    Get-SHA256 $installedBinary
}
else {
    ""
}
if (-not (Test-Path -LiteralPath $installedBinary -PathType Leaf) -or
    $installedPayloadHash -cne [string]$manifest.binary_sha256 -or
    $installedPayloadHash -cne [string]$readback.installed_payload_sha256) {
    throw "Microsoft Store-installed executable does not preserve the submitted payload"
}
[xml]$installedManifest = Get-AppxPackageManifest `
    -Package ([string]$installedPackage.PackageFullName)
$namespace = [System.Xml.XmlNamespaceManager]::new($installedManifest.NameTable)
$namespace.AddNamespace(
    "f", "http://schemas.microsoft.com/appx/manifest/foundation/windows10")
$identity = $installedManifest.SelectSingleNode("/f:Package/f:Identity", $namespace)
$publisherDisplay = $installedManifest.SelectSingleNode(
    "/f:Package/f:Properties/f:PublisherDisplayName", $namespace)
if ($null -eq $identity -or $null -eq $publisherDisplay -or
    $identity.GetAttribute("Name") -cne [string]$manifest.package_identity_name -or
    $identity.GetAttribute("Publisher") -cne [string]$manifest.package_publisher -or
    $identity.GetAttribute("Version") -cne [string]$manifest.version -or
    $identity.GetAttribute("ProcessorArchitecture") -cne
        [string]$manifest.processor_architecture -or
    [string]$publisherDisplay.InnerText -cne
        [string]$manifest.publisher_display_name) {
    throw "Live Microsoft Store package manifest differs from the submitted candidate"
}

$productEvidence = @($manifest.release_evidence | Where-Object {
        [string]$_.name -ceq "standard-code-product-e2e.json"
    })
if ($productEvidence.Count -ne 1) {
    throw "Store manifest does not bind exactly one final product report"
}
if ([string]$lifecycle.protocol_version -cne "windows_store_lifecycle.v1" -or
    [string]$lifecycle.release_revision -cne [string]$manifest.release_revision -or
    [string]$lifecycle.marketing_version -cne [string]$manifest.marketing_version -or
    [string]$lifecycle.store_package_version -cne [string]$manifest.version -or
    [string]$lifecycle.processor_architecture -cne
        [string]$manifest.processor_architecture -or
    [string]$lifecycle.msix_manifest_sha256 -cne $manifestHash -or
    [string]$lifecycle.installed_package_full_name -cne
        [string]$installedPackage.PackageFullName -or
    [string]$lifecycle.installed_package_family_name -cne
        [string]$installedPackage.PackageFamilyName -or
    [string]$lifecycle.store_signature_kind -cne "Store" -or
    [string]$lifecycle.payload_sha256 -cne [string]$manifest.binary_sha256 -or
    [string]$lifecycle.direct_exe_sha256 -cne
        [string]$directEvidence.artifact_sha256 -or
    [string]$lifecycle.standard_code_product_e2e_sha256 -cne
        [string]$productEvidence[0].sha256) {
    throw "Lifecycle evidence is not bound to the final Windows candidate"
}

$requiredRows = @(
    "windows_10|100|zh-CN",
    "windows_10|200|zh-CN",
    "windows_11|100|zh-CN",
    "windows_11|200|zh-CN"
)
$rows = @($lifecycle.matrix)
if ($rows.Count -ne $requiredRows.Count) {
    throw "Lifecycle evidence must contain the exact Windows/DPI/IME matrix"
}
$seenRows = [System.Collections.Generic.HashSet[string]]::new(
    [System.StringComparer]::Ordinal)
$seenLifecycleEvidenceFiles = [System.Collections.Generic.HashSet[string]]::new(
    [System.StringComparer]::OrdinalIgnoreCase)
$lifecycleDirectory = Split-Path -Parent $lifecycleFile
$currentPackageFullNameParts = @(
    ([string]$installedPackage.PackageFullName).Split('_'))
if ($currentPackageFullNameParts.Count -ne 5 -or
    $currentPackageFullNameParts[0] -cne [string]$manifest.package_identity_name -or
    $currentPackageFullNameParts[1] -cne [string]$manifest.version -or
    $currentPackageFullNameParts[2] -ine [string]$manifest.processor_architecture -or
    [string]::IsNullOrWhiteSpace($currentPackageFullNameParts[4])) {
    throw "Live Store package full name cannot be decomposed into the bound identity"
}
foreach ($row in $rows) {
    foreach ($field in @("install", "first_launch", "upgrade",
            "downgrade_rejected", "repair", "uninstall", "reinstall",
            "data_sentinel_preserved", "accessible_name_checked")) {
        Assert-BooleanField $row $field "Lifecycle matrix row"
        if (-not [bool]$row.$field) {
            throw "Lifecycle matrix row contains a failed gate: $field"
        }
    }
    $key = "$([string]$row.os_family)|$([int]$row.dpi_percent)|$([string]$row.ime)"
    $rowOSBuild = [System.Version]::new()
    if (-not [System.Version]::TryParse([string]$row.os_build, [ref]$rowOSBuild) -or
        $rowOSBuild.Major -ne 10 -or $rowOSBuild.Build -lt 0 -or
        ([string]$row.os_family -ceq "windows_10" -and
            $rowOSBuild.Build -ge 22000) -or
        ([string]$row.os_family -ceq "windows_11" -and
            $rowOSBuild.Build -lt 22000)) {
        throw "Lifecycle matrix row has an invalid Windows build: $key"
    }
    $rowEvidenceName = [string]$row.evidence_file
    if ($key -cnotin $requiredRows -or -not $seenRows.Add($key) -or
        [string]$row.package_full_name -cne
            [string]$installedPackage.PackageFullName -or
        [string]::IsNullOrWhiteSpace($rowEvidenceName) -or
        -not $seenLifecycleEvidenceFiles.Add($rowEvidenceName) -or
        [string]$row.evidence_sha256 -notmatch '^[0-9a-f]{64}$') {
        throw "Lifecycle matrix row is missing, duplicated, or unbound: $key"
    }
    $rowEvidenceFile = Resolve-EvidenceSibling `
        $lifecycleDirectory $rowEvidenceName "Lifecycle row evidence $key"
    if ((Get-SHA256 $rowEvidenceFile) -cne [string]$row.evidence_sha256) {
        throw "Lifecycle row evidence hash differs: $key"
    }
    $rowEvidence = Get-Content -LiteralPath $rowEvidenceFile -Raw | ConvertFrom-Json
    foreach ($field in @("install", "first_launch", "upgrade",
            "downgrade_rejected", "repair", "uninstall", "reinstall",
            "data_sentinel_preserved", "accessible_name_checked")) {
        Assert-BooleanField $rowEvidence $field "Lifecycle row report $key"
        if (-not [bool]$rowEvidence.$field) {
            throw "Lifecycle row report contains a failed gate: $key $field"
        }
    }
    if ([string]$rowEvidence.protocol_version -cne
            "windows_store_lifecycle_row.v1" -or
        [string]$rowEvidence.release_revision -cne
            [string]$manifest.release_revision -or
        [string]$rowEvidence.marketing_version -cne
            [string]$manifest.marketing_version -or
        [string]$rowEvidence.store_package_version -cne [string]$manifest.version -or
        [string]$rowEvidence.processor_architecture -cne
            [string]$manifest.processor_architecture -or
        [string]$rowEvidence.msix_manifest_sha256 -cne $manifestHash -or
        [string]$rowEvidence.store_signature_kind -cne "Store" -or
        [string]$rowEvidence.payload_sha256 -cne
            [string]$manifest.binary_sha256 -or
        [string]$rowEvidence.direct_exe_sha256 -cne
            [string]$directEvidence.artifact_sha256 -or
        [string]$rowEvidence.standard_code_product_e2e_sha256 -cne
            [string]$productEvidence[0].sha256 -or
        [string]$rowEvidence.installed_package_family_name -cne
            [string]$installedPackage.PackageFamilyName -or
        [string]$rowEvidence.package_full_name -cne [string]$row.package_full_name -or
        [string]$rowEvidence.os_family -cne [string]$row.os_family -or
        [string]$rowEvidence.os_build -cne [string]$row.os_build -or
        [int]$rowEvidence.dpi_percent -ne [int]$row.dpi_percent -or
        [string]$rowEvidence.ime -cne [string]$row.ime -or
        [string]::IsNullOrWhiteSpace(
            [string]$rowEvidence.previous_package_full_name) -or
        [string]$rowEvidence.previous_package_full_name -ceq
            [string]$rowEvidence.package_full_name -or
        [string]$rowEvidence.sentinel_before_sha256 -notmatch '^[0-9a-f]{64}$' -or
        [string]$rowEvidence.sentinel_after_sha256 -cne
            [string]$rowEvidence.sentinel_before_sha256) {
        throw "Lifecycle row report is not bound to its exact candidate and matrix row: $key"
    }
    $previousStoreVersion = Get-StoreVersion `
        ([string]$rowEvidence.previous_store_package_version) `
        "Lifecycle $key previous Store package version"
    $previousPackageFullNameParts = @(
        ([string]$rowEvidence.previous_package_full_name).Split('_'))
    if ($previousStoreVersion -ge $currentStoreVersion -or
        $previousPackageFullNameParts.Count -ne 5 -or
        $previousPackageFullNameParts[0] -cne
            [string]$manifest.package_identity_name -or
        $previousPackageFullNameParts[1] -cne $previousStoreVersion.ToString() -or
        $previousPackageFullNameParts[2] -ine
            [string]$manifest.processor_architecture -or
        $previousPackageFullNameParts[3] -cne $currentPackageFullNameParts[3] -or
        $previousPackageFullNameParts[4] -cne $currentPackageFullNameParts[4]) {
        throw "Lifecycle row does not prove an upgrade from the same older Store identity: $key"
    }
    $previousTimestamp = $acceptedAt.ToUniversalTime()
    foreach ($timestampField in @(
            "install_at_utc", "first_launch_at_utc", "upgrade_at_utc",
            "downgrade_rejected_at_utc", "repair_at_utc", "uninstall_at_utc",
            "reinstall_at_utc"
        )) {
        $operationTimestamp = Get-RoundTripTimestamp `
            ([string]$rowEvidence.$timestampField) "Lifecycle $key $timestampField"
        if ($operationTimestamp -lt $previousTimestamp) {
            throw "Lifecycle row operation timestamps are not monotonic: $key"
        }
        $previousTimestamp = $operationTimestamp
    }
}

if ($GitHubRepository -cne "CWNU-Open-Source-Community/Traverse-Board" -or
    [string]$attestationIndex.protocol_version -cne
        "windows_artifact_attestations.v1" -or
    [string]$attestationIndex.release_version -cne
        [string]$manifest.marketing_version -or
    [string]$attestationIndex.release_revision -cne
        [string]$manifest.release_revision -or
    [string]$attestationIndex.direct_exe_sha256 -cne
        [string]$directEvidence.artifact_sha256 -or
    [string]$attestationIndex.store_msixupload_sha256 -cne $candidateUploadHash) {
    throw "Artifact attestation index is not bound to the final Windows candidate"
}
$attestationDirectory = Split-Path -Parent $attestationIndexFile
$sbomEvidence = @($manifest.release_evidence | Where-Object {
        [string]$_.name -ceq "sbom.json"
    })
$sbomFile = Resolve-EvidenceSibling $manifestDirectory "sbom.json" `
    "CycloneDX SBOM"
if ($sbomEvidence.Count -ne 1 -or
    (Get-SHA256 $sbomFile) -cne [string]$sbomEvidence[0].sha256) {
    throw "Store manifest does not bind the exact CycloneDX SBOM"
}
$sbomDocument = Get-Content -LiteralPath $sbomFile -Raw | ConvertFrom-Json
$requiredAttestations = [ordered]@{
    direct_exe_provenance = [ordered]@{
        subject_name = "TraverseBoard.exe"
        subject_sha256 = [string]$directEvidence.artifact_sha256
        predicate_type = "https://slsa.dev/provenance/v1"
        artifact_path = $directArtifactFile
        bundle_name = "direct-exe-provenance.bundle.json"
    }
    direct_exe_sbom = [ordered]@{
        subject_name = "TraverseBoard.exe"
        subject_sha256 = [string]$directEvidence.artifact_sha256
        predicate_type = "https://cyclonedx.org/bom"
        artifact_path = $directArtifactFile
        bundle_name = "direct-exe-sbom.bundle.json"
    }
    store_msixupload_provenance = [ordered]@{
        subject_name = [string]$manifest.msixupload_name
        subject_sha256 = $candidateUploadHash
        predicate_type = "https://slsa.dev/provenance/v1"
        artifact_path = $candidateUploadFile
        bundle_name = "store-msixupload-provenance.bundle.json"
    }
}
$attestations = @($attestationIndex.attestations)
if ($attestations.Count -ne $requiredAttestations.Count) {
    throw "Artifact attestation index must contain the exact three final subjects"
}
$seenAttestations = [System.Collections.Generic.HashSet[string]]::new(
    [System.StringComparer]::Ordinal)
foreach ($attestation in $attestations) {
    $kind = [string]$attestation.kind
    if (-not $requiredAttestations.Contains($kind) -or
        -not $seenAttestations.Add($kind)) {
        throw "Artifact attestation kind is unexpected or duplicated: $kind"
    }
    $requiredAttestation = $requiredAttestations[$kind]
    $bundleName = [string]$attestation.bundle_name
    Assert-SHA256 ([string]$attestation.bundle_sha256) `
        "Artifact attestation bundle hash for $kind"
    if ([string]$attestation.subject_name -cne
            [string]$requiredAttestation.subject_name -or
        [string]$attestation.subject_sha256 -cne
            [string]$requiredAttestation.subject_sha256 -or
        [string]$attestation.predicate_type -cne
            [string]$requiredAttestation.predicate_type -or
        [string]::IsNullOrWhiteSpace([string]$attestation.attestation_id) -or
        $bundleName -cne [string]$requiredAttestation.bundle_name) {
        throw "Artifact attestation entry differs from the required subject: $kind"
    }
    $attestationURI = Assert-HTTPSURL ([string]$attestation.attestation_url) `
        "Artifact attestation URL for $kind"
    if ($attestationURI.Host -ine "github.com") {
        throw "Artifact attestation URL must use github.com: $kind"
    }
    $bundlePath = Resolve-EvidenceSibling $attestationDirectory $bundleName `
        "Artifact attestation bundle for $kind"
    if ((Get-SHA256 $bundlePath) -cne [string]$attestation.bundle_sha256 -or
        -not (Test-Path -LiteralPath ([string]$requiredAttestation.artifact_path) -PathType Leaf) -or
        (Get-SHA256 ([string]$requiredAttestation.artifact_path)) -cne
            [string]$requiredAttestation.subject_sha256) {
        throw "Artifact attestation bundle or subject has drifted: $kind"
    }
    $statement = Get-AttestationStatement $bundlePath
    $statementSubjects = @($statement.subject)
    if ([string]$statement.predicateType -cne
            [string]$requiredAttestation.predicate_type -or
        $statementSubjects.Count -ne 1 -or
        [string]$statementSubjects[0].name -cne
            [string]$requiredAttestation.subject_name -or
        [string]$statementSubjects[0].digest.sha256 -cne
            [string]$requiredAttestation.subject_sha256) {
        throw "Attestation bundle statement differs from its indexed subject: $kind"
    }
    if ($kind -ceq "direct_exe_sbom") {
        try {
            Assert-JSONEquivalent $sbomDocument $statement.predicate '$.predicate'
        }
        catch {
            throw "Direct EXE SBOM attestation differs from the bound CycloneDX document: $($_.Exception.Message)"
        }
    }
    & $ghCommand.Source attestation verify `
        ([string]$requiredAttestation.artifact_path) `
        --bundle $bundlePath `
        --repo $GitHubRepository `
        --signer-workflow "$GitHubRepository/.github/workflows/release-desktop.yml" `
        --source-digest ([string]$manifest.release_revision) `
        --source-ref "refs/tags/$([string]$manifest.marketing_version)" `
        --deny-self-hosted-runners `
        --predicate-type ([string]$requiredAttestation.predicate_type) | Out-Null
    if ($LASTEXITCODE -ne 0) {
        throw "Artifact attestation cryptographic verification failed: $kind"
    }
}

$releaseMetadataFile = Resolve-EvidenceSibling $manifestDirectory `
    "release-metadata.json" "Release metadata"
Assert-SHA256 ([string]$manifest.release_metadata_sha256) "Release metadata hash"
if ((Get-SHA256 $releaseMetadataFile) -cne
        [string]$manifest.release_metadata_sha256) {
    throw "Release metadata differs from the immutable Store manifest"
}
$expectedReleaseAssets = [System.Collections.Generic.Dictionary[string, string]]::new(
    [System.StringComparer]::Ordinal)
$expectedReleaseAssets["TraverseBoard.exe"] = [string]$directEvidence.artifact_sha256
$expectedReleaseAssets["direct-exe-signing.json"] = Get-SHA256 $directEvidenceFile
$expectedReleaseAssets["direct-exe-signing-request.json"] =
    [string]$directEvidence.signing_request_sha256
$expectedReleaseAssets["direct-exe-signing-handoff.json"] =
    [string]$directEvidence.signing_handoff_sha256
$expectedReleaseAssets["release-metadata.json"] =
    [string]$manifest.release_metadata_sha256
$expectedReleaseAssets["artifact-attestations.json"] = Get-SHA256 $attestationIndexFile
$publicEvidenceNames = @(
    "SHA256SUMS", "windows-compatibility.json", "standard-code-packaged-e2e.json",
    "standard-code-product-e2e.json", "standard-code-security-evidence.json",
    "standard-code-release-gate.json", "sbom.json", "NOTICE"
)
foreach ($releaseEvidenceName in $publicEvidenceNames) {
    if (-not $releaseEvidenceHashes.ContainsKey($releaseEvidenceName)) {
        throw "Store manifest does not bind required public evidence: $releaseEvidenceName"
    }
    $expectedReleaseAssets[$releaseEvidenceName] =
        $releaseEvidenceHashes[$releaseEvidenceName]
}
foreach ($attestation in $attestations) {
    $expectedReleaseAssets[[string]$attestation.bundle_name] =
        [string]$attestation.bundle_sha256
}
if ($githubReleaseAssets.Count -ne $expectedReleaseAssets.Count) {
    throw "Live GitHub Release asset count differs from the exact stable allowlist"
}
$seenReleaseAssets = [System.Collections.Generic.HashSet[string]]::new(
    [System.StringComparer]::Ordinal)
foreach ($asset in $githubReleaseAssets) {
    $assetName = [string]$asset.name
    if (-not $seenReleaseAssets.Add($assetName) -or
        -not $expectedReleaseAssets.ContainsKey($assetName) -or
        [string]$asset.state -cne "uploaded" -or [int64]$asset.size -le 0 -or
        [string]$asset.digest -cne
            "sha256:$([string]$expectedReleaseAssets[$assetName])") {
        throw "Live GitHub Release asset is missing, duplicated, or hash-drifted: $assetName"
    }
}

$completion = [ordered]@{
    protocol_version = "windows_release_completion.v1"
    complete = $true
    release_revision = [string]$manifest.release_revision
    marketing_version = [string]$manifest.marketing_version
    store_package_version = [string]$manifest.version
    processor_architecture = [string]$manifest.processor_architecture
    msix_manifest_sha256 = $manifestHash
    submitted_msix_sha256 = [string]$manifest.msix_sha256
    submitted_msixupload_sha256 = $candidateUploadHash
    installed_package_full_name = [string]$installedPackage.PackageFullName
    installed_package_family_name = [string]$installedPackage.PackageFamilyName
    store_signature_kind = "Store"
    store_payload_sha256 = $installedPayloadHash
    direct_exe_sha256 = [string]$directEvidence.artifact_sha256
    direct_exe_signing_evidence_sha256 = Get-SHA256 $directEvidenceFile
    direct_exe_signing_request_sha256 = [string]$directEvidence.signing_request_sha256
    direct_exe_signing_handoff_sha256 = [string]$directEvidence.signing_handoff_sha256
    direct_exe_signer_subject = [string]$directEvidence.signer_subject
    direct_exe_signer_thumbprint = [string]$directEvidence.signer_thumbprint
    direct_exe_signed_at_utc = $directSignedAt.ToString("o")
    partner_center_readback_sha256 = Get-SHA256 $storeReadbackFile
    lifecycle_evidence_sha256 = Get-SHA256 $lifecycleFile
    listing_zh_cn_sha256 = $listingZhCNHash
    listing_en_us_sha256 = $listingEnUSHash
    privacy_policy_sha256 = $privacyPolicyHash
    store_icon_sha256 = $storeIconHash
    listing_readback_sha256 = $listingReadbackHash
    listing_readback_captured_at_utc = $listingCapturedAt.ToString("o")
    github_release_readback_sha256 = $githubReleaseReadbackHash
    github_release_url = [string]$githubRelease.url
    github_release_published_at_utc = $githubReleasePublishedAt.ToString("o")
    github_tag_revision = $githubTagRevision
    artifact_attestation_index_sha256 = Get-SHA256 $attestationIndexFile
    partner_center_submission_id = [string]$readback.partner_center_submission_id
    accepted_at_utc = $acceptedAt.ToUniversalTime().ToString("o")
    store_product_url = [string]$readback.store_product_url
    privacy_policy_url = [string]$readback.privacy_policy_url
    age_rating = [string]$readback.age_rating
    run_full_trust_approved = $true
    microsoft_store_signature_kind_verified = $true
    lifecycle_matrix_passed = $true
    final_product_report_sha256 = [string]$productEvidence[0].sha256
    completed_at_utc = [System.DateTimeOffset]::UtcNow.ToString("o")
}
[System.IO.Directory]::CreateDirectory((Split-Path -Parent $outputFile)) | Out-Null
Assert-NoRepositoryReparsePoint $outputRoot "Windows completion output"
if (Test-Path -LiteralPath $outputFile) {
    throw "Windows completion evidence cannot overwrite an existing file"
}
$completionBytes = [System.Text.UTF8Encoding]::new($false).GetBytes(
    (($completion | ConvertTo-Json -Depth 5) + [System.Environment]::NewLine))
$temporaryOutput = Join-Path $outputRoot (
    ".windows-release-completion-$([guid]::NewGuid().ToString('N')).tmp")
$outputStream = $null
try {
    $outputStream = [System.IO.FileStream]::new(
        $temporaryOutput, [System.IO.FileMode]::CreateNew,
        [System.IO.FileAccess]::Write, [System.IO.FileShare]::None)
    $outputStream.Write($completionBytes, 0, $completionBytes.Length)
    $outputStream.Flush($true)
    $outputStream.Dispose()
    $outputStream = $null
    [System.IO.File]::Move($temporaryOutput, $outputFile)
}
finally {
    if ($null -ne $outputStream) {
        $outputStream.Dispose()
    }
    if (Test-Path -LiteralPath $temporaryOutput -PathType Leaf) {
        Remove-Item -LiteralPath $temporaryOutput -Force
    }
}

Write-Output "windows_release_completion: $outputFile"
Write-Output "windows_release_completion_sha256: $(Get-SHA256 $outputFile)"
Write-Output "windows_release_complete: true"
