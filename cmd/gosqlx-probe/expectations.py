"""Reviewed corrections to faulty/stale reference expectations, not runtime rules.

All corrections retain the original raw comparison. Duplicate outputs are
specified in SELECT-list order; no expected value is taken from Go output.
"""
from copy import deepcopy


def duplicate_projection(projects=False):
    def column(name, table, field, typ="TEXT"):
        table_name = (f"project{table}." if projects else "") + f"dataset{table}.table{table}"
        return {"name": name, "type": typ, "upstream": [{"table": table_name, "column": field}] if table else []}
    names = ("is_active", "project_credits", "credits_used", "projectname", "projectid", "department", "departmentid") if projects else (
        "is_refunded", "ai_credits", "ai_credits_used", "teamname", "teamid", "organization", "organizationid")
    return ([column(f"col{i}", 1, f"col{i}") for i in range(1, 7)] + [
        column(names[0], 1, "col7", "BOOLEAN"), column(names[1], 0, "", "INT"),
        column(names[2], 5, "col1", "INT"), column(names[3], 6, "col1"), column(names[4], 6, "col2"),
        column("col1", 7, "col1"), column("col2", 7, "col2"),
        column(names[5], 3, "col1"), column(names[6], 3, "col2"),
        column("col3", 7, "col3"), column("col4", 7, "col4"), column("programname", 2, "col1"),
        column("col3", 3, "col3"), column("col4", 3, "col4")])


def reviewed_columns(case, original):
    ident = case["id"]
    if ident in ("lineage/26", "non_selected/10", "python/lineage/25"):
        return duplicate_projection(), True, "Duplicate names refer to distinct qualified inputs by position."
    if ident in ("lineage/27", "non_selected/11", "python/lineage/26"):
        return duplicate_projection(True), True, "Duplicate names refer to distinct qualified inputs by position."
    if ident in ("non_selected/3", "non_selected/4", "non_selected/5"):
        columns = []
        for table, fields in [("orders", [("id", "BIGINT"), ("order_number", "TEXT"), ("customer_id", "BIGINT"), ("shipping_country", "TEXT")]),
                              ("customers", [("id", "BIGINT"), ("name", "TEXT"), ("age", "BIGINT"), ("country", "TEXT")])]:
            columns.extend({"name": name, "type": typ, "upstream": [{"table": table, "column": name}]} for name, typ in fields)
        return columns, True, "JOIN ON star preserves the second table's id, not the first id twice."
    if ident == "quoted":
        return [{"name": "ResultID", "type": "INT", "upstream": [{"table": "Sales.Orders", "column": "OrderID"}]}], True, "Quoted catalog column has declared INT type."
    if ident in ("lineage/31", "lineage/32"):
        columns = deepcopy(original)
        for c in columns:
            if ident == "lineage/31" and c["name"] == "FIRST_DEVICE_TYPE": c["type"] = "TEXT"
            if ident == "lineage/32" and c["name"] == "CREATED_AT": c["type"] = "TIMESTAMPLTZ"
        return columns, False, "Stale fixture types: MIN_BY returns its first argument; Snowflake CURRENT_TIMESTAMP is TIMESTAMPLTZ (also SQLGlot 30.13.0 behavior)."
    return original, False, None


def reviewed_dependencies(case, original):
    refs = {
        "bigquery-qualify": [("customer_id", "my-project.analytics.orders"), ("created_at", "my-project.analytics.orders")],
        "snowflake": [("ID", "RAW.ORDERS"), ("UPDATED_AT", "RAW.ORDERS")],
        "clickhouse": [("active", "orders")],
    }
    if case["id"] not in refs:
        return original, None
    return [{"name": n, "upstream": [{"table": t, "column": n}]} for n, t in refs[case["id"]]], "QUALIFY/PREWHERE affect row selection and must remain non-selected dependencies."
