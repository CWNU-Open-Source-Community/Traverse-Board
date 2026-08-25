#!/usr/bin/env bash
# Prayu macOS Desktop portable .app build.
#
# Produces an unsigned, ad-hoc-signed development artifact at
# build/desktop/Prayu.app. It intentionally ships no installer, no
# LaunchAgent, no notarization, no auto-update, and no registry-equivalent
# writes. Release metadata records that release_ready remains false until
# signing, notarization, and the manual macOS matrix are complete.
set -euo pipefail

OutputDirectory="build/desktop"
Version="v0.1.0"
SkipFrontend=false
VerifyReproducible=false

usage() {
    echo "usage: $0 [-OutputDirectory DIR] [-Version VERSION] [-SkipFrontend] [-VerifyReproducible]"
    exit 2
}

while [ "$#" -gt 0 ]; do
    case "$1" in
        -OutputDirectory) OutputDirectory="$2"; shift 2 ;;
        -Version) Version="$2"; shift 2 ;;
        -SkipFrontend) SkipFrontend=true; shift ;;
        -VerifyReproducible) VerifyReproducible=true; shift ;;
        *) usage ;;
    esac
done

die() {
    echo "error: $1" >&2
    exit 1
}

if [ "$(uname -s)" != "Darwin" ]; then
    die "Desktop portable build currently supports only macOS"
fi

repositoryRoot="$(cd "$(dirname "$0")/.." && pwd -P)"
cd "$repositoryRoot"
repositoryFull="$(pwd -P)"

mkdir -p "$OutputDirectory"
outputRoot="$(cd "$OutputDirectory" && pwd -P)"
case "$outputRoot" in
    "$repositoryFull"|"$repositoryFull"/*) ;;
    *) die "Desktop output directory must remain inside the repository" ;;
esac

if ! printf '%s' "$Version" | grep -Eq '^v[0-9]+.[0-9]+.[0-9]+([-+][0-9A-Za-z.-]+)?$'; then
    die "Desktop release version is invalid"
fi

appName="Prayu"
bundleDir="$outputRoot/$appName.app"
binaryPath="$bundleDir/Contents/MacOS/cyberagent-desktop"
reproBinaryPath="$outputRoot/cyberagent-desktop.repro"
metadataPath="$outputRoot/release-metadata.json"
launcherName="Start-Prayu-Operator-Preview.command"
guideName="LOCAL-TEST-GUIDE.txt"
plistSourcePath="packaging/macos/Info.plist"
iconSourcePath="packaging/macos/TraverseBoard.icns"
launcherSourcePath="packaging/macos/$launcherName"
guideSourcePath="packaging/macos/$guideName"
launcherPath="$outputRoot/$launcherName"
guidePath="$outputRoot/$guideName"
plistPath="$bundleDir/Contents/Info.plist"
iconPath="$bundleDir/Contents/Resources/TraverseBoard.icns"

for required in "$plistSourcePath" "$iconSourcePath" "$launcherSourcePath" "$guideSourcePath"; do
    if [ ! -f "$required" ]; then
        die "Desktop macOS packaging assets are required: $required"
    fi
done

if ! command -v codesign >/dev/null 2>&1; then
    die "codesign is required (install the Xcode command line tools)"
fi

if [ "$SkipFrontend" != true ]; then
    # The frontend test environment is pinned to the Node.js 24 baseline used
    # by CI and documented in the README. Newer local Node versions (for
    # example v26) currently break the jsdom test environment, so fail fast
    # with an actionable message instead of dumping unrelated test failures.
    nodeVersion="$(node -v 2>/dev/null || true)"
    nodeMajor="$(printf '%s' "$nodeVersion" | sed -E 's/^v([0-9]+).*/\1/')"
    if [ "$nodeMajor" != "24" ]; then
        die "Desktop frontend checks require the pinned Node.js 24 baseline (detected: ${nodeVersion:-none}). Switch with nvm (nvm use 24), or pass -SkipFrontend when web/dist is already built."
    fi
    (
        cd web
        npm ci
        npm run check:api
        npm test
        npm run build
    )
fi

go test ./internal/desktop ./internal/webui -count=1
go test -tags "desktop" ./cmd/cyberagent-desktop -count=1

revision="$(git rev-parse HEAD)"
if [ -z "$revision" ] || ! printf '%s' "$revision" | grep -Eq '^[0-9a-f]{40}$'; then
    die "Git release revision is unavailable"
fi
sourceDateEpoch="$(git show -s --format=%ct HEAD)"
if [ -z "$sourceDateEpoch" ] || ! printf '%s' "$sourceDateEpoch" | grep -Eq '^[1-9][0-9]*$'; then
    die "Git source date epoch is unavailable"
fi
if [ -n "$(git status --porcelain)" ]; then
    modified="true"
else
    modified="false"
fi
cgoEnabled="$(go env CGO_ENABLED)"
if [ "$cgoEnabled" != "1" ]; then
    die "Desktop macOS build requires CGO_ENABLED=1 (go-sqlite3 and Wails need cgo)"
fi
goVersion="$(go env GOVERSION)"
if ! printf '%s' "$goVersion" | grep -Eq '^go[0-9]+.[0-9]+'; then
    die "Go version build metadata is invalid"
fi
targetOS="$(go env GOOS)"
if [ "$targetOS" != "darwin" ]; then
    die "Desktop portable build requires GOOS=darwin"
fi
targetArch="$(go env GOARCH)"
case "$targetArch" in
    amd64|arm64) ;;
    *) die "Go macOS build architecture is invalid" ;;
esac

mkdir -p "$bundleDir/Contents/MacOS" "$bundleDir/Contents/Resources"
cp "$launcherSourcePath" "$launcherPath"
cp "$guideSourcePath" "$guidePath"
cp "$iconSourcePath" "$iconPath"
chmod +x "$launcherPath"
sed "s/@VERSION@/$Version/g" "$plistSourcePath" > "$plistPath"

ldflags="-s -w -X=cyberagent-workbench/internal/buildinfo.Version=$Version -X=cyberagent-workbench/internal/buildinfo.Revision=$revision -X=cyberagent-workbench/internal/buildinfo.SourceDateEpoch=$sourceDateEpoch -X=cyberagent-workbench/internal/buildinfo.Modified=$modified -X=cyberagent-workbench/internal/buildinfo.CGOEnabled=$cgoEnabled"

previousSourceDateEpoch="${SOURCE_DATE_EPOCH:-}"
previousCgoLdflags="${CGO_LDFLAGS:-}"
previousCgoCflags="${CGO_CFLAGS:-}"
# Mirror the wails build command's darwin link flags: WailsContext.m uses
# UTType (macOS 11+). The deployment target matches the Go 1.25 darwin
# minimum and the bundle's LSMinimumSystemVersion. Plain go build does not
# add these itself. CGO_CFLAGS keeps the clang objects on the same 11.0
# target; without it the macOS 26 SDK compiles them at 26.0 and the external
# linker floods the build with "built for newer macOS version" warnings.
export CGO_CFLAGS="-g -O2 -mmacosx-version-min=11.0${previousCgoCflags:+ $previousCgoCflags}"
export CGO_LDFLAGS="-framework UniformTypeIdentifiers -mmacosx-version-min=11.0${previousCgoLdflags:+ $previousCgoLdflags}"
export SOURCE_DATE_EPOCH="$sourceDateEpoch"
trap 'export SOURCE_DATE_EPOCH="$previousSourceDateEpoch"; export CGO_LDFLAGS="$previousCgoLdflags"; export CGO_CFLAGS="$previousCgoCflags"' EXIT
# The previous run codesigned the final binary in place. go build over an
# existing signed Mach-O reuses its signature layout and produces different
# bytes than a fresh link, so both outputs are removed before building to keep
# the consecutive-build comparison exact.
rm -f "$binaryPath" "$reproBinaryPath"
go build -tags "desktop,production" -trimpath -ldflags "$ldflags"     -o "$binaryPath" ./cmd/cyberagent-desktop
reproducible="false"
if [ "$VerifyReproducible" = true ]; then
    go build -tags "desktop,production" -trimpath -ldflags "$ldflags"         -o "$reproBinaryPath" ./cmd/cyberagent-desktop
    firstHash="$(shasum -a 256 "$binaryPath" | awk '{print $1}')"
    secondHash="$(shasum -a 256 "$reproBinaryPath" | awk '{print $1}')"
    if [ "$firstHash" != "$secondHash" ]; then
        die "Desktop reproducibility check failed: consecutive binary hashes differ (first=$firstHash second=$secondHash). Keeping $reproBinaryPath next to $binaryPath for diagnosis."
    fi
    rm -f "$reproBinaryPath"
    reproducible="true"
fi

# Ad-hoc signing makes the local artifact launchable on Apple Silicon
# without asserting any developer identity. It is not a distribution
# signature; release_ready stays false.
codesign --force --sign - --timestamp=none "$bundleDir"

hash="$(shasum -a 256 "$binaryPath" | awk '{print $1}')"
launcherHash="$(shasum -a 256 "$launcherPath" | awk '{print $1}')"
guideHash="$(shasum -a 256 "$guidePath" | awk '{print $1}')"

cat > "$metadataPath" <<JSONEOF
{
  "protocol_version": "portable_release_metadata.v1",
  "app_version": "$Version",
  "revision": "$revision",
  "source_date_epoch": $sourceDateEpoch,
  "modified": $modified,
  "go_version": "$goVersion",
  "target_os": "$targetOS",
  "target_arch": "$targetArch",
  "cgo_enabled": $cgoEnabled,
  "trimpath": true,
  "binary_name": "cyberagent-desktop",
  "app_bundle_name": "$appName.app",
  "app_bundle_ad_hoc_signed": true,
  "app_bundle_notarized": false,
  "sha256": "$hash",
  "operator_preview_included": true,
  "operator_preview_launcher_name": "$launcherName",
  "operator_preview_launcher_sha256": "$launcherHash",
  "local_test_guide_name": "$guideName",
  "local_test_guide_sha256": "$guideHash",
  "default_ui_language": "zh-CN",
  "reproducibility_checked": $VerifyReproducible,
  "reproducible": $reproducible,
  "installer_included": false,
  "registry_writes": false,
  "startup_task": false,
  "auto_update_enabled": false,
  "manual_macos_matrix_required": true
}
JSONEOF

scripts/check-macos-compat.sh -BinaryPath "$binaryPath" -MetadataPath "$metadataPath"

echo "desktop_app_bundle: $bundleDir"
echo "desktop_binary: $binaryPath"
echo "desktop_sha256: $(shasum -a 256 "$binaryPath" | awk '{print $1}')"
echo "release_metadata: $metadataPath"
echo "operator_preview_launcher: $launcherPath"
echo "local_test_guide: $guidePath"
echo "desktop_reproducible: $(grep -o '"reproducible": [a-z]*' "$metadataPath" | cut -d' ' -f2)"
echo "desktop_installer_included: false"
echo "desktop_ad_hoc_signed: true"
echo "desktop_notarized: false"
echo "desktop_profile_control_default: true"
echo "desktop_safe_view_flag: --safe-view"
