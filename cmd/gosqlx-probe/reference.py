#!/usr/bin/env python3
"""Offline differential reference runner (Python is never used by the Go API).

Usage: reference.py reference_directory probe_binary > report.json
Requires SQLGlot 30.13.0 and pytest solely to import Bruin's original fixtures.
Preserves raw failures; does not rewrite any fixture or baseline expectation.
"""
import json
import subprocess
import sys
from collections import Counter
from pathlib import Path

from compare import normalized
from reference_cases import extra_cases
from expectations import reviewed_columns


def lower_keys(value):
    if isinstance(value, dict):
        return {k.lower(): lower_keys(v) for k, v in value.items()}
    if isinstance(value, list):
        return [lower_keys(v) for v in value]
    return value


def main():
    root, binary = Path(sys.argv[1]).resolve(), Path(sys.argv[2]).resolve()
    sys.path.insert(0, str(root / "pythonsrc"))
    from parser import main as reference
    from parser import main_test as fixtures
    import sqlglot

    assert sqlglot.__version__ == "30.13.0"
    cases = []
    for i, case in enumerate(fixtures.test_cases):
        cases.append(dict(case, id=f"python/lineage/{i}"))
    for i, case in enumerate(fixtures.test_cases_non_selected_columns):
        expected = {}
        for ref in case["expected"]:
            expected.setdefault(ref.name, []).append({"table": ref.table, "column": ref.name})
        cases.append(dict(case, id=f"python/non_selected/{i}", expected=None,
                          expected_non_selected=[{"name": n, "upstream": v} for n, v in expected.items()]))
    exported = subprocess.check_output([
        "go", "run", "cmd/gosqlx-probe/import_reference.go", "--",
        str(root / "pkg/sqlparser/parser_test.go")], text=True)
    exports = json.loads(exported)
    export_errors = []
    for row in exports:
        if "export_error" in row:
            export_errors.append(row)
            continue
        case = {"id": f"go/{row['test']}/{row['index']}", "name": row["name"],
                "query": row.get("query", row.get("sql")), "dialect": row.get("dialect", ""),
                "schema": row.get("schema")}
        if isinstance(row.get("want"), dict):
            want = lower_keys(row["want"])
            case.update(expected=want.get("columns", []), expected_non_selected=want.get("nonselectedcolumns", []))
        else:
            case["expected_tables"] = row["want"]
        cases.append(case)
    cases.extend(extra_cases(root))
    # These non-parametrized tests exercise complete table results, including USE
    # context, CTAS and repeated semicolons. Capture input and independent assert
    # statements by running the original tests against the native adapter.
    table_test_results = []
    def native_tables(query, dialect):
        out = json.loads(subprocess.check_output([str(binary)], input=json.dumps({"query": query, "dialect": dialect}) + "\n", text=True))
        table_test_results.append({"query": query, "dialect": dialect, "go": out})
        assert not out["parse_error"], out["parse_error"]
        return {"tables": out["tables"]}
    fixtures.get_tables = native_tables
    table_tests = {}
    for name in ("test_get_tables", "test_get_tables_trailing_semicolon", "test_get_tables_tsql"):
        try:
            getattr(fixtures, name)()
            table_tests[name] = "pass"
        except Exception as exc:
            table_tests[name] = f"FAIL: {exc}"

    data = "".join(json.dumps({k: c.get(k) for k in ("query", "dialect", "schema")}) + "\n" for c in cases)
    outputs = [json.loads(line) for line in subprocess.check_output([str(binary)], input=data, text=True).splitlines()]
    assert len(outputs) == len(cases)
    counts, results = Counter(), []
    for case, out in zip(cases, outputs):
        checks = {"parse": not out["parse_error"]}
        if "expected_tables" in case:
            checks["fixture_tables"] = checks["parse"] and out["tables"] == case["expected_tables"]
        if "expected_lineage_error" in case:
            checks["expected_analysis_error"] = checks["parse"] and case["expected_lineage_error"] in out["lineage_error"]
        elif "expected_tables" not in case:
            checks["lineage"] = checks["parse"] and not out["lineage_error"]
            if case.get("expected") is not None:
                positional = case.get("positional", False)
                label = "positional_columns" if positional else "fixture_columns"
                checks[label] = checks["lineage"] and normalized(out["columns"], True, positional) == normalized(case["expected"], True, positional)
                cols, corrected_positional, reason = reviewed_columns(case, case["expected"])
                checks["reviewed_columns"] = checks["lineage"] and normalized(out["columns"], True, positional or corrected_positional) == normalized(cols, True, positional or corrected_positional)
                if reason:
                    case["reviewed_columns"] = {"reason": reason, "expected": cols, "positional": corrected_positional}
            if "expected_all_dependencies" in case:
                actual = sorted({(s["table"], s["column"]) for c in out["columns"] + out["non_selected_columns"] for s in c["upstream"]})
                checks["pipeline_all_dependencies"] = checks["lineage"] and actual == case["expected_all_dependencies"]
                checks["reviewed_pipeline_dependencies"] = checks["lineage"] and actual == case.get("reviewed_all_dependencies", case["expected_all_dependencies"])
            else:
                checks["fixture_dependencies"] = checks["lineage"] and normalized(out["non_selected_columns"]) == normalized(case.get("expected_non_selected", []))
        try:
            python = reference.get_column_lineage(case["query"], case.get("schema") or {}, case.get("dialect") or None)
        except Exception as exc:
            python = {"error": str(exc)}
        for check, passed in checks.items():
            counts[check + "/eligible"] += 1
            counts[check + "/pass"] += bool(passed)
        results.append({"case": case, "checks": checks, "go": out, "python": python})
    json.dump({"sqlglot_version": sqlglot.__version__, "checks": counts,
               "export_errors": export_errors, "table_tests": table_tests,
               "export_notes": ["The dynamic Go long-query fixture is recreated explicitly as go/long-query.",
                                "Python monkeypatch tests assert Python call internals; Go tests instead verify batch and per-query failure isolation.",
                                "Original fixtures sort outputs; independent positional cases do not."],
               "table_test_calls": table_test_results, "results": results}, sys.stdout, indent=2)
    print()


if __name__ == "__main__":
    main()
