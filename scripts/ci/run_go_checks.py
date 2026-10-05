#!/usr/bin/env python3
"""Select Go checks from the actual package graph, excluding sharded Store tests."""

from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys
from collections import defaultdict
from pathlib import Path, PurePosixPath
from typing import Sequence


VERSION = "go_checks_plan.v1"
HEAVY_INDIRECT = {"internal/application", "internal/app", "internal/httpapi", "internal/desktop"}
# These are bounded, existing product-service regressions. The graph still
# decides whether their package is affected; a directly changed package is full.
INTEGRATIONS = (
    ("internal/llm", "internal/application", "TestOpenAICompatibleProviderAgentRunnerToolRoundTrip"),
    ("internal/toolgateway", "internal/application", "TestThreadTurnStopsAtPendingMCPApprovalAndResumesSameTurn"),
    ("internal/toolgateway", "internal/httpapi", "TestMCPApprovalHTTPProductSameTurnAndReplay"),
    ("internal/mcp", "internal/app", "TestMCPServeCLIUsesPersistedWorkspaceForReadTools"),
    ("internal/toolgateway", "internal/app", "TestMCPServeCLIUsesPersistedWorkspaceForReadTools"),
)


class GoCheckError(RuntimeError):
    pass


def _output(repo: Path, *command: str) -> bytes:
    try:
        return subprocess.run(command, cwd=repo, check=True, capture_output=True).stdout
    except (OSError, subprocess.CalledProcessError) as exc:
        detail = exc.stderr.decode("utf-8", "replace").strip() if isinstance(exc, subprocess.CalledProcessError) else str(exc)
        raise GoCheckError(detail or f"{' '.join(command)} failed") from exc


def collect_packages(repo: Path) -> list[dict]:
    """Read native Go package metadata, including imports used only by tests."""
    raw = _output(repo, "go", "list", "-json", "./...").decode("utf-8")
    decoder = json.JSONDecoder()
    packages = []
    offset = 0
    while offset < len(raw):
        if raw[offset].isspace():
            offset += 1
            continue
        package, offset = decoder.raw_decode(raw, offset)
        if not isinstance(package, dict) or package.get("Error") or package.get("DepsErrors"):
            raise GoCheckError("go list returned an invalid or incomplete package graph")
        packages.append(package)
    if not packages:
        raise GoCheckError("go list returned an empty package graph")
    return packages


def changed_paths(repo: Path, base: str, head: str) -> list[str]:
    for revision in (base, head):
        if re.fullmatch(r"[0-9a-f]{40}", revision) is None:
            raise GoCheckError("base and head must be exact commit identities")
        _output(repo, "git", "cat-file", "-e", f"{revision}^{{commit}}")
    raw = _output(repo, "git", "diff", "--no-renames", "--name-only", "-z", base, head, "--")
    if not raw or not raw.endswith(b"\0"):
        raise GoCheckError("the change set is empty or malformed")
    return [name.decode("utf-8") for name in raw[:-1].split(b"\0")]


def _embedded(package: dict, directory: str, path: str, *, test: bool = False) -> bool:
    relative = PurePosixPath(path).relative_to(directory).as_posix()
    for field in (("TestEmbedFiles", "XTestEmbedFiles") if test else ("EmbedFiles",)):
        if relative in package.get(field, []):
            return True
    # Deleted embedded files are absent from EmbedFiles. Conservatively retain
    # the literal directory before a pattern's wildcard, rather than attempting
    # to reimplement Go's embed matching or inspect source text.
    for field in (("TestEmbedPatterns", "XTestEmbedPatterns") if test else ("EmbedPatterns",)):
        for pattern in package.get(field, []):
            pattern = pattern.removeprefix("all:")
            wildcard = re.search(r"[*?\[]", pattern)
            prefix = pattern[:wildcard.start()].rsplit("/", 1)[0] if wildcard else pattern
            if wildcard and "/" not in pattern[:wildcard.start()]:
                prefix = ""
            if not prefix or relative == prefix or relative.startswith(prefix + "/"):
                return True
    return False


def select_plan(packages: Sequence[dict], paths: Sequence[str | bytes], full: bool = False) -> dict:
    """Build a deterministic plan without reading source text or running tests."""
    modules = {package.get("Module", {}).get("Path") for package in packages}
    if len(modules) != 1 or None in modules:
        raise GoCheckError("expected packages from exactly one Go module")
    module = modules.pop()
    by_name = {package["ImportPath"]: package for package in packages}
    if len(by_name) != len(packages) or any(not name.startswith(module + "/") and name != module for name in by_name):
        raise GoCheckError("duplicate or foreign Go package")
    directories = {
        name: Path(package["Dir"]).relative_to(package["Module"]["Dir"]).as_posix()
        for name, package in by_name.items()
    }
    by_directory = {directory: name for name, directory in directories.items()}
    direct = set()
    production_direct = set()
    reasons = []
    for raw_path in paths:
        path = raw_path.decode("utf-8") if isinstance(raw_path, bytes) else raw_path
        pure_path = PurePosixPath(path)
        if pure_path.is_absolute() or ".." in pure_path.parts or "\\" in path:
            raise GoCheckError("changed paths must be relative repository paths")
        owners = set()
        missing_go_package = False
        if path.endswith(".go"):
            owner = by_directory.get(pure_path.parent.as_posix())
            if owner:
                owners.add(owner)
            else:
                missing_go_package = True
        else:
            # Runtime/test resources belong to their nearest current package.
            # Include other embed owners too (a parent may embed a subtree).
            for parent in pure_path.parents:
                owner = by_directory.get(parent.as_posix())
                if owner:
                    owners.add(owner)
                    break
        for name, directory in directories.items():
            if pure_path.is_relative_to(directory) and (
                    _embedded(by_name[name], directory, path) or
                    _embedded(by_name[name], directory, path, test=True)):
                owners.add(name)
        if not owners or missing_go_package:
            reasons.append(f"no current Go package owns {path}; use full checks")
        direct.update(owners)
        for name in owners:
            directory = directories[name]
            relative = pure_path.relative_to(directory)
            production_embed = _embedded(by_name[name], directory, path)
            if production_embed or (path.endswith(".go") and not path.endswith("_test.go")) or (
                    not path.endswith(".go") and "testdata" not in relative.parts and
                    not _embedded(by_name[name], directory, path, test=True)):
                production_direct.add(name)
    full = full or bool(reasons)
    reverse = defaultdict(set)
    for name, package in by_name.items():
        for imported in package.get("Imports", []):
            if imported in by_name:
                reverse[imported].add(name)
    production_affected = set(by_name) if full else set(production_direct)
    pending = list(production_affected)
    while pending:
        for dependent in reverse[pending.pop()] - production_affected:
            production_affected.add(dependent)
            pending.append(dependent)
    affected = production_affected | direct
    # A dependency change can affect a consumer's tests without changing its
    # production package. Do not propagate that test-only edge to more callers.
    for name, package in by_name.items():
        if any(imported in production_affected for field in ("TestImports", "XTestImports")
               for imported in package.get(field, [])):
            affected.add(name)
    store = module + "/internal/store"
    heavy = {module + "/" + path for path in HEAVY_INDIRECT}
    tests = affected - {store} if full else (affected - heavy | direct) - {store}
    compile_only = affected - tests - {store}
    selected = defaultdict(set)
    if not full:
        for dependency, target, test in INTEGRATIONS:
            package = module + "/" + target
            if module + "/" + dependency in production_affected and package in compile_only:
                selected[package].add(test)
    return {
        "version": VERSION, "module": module, "packages": sorted(by_name),
        "full": full, "changed": sorted(direct), "affected": sorted(affected),
        "production_affected": sorted(production_affected),
        "test": sorted(tests), "compile": sorted(compile_only),
        "store_affected": store in affected, "fallback_reasons": sorted(reasons),
        "integrations": [{"package": package, "run": "^(" + "|".join(sorted(names)) + ")$"}
                         for package, names in sorted(selected.items())],
    }


def _integration_names(expression: str) -> set[str]:
    if not re.fullmatch(r"\^\(Test[A-Za-z0-9_]+(?:\|Test[A-Za-z0-9_]+)*\)\$", expression):
        raise GoCheckError("integration filters must be exact test names")
    return set(expression[2:-2].split("|"))


def validate_plan(plan: dict) -> None:
    if plan.get("version") != VERSION or not isinstance(plan.get("module"), str):
        raise GoCheckError("unsupported Go check plan")
    module = plan["module"]
    if re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.~/-]*", module) is None:
        raise GoCheckError("invalid Go module identity")
    available = set(plan["packages"])
    if any((not name.startswith(module + "/") and name != module) or
           not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.~/-]*", name) for name in available):
        raise GoCheckError("plan contains a foreign package")
    for field in ("changed", "affected", "production_affected", "test", "compile"):
        names = plan[field]
        if len(names) != len(set(names)) or not set(names) <= available:
            raise GoCheckError(f"invalid plan {field} packages")
    if not set(plan["production_affected"]) <= set(plan["affected"]):
        raise GoCheckError("production impact must belong to the affected package set")
    store = module + "/internal/store"
    tested, compiled = set(plan["test"]), set(plan["compile"])
    if tested & compiled or store in tested | compiled or tested | compiled != set(plan["affected"]) - {store}:
        raise GoCheckError("plan must cover every affected package and exclude Store tests")
    if plan["store_affected"] != (store in plan["affected"]):
        raise GoCheckError("Store impact does not match the package graph")
    if plan["full"] and (set(plan["affected"]) != available or compiled):
        raise GoCheckError("full plan must test every non-Store package")
    if not set(plan["changed"]) - {store} <= tested:
        raise GoCheckError("directly changed packages require full tests")
    for integration in plan["integrations"]:
        if integration["package"] not in compiled:
            raise GoCheckError("integration must belong to an indirectly compiled package")
        _integration_names(integration["run"])


def execute_plan(repo: Path, plan: dict) -> int:
    validate_plan(plan)
    vet_packages = ["./..."] if plan["full"] else plan["affected"]
    if vet_packages:
        command = ["go", "vet", *vet_packages]
        print(" ".join(command), flush=True)
        completed = subprocess.run(command, cwd=repo, check=False)
        if completed.returncode:
            return completed.returncode
    for packages, flags in ((plan["test"], []), (plan["compile"], ["-run", "^$"])):
        if packages:
            command = ["go", "test", "-vet=off", "-count=1", "-timeout=60m", *flags, *packages]
            print(" ".join(command), flush=True)
            completed = subprocess.run(command, cwd=repo, check=False)
            if completed.returncode:
                return completed.returncode
    for integration in plan["integrations"]:
        command = ["go", "test", "-json", "-vet=off", "-count=1", "-timeout=60m",
                   "-run", integration["run"], integration["package"]]
        print(" ".join(command), flush=True)
        completed = set()
        with subprocess.Popen(command, cwd=repo, stdout=subprocess.PIPE,
                              text=True, encoding="utf-8") as process:
            assert process.stdout is not None
            for line in process.stdout:
                sys.stdout.write(line)
                event = json.loads(line)
                if event.get("Package") == integration["package"] and event.get("Action") in ("pass", "skip") and "/" not in event.get("Test", "/"):
                    completed.add(event["Test"])
            return_code = process.wait()
        if return_code:
            return return_code
        if completed != _integration_names(integration["run"]):
            raise GoCheckError("an integration filter no longer matches every selected test")
    return 0


def main(argv: Sequence[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repo", type=Path, default=Path.cwd())
    parser.add_argument("--mode", choices=("affected", "full"), default="affected")
    parser.add_argument("--base", default="")
    parser.add_argument("--head", default="")
    parser.add_argument("--plan", type=Path)
    parser.add_argument("--plan-output", type=Path)
    parser.add_argument("--plan-only", action="store_true")
    args = parser.parse_args(argv)
    try:
        if args.plan:
            plan = json.loads(args.plan.read_text(encoding="utf-8"))
        else:
            packages = collect_packages(args.repo)
            paths = [] if args.mode == "full" else changed_paths(args.repo, args.base, args.head)
            plan = select_plan(packages, paths, full=args.mode == "full")
        validate_plan(plan)
        if args.plan_output:
            args.plan_output.parent.mkdir(parents=True, exist_ok=True)
            args.plan_output.write_text(json.dumps(plan, indent=2) + "\n", encoding="utf-8")
        if args.plan_only:
            print(json.dumps(plan, indent=2))
            return 0
        return execute_plan(args.repo, plan)
    except (GoCheckError, OSError, ValueError, KeyError, TypeError) as exc:
        print(f"Go checks failed: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
