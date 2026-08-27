# Exact Microsoft Store visual-asset contract shared by package and verification.
# Keep the physical filenames literal: manifest resource qualifiers are encoded in
# the names, and a missing, renamed, or additional file is a release failure.

function New-WindowsVisualAssetSpec {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][int]$Width,
        [Parameter(Mandatory = $true)][int]$Height
    )
    return [pscustomobject][ordered]@{
        name = $Name
        width = $Width
        height = $Height
    }
}

function Get-WindowsVisualAssetSpecs {
    return @(
        New-WindowsVisualAssetSpec -Name 'StoreLogo.png' -Width 50 -Height 50
        New-WindowsVisualAssetSpec -Name 'Square150x150Logo.png' -Width 150 -Height 150
        New-WindowsVisualAssetSpec -Name 'Square44x44Logo.png' -Width 44 -Height 44
        New-WindowsVisualAssetSpec -Name 'Square44x44Logo.scale-200.png' -Width 88 -Height 88
        New-WindowsVisualAssetSpec -Name 'Square44x44Logo.scale-400.png' -Width 176 -Height 176
        New-WindowsVisualAssetSpec -Name 'Square150x150Logo.scale-200.png' -Width 300 -Height 300
        New-WindowsVisualAssetSpec -Name 'Square150x150Logo.scale-400.png' -Width 600 -Height 600
        New-WindowsVisualAssetSpec -Name 'StoreLogo.scale-125.png' -Width 63 -Height 63
        New-WindowsVisualAssetSpec -Name 'StoreLogo.scale-150.png' -Width 75 -Height 75
        New-WindowsVisualAssetSpec -Name 'StoreLogo.scale-200.png' -Width 100 -Height 100
        New-WindowsVisualAssetSpec -Name 'StoreLogo.scale-400.png' -Width 200 -Height 200
        New-WindowsVisualAssetSpec -Name 'Square44x44Logo.targetsize-16.png' -Width 16 -Height 16
        New-WindowsVisualAssetSpec -Name 'Square44x44Logo.targetsize-16_altform-unplated.png' -Width 16 -Height 16
        New-WindowsVisualAssetSpec -Name 'Square44x44Logo.targetsize-16_altform-lightunplated.png' -Width 16 -Height 16
        New-WindowsVisualAssetSpec -Name 'Square44x44Logo.targetsize-20.png' -Width 20 -Height 20
        New-WindowsVisualAssetSpec -Name 'Square44x44Logo.targetsize-20_altform-unplated.png' -Width 20 -Height 20
        New-WindowsVisualAssetSpec -Name 'Square44x44Logo.targetsize-20_altform-lightunplated.png' -Width 20 -Height 20
        New-WindowsVisualAssetSpec -Name 'Square44x44Logo.targetsize-24.png' -Width 24 -Height 24
        New-WindowsVisualAssetSpec -Name 'Square44x44Logo.targetsize-24_altform-unplated.png' -Width 24 -Height 24
        New-WindowsVisualAssetSpec -Name 'Square44x44Logo.targetsize-24_altform-lightunplated.png' -Width 24 -Height 24
        New-WindowsVisualAssetSpec -Name 'Square44x44Logo.targetsize-30.png' -Width 30 -Height 30
        New-WindowsVisualAssetSpec -Name 'Square44x44Logo.targetsize-30_altform-unplated.png' -Width 30 -Height 30
        New-WindowsVisualAssetSpec -Name 'Square44x44Logo.targetsize-30_altform-lightunplated.png' -Width 30 -Height 30
        New-WindowsVisualAssetSpec -Name 'Square44x44Logo.targetsize-32.png' -Width 32 -Height 32
        New-WindowsVisualAssetSpec -Name 'Square44x44Logo.targetsize-32_altform-unplated.png' -Width 32 -Height 32
        New-WindowsVisualAssetSpec -Name 'Square44x44Logo.targetsize-32_altform-lightunplated.png' -Width 32 -Height 32
        New-WindowsVisualAssetSpec -Name 'Square44x44Logo.targetsize-36.png' -Width 36 -Height 36
        New-WindowsVisualAssetSpec -Name 'Square44x44Logo.targetsize-36_altform-unplated.png' -Width 36 -Height 36
        New-WindowsVisualAssetSpec -Name 'Square44x44Logo.targetsize-36_altform-lightunplated.png' -Width 36 -Height 36
        New-WindowsVisualAssetSpec -Name 'Square44x44Logo.targetsize-40.png' -Width 40 -Height 40
        New-WindowsVisualAssetSpec -Name 'Square44x44Logo.targetsize-40_altform-unplated.png' -Width 40 -Height 40
        New-WindowsVisualAssetSpec -Name 'Square44x44Logo.targetsize-40_altform-lightunplated.png' -Width 40 -Height 40
        New-WindowsVisualAssetSpec -Name 'Square44x44Logo.targetsize-48.png' -Width 48 -Height 48
        New-WindowsVisualAssetSpec -Name 'Square44x44Logo.targetsize-48_altform-unplated.png' -Width 48 -Height 48
        New-WindowsVisualAssetSpec -Name 'Square44x44Logo.targetsize-48_altform-lightunplated.png' -Width 48 -Height 48
        New-WindowsVisualAssetSpec -Name 'Square44x44Logo.targetsize-60.png' -Width 60 -Height 60
        New-WindowsVisualAssetSpec -Name 'Square44x44Logo.targetsize-60_altform-unplated.png' -Width 60 -Height 60
        New-WindowsVisualAssetSpec -Name 'Square44x44Logo.targetsize-60_altform-lightunplated.png' -Width 60 -Height 60
        New-WindowsVisualAssetSpec -Name 'Square44x44Logo.targetsize-64.png' -Width 64 -Height 64
        New-WindowsVisualAssetSpec -Name 'Square44x44Logo.targetsize-64_altform-unplated.png' -Width 64 -Height 64
        New-WindowsVisualAssetSpec -Name 'Square44x44Logo.targetsize-64_altform-lightunplated.png' -Width 64 -Height 64
        New-WindowsVisualAssetSpec -Name 'Square44x44Logo.targetsize-72.png' -Width 72 -Height 72
        New-WindowsVisualAssetSpec -Name 'Square44x44Logo.targetsize-72_altform-unplated.png' -Width 72 -Height 72
        New-WindowsVisualAssetSpec -Name 'Square44x44Logo.targetsize-72_altform-lightunplated.png' -Width 72 -Height 72
        New-WindowsVisualAssetSpec -Name 'Square44x44Logo.targetsize-80.png' -Width 80 -Height 80
        New-WindowsVisualAssetSpec -Name 'Square44x44Logo.targetsize-80_altform-unplated.png' -Width 80 -Height 80
        New-WindowsVisualAssetSpec -Name 'Square44x44Logo.targetsize-80_altform-lightunplated.png' -Width 80 -Height 80
        New-WindowsVisualAssetSpec -Name 'Square44x44Logo.targetsize-96.png' -Width 96 -Height 96
        New-WindowsVisualAssetSpec -Name 'Square44x44Logo.targetsize-96_altform-unplated.png' -Width 96 -Height 96
        New-WindowsVisualAssetSpec -Name 'Square44x44Logo.targetsize-96_altform-lightunplated.png' -Width 96 -Height 96
        New-WindowsVisualAssetSpec -Name 'Square44x44Logo.targetsize-256.png' -Width 256 -Height 256
        New-WindowsVisualAssetSpec -Name 'Square44x44Logo.targetsize-256_altform-unplated.png' -Width 256 -Height 256
        New-WindowsVisualAssetSpec -Name 'Square44x44Logo.targetsize-256_altform-lightunplated.png' -Width 256 -Height 256
    )
}

function Get-WindowsVisualAssetBytesSHA256 {
    param([Parameter(Mandatory = $true)][byte[]]$Bytes)
    $sha = [System.Security.Cryptography.SHA256]::Create()
    try {
        return ([System.BitConverter]::ToString($sha.ComputeHash($Bytes))).Replace(
            '-', '').ToLowerInvariant()
    }
    finally {
        $sha.Dispose()
    }
}

function Get-WindowsVisualAssetPNGMetadata {
    param(
        [Parameter(Mandatory = $true)][byte[]]$Bytes,
        [Parameter(Mandatory = $true)][object]$Spec,
        [Parameter(Mandatory = $true)][string]$Label
    )
    if ($Bytes.LongLength -ge 204800) {
        throw "$Label must be smaller than 204800 bytes for Windows App Certification"
    }
    $signature = [byte[]](137, 80, 78, 71, 13, 10, 26, 10)
    if ($Bytes.Length -lt 33) {
        throw "$Label is not a complete PNG"
    }
    for ($index = 0; $index -lt $signature.Length; $index++) {
        if ($Bytes[$index] -ne $signature[$index]) {
            throw "$Label does not have the PNG signature"
        }
    }
    $ihdrLength = ([uint32]$Bytes[8] -shl 24) -bor
        ([uint32]$Bytes[9] -shl 16) -bor ([uint32]$Bytes[10] -shl 8) -bor
        [uint32]$Bytes[11]
    $ihdrName = [System.Text.Encoding]::ASCII.GetString($Bytes, 12, 4)
    if ($ihdrLength -ne 13 -or $ihdrName -cne 'IHDR') {
        throw "$Label does not begin with a canonical PNG IHDR chunk"
    }
    $width = ([uint32]$Bytes[16] -shl 24) -bor
        ([uint32]$Bytes[17] -shl 16) -bor ([uint32]$Bytes[18] -shl 8) -bor
        [uint32]$Bytes[19]
    $height = ([uint32]$Bytes[20] -shl 24) -bor
        ([uint32]$Bytes[21] -shl 16) -bor ([uint32]$Bytes[22] -shl 8) -bor
        [uint32]$Bytes[23]
    if ($width -ne [uint32]$Spec.width -or $height -ne [uint32]$Spec.height) {
        throw "$Label dimensions are $width x $height; expected $($Spec.width) x $($Spec.height)"
    }
    if ($Bytes[26] -ne 0 -or $Bytes[27] -ne 0 -or $Bytes[28] -gt 1) {
        throw "$Label uses unsupported PNG compression, filtering, or interlacing metadata"
    }
    return [pscustomobject][ordered]@{
        name = [string]$Spec.name
        width = [int]$width
        height = [int]$height
        length = [int64]$Bytes.LongLength
        sha256 = Get-WindowsVisualAssetBytesSHA256 -Bytes $Bytes
    }
}

function Get-WindowsVisualAssetDirectoryInventory {
    param([Parameter(Mandatory = $true)][string]$AssetRoot)
    if (-not (Test-Path -LiteralPath $AssetRoot -PathType Container)) {
        throw "Windows visual-asset directory is missing: $AssetRoot"
    }
    $specs = @(Get-WindowsVisualAssetSpecs)
    if ($specs.Count -ne 53) {
        throw "Windows visual-asset contract must contain exactly 53 PNG files"
    }
    $children = @(Get-ChildItem -LiteralPath $AssetRoot -Force)
    if ($children.Count -ne $specs.Count) {
        throw "Windows visual-asset directory must contain exactly 53 files"
    }
    $actual = [System.Collections.Generic.Dictionary[string, object]]::new(
        [System.StringComparer]::Ordinal)
    foreach ($child in $children) {
        if ($child.PSIsContainer -or $actual.ContainsKey([string]$child.Name)) {
            throw "Windows visual-asset directory contains a directory or duplicate name: $($child.Name)"
        }
        $actual.Add([string]$child.Name, $child)
    }
    $inventory = [System.Collections.Generic.List[object]]::new()
    foreach ($spec in $specs) {
        if (-not $actual.ContainsKey([string]$spec.name)) {
            throw "Windows visual asset is missing or has incorrect case: $($spec.name)"
        }
        $file = $actual[[string]$spec.name]
        $bytes = [System.IO.File]::ReadAllBytes([string]$file.FullName)
        $inventory.Add((Get-WindowsVisualAssetPNGMetadata -Bytes $bytes -Spec $spec `
            -Label ([string]$file.FullName)))
    }
    return @($inventory)
}

function Get-WindowsVisualAssetArchiveInventory {
    param([Parameter(Mandatory = $true)][object]$Archive)
    $specs = @(Get-WindowsVisualAssetSpecs)
    $entries = @($Archive.Entries | Where-Object {
            ([string]$_.FullName).StartsWith(
                'Assets/', [System.StringComparison]::OrdinalIgnoreCase) -or
            ([string]$_.FullName).StartsWith(
                'Assets\', [System.StringComparison]::OrdinalIgnoreCase)
        })
    if ($entries.Count -ne $specs.Count) {
        throw "MSIX must contain exactly 53 files under Assets/"
    }
    $actual = [System.Collections.Generic.Dictionary[string, object]]::new(
        [System.StringComparer]::Ordinal)
    foreach ($entry in $entries) {
        $fullName = [string]$entry.FullName
        if (-not $fullName.StartsWith('Assets/', [System.StringComparison]::Ordinal) -or
            $fullName.Substring('Assets/'.Length).Contains('/') -or
            $fullName.Substring('Assets/'.Length).Contains('\')) {
            throw "MSIX contains a non-canonical visual-asset path: $fullName"
        }
        $name = $fullName.Substring('Assets/'.Length)
        if ($actual.ContainsKey($name)) {
            throw "MSIX contains a duplicate visual asset: $name"
        }
        $actual.Add($name, $entry)
    }
    $inventory = [System.Collections.Generic.List[object]]::new()
    foreach ($spec in $specs) {
        if (-not $actual.ContainsKey([string]$spec.name)) {
            throw "MSIX visual asset is missing or has incorrect case: $($spec.name)"
        }
        $entry = $actual[[string]$spec.name]
        if ([int64]$entry.Length -ge 204800) {
            throw "MSIX visual asset is too large: $($spec.name)"
        }
        $entryStream = $entry.Open()
        $memory = [System.IO.MemoryStream]::new()
        try {
            $entryStream.CopyTo($memory)
            $bytes = $memory.ToArray()
        }
        finally {
            $memory.Dispose()
            $entryStream.Dispose()
        }
        $inventory.Add((Get-WindowsVisualAssetPNGMetadata -Bytes $bytes -Spec $spec `
            -Label "MSIX Assets/$($spec.name)"))
    }
    return @($inventory)
}

function Assert-WindowsVisualAssetInventoriesEqual {
    param(
        [Parameter(Mandatory = $true)][object[]]$Expected,
        [Parameter(Mandatory = $true)][object[]]$Actual,
        [Parameter(Mandatory = $true)][string]$Label
    )
    if ($Expected.Count -ne 53 -or $Actual.Count -ne 53) {
        throw "$Label visual-asset inventory must contain exactly 53 files"
    }
    $actualByName = [System.Collections.Generic.Dictionary[string, object]]::new(
        [System.StringComparer]::Ordinal)
    foreach ($item in $Actual) {
        if ($actualByName.ContainsKey([string]$item.name)) {
            throw "$Label visual-asset inventory contains a duplicate: $($item.name)"
        }
        $actualByName.Add([string]$item.name, $item)
    }
    foreach ($expectedItem in $Expected) {
        $name = [string]$expectedItem.name
        if (-not $actualByName.ContainsKey($name)) {
            throw "$Label visual-asset inventory is missing: $name"
        }
        $actualItem = $actualByName[$name]
        if ([int]$actualItem.width -ne [int]$expectedItem.width -or
            [int]$actualItem.height -ne [int]$expectedItem.height -or
            [int64]$actualItem.length -ne [int64]$expectedItem.length -or
            [string]$actualItem.sha256 -cne [string]$expectedItem.sha256) {
            throw "$Label visual asset differs from its exact source: $name"
        }
    }
}

function Find-WindowsVisualAssetSDKTool {
    param([Parameter(Mandatory = $true)][string]$Name)
    $kitRoot = 'C:\Program Files (x86)\Windows Kits\10\bin'
    if (-not (Test-Path -LiteralPath $kitRoot -PathType Container)) {
        return $null
    }
    foreach ($sdkVersion in @(Get-ChildItem -LiteralPath $kitRoot -Directory |
            Sort-Object Name -Descending)) {
        $candidate = Join-Path $sdkVersion.FullName "x64\$Name.exe"
        if (Test-Path -LiteralPath $candidate -PathType Leaf) {
            return $candidate
        }
    }
    return $null
}

function Assert-WindowsVisualAssetsPRI {
    param(
        [Parameter(Mandatory = $true)][string]$PRIPath,
        [Parameter(Mandatory = $true)][string]$PackageIdentityName,
        [Parameter(Mandatory = $true)][string]$MakePriPath,
        [Parameter(Mandatory = $true)][string]$DumpPath
    )
    if (-not (Test-Path -LiteralPath $PRIPath -PathType Leaf) -or
        (Get-Item -LiteralPath $PRIPath).Length -le 0) {
        throw "resources.pri is missing or empty: $PRIPath"
    }
    if (-not (Test-Path -LiteralPath $MakePriPath -PathType Leaf)) {
        throw "MakePri is required to validate resources.pri"
    }
    & $MakePriPath dump /if $PRIPath /of $DumpPath /o | Out-Null
    if ($LASTEXITCODE -ne 0 -or -not (Test-Path -LiteralPath $DumpPath -PathType Leaf)) {
        throw "MakePri could not dump resources.pri"
    }
    $dumpText = [System.IO.File]::ReadAllText($DumpPath)
    try {
        [xml]$dumpDocument = $dumpText
    }
    catch {
        throw "MakePri emitted an invalid resources.pri dump: $($_.Exception.Message)"
    }
    $primaryMaps = @($dumpDocument.SelectNodes(
            "//*[local-name()='ResourceMap' and @primary='true']"))
    if ($primaryMaps.Count -ne 1 -or
        [string]$primaryMaps[0].GetAttribute('name') -cne $PackageIdentityName) {
        throw "resources.pri primary map does not match the exact package identity"
    }
    if ($dumpText.IndexOf('ReverseMap', [System.StringComparison]::OrdinalIgnoreCase) -ge 0) {
        throw "resources.pri must not contain a reverse map"
    }
    $indexedAssets = [System.Collections.Generic.Dictionary[string, bool]]::new(
        [System.StringComparer]::Ordinal)
    foreach ($valueNode in @($dumpDocument.SelectNodes("//*[local-name()='Value']"))) {
        $physicalPath = ([string]$valueNode.InnerText).Replace('\', '/')
        if (-not $physicalPath.StartsWith(
                'Assets/', [System.StringComparison]::OrdinalIgnoreCase)) {
            continue
        }
        if (-not $physicalPath.StartsWith(
                'Assets/', [System.StringComparison]::Ordinal) -or
            $physicalPath.Substring('Assets/'.Length).Contains('/')) {
            throw "resources.pri contains a non-canonical visual-asset path: $physicalPath"
        }
        if ($indexedAssets.ContainsKey($physicalPath)) {
            throw "resources.pri indexes a visual asset more than once: $physicalPath"
        }
        $indexedAssets.Add($physicalPath, $true)
    }
    if ($indexedAssets.Count -ne 53) {
        throw "resources.pri must index exactly 53 visual-asset paths"
    }
    foreach ($spec in @(Get-WindowsVisualAssetSpecs)) {
        $physicalPath = "Assets/$($spec.name)"
        if (-not $indexedAssets.ContainsKey($physicalPath)) {
            throw "resources.pri does not index the exact visual asset: $physicalPath"
        }
    }
    return (Get-FileHash -Algorithm SHA256 -LiteralPath $PRIPath).Hash.ToLowerInvariant()
}
