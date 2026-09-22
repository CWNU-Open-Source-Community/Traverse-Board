import importlib.util
import unittest
from pathlib import Path

spec = importlib.util.spec_from_file_location("run_store_shard", Path(__file__).with_name("run_store_shard.py"))
runner = importlib.util.module_from_spec(spec)
spec.loader.exec_module(runner)


class StorePartitionTests(unittest.TestCase):
    def test_compiled_inventory_preserves_fuzz_examples_and_tests(self):
        names = runner.compiled_inventory(
            "TestMigration169\nFuzzProtocol\nExampleOpen\nok\t" + runner.IMPORT_PATH + "\t0.01s\n"
        )
        self.assertEqual(names, ["ExampleOpen", "FuzzProtocol", "TestMigration169"])

    def test_unknown_duplicate_or_empty_inventory_fails(self):
        for raw in ("", "TestOne\nTestOne\n", "TestOne\nTest two\n", "ok\twrong/package\n"):
            with self.subTest(raw=raw), self.assertRaises(ValueError):
                runner.compiled_inventory(raw)

    def test_every_new_test_is_in_exactly_one_balanced_shard(self):
        names = [f"TestCase{index:04}" for index in range(607)] + ["TestNewMigration"]
        shards = runner.partition(list(reversed(names)), 8)
        self.assertEqual(sorted(n for shard in shards for n in shard), sorted(names))
        self.assertEqual(len({n for shard in shards for n in shard}), len(names))
        self.assertLessEqual(max(map(len, shards)) - min(map(len, shards)), 1)
        self.assertEqual(shards, runner.partition(names, 8))

    def test_invalid_partition_cannot_silently_omit_tests(self):
        for names, count in [(["TestOne"], 0), (["TestOne"], 2), (["TestOne"] * 2, 1),
                             (["TestOne/child"], 1), (["TestOne|TestTwo"], 1)]:
            with self.subTest(names=names, count=count), self.assertRaises(ValueError):
                runner.partition(names, count)

    def event(self, action, name=None):
        return {"Package": runner.IMPORT_PATH, "Action": action, **({"Test": name} if name else {})}

    def test_completion_requires_every_selected_root_and_package_success(self):
        selected = ["TestOne", "TestTwo", "FuzzSeed"]
        events = [self.event("pass", "TestOne/subtest"), self.event("pass", "TestOne"),
                  self.event("skip", "TestTwo"), self.event("pass", "FuzzSeed/seed#0"),
                  self.event("pass", "FuzzSeed"), self.event("pass")]
        self.assertTrue(runner.verify_completion(selected, events, 0)["success"])
        self.assertFalse(runner.verify_completion(selected + ["TestMissing"], events, 0)["success"])
        self.assertFalse(runner.verify_completion(selected, events + [self.event("pass", "TestOne")], 0)["success"])
        self.assertFalse(runner.verify_completion(selected, events + [self.event("pass", "TestUnexpected")], 0)["success"])
        self.assertFalse(runner.verify_completion(selected, events[:-1], 0)["success"])
        self.assertFalse(runner.verify_completion(selected, events, 1)["success"])

    def test_failure_and_package_skip_cannot_be_green(self):
        for events in ([self.event("fail", "TestOne"), self.event("fail")],
                       [self.event("pass", "TestOne"), self.event("skip")]):
            self.assertFalse(runner.verify_completion(["TestOne"], events, 0)["success"])

    def test_command_is_exact_uncached_and_keeps_timeout(self):
        cmd = runner.command(["TestOne", "TestOneMore"])
        self.assertIn("-count=1", cmd)
        self.assertIn("-timeout=60m", cmd)
        self.assertEqual(cmd[cmd.index("-run") + 1], "^(TestOne|TestOneMore)$")
        self.assertEqual(cmd[-1], "./internal/store")

    def test_full_suite_avoids_windows_command_line_limit_without_filtering(self):
        names = ["Test" + "LongName" * 20 + str(index) for index in range(610)]
        cmd = runner.command(names, full_suite=True)
        self.assertNotIn("-run", cmd)
        self.assertEqual(cmd, ["go", "test", "-json", "-count=1", "-timeout=60m", "./internal/store"])


if __name__ == "__main__":
    unittest.main()
