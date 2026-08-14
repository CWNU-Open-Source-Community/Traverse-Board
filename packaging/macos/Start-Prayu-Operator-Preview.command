#!/bin/sh
# Prayu macOS operator-preview launcher.
#
# Enables only the safe operator capability bundle for local product testing.
# It never adds danger-full-access, maximum Debug access, Full CDP, the
# background wake worker, or an Agent-controlled persistent terminal.
set -eu
script_dir="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)"
exec "$script_dir/Prayu.app/Contents/MacOS/cyberagent-desktop" --operator-preview
