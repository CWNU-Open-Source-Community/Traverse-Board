from __future__ import annotations

import os
import re
import subprocess
import tempfile
import unittest
from contextlib import redirect_stderr, redirect_stdout
from io import StringIO
from pathlib import Path

from classify_changes import ClassificationError, changed_paths, classify_paths, main


class ClassifyPathsTests(unittest.TestCase):
    def test_exact_documentation_allowlist_is_lightweight(self) -> None:
        self.assertTrue(
            classify_paths(
                [
                    b"README.md",
                    b"README.en.md",
                    b"CONTRIBUTING.md",
                    b"docs/mac-keychain.md",
                    b"docs/macos-release.md",
                    b"docs/scheduled-jobs-diagnostics.md",
                ]
            )
        )

    def test_unknown_generated_and_mixed_paths_are_full(self) -> None:
        for paths in (
            [b"docs/development-history.md"],
            [b"docs/convergence/protocol-registry.md"],
            [b"docs/convergence/surface-registry.json"],
            [b"README.md", b"internal/store/sqlite.go"],
            [b"README.md\nmalicious=true"],
        ):
            with self.subTest(paths=paths):
                self.assertFalse(classify_paths(paths))

    def test_empty_diff_fails_closed(self) -> None:
        with self.assertRaises(ClassificationError):
            classify_paths([])

    def test_non_pull_request_is_full_without_reading_git(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            output = StringIO()
            with redirect_stdout(output):
                self.assertEqual(main(["--event", "push", "--repo", directory]), 0)
            self.assertEqual(output.getvalue(), "docs_only=false\n")

    def test_classification_failure_emits_no_skip_output(self) -> None:
        output = StringIO()
        errors = StringIO()
        with tempfile.TemporaryDirectory() as directory, redirect_stdout(output), \
                redirect_stderr(errors):
            result = main(
                [
                    "--event",
                    "pull_request",
                    "--repo",
                    directory,
                    "--base",
                    "bad",
                    "--head",
                    "also-bad",
                ]
            )
        self.assertEqual(result, 1)
        self.assertEqual(output.getvalue(), "")
        self.assertIn("classification failed", errors.getvalue())


class GitDiffTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary_directory = tempfile.TemporaryDirectory()
        self.repo = Path(self.temporary_directory.name)
        self.git("init", "-q")
        self.git("config", "user.name", "CI test")
        self.git("config", "user.email", "ci@example.invalid")

    def tearDown(self) -> None:
        self.temporary_directory.cleanup()

    def git(self, *args: str) -> str:
        return subprocess.run(
            ["git", "-C", str(self.repo), *args],
            check=True,
            stdout=subprocess.PIPE,
            text=True,
        ).stdout.strip()

    def commit_file(self, name: str, content: str) -> str:
        path = self.repo / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content, encoding="utf-8")
        self.git("add", "--", name)
        self.git("commit", "-qm", name)
        return self.git("rev-parse", "HEAD")

    @unittest.skipIf(os.name == "nt", "Windows filenames cannot contain newlines")
    def test_diff_is_nul_safe_for_newline_path(self) -> None:
        base = self.commit_file("README.md", "base\n")
        self.commit_file("docs/bad\nname.md", "content\n")
        head = self.git("rev-parse", "HEAD")
        paths = changed_paths(self.repo, base, head)
        self.assertEqual(paths, [b"docs/bad\nname.md"])
        self.assertFalse(classify_paths(paths))

    def test_rename_from_unknown_path_exposes_both_sides(self) -> None:
        base = self.commit_file("source.md", "content\n")
        self.git("mv", "source.md", "README.en.md")
        self.git("commit", "-qm", "rename")
        head = self.git("rev-parse", "HEAD")
        paths = changed_paths(self.repo, base, head)
        self.assertEqual(paths, [b"README.en.md", b"source.md"])
        self.assertFalse(classify_paths(paths))

    def test_invalid_revision_fails_closed(self) -> None:
        head = self.commit_file("README.md", "content\n")
        with self.assertRaises(ClassificationError):
            changed_paths(self.repo, "not-a-commit", head)


class WorkflowContractTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.root = Path(__file__).resolve().parents[2]
        cls.ci = (cls.root / ".github/workflows/ci.yml").read_text(encoding="utf-8")
        cls.release = (cls.root / ".github/workflows/release-desktop.yml").read_text(
            encoding="utf-8"
        )

    def test_ci_keeps_go_aggregate_and_fails_closed(self) -> None:
        self.assertIn("  go:\n    name: Go control plane", self.ci)
        self.assertIn("CLASSIFY_RESULT: ${{ needs.classify.result }}", self.ci)
        self.assertIn('if [[ "$CLASSIFY_RESULT" != success', self.ci)
        self.assertIn('elif [[ "$STORE_TESTS_RESULT" != success ]]', self.ci)
        # A missing classifier output differs from the sole skip value "true".
        self.assertGreaterEqual(
            self.ci.count("needs.classify.outputs.docs_only != 'true'"),
            8,
        )
        self.assertIn("github.event_name != 'pull_request'", self.ci)

    def test_cancelled_runs_do_not_start_or_continue_heavy_jobs(self) -> None:
        job_starts = {
            match.group(1): match.start()
            for match in re.finditer(r"(?m)^  ([a-z][a-z0-9-]*):\n", self.ci)
        }
        ordered_starts = sorted(job_starts.values())
        for job in (
            "go-checks",
            "store-tests",
            "code-intel-lsp",
            "web",
            "rust",
            "desktop-macos",
            "desktop",
            "ui-evidence-windows",
        ):
            with self.subTest(job=job):
                start = job_starts[job]
                end = next(
                    (candidate for candidate in ordered_starts if candidate > start),
                    len(self.ci),
                )
                header = self.ci[start:end].split("    runs-on:", 1)[0]
                self.assertIn("if: ${{ !cancelled()", header)
                self.assertNotIn("if: ${{ always()", header)
        go_start = job_starts["go"]
        go_end = next(candidate for candidate in ordered_starts if candidate > go_start)
        aggregate_header = self.ci[go_start:go_end].split("    runs-on:", 1)[0]
        self.assertIn("if: ${{ always() }}", aggregate_header)

    def test_release_pr_paths_only_drop_non_inputs(self) -> None:
        trigger, _ = self.release.split("permissions:", 1)
        self.assertIn("      - 'README.md'", trigger)
        for removed in (
            "README.en.md",
            "docs/macos-release.md",
            "docs/standard-code-packaged-e2e.md",
            "docs/standard-code-product-e2e.md",
            "docs/standard-code-release-gate.md",
            "docs/adr/0138-standard-code-packaged-e2e-foundation.md",
            "docs/adr/0144-standard-code-release-gate-aggregation.md",
            "docs/convergence/protocol-registry.md",
        ):
            with self.subTest(removed=removed):
                self.assertNotIn(f"      - '{removed}'", trigger)
        self.assertIn("      - 'protocols/registry.json'", trigger)
        self.assertIn(
            "cancel-in-progress: ${{ github.event_name == 'pull_request' }}",
            self.release,
        )


if __name__ == "__main__":
    unittest.main()
