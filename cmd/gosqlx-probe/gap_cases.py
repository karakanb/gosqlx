"""Independent, positional expectations for exclusions discovered after milestone 1.

Raw Python results are retained by gap_report.py, including its wrong output
schemas/names and missing control dependencies. No values come from Go output.
"""


def col(name, typ, table="", *fields):
    return {"name": name, "type": typ, "upstream": [{"table": table, "column": f} for f in fields]}


def deps(table, *fields):
    return [{"name": f, "upstream": [{"table": table, "column": f}]} for f in fields]


def cases():
    sales = {"sales": {"product": "varchar", "quarter": "varchar", "amount": "int"}}
    product = col("PRODUCT", "VARCHAR", "SALES", "PRODUCT")
    for name, aggregate, values, outputs, typ in [
        ("pivot-sum", "SUM(amount)", "'Q1', 'Q2'", ["'Q1'", "'Q2'"], "BIGINT"),
        ("pivot-avg-alias", "AVG(amount) AS avg", "'Q1' AS first, 'Q2' AS second", ["FIRST_AVG", "SECOND_AVG"], "DOUBLE"),
        ("pivot-max-quoted", "MAX(amount)", "'Q1' AS \"MiX\", 'Q2' AS last", ["MiX", "LAST"], "INT"),
        ("pivot-sum-suffix", "SUM(amount) AS total", "'Q1', 'Q2'", ["'Q1_TOTAL'", "'Q2_TOTAL'"], "BIGINT"),
    ]:
        yield {"id": "gap/" + name, "dialect": "snowflake", "schema": sales,
               "query": f"SELECT * FROM sales PIVOT({aggregate} FOR quarter IN ({values})) AS p",
               "expected": [product] + [col(n, typ, "SALES", "AMOUNT") for n in outputs],
               "expected_non_selected": deps("SALES", "PRODUCT", "QUARTER"),
               "review_note": "Snowflake PIVOT docs: value alias replaces value; aggregate suffix appended; remaining columns are implicit groups. Group keys and pivot key are controls, not aggregated value sources."}
    yield {"id": "gap/pivot-default", "dialect": "snowflake", "schema": sales,
           "query": "SELECT * FROM sales PIVOT(SUM(amount) FOR quarter IN ('Q1','Q2') DEFAULT ON NULL (0)) p",
           "expected": [product, col("'Q1'", "BIGINT", "SALES", "AMOUNT"), col("'Q2'", "BIGINT", "SALES", "AMOUNT")],
           "expected_non_selected": deps("SALES", "PRODUCT", "QUARTER")}
    for modifier in ["", "INCLUDE NULLS ", "EXCLUDE NULLS "]:
        yield {"id": "gap/unpivot-" + (modifier.strip() or "default"), "dialect": "snowflake",
               "schema": {"sales": {"product": "varchar", "q1": "int", "q2": "int"}},
               "query": "SELECT * FROM sales UNPIVOT " + modifier + "(amount FOR quarter IN(q1 AS 'first',q2 AS 'second')) AS p",
               "expected": [product, col("QUARTER", "VARCHAR", "SALES", "Q1", "Q2"), col("AMOUNT", "INT", "SALES", "Q1", "Q2")],
               "expected_non_selected": [] if modifier == "INCLUDE NULLS " else deps("SALES", "Q1", "Q2"),
               "review_note": "EXCLUDE NULLS filters on source values; INCLUDE NULLS does not. Name/value origins include both source columns; aliases label rows, not new physical sources."}
    joins = {"a": {"id": "int"}, "b": {"id": "int"}, "c": {"id": "int", "value": "text"}}
    for i, sql in enumerate([
        "SELECT a.id,c.value FROM a, b JOIN c ON b.id=c.id",
        "SELECT a.id,c.value FROM b JOIN c ON b.id=c.id, a",
    ]):
        yield {"id": f"gap/comma-join-{i}", "dialect": "postgres", "schema": joins, "query": sql,
               "expected": [col("id", "INT", "a", "id"), col("value", "TEXT", "c", "value")],
               "expected_non_selected": [{"name": "id", "upstream": [{"table": "b", "column": "id"}, {"table": "c", "column": "id"}]}]}
    yield {"id": "gap/illegal-on-scope", "dialect": "postgres", "schema": joins,
           "query": "SELECT a.id FROM a,b JOIN c ON a.id=c.id", "expected_lineage_error": "unresolved column",
           "review_note": "PostgreSQL ON cannot reference another comma item; SQLGlot accepts this invalid scope."}
    for name, dialect, sql in [
        ("sample", "clickhouse", "SELECT id FROM sales SAMPLE 0.1"),
        ("tablesample", "postgres", "SELECT id FROM sales TABLESAMPLE SYSTEM(10)"),
        ("snapshot", "snowflake", "SELECT id FROM sales AT (TIMESTAMP => '2020-01-01'::TIMESTAMP)"),
        ("system-time", "tsql", "SELECT s.id FROM sales FOR SYSTEM_TIME AS OF '2020-01-01' AS s"),
    ]:
        upper = dialect == "snowflake"
        yield {"id": "gap/" + name, "dialect": dialect, "schema": {"sales": {"id": "int"}}, "query": sql,
               "expected": [col("ID" if upper else "id", "INT", "SALES" if upper else "sales", "ID" if upper else "id")]}
    yield {"id": "gap/hierarchy", "dialect": "oracle", "schema": {"sales": {"id": "int", "parent_id": "int"}},
           "query": "SELECT id FROM sales START WITH parent_id IS NULL CONNECT BY PRIOR id=parent_id",
           "expected": [col("ID", "INT", "SALES", "ID")], "expected_non_selected": deps("SALES", "ID", "PARENT_ID"),
           "review_note": "Hierarchy seed and edge predicates select rows; Python omits these controls."}
    for rows in ["ONE ROW PER MATCH", "ALL ROWS PER MATCH"]:
        output = [product]
        if rows.startswith("ALL"):
            output += [col("TS", "TIMESTAMP", "SALES", "TS"), col("AMOUNT", "INT", "SALES", "AMOUNT")]
        output += [col("FIRST_AMOUNT", "INT", "SALES", "AMOUNT")]
        yield {"id": "gap/pattern-" + rows, "dialect": "snowflake",
               "schema": {"sales": {"product": "varchar", "ts": "timestamp", "amount": "int"}},
               "query": "SELECT * FROM sales MATCH_RECOGNIZE (PARTITION BY product ORDER BY ts MEASURES FIRST(UP.amount) AS first_amount " + rows + " PATTERN (UP+) DEFINE UP AS amount>0) AS m",
               "expected": output, "expected_non_selected": deps("SALES", "PRODUCT", "TS", "AMOUNT"),
               "review_note": "ONE ROW outputs partition keys+measures; ALL ROWS adds input columns. Python currently ignores MATCH_RECOGNIZE's output schema."}
    yield {"id": "gap/unnamed", "dialect": "postgres", "schema": {"sales": {"product": "varchar", "amount": "int"}},
           "query": "SELECT amount+1,UPPER(product),CAST(amount AS TEXT),42,TRUE FROM sales",
           "expected": [col("_col_0", "INT", "sales", "amount"), col("_col_1", "VARCHAR", "sales", "product"), col("amount", "TEXT", "sales", "amount"), col("42", "INT"), col("_col_4", "BOOLEAN")]}
    yield {"id": "gap/regex-bitwise", "dialect": "postgres", "schema": {"sales": {"product": "varchar", "amount": "int"}},
           "query": "SELECT product ~ '^Q' AS yes, amount & 3 AS bits, amount << 2 AS shifted FROM sales",
           "expected": [col("yes", "BOOLEAN", "sales", "product"), col("bits", "INT", "sales", "amount"), col("shifted", "INT", "sales", "amount")]}
    yield {"id": "gap/quantifier", "dialect": "postgres", "schema": joins,
           "query": "SELECT a.id = ANY(SELECT b.id FROM b) AS matched FROM a",
           "expected": [{"name": "matched", "type": "BOOLEAN", "upstream": [{"table": "a", "column": "id"}, {"table": "b", "column": "id"}]}],
           "review_note": "Comparison output depends on both operands; Python's rewritten scope loses a.id and adds artificial non-selected dependencies."}
    yield {"id": "gap/unknown-function", "dialect": "postgres", "schema": {"a": {"name": "varchar"}},
           "query": "SELECT mystery(name) AS result FROM a", "expected": [col("result", "UNKNOWN", "a", "name")],
           "review_note": "Argument-only lineage and UNKNOWN type; UDF body unavailable."}
    yield {"id": "gap/partial-operator", "dialect": "postgres", "schema": {"a": {"id": "int", "name": "varchar"}},
           "query": "SELECT id,id OPERATOR(public.===) id AS bad,name FROM a WHERE id>0", "expected_lineage_error": "partial analysis",
           "expected_partial": True, "expected": [col("id", "INT", "a", "id"), col("bad", "UNKNOWN"), col("name", "VARCHAR", "a", "name")], "expected_output_errors": [1],
           "expected_non_selected": deps("a", "id"), "review_note": "Custom operator definition unavailable; retain siblings without inventing semantics/type."}
    for values in ["ANY ORDER BY quarter", "SELECT quarter FROM quarters"]:
        yield {"id": "gap/dynamic-pivot-" + values, "dialect": "snowflake", "schema": sales,
               "query": "SELECT * FROM sales PIVOT(SUM(amount) FOR quarter IN (" + values + "))",
               "expected_lineage_error": "dynamic PIVOT requires data values",
               "review_note": "Schema cannot determine data-dependent headings. Python returns a fabricated empty-name column for ANY; Go parses/extracts tables but explicitly rejects lineage."}
    yield {"id": "gap/unknown-table-function", "dialect": "snowflake", "schema": {},
           "query": "SELECT * FROM TABLE(mystery(1)) AS u", "expected_lineage_error": "table function",
           "review_note": "Missing output signature. Python's literal '*' UNKNOWN with no sources is not an expanded schema or meaningful lineage."}
    for projection in ["id", "*"]:
        output = [col("ID", "INT", "SALES", "ID")]
        if projection == "*":
            output += [col("METADATA$ACTION", "VARCHAR"), col("METADATA$ISUPDATE", "BOOLEAN"), col("METADATA$ROW_ID", "VARCHAR")]
        yield {"id": "gap/changes-" + projection, "dialect": "snowflake", "schema": {"sales": {"id": "int"}},
               "query": "SELECT " + projection + " FROM sales CHANGES(INFORMATION => DEFAULT) AT(OFFSET => -60) END(OFFSET => -1)",
               "expected": output, "review_note": "Snowflake CHANGES docs add three metadata columns. They have no user-column value inputs; native reports a warning, while Python drops them from star expansion."}
    yield {"id": "gap/pattern-implicit", "dialect": "snowflake",
           "schema": {"sales": {"product": "varchar", "ts": "timestamp", "amount": "int"}},
           "query": "SELECT * FROM sales MATCH_RECOGNIZE (PARTITION BY product ORDER BY ts MEASURES FIRST(A.amount) AS first_amount ONE ROW PER MATCH AFTER MATCH SKIP PAST LAST ROW PATTERN (A+)) AS m",
           "expected": [product, col("FIRST_AMOUNT", "INT", "SALES", "AMOUNT")], "expected_non_selected": deps("SALES", "PRODUCT", "TS"),
           "review_note": "Undeclared pattern variables have implicit TRUE conditions, not missing physical schemas; Python mistakes A for a physical table."}
    for name, query, schema, error in [
        ("scalar-with", "WITH 2 AS factor SELECT amount*factor AS total FROM sales", {"sales": {"amount": "int"}}, "scalar CTE"),
        ("array-join", "SELECT id,item FROM sales ARRAY JOIN items AS item", {"sales": {"id": "int", "items": "Array(Int32)"}}, "ARRAY JOIN"),
    ]:
        yield {"id": "gap/" + name, "dialect": "clickhouse", "schema": schema, "query": query, "expected_lineage_error": error,
               "review_note": "Both analyzers reject lineage for this precise form; SQLGlot syntax acceptance alone is not Bruin lineage support."}


def gap_cases():
    for case in cases():
        case["positional"] = True
        yield case
