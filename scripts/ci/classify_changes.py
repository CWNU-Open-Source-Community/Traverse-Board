#!/usr/bin/env python3
"""Classify the small, explicit pull-request documentation fast path."""

from __future__ import annotations

import argparse
import re
import subprocess
import sys
from pathlib import Path
from typing import Sequence


_COMMIT_RE = re.compile(r"[0-9a-f]{40}")

# Keep this list deliberately exact. Documentation that is generated, embedded,
# parsed by tests, or used as release input must take the full path unless it is
# reviewed and added here separately.
_DOC_ONLY_PATHS = frozenset(
    {
        b"CONTRIBUTING.md",
        b"README.en.md",
        b"README.md",
        b"docs/mac-keychain.md",
        b"docs/macos-release.md",
        b"docs/scheduled-jobs-diagnostics.md",
    }
)


class ClassificationError(RuntimeError):
    """The change set could not be classified safely."""


def classify_paths(paths: Sequence[bytes]) -> bool:
    """Return whether every changed path is in the exact documentation allowlist."""
    if not paths:
        raise ClassificationError("the pull request diff is empty")
    return all(path in _DOC_ONLY_PATHS for path in paths)


def _git(repo: Path, *args: str) -> bytes:
    try:
        return subprocess.run(
            ["git", "-C", str(repo), *args],
            check=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        ).stdout
    except (OSError, subprocess.CalledProcessError) as exc:
        detail = ""
        if isinstance(exc, subprocess.CalledProcessError):
            detail = exc.stderr.decode("utf-8", "replace").strip()
        raise ClassificationError(detail or f"git {' '.join(args)} failed") from exc


def changed_paths(repo: Path, base: str, head: str) -> list[bytes]:
    """Read an exact, NUL-delimited diff while exposing both sides of renames."""
    for label, revision in (("base", base), ("head", head)):
        if _COMMIT_RE.fullmatch(revision) is None:
            raise ClassificationError(f"{label} revision is invalid")
        _git(repo, "cat-file", "-e", f"{revision}^{{commit}}")

    output = _git(
        repo,
        "diff",
        "--no-renames",
        "--name-only",
        "-z",
        base,
        head,
        "--",
    )
    if not output:
        raise ClassificationError("the pull request diff is empty")
    if not output.endswith(b"\0"):
        raise ClassificationError("git returned a malformed path list")
    paths = output[:-1].split(b"\0")
    if any(not path or path.startswith(b"/") for path in paths):
        raise ClassificationError("git returned an invalid repository path")
    return paths


def main(argv: Sequence[str] | None = None) -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--event", required=True)
    parser.add_argument("--repo", type=Path, required=True)
    parser.add_argument("--base", default="")
    parser.add_argument("--head", default="")
    args = parser.parse_args(argv)

    # Pushes to main and every non-PR event always take the full test path.
    if args.event != "pull_request":
        print("docs_only=false")
        return 0

    try:
        docs_only = classify_paths(changed_paths(args.repo, args.base, args.head))
    except ClassificationError as exc:
        print(f"change classification failed: {exc}", file=sys.stderr)
        return 1
    print(f"docs_only={str(docs_only).lower()}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
