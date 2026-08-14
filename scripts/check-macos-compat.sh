#!/usr/bin/env bash
# macOS portable .app compatibility checklist.
#
# Mirrors check-windows-compat.ps1: automated checks are green-gated, while
# signing/notarization and the manual macOS matrix keep release_ready=false.
set -euo pipefail

BinaryPath=""
MetadataPath=""
OutputPath=""

usage() {
    echo "usage: $0 -BinaryPath PATH -MetadataPath PATH [-OutputPath PATH]"
    exit 2
}

while [ "$#" -gt 0 ]; do
    case "$1" in
        -BinaryPath) BinaryPath="$2"; shift 2 ;;
        -MetadataPath) MetadataPath="$2"; shift 2 ;;
        -OutputPath) OutputPath="$2"; shift 2 ;;
        *) usage ;;
    esac
done

die() {
    echo "error: $1" >&2
    exit 1
}

if [ "$(uname -s)" != "Darwin" ]; then
    die "macOS compatibility checks require macOS"
fi
if [ -z "$BinaryPath" ] || [ -z "$MetadataPath" ]; then
    die "portable binary and release metadata are required"
fi
binary="$(cd "$(dirname "$BinaryPath")" && pwd -P)/$(basename "$BinaryPath")"
metadataFile="$(cd "$(dirname "$MetadataPath")" && pwd -P)/$(basename "$MetadataPath")"
if [ ! -f "$binary" ] || [ ! -f "$metadataFile" ]; then
    die "portable binary and release metadata must exist"
fi
if ! command -v python3 >/dev/null 2>&1; then
    die "python3 is required to read the release metadata"
fi
if [ -z "$OutputPath" ]; then
    OutputPath="$(dirname "$metadataFile")/macos-compatibility.json"
fi
output="$(cd "$(dirname "$OutputPath")" && pwd -P)/$(basename "$OutputPath")"
bundleDir="$(cd "$(dirname "$binary")/../.." && pwd -P)"

# Mach-O 64-bit little-endian magic: 0xfeedfacf stored as cf fa ed fe.
MAGIC_OK="false"
MACHINE="unknown"
magic="$(od -An -tx1 -N4 "$binary" | tr -d ' 
')"
if [ "$magic" = "cffaedfe" ]; then
    MAGIC_OK="true"
fi
cputype="$(od -An -tx4 -j4 -N4 "$binary" | tr -d ' 
')"
case "$cputype" in
    01000007) MACHINE="amd64" ;;
    0100000c) MACHINE="arm64" ;;
    *) MACHINE="unknown" ;;
esac

CODESIGN_OK="false"
if codesign --verify --strict "$bundleDir" >/dev/null 2>&1; then
    CODESIGN_OK="true"
fi

GO_BUILD_METADATA_OK="false"
if go version -m "$binary" 2>/dev/null | grep -q "cyberagent-workbench" &&
    go version -m "$binary" 2>/dev/null | grep -q -- "-trimpath=true"; then
    GO_BUILD_METADATA_OK="true"
fi

# The deployment target must match the Go 1.25 darwin minimum (macOS 11). The
# build script aligns both the cgo compile flags and the bundle plist to 11.0.
DEPLOYMENT_TARGET="unknown"
minimumOS="$(otool -l "$binary" | awk '/LC_BUILD_VERSION/{seen=1; next} seen && /minos/{print $2; exit}')"
if [ -n "$minimumOS" ]; then
    DEPLOYMENT_TARGET="$minimumOS"
fi

export MACOS_COMPAT_BINARY="$binary"
export MACOS_COMPAT_BUNDLE="$bundleDir"
export MACOS_COMPAT_METADATA="$metadataFile"
export MACOS_COMPAT_OUTPUT="$output"
export MACOS_COMPAT_MAGIC_OK="$MAGIC_OK"
export MACOS_COMPAT_MACHINE="$MACHINE"
export MACOS_COMPAT_CODESIGN_OK="$CODESIGN_OK"
export MACOS_COMPAT_GO_BUILD_METADATA_OK="$GO_BUILD_METADATA_OK"
export MACOS_COMPAT_DEPLOYMENT_TARGET="$DEPLOYMENT_TARGET"

python3 - <<'PYEOF'
import hashlib
import json
import os
import re

binary = os.environ["MACOS_COMPAT_BINARY"]
bundle = os.environ["MACOS_COMPAT_BUNDLE"]
metadata_path = os.environ["MACOS_COMPAT_METADATA"]
output = os.environ["MACOS_COMPAT_OUTPUT"]
machine = os.environ["MACOS_COMPAT_MACHINE"]

with open(metadata_path, encoding="utf-8") as handle:
    metadata = json.load(handle)
package_root = os.path.dirname(metadata_path)
checks = []


def add_check(check_id, status, detail):
    checks.append({"id": check_id, "status": status, "detail": detail})


def sha256(path):
    digest = hashlib.sha256()
    with open(path, "rb") as handle:
        for chunk in iter(lambda: handle.read(65536), b""):
            digest.update(chunk)
    return digest.hexdigest()


binary_hash = sha256(binary)
add_check(
    "macho_magic",
    "pass" if os.environ["MACOS_COMPAT_MAGIC_OK"] == "true" else "fail",
    "binary has the 64-bit little-endian Mach-O magic",
)
add_check(
    "macho_arch",
    "pass" if machine in ("amd64", "arm64") and machine == metadata["target_arch"] else "fail",
    "Mach-O CPU type matches the release target",
)
add_check(
    "macho_deployment_target",
    "pass" if os.environ["MACOS_COMPAT_DEPLOYMENT_TARGET"] == "11.0" else "fail",
    "Mach-O deployment target matches the Go 1.25 macOS 11 minimum",
)
add_check(
    "sha256_binding",
    "pass" if metadata["sha256"] == binary_hash else "fail",
    "binary SHA-256 matches release metadata",
)
add_check(
    "release_identity",
    "pass"
    if metadata["protocol_version"] == "portable_release_metadata.v1"
    and re.match(r"^v[0-9]+.[0-9]+.[0-9]+", metadata["app_version"])
    and re.match(r"^[0-9a-f]{40}$", metadata["revision"])
    and int(metadata["source_date_epoch"]) > 0
    else "fail",
    "version, revision, and source date are pinned",
)
add_check(
    "go_target",
    "pass"
    if metadata["target_os"] == "darwin"
    and metadata["target_arch"] in ("amd64", "arm64")
    and str(metadata["cgo_enabled"]) == "1"
    else "fail",
    "macOS target and CGO mode are recorded",
)
add_check(
    "non_installing_boundary",
    "pass"
    if not metadata["installer_included"]
    and not metadata["registry_writes"]
    and not metadata["startup_task"]
    and not metadata["auto_update_enabled"]
    else "fail",
    "build has no installer, registry, startup-task, or auto-update authority",
)

launcher_name = str(metadata.get("operator_preview_launcher_name", ""))
guide_name = str(metadata.get("local_test_guide_name", ""))
launcher_exists = bool(launcher_name) and os.path.basename(launcher_name) == launcher_name
guide_exists = bool(guide_name) and os.path.basename(guide_name) == guide_name
if launcher_exists:
    launcher_path = os.path.join(package_root, launcher_name)
    launcher_exists = os.path.isfile(launcher_path)
else:
    launcher_path = ""
if guide_exists:
    guide_path = os.path.join(package_root, guide_name)
    guide_exists = os.path.isfile(guide_path)
else:
    guide_path = ""

launcher_hash = sha256(launcher_path) if launcher_exists else ""
guide_hash = sha256(guide_path) if guide_exists else ""
launcher_text = ""
if launcher_exists:
    with open(launcher_path, encoding="utf-8", errors="replace") as handle:
        launcher_text = handle.read()

add_check(
    "operator_preview_package",
    "pass"
    if metadata["operator_preview_included"]
    and launcher_exists
    and metadata.get("operator_preview_launcher_sha256") == launcher_hash
    else "fail",
    "portable package contains the hash-bound safe operator-preview launcher",
)
add_check(
    "operator_preview_safe_flags",
    "pass"
    if "--operator-preview" in launcher_text
    and "--enable-danger-full-access" not in launcher_text
    and "--enable-debug-maximum-access" not in launcher_text
    and "--enable-full-cdp-debug" not in launcher_text
    and "--enable-user-terminal" not in launcher_text
    and "--enable-wake-worker" not in launcher_text
    else "fail",
    "operator-preview launcher does not add high-risk or persistent execution gates",
)
add_check(
    "local_test_guide",
    "pass"
    if guide_exists and metadata.get("local_test_guide_sha256") == guide_hash
    else "fail",
    "portable package contains the hash-bound bilingual local test guide",
)
add_check(
    "default_ui_language",
    "pass" if metadata.get("default_ui_language") == "zh-CN" else "fail",
    "portable package records Chinese as the default interface language",
)
add_check(
    "go_build_metadata",
    "pass" if os.environ["MACOS_COMPAT_GO_BUILD_METADATA_OK"] == "true" and metadata["trimpath"] else "fail",
    "Go module identity and trimpath are embedded",
)
add_check(
    "codesign_signature",
    "pass" if os.environ["MACOS_COMPAT_CODESIGN_OK"] == "true" else "fail",
    "app bundle carries a structurally valid ad-hoc code signature",
)
if metadata["reproducibility_checked"]:
    add_check(
        "consecutive_build_hash",
        "pass" if metadata["reproducible"] else "fail",
        "two consecutive builds produced the same SHA-256",
    )
else:
    add_check(
        "consecutive_build_hash",
        "manual",
        "run build-desktop-darwin.sh -VerifyReproducible before release",
    )
add_check(
    "macos_matrix",
    "manual",
    "verify macOS 10.15+, Retina scaling, notarized distribution, launch, and recovery on a clean machine",
)

failed = [check for check in checks if check["status"] == "fail"]
manual = [check for check in checks if check["status"] == "manual"]
result = {
    "protocol_version": "macos_portable_compatibility.v1",
    "binary_name": os.path.basename(binary),
    "app_bundle_name": metadata.get("app_bundle_name", ""),
    "sha256": binary_hash,
    "machine": machine,
    "ad_hoc_signed": bool(metadata.get("app_bundle_ad_hoc_signed", False)),
    "notarized": bool(metadata.get("app_bundle_notarized", False)),
    "checks": checks,
    "automated_checks_passed": len(failed) == 0,
    "release_ready": len(failed) == 0 and len(manual) == 0,
    "manual_macos_matrix_required": True,
}
with open(output, "w", encoding="utf-8") as handle:
    json.dump(result, handle, ensure_ascii=False, indent=2)
    handle.write("\n")

print("macos_compatibility: %s" % output)
print("macos_automated_checks_passed: %s" % str(result["automated_checks_passed"]).lower())
print("macos_release_ready: %s" % str(result["release_ready"]).lower())
if failed:
    raise SystemExit("macOS portable compatibility checks failed: %s" % ", ".join(c["id"] for c in failed))
PYEOF
