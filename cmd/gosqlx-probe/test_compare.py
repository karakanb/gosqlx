import copy
import unittest

from compare import normalized
from expectations import reviewed_columns


class ComparisonTests(unittest.TestCase):
    def test_positional_duplicate_names_do_not_collapse(self):
        columns = [
            {"name": "id", "type": "INT", "upstream": [{"table": "a", "column": "x"}]},
            {"name": "id", "type": "BIGINT", "upstream": [{"table": "b", "column": "y"}]},
        ]
        self.assertNotEqual(normalized(columns, True, True), normalized(columns[::-1], True, True))
        self.assertNotEqual(normalized(columns, True, True), normalized([columns[0], columns[0]], True, True))
        self.assertNotEqual(normalized(columns, True), normalized(columns[:1], True))

    def test_review_keeps_original_fixture_unchanged(self):
        original = [{"name": "CREATED_AT", "type": "TIMESTAMP", "upstream": []}]
        before = copy.deepcopy(original)
        corrected, _, reason = reviewed_columns({"id": "lineage/32"}, original)
        self.assertEqual(original, before)
        self.assertEqual(corrected[0]["type"], "TIMESTAMPLTZ")
        self.assertIsNotNone(reason)

    def test_reviewed_sources_follow_qualified_projection_positions(self):
        cols, positional, _ = reviewed_columns({"id": "lineage/26"}, [])
        self.assertTrue(positional)
        self.assertEqual(len(cols), 20)
        self.assertEqual(cols[0]["upstream"], [{"table": "dataset1.table1", "column": "col1"}])
        self.assertEqual(cols[11]["upstream"], [{"table": "dataset7.table7", "column": "col1"}])
        self.assertEqual(cols[18]["upstream"], [{"table": "dataset3.table3", "column": "col3"}])


if __name__ == "__main__":
    unittest.main()
