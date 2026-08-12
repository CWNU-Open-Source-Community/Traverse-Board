#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
cargo_home="${CARGO_HOME:-${HOME}/.cargo}"
mkdir -p -- "$cargo_home"
cargo_home="$(cd -- "$cargo_home" && pwd -P)"

# Rust keeps source paths in panic and diagnostic strings. Map both project and
# dependency roots to stable virtual paths so the embedded module is identical
# across checkout locations and build hosts. The encoded form preserves spaces.
separator=$'\x1f'
export CARGO_ENCODED_RUSTFLAGS="--remap-path-prefix=${repo_root}=/workspace${separator}--remap-path-prefix=${cargo_home}=/cargo"
export CARGO_INCREMENTAL=0
unset RUSTFLAGS

rustup target add wasm32-wasip1 --toolchain 1.97.1
(
  cd -- "$repo_root/analyzers"
  cargo +1.97.1 build --locked --target wasm32-wasip1 \
    --package cyberagent-analyzer-fixture --release
)
