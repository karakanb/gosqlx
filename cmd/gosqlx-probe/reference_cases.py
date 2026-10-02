"""Independent positional and integration expectations for the offline runner.

These values come from SQL projection positions and checked-in Bruin integration
fixtures, never from Go output. Original reference fixture failures stay visible.
"""
import json


def extra_cases(root):
    selects, schema = [], {}
    for branch in range(4):
        table = f"synthetic.source_{branch}"
        schema[table] = {f"source_{i:02d}": "INT64" for i in range(8)}
        selects.append("SELECT " + ", ".join(
            f"COALESCE(src.source_{i:02d}, 0) + {branch} AS output_{i:02d}"
            for i in range(8)) + f" FROM `{table}` AS src")
    yield {"id": "independent/wide-union", "query": " UNION ALL ".join(selects),
           "schema": schema, "dialect": "bigquery", "positional": True,
           "expected": [{"name": f"output_{i:02d}", "type": "BIGINT", "upstream": [
               {"table": f"synthetic.source_{branch}", "column": f"source_{i:02d}"}
               for branch in range(4)]} for i in range(8)]}
    yield {"id": "independent/duplicate-aliases", "dialect": "bigquery", "positional": True,
           "query": "SELECT src.first_value AS duplicate, src.second_value AS duplicate FROM synthetic.events AS src",
           "schema": {"synthetic.events": {"first_value": "INT64", "second_value": "INT64"}},
           "expected": [{"name": "duplicate", "type": "BIGINT", "upstream": [
               {"table": "synthetic.events", "column": name}]} for name in ("first_value", "second_value")]}
    base = "SELECT * FROM (SELECT * FROM table1) t1 JOIN (SELECT * FROM table2) t2"
    long_query = " UNION ALL ".join([base] * 101)
    yield {"id": "go/long-query", "query": long_query, "dialect": "", "schema": {},
           "expected_lineage_error": "missing schema", "expected_tables": ["table1", "table2"],
           "contract_note": "Bruin skips by length. Native parses, but cannot expand schema-less stars."}
    yield {"id": "independent/long-query-with-schema", "query": long_query, "dialect": "", "positional": True,
           "schema": {"table1": {"z": "INT"}, "table2": {"a": "TEXT"}},
           "expected": [{"name": n, "type": t, "upstream": [{"table": table, "column": n}]}
                        for n, t, table in [("z", "INT", "table1"), ("a", "TEXT", "table2")]]}
    for value in [[], {"table1": {"id": ["INT"]}}, {"table1": {"id": "INT", "mixed": {"x": "INT"}}}]:
        yield {"id": "independent/schema-error/" + json.dumps(value), "query": "SELECT 1 FROM table1",
               "dialect": "postgres", "schema": value, "expected_tables": ["table1"], "expected_lineage_error": "invalid schema"}
    for directory in sorted((root / "integration-tests/test-pipelines").glob("*lineage-pipeline")):
        fixture = json.loads((directory / "expectations/lineage.json").read_text())
        assets = fixture["assets"]
        schema = {a["name"]: {c["name"]: c["type"] for c in a["columns"]} for a in assets}
        for asset in assets:
            case = {"id": f"pipeline/{directory.name}/{asset['name']}",
                    "query": (directory / "assets" / (asset["name"] + ".sql")).read_text(),
                    "schema": schema, "dialect": "duckdb"}
            if asset["name"] == "users":
                # The source is not an asset and has no known schema. Bruin's
                # pipeline layer leaves its explicitly declared output metadata
                # intact; that is not a successful parser lineage expectation.
                case.update(expected_lineage_error="missing schema", expected_tables=["user_data"],
                            contract_note="Declared root asset metadata is not native schema inference.")
            else:
                case["expected"] = [{"name": c["name"], "type": {"integer": "INT"}.get(c["type"], c["type"].upper()),
                                     "upstream": c["upstreams"]} for c in asset["columns"]]
                case["expected_all_dependencies"] = sorted((u["value"], c["name"])
                    for u in asset["upstreams"] for c in u.get("columns", []))
                if directory.name == "parse-asset-lineage-pipeline" and asset["name"] == "example":
                    case["review_note"] = "Fixture upstream lists people.updated_at, but SQL reads people.created_at AS updated_at; its own column fixture agrees."
                    case["reviewed_all_dependencies"] = [("country", "country"), ("country", "id"), ("people", "created_at"),
                                                         ("people", "id"), ("people", "last_name"), ("people", "name")]
            yield case
