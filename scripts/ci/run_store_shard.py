#!/usr/bin/env python3
"""Run a complete, disjoint slice of the compiled Store test inventory.

Each runner still uses real databases and real migrations. Partitioning changes
where tests run, never the test bodies or the existing 60 minute deadline.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import re
import subprocess
import sys
import time
from pathlib import Path

PACKAGE = "./internal/store"
IMPORT_PATH = "cyberagent-workbench/internal/store"
TEST_NAME = re.compile(r"(?:Test|Example|Fuzz)[A-Za-z0-9_]*\Z", re.ASCII)


def compiled_inventory(output: str) -> list[str]:
    names = []
    for line in output.splitlines():
        if not line.strip():
            continue
        if TEST_NAME.fullmatch(line):
            names.append(line)
        elif re.match(r"^(?:ok|\?)\s+" + re.escape(IMPORT_PATH) + r"(?:\s|$)", line):
            continue
        else:
            raise ValueError(f"unexpected Go inventory output: {line!r}")
    if not names or len(names) != len(set(names)):
        raise ValueError("compiled inventory is empty or contains duplicate test names")
    return sorted(names)


def partition(names: list[str], count: int) -> list[list[str]]:
    if not 1 <= count <= 32 or len(names) < count:
        raise ValueError("shard count must be 1..32 with at least one test per shard")
    if len(names) != len(set(names)) or any(not TEST_NAME.fullmatch(n) for n in names):
        raise ValueError("invalid or duplicate test name")
    ordered = sorted(names)
    shards = [ordered[index::count] for index in range(count)]
    flattened = [name for shard in shards for name in shard]
    if len(flattened) != len(ordered) or sorted(flattened) != ordered:
        raise ValueError("test partition is not complete and disjoint")
    return shards


def command(selected: list[str], *, full_suite: bool = False) -> list[str]:
    if not selected or any(not TEST_NAME.fullmatch(n) for n in selected):
        raise ValueError("cannot run an empty or invalid shard")
    arguments = ["go", "test", "-json", "-count=1", "-timeout=60m"]
    # A single shard is the entire package. No filter is needed, and expanding
    # every name can exceed Windows' command-line limit. Completion is still
    # checked against every name in the compiled inventory.
    if not full_suite:
        expression = "^(" + "|".join(selected) + ")$"
        arguments.extend(["-run", expression])
    return arguments + [PACKAGE]


def verify_completion(selected: list[str], events: list[dict], return_code: int) -> dict:
    terminals: dict[str, list[str]] = {}
    packages = []
    for event in events:
        if event.get("Package") != IMPORT_PATH:
            continue
        action = event.get("Action")
        name = event.get("Test")
        if action not in ("pass", "skip", "fail"):
            continue
        if not name:
            packages.append(action)
        elif "/" not in name:
            terminals.setdefault(name, []).append(action)
    missing = sorted(set(selected) - terminals.keys())
    unexpected = sorted(terminals.keys() - set(selected))
    duplicate = sorted(name for name, outcomes in terminals.items() if len(outcomes) != 1)
    failed = sorted(name for name, outcomes in terminals.items() if "fail" in outcomes)
    success = (return_code == 0 and packages == ["pass"] and not missing
               and not unexpected and not duplicate and not failed)
    return {
        "success": success, "return_code": return_code,
        "selected": len(selected), "completed": len(terminals),
        "passed": sum(outcomes == ["pass"] for outcomes in terminals.values()),
        "skipped": sorted(name for name, outcomes in terminals.items() if outcomes == ["skip"]),
        "failed": failed, "missing": missing, "unexpected": unexpected,
        "duplicate": duplicate, "package_outcomes": packages,
    }


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--index", type=int, required=True)
    parser.add_argument("--count", type=int, required=True)
    parser.add_argument("--output-dir", type=Path, required=True)
    args = parser.parse_args()
    if not 0 <= args.index < args.count <= 32:
        parser.error("require 0 <= index < count <= 32")
    args.output_dir.mkdir(parents=True, exist_ok=True)
    started = time.monotonic()
    listing = subprocess.run(
        ["go", "test", PACKAGE, "-list", "^(Test|Example|Fuzz)"],
        text=True, encoding="utf-8", capture_output=True, check=False,
    )
    (args.output_dir / "inventory.stdout").write_text(listing.stdout, encoding="utf-8")
    (args.output_dir / "inventory.stderr").write_text(listing.stderr, encoding="utf-8")
    if listing.returncode:
        sys.stderr.write(listing.stderr)
        raise RuntimeError("compiled test inventory failed; no tests may be skipped")
    names = compiled_inventory(listing.stdout)
    shards = partition(names, args.count)
    selected = shards[args.index]
    invocation = command(selected, full_suite=args.count == 1)
    manifest = {
        "package": IMPORT_PATH, "index": args.index, "count": args.count,
        "inventory_sha256": hashlib.sha256("\n".join(names).encode()).hexdigest(),
        "all_test_names": names, "shards": shards, "command": invocation,
    }
    (args.output_dir / "manifest.json").write_text(json.dumps(manifest, indent=2), encoding="utf-8")
    print(f"Store shard {args.index + 1}/{args.count}: {len(selected)}/{len(names)} tests", flush=True)
    events = []
    with (args.output_dir / "tests.jsonl").open("w", encoding="utf-8") as log, \
            (args.output_dir / "tests.stderr").open("w", encoding="utf-8") as stderr:
        process = subprocess.Popen(invocation, stdout=subprocess.PIPE, stderr=stderr,
                                   text=True, encoding="utf-8")
        assert process.stdout is not None
        for line in process.stdout:
            log.write(line)
            try:
                event = json.loads(line)
            except json.JSONDecodeError:
                # Keep invalid lines as evidence; an invalid event stream cannot pass.
                events.append({"invalid_line": line})
                continue
            events.append(event)
            if event.get("Action") == "fail":
                print(f"FAIL {event.get('Test', event.get('Package', ''))}", flush=True)
        return_code = process.wait()
    summary = verify_completion(selected, events, return_code)
    if any("invalid_line" in event for event in events):
        summary["success"] = False
        summary["invalid_event_stream"] = True
    summary["wall_seconds"] = round(time.monotonic() - started, 3)
    (args.output_dir / "summary.json").write_text(json.dumps(summary, indent=2), encoding="utf-8")
    if not summary["success"]:
        for event in events:
            if event.get("Action") == "output":
                sys.stdout.write(event.get("Output", ""))
        sys.stderr.write((args.output_dir / "tests.stderr").read_text(encoding="utf-8"))
    print(json.dumps(summary), flush=True)
    return 0 if summary["success"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
