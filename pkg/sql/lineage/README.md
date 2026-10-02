# Experimental native column lineage

```go
import (
    "github.com/ajitpratap0/GoSQLX/pkg/sql/keywords"
    "github.com/ajitpratap0/GoSQLX/pkg/sql/lineage"
)

catalog := lineage.Schema{
    "orders": {
        {Name: "id", Type: "INT"},
        {Name: "amount", Type: "DECIMAL"},
    },
}
result, err := lineage.Analyze(
    "WITH c AS (SELECT * FROM orders) SELECT amount AS total FROM c WHERE id > 3",
    catalog, keywords.DialectPostgreSQL,
)
// Check err before using result; inspect result.Partial and result.Warnings.
// result.Columns: total, DECIMAL, upstream orders.amount
// result.NonSelectedColumns: id, upstream orders.id
```

The analyzer uses the native tokenizer/parser and explicit query scopes. It does
not execute SQL or invoke another language. Schema is an ordered column slice
per table: `*` and `alias.*` expand into output metadata in that order, not into
rewritten SQL. The API returns detached Go values; callers need no pool cleanup.
It is safe for concurrent calls with a read-only schema.

## Resolution and dependency contract

- Unqualified columns must resolve exactly once in the closest visible scope.
  Unknown columns, missing table schemas, duplicate aliases/schema columns,
  ambiguous schema suffixes, and ambiguous projections return errors. A local
  alias shadows an outer alias even when the local relation lacks the column.
- Physical tables, aliases, derived queries, nested CTEs (including positional
  column renaming), lateral queries, and correlated subqueries have separate scopes.
  CTE definitions are analyzed only when referenced. Catalog keys may be
  qualified; unqualified table lookup accepts only a unique matching suffix.
  Source table spelling follows the SQL reference with dialect normalization.
- Recursive UNION CTEs propagate sources to a fixed point, including column
  permutations. Missing anchors, cycles without an anchor, and failure to converge
  within 128 iterations are errors, not incomplete successful results.
- UNION/UNION ALL combine sources **by column position**, preserving output
  names from the left branch. Unequal widths error. Duplicate output names are
  retained, not collapsed into a name-keyed map. EXCEPT/INTERSECT return left
  value sources and record both inputs as row-selection dependencies. Set-level
  ordering resolves output names/ordinals; parenthesized branch limits remain local.
- Unqualified JOIN USING stars emit merged keys first (USING-list order), then
  remaining left and right columns. Merged keys reference both sides, matching
  the Bruin/SQLGlot dependency contract, including outer joins. Qualified stars
  still expose each input relation's individual columns. ON joins do not merge
  equal names. NATURAL derives keys from complete schemas. SEMI/ANTI joins
  contribute right-side filtering dependencies but project only the left side.
- Explicit JOIN chains bind within each comma-separated FROM item. ON cannot
  reference a different comma item; LATERAL inputs can reference preceding items.
  The AST retains this association in `JoinClause.FromIndex` and renderers preserve it.
- Stars preserve input/schema order, including duplicate names. EXCEPT/EXCLUDE
  remove fields; REPLACE changes one uniquely resolved field in place. Struct
  stars expand declared or constructed fields. UNNEST/EXPLODE/POSEXPLODE,
  GENERATE_SERIES and FLATTEN have modeled output schemas; unknown table
  functions fail rather than inventing columns.
- Output references include expression arguments, CASE conditions/results,
  aggregate FILTER/order expressions, and named/inherited window partition/order/frame inputs.
  WHERE, JOIN, GROUP BY, HAVING, ORDER BY, DISTINCT ON, QUALIFY and PREWHERE
  references are returned separately as `NonSelectedColumns`, grouped by
  physical column name. A source can occur in both lists.
- This is **column value lineage**, not row/cardinality lineage: COUNT(*) has
  no column inputs. Scalar-subquery predicates are separate non-selected
  dependencies. EXISTS has BOOLEAN type and no output value sources, while its
  predicates remain non-selected dependencies. Unused CTEs are not a whole-query
  validation target.
- Bare GROUP BY output aliases/ordinals and ORDER BY aliases/ordinals are
  resolved; GROUP BY prefers input names and ORDER BY prefers output aliases.
  Vendor/version-specific alias rules are not a complete SQL validity check.
- Schema, ambiguity, query-shape and control-clause failures return `(nil, error)`.
  An unsupported output expression instead preserves its position/name with
  `Column.Error`, `UNKNOWN`, empty sources and `Result.Partial`. Healthy siblings
  survive. Failed values propagate through derived/CTE outputs, scalar/IN/ARRAY
  subqueries and UNION; using one as a filtering/sorting input is fatal.
  Unknown return types alone use `UNKNOWN` and warnings, not a column error.
- With no catalog, qualified references (or unqualified references with one
  visible table) can be inferred with UNKNOWN types and warnings. A supplied
  catalog must resolve all used relations/columns; schema-less stars fail.

## Table modifiers

Static PIVOT retains implicit grouping columns in schema order and appends the
IN-list headings in order, respecting value/aggregate aliases. Aggregate inputs
are value sources; grouping and pivot keys are separate control dependencies.
DEFAULT ON NULL can use constants or grouping fields, not consumed pivot/aggregate
inputs. UNPIVOT returns remaining fields, then the name and value fields, with
origins from its input columns. Default/EXCLUDE NULLS adds filtering dependencies;
INCLUDE NULLS does not. This models scalar-column PIVOT/UNPIVOT, not every vendor
extension or data-dependent dynamic output schema.

MATCH_RECOGNIZE exposes partition expressions plus measures for ONE ROW (the
default), or input fields plus measures for ALL ROWS. Pattern variables resolve
to the input relation, including variables with implicit TRUE definitions.
Partition/order/DEFINE inputs are controls. Snapshot reads and SQL Server
FOR SYSTEM_TIME preserve stored column origins. Snowflake CHANGES adds its three
documented metadata fields, with no user-column value sources and an explicit
warning. Hierarchy seed/edge predicates are controls; sampling preserves origins.

## Query extraction and table references

`Analyze` accepts exactly one query, not arbitrary writes or statement batches.
`ExtractQuery(sql, dialect)` separately unwraps CREATE TABLE/VIEW AS and INSERT
SELECT, preserves WITH/quoted names, and strips SELECT INTO. Writes without a
query, data-modifying CTEs and multiple statements are errors. Its canonical AST
rendering is not a dialect transpiler or an execution-safety guarantee: functions
can have side effects. Fabric extraction preserves explicit output alias case.

`gosqlx.ExtractTableDependencies(tree, dialect)` returns sorted physical references
with lexical CTE scoping and **includes write targets**, matching Bruin UsedTables.
It supports statement batches and SQL Server USE context. Legacy qualified
reference APIs remain available; they are not schema-resolved lineage APIs.

## Identifiers and types

The public AST normally discards identifier quote metadata. This package keeps
quoted identifier spelling in its privately owned token values before native
parsing, allowing quoted names, aliases, CTE names, escaped quotes and literal
dots to remain distinguishable. It does not change normal parser output.
PostgreSQL/Redshift fold unquoted SQL identifiers down; Snowflake/Oracle fold
them up; quoted identifiers retain exact case. ClickHouse retains case.
MySQL/MariaDB, SQLite, SQL Server, BigQuery, DuckDB, Spark/Databricks,
Trino/Athena, Fabric, Doris/StarRocks and generic lookups are assumed
case-insensitive here; configurable database collations are outside this API.
Fabric and Doris/StarRocks preserve output spelling. BigQuery table source
spelling is preserved. BigQuery/MySQL/MariaDB/Spark/Databricks/Doris/StarRocks double
quotes mean string literals (default SQL modes); ANSI_QUOTES is not modeled.

Native schema names are actual catalog names, so supply uppercase fields for
unquoted Snowflake/Oracle columns. Table keys use dots for qualification; quote
components containing literal dots. Field names are literal strings, not SQL
identifier spellings. `Schema.UnmarshalJSON` accepts Bruin's ordered
`{"table":{"column":"type"}}` form and nested catalog/schema/table objects.
Mixed leaf/object nodes, duplicate keys, malformed types and flattening collisions
are errors. The probe additionally folds its unquoted
Snowflake/Oracle schema keys according to Bruin's schema convention.

Type propagation covers declared primitive types, literals, casts, arithmetic,
CASE, UNION and a bounded set of built-in functions. It returns SQLGlot-style
coarse labels, **not authoritative database result types**: precision, scale,
collation, timezone/session settings, function overloads and dialect-specific
numeric coercion are not fully modeled. Arrays, declared structs/rows and constructed
STRUCT/ROW/NAMED_STRUCT values preserve field types and origins; stored structured
fields depend on their root physical column. Index/CASE/filter controls propagate
to projected fields. JSON operators retain root dependencies. Unmodeled return
types are UNKNOWN. Unknown/UDF functions retain argument dependencies and warn
that their bodies and return types are unavailable. Unaliased computed outputs
use `_col_N` labels; SQLGlot's naming
for every expression form is not promised.

## Remaining limits

Native parsing coverage still limits analysis. The persistent differential ledger
in `cmd/gosqlx-probe/gap_report.py` records exact inputs and both analyzers' results:

- Scalar ClickHouse WITH and ARRAY JOIN examples parse in both but neither
  analyzer resolves their lineage. Go reports explicit analysis errors.
- Dynamic PIVOT (ANY or a value subquery) needs actual data to determine headings;
  Go parses/extracts references but errors rather than inventing an output schema.
  Python's returned empty-name column is not useful star expansion.
- Unknown table functions require a catalog output signature; Python's literal
  `*` UNKNOWN is not expanded schema. Unknown scalar UDFs retain argument origins
  and warn about unavailable bodies/types. Custom operators isolate failed outputs.
- Scope nesting and recursive convergence have limits. Coarse types do not model
  database/session-specific overloads, collations or complete coercion rules.

The ledger retains raw Python disagreements separately from independent checks:
PIVOT aliases, omitted modifier controls, MATCH_RECOGNIZE/CHANGES result schemas,
quantified-comparison inputs, and invalid cross-comma ON scope. It does not count
these disagreements or information-limited analyses as raw parity.
Dialect registration plus passing fixtures does not establish complete dialect
compatibility. This is not universal SQLGlot equivalence, a production-readiness
claim, or a read-only/security classifier.

See `cmd/gosqlx-probe/README.md` for the reproducible corpus/reference comparison, including
intentional differences from the supplied SQLGlot baseline. Independent
adversarial tests cover ambiguous joins, unequal schemas, USING vs ON, renamed
CTE columns, positional UNIONs, nested alias shadowing, quoted identifiers,
schema order, missing schema and concurrent isolation.
