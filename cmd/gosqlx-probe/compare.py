#!/usr/bin/env python3
"""Compare probe JSONL with an immutable Bruin baseline; no SQL execution.

Usage: python3 compare.py baseline.json results.jsonl > comparison.json
Fixture expectations take precedence. SQLGlot agreement and fixture agreement
are separate measurements. Column ordering is ignored, duplicates/case retained.
"""

import hashlib
import json
import sys
from collections import Counter
from pathlib import Path
from expectations import reviewed_columns, reviewed_dependencies


def normalized(columns, types=False, positional=False):
    values = [
        (
            c["name"],
            c.get("type", "UNKNOWN") if types else "",
            tuple(sorted((s["table"], s["column"]) for s in c["upstream"])),
        )
        for c in columns
    ]
    return values if positional else sorted(values)


def compare(baseline, outputs):
    records = baseline["results"]
    if len(records) != len(outputs):
        raise ValueError(f"row count mismatch: {len(records)} vs {len(outputs)}")
    summary = Counter(cases=len(records))
    checks = {}
    results = []
    for record, output in zip(records, outputs):
        case, python, before = record["case"], record["python"], record["go"]
        success = not output["parse_error"] and not output["lineage_error"]
        summary["before_parse_success"] += not before["parse_error"]
        summary["after_parse_success"] += not output["parse_error"]
        summary["before_lineage_success"] += not before["lineage_error"]
        summary["after_lineage_success"] += success
        summary["unsupported_dialect"] += output["dialect_status"] == "unsupported"
        summary["valid_rejected_by_parser"] += (
            case.get("valid", True)
            and bool(output["parse_error"])
            and output["dialect_status"] != "unsupported"
        )
        detail = {"id": case["id"], "group": case["group"], "checks": {}, "go": output}

        if case.get("valid", True) and not python["parse_error"]:
            passed = not output["parse_error"] and not output.get("tables_error") and output["tables"] == python["tables"]
            count = checks.setdefault("sqlglot_tables", {"pass": 0, "eligible": 0})
            count["pass"] += bool(passed)
            count["eligible"] += 1
            detail["checks"]["sqlglot_tables"] = bool(passed)

        def check(label, actual, expected, include_types=False, positional=False):
            passed = success and normalized(actual, include_types, positional) == normalized(expected, include_types, positional)
            count = checks.setdefault(label, {"pass": 0, "eligible": 0})
            count["pass"] += passed
            count["eligible"] += 1
            detail["checks"][label] = passed
            if not passed:
                detail.setdefault("expected", {})[label] = expected

        if not python["lineage_error"]:
            check("sqlglot_columns", output["columns"], python["columns"])
            check("sqlglot_columns_and_types", output["columns"], python["columns"], True)
            check("sqlglot_non_selected", output["non_selected_columns"], python["non_selected_columns"])
            cols, positional, reason = reviewed_columns(case, python["columns"])
            check("reviewed_columns_and_types", output["columns"], cols, True, positional)
            if reason:
                detail["reviewed_columns"] = {"reason": reason, "positional": positional, "expected": cols}
            deps, reason = reviewed_dependencies(case, python["non_selected_columns"])
            check("reviewed_non_selected", output["non_selected_columns"], deps)
            if reason:
                detail["reviewed_dependencies"] = {"reason": reason, "expected": deps}
        if case["group"] == "lineage" and "expected" in case:
            check("fixture_columns", output["columns"], case["expected"])
            check("fixture_columns_and_types", output["columns"], case["expected"], True)
            cols, positional, reason = reviewed_columns(case, case["expected"])
            check("reviewed_fixture_columns_and_types", output["columns"], cols, True, positional)
            if reason:
                detail["reviewed_fixture_columns"] = {"reason": reason, "positional": positional, "expected": cols}
        if "expected_non_selected" in case:
            check("fixture_non_selected", output["non_selected_columns"], case["expected_non_selected"])
        elif case["group"] == "non_selected":
            # These original Bruin fixtures store independent expectations as
            # flat {name, table} pairs, not output-column lineage.
            grouped = {}
            for ref in case["expected"]:
                grouped.setdefault(ref["name"], []).append(
                    {"table": ref["table"], "column": ref["name"]}
                )
            expected = [{"name": name, "upstream": refs} for name, refs in grouped.items()]
            check("fixture_non_selected", output["non_selected_columns"], expected)
        if case.get("valid") is False:
            summary["invalid_controls"] += 1
            summary["invalid_controls_rejected"] += bool(output["parse_error"])
        results.append(detail)
    return {
        "sqlglot_version": baseline["sqlglot_version"],
        "notes": [
            "before_* values refer to the original native-only probe saved in the immutable baseline, not the later 49-analysis feasibility checkpoint.",
            "All unsupported/failed cases remain in eligible denominators.",
            "Fixtures are independent expectations; SQLGlot-only cases measure agreement, not correctness.",
            "Reviewed comparisons correct explicitly documented reference defects; raw comparisons remain unchanged. These are not full SQLGlot parity.",
            "No output sorting/deduplication is imposed on the analyzer; comparison ignores order only.",
            "Go lineage_ns includes a separate quote-preserving native parse, not only an AST walk.",
        ],
        "summary": dict(summary),
        "checks": checks,
        "results": results,
    }


if __name__ == "__main__":
    baseline_bytes = Path(sys.argv[1]).read_bytes()
    outputs = [json.loads(line) for line in Path(sys.argv[2]).read_text().splitlines()]
    report = compare(json.loads(baseline_bytes), outputs)
    report["baseline_sha256"] = hashlib.sha256(baseline_bytes).hexdigest()
    json.dump(report, sys.stdout, indent=2)
    print()
