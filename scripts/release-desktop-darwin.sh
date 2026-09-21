#!/usr/bin/env bash
# Native preview archive and macos_release_archive.v1 sidecars.
set -euo pipefail
cd "$(dirname "$0")/.."
version="${1:?release version is required}"
revision="${2:?exact source revision is required}"
arch="${3:?native architecture is required}"
[ "$#" -eq 3 ] || { echo "expected VERSION REVISION ARCH" >&2; exit 2; }
[ "$(uname -s)" = Darwin ] || { echo "macOS host required" >&2; exit 1; }
case "$(uname -m):$arch:$(go env GOARCH)" in
    arm64:arm64:arm64|x86_64:amd64:amd64) ;;
    *) echo "runner, requested and Go native architectures differ" >&2; exit 1 ;;
esac
[ "$(git rev-parse HEAD)" = "$revision" ] || { echo "source revision differs" >&2; exit 1; }
[ -z "$(git status --porcelain)" ] || { echo "release build requires a clean checkout" >&2; exit 1; }
./scripts/build-desktop-darwin.sh -Version "$version" -SkipFrontend -VerifyReproducible
go run ./cmd/releasegen --out build/desktop --version "$version" --tags desktop,production
python3 scripts/macos-release.py package --source build/desktop --directory build/macos-public \
    --version "$version" --revision "$revision" --arch "$arch"
