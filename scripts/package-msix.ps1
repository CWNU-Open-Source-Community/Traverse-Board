[CmdletBinding()]
param(
    [string]$OutputDirectory = "build/desktop",
    [string]$Version = "0.1.0.0",
    [ValidateSet("Development", "MicrosoftStore")]
    [string]$Distribution = "Development",
    [ValidateSet("x64", "arm64")]
    [string]$ProcessorArchitecture = "x64",
    [string]$PackageIdentityName = "",
    [string]$PackagePublisher = "",
    [string]$PublisherDisplayName = "",
    [string]$ExpectedDirectSignerSubject = "",
    [string]$ExpectedDirectSignerThumbprint = "",
    [string]$CertificatePath = "",
    [string]$CertificatePassword = "",
    [string]$TimestampURL = "http://timestamp.digicert.com",
    [switch]$ContractTest
)

<#
.SYNOPSIS
Creates either a local-development MSIX or a Partner Center-bound Store upload.

.DESCRIPTION
MicrosoftStore mode requires the exact Name, Publisher, and
PublisherDisplayName values copied from Partner Center. It rejects the
checked-in development placeholders, injects the selected architecture, packs
with MakeAppx semantic validation enabled, verifies the packaged manifest and
payload, and creates a deterministic .msixupload containing the MSIX.

The Store path deliberately does not accept a PFX: Microsoft signs the package
after certification. Development mode retains optional certificate signing for
local lifecycle tests, but such a package is never marked as a Store candidate.
ContractTest exercises the Store-only packaging/verification branch with a
synthetic identity, marks the evidence as non-submittable, and does not waive
release evidence for a real MicrosoftStore candidate.
#>

$ErrorActionPreference = "Stop"
if ([System.Environment]::OSVersion.Platform -ne [System.PlatformID]::Win32NT) {
    throw "MSIX packaging requires Windows"
}
$Distribution = if ($Distribution -ieq "MicrosoftStore") {
    "MicrosoftStore"
}
else {
    "Development"
}
$ProcessorArchitecture = $ProcessorArchitecture.ToLowerInvariant()

function Assert-Text {
    param(
        [string]$Value,
        [string]$Label,
        [int]$MinimumLength,
        [int]$MaximumLength
    )
    if ($null -eq $Value -or $Value.Length -lt $MinimumLength -or
        $Value.Length -gt $MaximumLength -or
        [regex]::IsMatch($Value, '[\x00-\x1f\x7f]')) {
        throw "$Label is missing, oversized, or contains control characters"
    }
}

function Find-SDKTool {
    param([string]$Name)
    $kitRoot = "C:\Program Files (x86)\Windows Kits\10\bin"
    if (-not (Test-Path -LiteralPath $kitRoot)) { return $null }
    $versions = Get-ChildItem -LiteralPath $kitRoot -Directory | Sort-Object Name -Descending
    foreach ($sdkVersion in $versions) {
        $candidate = Join-Path $sdkVersion.FullName "x64\$Name.exe"
        if (Test-Path -LiteralPath $candidate -PathType Leaf) { return $candidate }
    }
    return $null
}

function Get-SHA256 {
    param([string]$Path)
    return (Get-FileHash -Algorithm SHA256 -LiteralPath $Path).Hash.ToLowerInvariant()
}

function Get-ZipEntrySHA256 {
    param([System.IO.Compression.ZipArchiveEntry]$Entry)
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

function Get-PEProcessorArchitecture {
    param([string]$Path)
    $stream = [System.IO.File]::Open(
        $Path, [System.IO.FileMode]::Open, [System.IO.FileAccess]::Read,
        [System.IO.FileShare]::Read)
    $reader = [System.IO.BinaryReader]::new($stream)
    try {
        if ($stream.Length -lt 64 -or $reader.ReadUInt16() -ne 0x5a4d) {
            throw "MSIX payload is not a valid PE executable"
        }
        $stream.Position = 0x3c
        $peOffset = [uint32]$reader.ReadUInt32()
        if ([uint64]$peOffset + 6 -gt [uint64]$stream.Length) {
            throw "MSIX payload has an invalid PE header offset"
        }
        $stream.Position = $peOffset
        if ($reader.ReadUInt32() -ne 0x00004550) {
            throw "MSIX payload has an invalid PE signature"
        }
        switch ($reader.ReadUInt16()) {
            0x8664 { return "x64" }
            0xaa64 { return "arm64" }
            default { throw "MSIX payload uses an unsupported PE machine type" }
        }
    }
    finally {
        $reader.Dispose()
        $stream.Dispose()
    }
}

function Write-ManifestXML {
    param(
        [xml]$Document,
        [string]$Path
    )
    $settings = [System.Xml.XmlWriterSettings]::new()
    $settings.Encoding = [System.Text.UTF8Encoding]::new($false)
    $settings.Indent = $true
    $settings.NewLineChars = [System.Environment]::NewLine
    $settings.NewLineHandling = [System.Xml.NewLineHandling]::Replace
    $writer = [System.Xml.XmlWriter]::Create($Path, $settings)
    try {
        $Document.Save($writer)
    }
    finally {
        $writer.Dispose()
    }
}

$versionParts = @($Version.Split('.'))
if ($versionParts.Count -ne 4) {
    throw "MSIX version must use Major.Minor.Build.Revision"
}
foreach ($part in $versionParts) {
    if ($part -notmatch '^[0-9]+$' -or [uint64]$part -gt 65535) {
        throw "MSIX version components must be integers from 0 through 65535"
    }
}

$repositoryRoot = [System.IO.Path]::GetFullPath((Split-Path -Parent $PSScriptRoot))
$outputRoot = [System.IO.Path]::GetFullPath((Join-Path $repositoryRoot $OutputDirectory))
$repositoryPrefix = $repositoryRoot.TrimEnd('\', '/') +
    [System.IO.Path]::DirectorySeparatorChar
if (-not $outputRoot.StartsWith($repositoryPrefix,
        [System.StringComparison]::OrdinalIgnoreCase)) {
    throw "MSIX output directory must remain inside the repository"
}

$binaryPath = Join-Path $outputRoot "TraverseBoard.exe"
$releaseMetadataPath = Join-Path $outputRoot "release-metadata.json"
$directArtifactPath = Join-Path $outputRoot "TraverseBoard.direct.exe"
$directSigningRequestPath = Join-Path $outputRoot "direct-exe-signing-request.json"
$directSigningHandoffPath = Join-Path $outputRoot "direct-exe-signing-handoff.json"
$directSigningEvidencePath = Join-Path $outputRoot "direct-exe-signing.json"
$manifestPath = Join-Path $repositoryRoot "packaging/windows/AppxManifest.xml"
$assetRoot = Join-Path $repositoryRoot "packaging/windows/Assets"
$visualAssetHelperPath = Join-Path $repositoryRoot "scripts/windows-visual-assets.ps1"
$priConfigPath = Join-Path $repositoryRoot "packaging/windows/priconfig.xml"
$storeRunbookRelativePath = "packaging/windows/STORE-SUBMISSION.md"
$storeRunbookPath = Join-Path $repositoryRoot $storeRunbookRelativePath
$runFullTrustJustification = "Traverse Board is a packaged classic Win32 local development workbench. runFullTrust launches the primary desktop process and, only after the user explicitly selects and trusts a workspace and approves the relevant permission, invokes local developer tools. It runs as the current user, requests no administrator elevation, installs no service or driver, does not modify system security policy, and does not declare allowElevation."
$isStore = $Distribution -ceq "MicrosoftStore"
$isStoreContractTest = $isStore -and [bool]$ContractTest
$isStoreSubmission = $isStore -and -not $isStoreContractTest
if ($ContractTest -and -not $isStore) {
    throw "ContractTest is valid only with MicrosoftStore distribution"
}
$expectedGoArch = if ($ProcessorArchitecture -ceq "x64") { "amd64" } else { "arm64" }

if ($isStore -and ([uint64]$versionParts[0] -eq 0 -or [uint64]$versionParts[3] -ne 0)) {
    throw "Microsoft Store package version requires Major >= 1 and Revision = 0"
}

foreach ($required in @(
        $binaryPath,
        $releaseMetadataPath,
        $manifestPath,
        $storeRunbookPath,
        $visualAssetHelperPath,
        $priConfigPath
    )) {
    if (-not (Test-Path -LiteralPath $required -PathType Leaf)) {
        throw "MSIX input is missing: $required"
    }
}

. $visualAssetHelperPath
$sourceVisualAssetInventory = @(
    Get-WindowsVisualAssetDirectoryInventory -AssetRoot $assetRoot
)

$releaseMetadata = Get-Content -LiteralPath $releaseMetadataPath -Raw | ConvertFrom-Json
foreach ($booleanField in @("modified", "reproducibility_checked", "reproducible")) {
    $property = $releaseMetadata.PSObject.Properties[$booleanField]
    if ($null -eq $property -or $property.Value -isnot [bool]) {
        throw "Release metadata field must be a JSON boolean: $booleanField"
    }
}
$binaryHash = Get-SHA256 $binaryPath
if ([string]$releaseMetadata.binary_name -cne "TraverseBoard.exe" -or
    [string]$releaseMetadata.target_os -cne "windows" -or
    [string]$releaseMetadata.target_arch -cne $expectedGoArch -or
    [string]$releaseMetadata.sha256 -cne $binaryHash) {
    throw "MSIX input does not match the Windows release metadata"
}
$peArchitecture = Get-PEProcessorArchitecture $binaryPath
if ($peArchitecture -cne $ProcessorArchitecture) {
    throw "MSIX ProcessorArchitecture does not match the TraverseBoard.exe PE machine type"
}

$directArtifactPresent = Test-Path -LiteralPath $directArtifactPath -PathType Leaf
$directEvidencePresent = Test-Path -LiteralPath $directSigningEvidencePath -PathType Leaf
if ($directArtifactPresent -ne $directEvidencePresent) {
    throw "Direct-download artifact and signing evidence must be staged together"
}
$directArtifactName = "TraverseBoard.exe"
$directArtifactHash = $binaryHash
$directSigningEvidenceName = ""
$directSigningEvidenceHash = ""
$directSigningRequestName = ""
$directSigningRequestHash = ""
$directSigningHandoffName = ""
$directSigningHandoffHash = ""
$directPayloadBindingVerified = $true
if ($directArtifactPresent) {
    foreach ($directInput in @($directArtifactPath, $directSigningEvidencePath)) {
        if (-not $directInput.StartsWith($repositoryPrefix,
                [System.StringComparison]::OrdinalIgnoreCase)) {
            throw "Direct-download evidence must remain inside the repository"
        }
    }
    & (Join-Path $PSScriptRoot "stage-direct-exe.ps1") `
        -OutputDirectory $OutputDirectory `
        -ExpectedVersion ([string]$releaseMetadata.app_version) `
        -ExpectedRevision ([string]$releaseMetadata.revision) `
        -ExpectedSignerSubject $ExpectedDirectSignerSubject `
        -ExpectedSignerThumbprint $ExpectedDirectSignerThumbprint `
        -VerifyOnly
    $directEvidence = Get-Content -LiteralPath $directSigningEvidencePath -Raw |
        ConvertFrom-Json
    foreach ($booleanField in @("trusted_release", "prerelease",
            "payload_binding_verified", "timestamp_present", "signtool_verified")) {
        $property = $directEvidence.PSObject.Properties[$booleanField]
        if ($null -eq $property -or $property.Value -isnot [bool]) {
            throw "Direct EXE signing evidence field must be a JSON boolean: $booleanField"
        }
    }
    $directArtifactName = [System.IO.Path]::GetFileName($directArtifactPath)
    $directArtifactHash = Get-SHA256 $directArtifactPath
    $directSigningEvidenceName = [System.IO.Path]::GetFileName($directSigningEvidencePath)
    $directSigningEvidenceHash = Get-SHA256 $directSigningEvidencePath
    $directPayloadBindingVerified = [bool]$directEvidence.payload_binding_verified
    if ([string]$directEvidence.protocol_version -cne "direct_exe_signing.v1" -or
        [string]$directEvidence.payload_sha256 -cne $binaryHash -or
        [string]$directEvidence.artifact_sha256 -cne $directArtifactHash -or
        [string]$directEvidence.release_version -cne [string]$releaseMetadata.app_version -or
        [string]$directEvidence.release_revision -cne [string]$releaseMetadata.revision -or
        -not $directPayloadBindingVerified) {
        throw "Direct-download signing evidence does not bind the MSIX payload"
    }
    if ($directEvidence.trusted_release) {
        if ([string]$directEvidence.signing_request_name -cne
                "direct-exe-signing-request.json" -or
            -not (Test-Path -LiteralPath $directSigningRequestPath -PathType Leaf) -or
            (Get-SHA256 $directSigningRequestPath) -cne
                [string]$directEvidence.signing_request_sha256) {
            throw "Trusted direct-download evidence is missing its signing request"
        }
        $directSigningRequestName = "direct-exe-signing-request.json"
        $directSigningRequestHash = Get-SHA256 $directSigningRequestPath
        if ([string]$directEvidence.signing_handoff_name -cne
                "direct-exe-signing-handoff.json" -or
            -not (Test-Path -LiteralPath $directSigningHandoffPath -PathType Leaf) -or
            (Get-SHA256 $directSigningHandoffPath) -cne
                [string]$directEvidence.signing_handoff_sha256) {
            throw "Trusted direct-download evidence is missing its signing handoff"
        }
        $directSigningHandoffName = "direct-exe-signing-handoff.json"
        $directSigningHandoffHash = Get-SHA256 $directSigningHandoffPath
    }
    elseif (-not [string]::IsNullOrEmpty([string]$directEvidence.signing_request_name) -or
        -not [string]::IsNullOrEmpty([string]$directEvidence.signing_request_sha256) -or
        -not [string]::IsNullOrEmpty([string]$directEvidence.signing_handoff_name) -or
        -not [string]::IsNullOrEmpty([string]$directEvidence.signing_handoff_sha256)) {
        throw "Untrusted prerelease evidence cannot claim a signing handoff"
    }
}
if ($isStoreSubmission -and (-not $directArtifactPresent -or
        -not $directEvidence.trusted_release -or $directEvidence.prerelease -or
        -not $directEvidence.timestamp_present -or -not $directEvidence.signtool_verified)) {
    throw "A real Store submission requires the trusted direct-EXE signing handoff"
}
if ($isStore -and ($releaseMetadata.modified -or
        -not $releaseMetadata.reproducibility_checked -or
        -not $releaseMetadata.reproducible)) {
    throw "Microsoft Store packaging requires a clean, reproducible EXE candidate"
}

$releaseEvidence = @()
if ($isStoreSubmission) {
    $portableManifestPath = Join-Path $outputRoot "portable-zip-manifest.json"
    $bootstrapReportPath = Join-Path $outputRoot "standard-code-packaged-e2e.json"
    $productReportPath = Join-Path $outputRoot "standard-code-product-e2e.json"
    $securityReportPath = Join-Path $outputRoot "standard-code-security-evidence.json"
    $releaseGatePath = Join-Path $outputRoot "standard-code-release-gate.json"
    foreach ($requiredEvidencePath in @(
            $portableManifestPath,
            $bootstrapReportPath,
            $productReportPath,
            $securityReportPath,
            $releaseGatePath,
            (Join-Path $outputRoot "sbom.json"),
            (Join-Path $outputRoot "NOTICE"),
            (Join-Path $outputRoot "windows-compatibility.json"),
            $directArtifactPath,
            $directSigningRequestPath,
            $directSigningHandoffPath,
            $directSigningEvidencePath,
            (Join-Path $outputRoot "SHA256SUMS"),
            (Join-Path $outputRoot "verification-SHA256SUMS")
        )) {
        if (-not (Test-Path -LiteralPath $requiredEvidencePath -PathType Leaf)) {
            throw "Store submission release evidence is missing: $requiredEvidencePath"
        }
    }
    $portableManifest = Get-Content -LiteralPath $portableManifestPath -Raw |
        ConvertFrom-Json
    $portableZipName = [string]$portableManifest.zip_name
    if ([string]::IsNullOrWhiteSpace($portableZipName) -or
        [System.IO.Path]::GetFileName($portableZipName) -cne $portableZipName) {
        throw "Store submission portable evidence contains an unsafe archive name"
    }
    $portableZipPath = Join-Path $outputRoot $portableZipName
    if (-not (Test-Path -LiteralPath $portableZipPath -PathType Leaf)) {
        throw "Store submission portable archive is missing: $portableZipPath"
    }
    & go -C $repositoryRoot run ./cmd/releasegate `
        --binary $binaryPath `
        --archive $portableZipPath `
        --portable-manifest $portableManifestPath `
        --release-metadata $releaseMetadataPath `
        --bootstrap $bootstrapReportPath `
        --product $productReportPath `
        --security $securityReportPath `
        --expected-revision ([string]$releaseMetadata.revision) `
        --verify-report $releaseGatePath
    if ($LASTEXITCODE -ne 0) {
        throw "Store submission Standard Code release evidence verification failed"
    }
    $releaseEvidenceNames = @(
        $portableZipName,
        "portable-zip-manifest.json",
        "standard-code-packaged-e2e.json",
        "standard-code-product-e2e.json",
        "standard-code-security-evidence.json",
        "standard-code-release-gate.json",
        "sbom.json",
        "NOTICE",
        "windows-compatibility.json",
        $directSigningRequestName,
        $directSigningHandoffName,
        $directSigningEvidenceName,
        "SHA256SUMS",
        "verification-SHA256SUMS"
    )
    $releaseEvidence = @($releaseEvidenceNames | ForEach-Object {
            $evidencePath = Join-Path $outputRoot $_
            [ordered]@{
                name = $_
                sha256 = (Get-SHA256 $evidencePath)
            }
        })
}

$templateContent = [System.IO.File]::ReadAllText($manifestPath)
[xml]$manifestDocument = $templateContent
$namespaceManager = [System.Xml.XmlNamespaceManager]::new($manifestDocument.NameTable)
$namespaceManager.AddNamespace("f", "http://schemas.microsoft.com/appx/manifest/foundation/windows10")
$identity = $manifestDocument.SelectSingleNode("/f:Package/f:Identity", $namespaceManager)
$publisherDisplayNode = $manifestDocument.SelectSingleNode(
    "/f:Package/f:Properties/f:PublisherDisplayName", $namespaceManager)
if ($null -eq $identity -or $null -eq $publisherDisplayNode) {
    throw "AppxManifest.xml is missing its identity or PublisherDisplayName"
}

$identitySupplied = -not [string]::IsNullOrWhiteSpace($PackageIdentityName) -or
    -not [string]::IsNullOrWhiteSpace($PackagePublisher) -or
    -not [string]::IsNullOrWhiteSpace($PublisherDisplayName)
if ($identitySupplied -and ([string]::IsNullOrWhiteSpace($PackageIdentityName) -or
        [string]::IsNullOrWhiteSpace($PackagePublisher) -or
        [string]::IsNullOrWhiteSpace($PublisherDisplayName))) {
    throw "Package identity overrides must provide Name, Publisher, and PublisherDisplayName together"
}

if ($isStore) {
    if (-not $identitySupplied) {
        throw "Microsoft Store packaging requires the exact Partner Center identity values"
    }
    if ($PackageIdentityName -ieq "PrayuDesktop" -or $PackagePublisher -ieq "CN=Prayu") {
        throw "Microsoft Store packaging rejects the checked-in development identity"
    }
    if (-not [string]::IsNullOrWhiteSpace($CertificatePath)) {
        throw "Microsoft Store packaging does not accept a repository PFX path; Microsoft re-signs after certification"
    }
}
elseif (-not $identitySupplied) {
    $PackageIdentityName = [string]$identity.GetAttribute("Name")
    $PackagePublisher = [string]$identity.GetAttribute("Publisher")
    $PublisherDisplayName = [string]$publisherDisplayNode.InnerText
}

Assert-Text -Value $PackageIdentityName -Label "Package identity Name" -MinimumLength 3 -MaximumLength 50
if ($PackageIdentityName -notmatch '^[A-Za-z0-9.-]+$') {
    throw "Package identity Name may contain only letters, digits, periods, and dashes"
}
Assert-Text -Value $PackagePublisher -Label "Package Publisher" -MinimumLength 1 -MaximumLength 8192
Assert-Text -Value $PublisherDisplayName -Label "PublisherDisplayName" -MinimumLength 1 -MaximumLength 256
if ($PackagePublisher -notmatch '^(?i:(CN|L|O|OU|E|C|S|STREET|T|G|I|SN|DC|SERIALNUMBER|OID\.[0-9.]+)=)') {
    throw "Package Publisher must be an X.500 distinguished name copied from Partner Center or a signing certificate"
}

$identity.SetAttribute("Name", $PackageIdentityName)
$identity.SetAttribute("Publisher", $PackagePublisher)
$identity.SetAttribute("Version", $Version)
$identity.SetAttribute("ProcessorArchitecture", $ProcessorArchitecture)
$publisherDisplayNode.InnerText = $PublisherDisplayName

$safeVersion = $Version -replace '[^0-9A-Za-z._-]', '_'
$msixName = if ($isStore) {
    "TraverseBoard_$safeVersion" + "_$ProcessorArchitecture.msix"
}
else {
    "PrayuDesktop.msix"
}
$msixUploadName = if ($isStore) {
    "TraverseBoard_$safeVersion" + "_$ProcessorArchitecture.msixupload"
}
else {
    ""
}
$msixPath = Join-Path $outputRoot $msixName
$msixUploadPath = if ($isStore) { Join-Path $outputRoot $msixUploadName } else { "" }

$makeappx = Find-SDKTool "makeappx"
if ($null -eq $makeappx) { throw "makeappx.exe was not found in the Windows SDK" }
$makepri = Join-Path (Split-Path -Parent $makeappx) "makepri.exe"
if (-not (Test-Path -LiteralPath $makepri -PathType Leaf)) {
    throw "makepri.exe was not found alongside makeappx.exe in the Windows SDK"
}

$staging = Join-Path $outputRoot (".msix-staging-" + [guid]::NewGuid().ToString("N"))
$unpackRoot = Join-Path $outputRoot (".msix-unpack-" + [guid]::NewGuid().ToString("N"))
$uploadRoot = Join-Path $outputRoot (".msix-upload-" + [guid]::NewGuid().ToString("N"))
$priAuditRoot = Join-Path $outputRoot (".msix-pri-audit-" + [guid]::NewGuid().ToString("N"))
[System.IO.Directory]::CreateDirectory((Join-Path $staging "Assets")) | Out-Null
[System.IO.Directory]::CreateDirectory($priAuditRoot) | Out-Null
try {
    Copy-Item -LiteralPath $binaryPath -Destination (Join-Path $staging "TraverseBoard.exe")
    $stagedManifestPath = Join-Path $staging "AppxManifest.xml"
    Write-ManifestXML -Document $manifestDocument -Path $stagedManifestPath
    foreach ($asset in $sourceVisualAssetInventory) {
        Copy-Item -LiteralPath (Join-Path $assetRoot ([string]$asset.name)) -Destination (
            Join-Path (Join-Path $staging "Assets") ([string]$asset.name))
    }
    $stagedVisualAssetInventory = @(
        Get-WindowsVisualAssetDirectoryInventory -AssetRoot (Join-Path $staging "Assets")
    )
    Assert-WindowsVisualAssetInventoriesEqual -Expected $sourceVisualAssetInventory `
        -Actual $stagedVisualAssetInventory -Label "MSIX staging"

    $stagedPRIPath = Join-Path $staging "resources.pri"
    & $makepri new /pr $staging /cf $priConfigPath /mn $stagedManifestPath `
        /of $stagedPRIPath /o
    if ($LASTEXITCODE -ne 0) { throw "makepri new failed" }
    $stagedPRIFiles = @(Get-ChildItem -LiteralPath $staging -Recurse -File -Filter "*.pri")
    if ($stagedPRIFiles.Count -ne 1 -or
        -not $stagedPRIFiles[0].FullName.Equals(
            $stagedPRIPath, [System.StringComparison]::Ordinal)) {
        throw "MakePri must produce exactly one root resources.pri"
    }
    $stagedPRIHash = Assert-WindowsVisualAssetsPRI -PRIPath $stagedPRIPath `
        -PackageIdentityName $PackageIdentityName -MakePriPath $makepri `
        -DumpPath (Join-Path $priAuditRoot "staged-resources.xml")

    if (Test-Path -LiteralPath $msixPath -PathType Leaf) {
        Remove-Item -LiteralPath $msixPath -Force
    }
    # Do not add /nv: MakeAppx pack performs its built-in semantic validation.
    & $makeappx pack /d $staging /p $msixPath /o
    if ($LASTEXITCODE -ne 0) { throw "makeappx pack failed" }

    $signed = $false
    if (-not [string]::IsNullOrWhiteSpace($CertificatePath)) {
        if (-not (Test-Path -LiteralPath $CertificatePath -PathType Leaf)) {
            throw "Code-signing certificate was not found: $CertificatePath"
        }
        $signtool = Find-SDKTool "signtool"
        if ($null -eq $signtool) { throw "signtool.exe was not found in the Windows SDK" }
        $secureCertificatePassword = if ([string]::IsNullOrEmpty($CertificatePassword)) {
            [System.Security.SecureString]::new()
        }
        else {
            ConvertTo-SecureString -String $CertificatePassword -AsPlainText -Force
        }
        $pfxData = Get-PfxData -FilePath $CertificatePath `
            -Password $secureCertificatePassword
        $signingCertificates = @($pfxData.EndEntityCertificates)
        if ($signingCertificates.Count -ne 1 -or
            [string]$signingCertificates[0].Subject -cne $PackagePublisher) {
            throw "Development signing certificate Subject must exactly match Package Publisher"
        }
        $passwordArg = @()
        if (-not [string]::IsNullOrWhiteSpace($CertificatePassword)) {
            $passwordArg = @("/p", $CertificatePassword)
        }
        & $signtool sign /fd SHA256 /f $CertificatePath @passwordArg /tr $TimestampURL /td SHA256 $msixPath
        if ($LASTEXITCODE -ne 0) { throw "signtool sign failed" }
        & $signtool verify /pa $msixPath
        if ($LASTEXITCODE -ne 0) { throw "signtool verify failed" }
        $signed = $true
    }

    [System.IO.Directory]::CreateDirectory($unpackRoot) | Out-Null
    & $makeappx unpack /p $msixPath /d $unpackRoot /o
    if ($LASTEXITCODE -ne 0) { throw "makeappx unpack verification failed" }
    $unpackedManifestPath = Join-Path $unpackRoot "AppxManifest.xml"
    $unpackedBinaryPath = Join-Path $unpackRoot "TraverseBoard.exe"
    if (-not (Test-Path -LiteralPath $unpackedManifestPath -PathType Leaf) -or
        -not (Test-Path -LiteralPath $unpackedBinaryPath -PathType Leaf)) {
        throw "MSIX verification could not recover its manifest and executable"
    }
    $unpackedVisualAssetInventory = @(
        Get-WindowsVisualAssetDirectoryInventory -AssetRoot (Join-Path $unpackRoot "Assets")
    )
    Assert-WindowsVisualAssetInventoriesEqual -Expected $sourceVisualAssetInventory `
        -Actual $unpackedVisualAssetInventory -Label "unpacked MSIX"
    $unpackedPRIPath = Join-Path $unpackRoot "resources.pri"
    $unpackedPRIFiles = @(Get-ChildItem -LiteralPath $unpackRoot -Recurse -File -Filter "*.pri")
    if ($unpackedPRIFiles.Count -ne 1 -or
        -not $unpackedPRIFiles[0].FullName.Equals(
            $unpackedPRIPath, [System.StringComparison]::Ordinal) -or
        (Get-SHA256 $unpackedPRIPath) -cne $stagedPRIHash) {
        throw "Packed MSIX must preserve the exact single root resources.pri"
    }
    $null = Assert-WindowsVisualAssetsPRI -PRIPath $unpackedPRIPath `
        -PackageIdentityName $PackageIdentityName -MakePriPath $makepri `
        -DumpPath (Join-Path $priAuditRoot "unpacked-resources.xml")
    [xml]$unpackedManifest = [System.IO.File]::ReadAllText($unpackedManifestPath)
    $unpackedNamespace = [System.Xml.XmlNamespaceManager]::new($unpackedManifest.NameTable)
    $unpackedNamespace.AddNamespace(
        "f", "http://schemas.microsoft.com/appx/manifest/foundation/windows10")
    $unpackedIdentity = $unpackedManifest.SelectSingleNode(
        "/f:Package/f:Identity", $unpackedNamespace)
    $unpackedPublisherDisplay = $unpackedManifest.SelectSingleNode(
        "/f:Package/f:Properties/f:PublisherDisplayName", $unpackedNamespace)
    if ($null -eq $unpackedIdentity -or $null -eq $unpackedPublisherDisplay -or
        $unpackedIdentity.GetAttribute("Name") -cne $PackageIdentityName -or
        $unpackedIdentity.GetAttribute("Publisher") -cne $PackagePublisher -or
        $unpackedIdentity.GetAttribute("Version") -cne $Version -or
        $unpackedIdentity.GetAttribute("ProcessorArchitecture") -cne $ProcessorArchitecture -or
        [string]$unpackedPublisherDisplay.InnerText -cne $PublisherDisplayName -or
        (Get-SHA256 $unpackedBinaryPath) -cne $binaryHash) {
        throw "Packed MSIX identity or executable does not match its exact input"
    }
    $packagedLaunchers = @(Get-ChildItem -LiteralPath $unpackRoot -Recurse -File -Filter "*.cmd")
    if ($packagedLaunchers.Count -ne 0) {
        throw "MSIX unexpectedly contains a launcher script"
    }

    $msixUploadHash = ""
    if ($isStore) {
        [System.IO.Directory]::CreateDirectory($uploadRoot) | Out-Null
        Copy-Item -LiteralPath $msixPath -Destination (Join-Path $uploadRoot $msixName)
        if (Test-Path -LiteralPath $msixUploadPath -PathType Leaf) {
            Remove-Item -LiteralPath $msixUploadPath -Force
        }
        & go -C $repositoryRoot run ./cmd/releasegen -zip $uploadRoot -out $outputRoot -zip-name $msixUploadName
        if ($LASTEXITCODE -ne 0) { throw "MSIX upload packaging failed" }
        Add-Type -AssemblyName System.IO.Compression.FileSystem
        $uploadArchive = [System.IO.Compression.ZipFile]::OpenRead($msixUploadPath)
        try {
            $uploadEntries = @($uploadArchive.Entries)
            if ($uploadEntries.Count -ne 1 -or
                [string]$uploadEntries[0].FullName -cne $msixName -or
                [int64]$uploadEntries[0].Length -ne (Get-Item -LiteralPath $msixPath).Length -or
                (Get-ZipEntrySHA256 $uploadEntries[0]) -cne (Get-SHA256 $msixPath)) {
                throw "MSIX upload must contain exactly the Store MSIX candidate"
            }
        }
        finally {
            $uploadArchive.Dispose()
        }
        $msixUploadHash = Get-SHA256 $msixUploadPath
    }

    $msixHash = Get-SHA256 $msixPath
    $signature = Get-AuthenticodeSignature -LiteralPath $msixPath
    $manifest = [ordered]@{
        protocol_version = "msix_manifest.v2"
        distribution = if ($isStoreSubmission) {
            "microsoft_store"
        } elseif ($isStoreContractTest) {
            "microsoft_store_contract_test"
        } else {
            "development"
        }
        msix_name = $msixName
        msix_sha256 = $msixHash
        msixupload_name = $msixUploadName
        msixupload_sha256 = $msixUploadHash
        version = $Version
        marketing_version = [string]$releaseMetadata.app_version
        release_revision = [string]$releaseMetadata.revision
        processor_architecture = $ProcessorArchitecture
        package_identity_name = $PackageIdentityName
        package_publisher = $PackagePublisher
        publisher_display_name = $PublisherDisplayName
        binary_name = "TraverseBoard.exe"
        binary_sha256 = $binaryHash
        direct_download_name = $directArtifactName
        direct_download_sha256 = $directArtifactHash
        direct_exe_signing_evidence_name = $directSigningEvidenceName
        direct_exe_signing_evidence_sha256 = $directSigningEvidenceHash
        direct_exe_signing_request_name = $directSigningRequestName
        direct_exe_signing_request_sha256 = $directSigningRequestHash
        direct_exe_signing_handoff_name = $directSigningHandoffName
        direct_exe_signing_handoff_sha256 = $directSigningHandoffHash
        direct_exe_payload_binding_verified = $directPayloadBindingVerified
        binary_matches_direct_download = ($binaryHash -ceq $directArtifactHash)
        release_metadata_sha256 = (Get-SHA256 $releaseMetadataPath)
        signed = $signed
        signature_status = [string]$signature.Status
        contract_test = $isStoreContractTest
        store_submission_candidate = $isStoreSubmission
        microsoft_store_resigning_expected = $isStoreSubmission
        release_gate_complete = $isStoreSubmission
        release_evidence = @($releaseEvidence)
        symbols_included = $false
        restricted_capabilities = @("runFullTrust")
        restricted_capability_justification_required = $isStoreSubmission
        restricted_capability_justification = $runFullTrustJustification
        restricted_capability_justification_document = $storeRunbookRelativePath
        restricted_capability_justification_document_sha256 = (Get-SHA256 $storeRunbookPath)
        installer_kind = "per_user_msix"
        data_home_contract = "external_user_data_home"
        lifecycle_validation_required = $isStoreSubmission
        lifecycle_validation_status = "not_run"
        lifecycle_evidence_sha256 = ""
    }
    $manifestOut = Join-Path $outputRoot "msix-manifest.json"
    [System.IO.File]::WriteAllText(
        $manifestOut,
        (($manifest | ConvertTo-Json -Depth 4) + [System.Environment]::NewLine),
        [System.Text.UTF8Encoding]::new($false))

    Write-Output "msix: $msixPath"
    Write-Output "msix_sha256: $msixHash"
    Write-Output "msix_signed: $signed"
    Write-Output "msix_distribution: $($manifest.distribution)"
    if ($isStore) {
        Write-Output "msixupload: $msixUploadPath"
        Write-Output "msixupload_sha256: $msixUploadHash"
        if ($isStoreContractTest) {
            Write-Output "msix_note: synthetic Store contract test; this upload must never be submitted"
        }
        else {
            Write-Output "msix_note: Partner Center, lifecycle validation, and Microsoft re-signing remain external"
        }
    }
    elseif (-not $signed) {
        Write-Output "msix_note: unsigned development candidate; not a Store or public distribution artifact"
    }
    Write-Output "msix_manifest: $manifestOut"
}
finally {
    foreach ($temporaryRoot in @($staging, $unpackRoot, $uploadRoot, $priAuditRoot)) {
        if (-not [string]::IsNullOrWhiteSpace($temporaryRoot) -and
            (Test-Path -LiteralPath $temporaryRoot)) {
            Remove-Item -LiteralPath $temporaryRoot -Recurse -Force
        }
    }
}
