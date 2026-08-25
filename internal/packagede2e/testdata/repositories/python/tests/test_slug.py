import unittest

from app.slug import slugify


class SlugifyTests(unittest.TestCase):
    def test_collapses_whitespace_and_trims_separators(self) -> None:
        self.assertEqual(slugify("  Safe   Workspace  "), "safe-workspace")


if __name__ == "__main__":
    unittest.main()
