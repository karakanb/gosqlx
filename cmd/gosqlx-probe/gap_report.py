#!/usr/bin/env python3
"""Differential ledger: gap_report.py reference-directory probe > ledger.json.

Independent checks are positional, raw Python checks use its sorted contract.
Every input and both full outputs remain in the report, including known errors.
"""
import json
import subprocess
import sys
from collections import Counter
from pathlib import Path

from compare import normalized
from gap_cases import gap_cases


def main():
    root, binary = Path(sys.argv[1]).resolve(), Path(sys.argv[2]).resolve()
    sys.path.insert(0, str(root / "pythonsrc"))
    from parser.main import get_column_lineage, get_tables
    import sqlglot
    assert sqlglot.__version__ == "30.13.0"
    cases = list(gap_cases())
    outputs = [json.loads(row) for row in subprocess.check_output(
        [str(binary)], input="".join(json.dumps(c) + "\n" for c in cases), text=True).splitlines()]
    assert len(cases) == len(outputs)
    counts, results = Counter(), []
    for case, out in zip(cases, outputs):
        try:
            python = get_column_lineage(case["query"], case["schema"], case["dialect"])
        except Exception as exc:
            python = {"errors": [str(exc)], "columns": [], "non_selected_columns": []}
        try:
            tables = get_tables(case["query"], case["dialect"])
        except Exception as exc:
            tables = {"error": str(exc)}
        expected_error = case.get("expected_lineage_error")
        checks = {"parse": not out["parse_error"],
                  "analysis_status": expected_error in out["lineage_error"] if expected_error else not out["lineage_error"],
                  "partial_status": out["lineage_partial"] == case.get("expected_partial", False)}
        if "expected" in case:
            checks["columns"] = normalized(out["columns"], True, True) == normalized(case["expected"], True, True)
            checks["dependencies"] = normalized(out["non_selected_columns"]) == normalized(case.get("expected_non_selected", []))
            checks["column_errors"] = [i for i, c in enumerate(out["columns"]) if c.get("error")] == case.get("expected_output_errors", [])
        raw = {"tables": not out["parse_error"] and not out["tables_error"] and out["tables"] == sorted(tables.get("tables", [])),
               "columns_types": not python.get("errors") and not out["lineage_error"] and normalized(out["columns"], True) == normalized(python["columns"], True),
               "dependencies": not python.get("errors") and not out["lineage_error"] and normalized(out["non_selected_columns"]) == normalized(python["non_selected_columns"])}
        for label, passed in checks.items():
            counts[label + "/total"] += 1
            counts[label + "/pass"] += bool(passed)
        for label, passed in raw.items():
            counts["raw_" + label + "/total"] += 1
            counts["raw_" + label + "/pass"] += bool(passed)
        results.append({"case": case, "checks": checks, "raw_agreement": raw, "go": out, "python": python, "python_tables": tables})
    json.dump({"sqlglot_version": sqlglot.__version__, "checks": counts,
               "notes": ["Independent expectations retain output order and duplicates. Raw Python failures/differences are not removed.",
                         "PIVOT aliases: https://docs.snowflake.com/en/sql-reference/constructs/pivot",
                         "MATCH_RECOGNIZE result schema: https://docs.snowflake.com/en/sql-reference/constructs/match_recognize",
                         "CHANGES metadata: https://docs.snowflake.com/en/sql-reference/constructs/changes"],
               "results": results}, sys.stdout, indent=2)
    print()
    if any(not all(r["checks"].values()) for r in results):
        sys.exit(1)


if __name__ == "__main__":
    main()
