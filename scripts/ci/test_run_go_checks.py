from __future__ import annotations

import copy
import json
import subprocess
import tempfile
import unittest
from contextlib import redirect_stderr, redirect_stdout
from io import StringIO
from pathlib import Path
from unittest.mock import MagicMock, Mock, patch

import run_go_checks as runner


MODULE = "cyberagent-workbench"
ROOT = Path("fixture-repo").absolute()


def package(path: str, **fields) -> dict:
    return {"ImportPath": MODULE + "/" + path, "Dir": str(ROOT / path),
            "Module": {"Path": MODULE, "Dir": str(ROOT)}, **fields}


def name(path: str) -> str:
    return MODULE + "/" + path


class GoSelectionTests(unittest.TestCase):
    def setUp(self):
        self.packages = [
            package("internal/llm"), package("internal/mcp"), package("internal/toolgateway"),
            package("internal/application", Imports=[name("internal/llm"), name("internal/toolgateway"), name("internal/mcp")]),
            package("internal/store", TestImports=[name("internal/application")]),
            package("internal/httpapi", Imports=[name("internal/application")]),
            package("internal/app", Imports=[name("internal/application")], XTestImports=[name("internal/httpapi")]),
            package("internal/desktop", Imports=[name("internal/app")]),
            package("internal/producte2e", Imports=[name("internal/desktop")]),
            package("internal/packagede2e", Imports=[name("internal/desktop")]),
            package("cmd/cyberagent-desktop", Imports=[name("internal/desktop")]),
            package("internal/unrelated"),
        ]

    def plan(self, paths, **kwargs):
        plan = runner.select_plan(self.packages, paths, **kwargs)
        runner.validate_plan(plan)
        return plan

    def test_reverse_closure_includes_production_internal_and_external_test_imports(self):
        plan = self.plan(["internal/llm/openai.go"])
        self.assertEqual(plan["changed"], [name("internal/llm")])
        self.assertEqual(set(plan["affected"]), {p["ImportPath"] for p in self.packages} -
                         {name("internal/mcp"), name("internal/toolgateway"), name("internal/unrelated")})
        self.assertTrue(plan["store_affected"])
        self.assertNotIn(name("internal/store"), plan["test"] + plan["compile"])
        self.assertEqual(set(plan["compile"]), {name(p) for p in runner.HEAVY_INDIRECT})
        self.assertIn(name("internal/producte2e"), plan["test"])
        self.assertIn(name("internal/packagede2e"), plan["test"])
        self.assertEqual(plan["integrations"], [{"package": name("internal/application"),
                         "run": "^(TestOpenAICompatibleProviderAgentRunnerToolRoundTrip)$"}])

    def test_direct_large_package_runs_its_entire_suite(self):
        plan = self.plan(["internal/application/thread_turn_test.go"])
        self.assertIn(name("internal/application"), plan["test"])
        self.assertNotIn(name("internal/application"), plan["compile"])
        self.assertNotIn(name("internal/application"), [x["package"] for x in plan["integrations"]])

    def test_cycles_and_duplicate_imports_are_finite_and_deterministic(self):
        self.packages[0]["TestImports"] = [name("internal/application")] * 2
        a = self.plan(["internal/llm/deleted.go"])
        b = runner.select_plan(list(reversed(self.packages)), ["internal/llm/deleted.go"])
        self.assertEqual(a, b)

    def test_test_import_cycle_does_not_propagate_into_unrelated_production_consumers(self):
        records = [package("internal/llm"),
                   package("internal/application", Imports=[name("internal/llm")],
                           TestImports=[name("internal/store")]),
                   package("internal/store", XTestImports=[name("internal/application")]),
                   package("internal/workspace", Imports=[name("internal/store")]),
                   package("internal/toolgateway", TestImports=[name("internal/store")])]
        plan = runner.select_plan(records, ["internal/llm/openai.go"])
        runner.validate_plan(plan)
        self.assertEqual(set(plan["production_affected"]), {name("internal/llm"), name("internal/application")})
        self.assertEqual(set(plan["affected"]), {name("internal/llm"), name("internal/application"), name("internal/store")})

    def test_test_file_and_testdata_changes_do_not_propagate_to_consumers(self):
        for path in ("internal/llm/openai_test.go", "internal/llm/testdata/response.json"):
            with self.subTest(path=path):
                plan = self.plan([path])
                self.assertEqual(plan["production_affected"], [])
                self.assertEqual(plan["affected"], [name("internal/llm")])
                self.assertEqual(plan["test"], [name("internal/llm")])
                self.assertFalse(plan["store_affected"])
                self.assertEqual(plan["integrations"], [])

    def test_production_embed_remains_a_production_input_even_inside_testdata(self):
        self.packages[0]["EmbedPatterns"] = ["testdata"]
        plan = self.plan(["internal/llm/testdata/runtime.json"])
        self.assertIn(name("internal/application"), plan["production_affected"])

    def test_deleted_file_keeps_current_package_but_deleted_directory_is_full(self):
        self.assertFalse(self.plan(["internal/llm/deleted.go"])["full"])
        plan = self.plan(["internal/removed/last.go"])
        self.assertTrue(plan["full"])
        self.assertTrue(plan["store_affected"])
        self.assertEqual(plan["compile"], [])
        self.assertTrue(plan["fallback_reasons"])

    def test_ignored_platform_source_still_marks_its_package_direct(self):
        self.packages[0]["IgnoredGoFiles"] = ["platform_windows.go"]
        self.assertEqual(self.plan(["internal/llm/platform_windows.go"])["changed"], [name("internal/llm")])

    def test_embedded_and_testdata_resources_mark_the_owner(self):
        self.packages.append(package("internal/skills", EmbedPatterns=["builtins", "all:archives"],
                                     EmbedFiles=["builtins/current.md"], TestEmbedPatterns=["testdata/*.json"]))
        for path in ("internal/skills/builtins/new.md", "internal/skills/archives/deleted/data.bin",
                     "internal/skills/testdata/deleted.json", "internal/skills/testdata/unembedded.txt"):
            with self.subTest(path=path):
                self.assertEqual(self.plan([path])["changed"], [name("internal/skills")])

    def test_parent_embed_and_child_go_package_are_both_affected(self):
        self.packages.append(package("internal/container", EmbedFiles=["child/data.txt"],
                                     EmbedPatterns=["all:child"]))
        self.packages.append(package("internal/container/child"))
        for path in ("internal/container/child/data.txt", "internal/container/child/deleted.go"):
            with self.subTest(path=path):
                self.assertEqual(set(self.plan([path])["changed"]),
                                 {name("internal/container"), name("internal/container/child")})

    def test_full_mode_runs_every_non_store_package(self):
        plan = self.plan([], full=True)
        self.assertEqual(set(plan["test"]), {p["ImportPath"] for p in self.packages} - {name("internal/store")})
        self.assertEqual(plan["compile"], [])
        self.assertEqual(plan["integrations"], [])

    def test_empty_selection_does_not_manufacture_a_full_run(self):
        plan = self.plan([])
        self.assertEqual(plan["affected"], [])
        self.assertEqual(plan["test"], [])
        self.assertFalse(plan["store_affected"])

    def test_four_integration_tests_are_selected_once_and_only_for_affected_indirect_packages(self):
        plan = self.plan(["internal/llm/openai.go", "internal/toolgateway/gateway.go", "internal/mcp/tools.go"])
        selected = {(entry["package"], test) for entry in plan["integrations"]
                    for test in runner._integration_names(entry["run"])}
        self.assertEqual(len(selected), 4)
        direct = self.plan(["internal/llm/openai.go", "internal/application/thread_turn.go"])
        self.assertNotIn(name("internal/application"), [x["package"] for x in direct["integrations"]])

    def test_paths_cannot_escape_the_repository(self):
        for path in ("/absolute.go", "../escape.go", "internal/../escape.go", "internal\\llm\\file.go"):
            with self.subTest(path=path), self.assertRaises(runner.GoCheckError):
                self.plan([path])

    def test_invalid_plan_cannot_skip_a_direct_package_or_execute_store_tests(self):
        original = self.plan(["internal/llm/openai.go"])
        mutations = []
        bad = copy.deepcopy(original)
        bad["test"].remove(name("internal/llm"))
        mutations.append(bad)
        bad = copy.deepcopy(original)
        bad["test"].append(name("internal/store"))
        mutations.append(bad)
        bad = copy.deepcopy(original)
        bad["compile"].append(name("internal/llm"))
        mutations.append(bad)
        bad = copy.deepcopy(original)
        bad["store_affected"] = False
        mutations.append(bad)
        bad = copy.deepcopy(original)
        bad["integrations"][0]["run"] = ".*"
        mutations.append(bad)
        for plan in mutations:
            with self.subTest(plan=plan), self.assertRaises(runner.GoCheckError):
                runner.validate_plan(plan)

    def test_execution_vets_the_affected_set_and_disables_duplicate_test_vet(self):
        plan = self.plan(["internal/llm/openai.go", "internal/application/thread_turn.go"])
        with patch.object(runner.subprocess, "run", return_value=Mock(returncode=0)) as run, redirect_stdout(StringIO()):
            self.assertEqual(runner.execute_plan(ROOT, plan), 0)
        commands = [call.args[0] for call in run.call_args_list]
        self.assertEqual(commands[0], ["go", "vet", *plan["affected"]])
        self.assertIn(name("internal/store"), commands[0])
        self.assertTrue(all("-vet=off" in command for command in commands[1:]))
        self.assertTrue(all(name("internal/store") not in command for command in commands[1:]))
        self.assertIn(["-run", "^$"], [command[5:7] for command in commands[1:]])

    def test_full_execution_vets_the_whole_module_and_never_filters_tests(self):
        plan = self.plan([], full=True)
        with patch.object(runner.subprocess, "run", return_value=Mock(returncode=0)) as run, redirect_stdout(StringIO()):
            self.assertEqual(runner.execute_plan(ROOT, plan), 0)
        self.assertEqual(run.call_args_list[0].args[0], ["go", "vet", "./..."])
        self.assertNotIn("-run", run.call_args_list[1].args[0])

    def test_failed_vet_prevents_test_execution(self):
        with patch.object(runner.subprocess, "run", return_value=Mock(returncode=2)) as run, redirect_stdout(StringIO()):
            self.assertEqual(runner.execute_plan(ROOT, self.plan(["internal/llm/openai.go"])), 2)
        self.assertEqual(run.call_count, 1)

    def test_integration_filter_requires_the_selected_test_in_its_own_package(self):
        plan = self.plan(["internal/llm/openai.go"])
        selected = plan["integrations"][0]
        test_name = "TestOpenAICompatibleProviderAgentRunnerToolRoundTrip"
        for events, succeeds in (([], False),
                                 ([{"Action": "pass", "Package": name("internal/unrelated"), "Test": test_name}], False),
                                 ([{"Action": "pass", "Package": selected["package"], "Test": test_name}], True)):
            with self.subTest(events=events):
                process = MagicMock()
                process.__enter__.return_value = process
                process.stdout = iter(json.dumps(event) + "\n" for event in events)
                process.wait.return_value = 0
                with patch.object(runner.subprocess, "run", return_value=Mock(returncode=0)), \
                        patch.object(runner.subprocess, "Popen", return_value=process), redirect_stdout(StringIO()):
                    if succeeds:
                        self.assertEqual(runner.execute_plan(ROOT, plan), 0)
                    else:
                        with self.assertRaises(runner.GoCheckError):
                            runner.execute_plan(ROOT, plan)


class GoMetadataTests(unittest.TestCase):
    def test_collect_decodes_the_real_go_json_sequence_without_source_inspection(self):
        records = [package("internal/one", TestImports=[name("internal/two")]), package("internal/two")]
        output = "\n\n".join(json.dumps(p) for p in records).encode()
        with patch.object(runner, "_output", return_value=output) as command:
            self.assertEqual(runner.collect_packages(ROOT), records)
        command.assert_called_once_with(ROOT, "go", "list", "-json", "./...")

    def test_invalid_metadata_fails_closed(self):
        for output in (b"", b"not json", b'{"Error":{"Err":"missing import"}}'):
            with self.subTest(output=output), patch.object(runner, "_output", return_value=output), self.assertRaises((runner.GoCheckError, ValueError)):
                runner.collect_packages(ROOT)

    def test_plan_only_and_plan_file_execution_do_not_recompute_the_graph(self):
        records = [package("internal/llm"), package("internal/store")]
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / "plan.json"
            with patch.object(runner, "collect_packages", return_value=records), \
                    patch.object(runner, "changed_paths", return_value=["internal/llm/openai.go"]), \
                    patch.object(runner, "execute_plan") as execute, redirect_stdout(StringIO()):
                result = runner.main(["--plan-only", "--plan-output", str(output), "--base", "b", "--head", "h"])
                self.assertEqual(result, 0)
                execute.assert_not_called()
            with patch.object(runner, "collect_packages") as collect, \
                    patch.object(runner, "execute_plan", return_value=0) as execute:
                self.assertEqual(runner.main(["--plan", str(output)]), 0)
                collect.assert_not_called()
                self.assertEqual(execute.call_args.args[1], json.loads(output.read_text()))

    def test_real_git_diff_exposes_renames_and_deleted_packages(self):
        with tempfile.TemporaryDirectory() as directory:
            repo = Path(directory)
            def git(*args):
                return subprocess.check_output(["git", "-C", str(repo), *args], text=True).strip()
            git("init", "-q")
            git("config", "user.name", "CI test")
            git("config", "user.email", "ci@example.invalid")
            old = repo / "internal/old/last.go"
            old.parent.mkdir(parents=True)
            old.write_text("package old\n")
            git("add", ".")
            git("commit", "-qm", "old package")
            base = git("rev-parse", "HEAD")
            git("mv", "internal/old", "internal/new")
            git("commit", "-qam", "rename package")
            self.assertEqual(runner.changed_paths(repo, base, git("rev-parse", "HEAD")),
                             ["internal/new/last.go", "internal/old/last.go"])
            with self.assertRaises(runner.GoCheckError):
                runner.changed_paths(repo, "bad", git("rev-parse", "HEAD"))


if __name__ == "__main__":
    unittest.main()
