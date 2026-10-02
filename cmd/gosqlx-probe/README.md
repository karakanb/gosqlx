# GoSQLX offline comparison probe

This JSON-lines harness measures native APIs. Output-column lineage now uses the
experimental Go `pkg/sql/lineage` package; SQL semantics live in the library, not
the adapter. No Python runtime or dialect fallback is used for analysis.

```sh
go build -o /tmp/gosqlx-lineage-probe ./cmd/gosqlx-probe
mkdir -p bin && cp /tmp/gosqlx-lineage-probe bin/gosqlx-lineage-probe
bin/gosqlx-lineage-probe < corpus.jsonl > results.jsonl
python3 cmd/gosqlx-probe/compare.py baseline.json results.jsonl > comparison.json
python3 cmd/gosqlx-probe/reference.py reference-directory bin/gosqlx-lineage-probe > reference.json
python3 cmd/gosqlx-probe/gap_report.py reference-directory bin/gosqlx-lineage-probe > gaps.json
go test -race -count=1 ./cmd/gosqlx-probe ./pkg/sql/lineage
```

`bin/` is already ignored. The build requires the repository's Go 1.26.1 toolchain.
Each input line is an object with `query` (required string), `dialect` (string),
and `schema` (flat or nested catalog/schema/table objects with ordered column/type leaves).
Additional corpus fields are ignored. Schema is used for lineage; JSON member
order is retained for star expansion. Missing/null schema means an empty catalog.
The adapter folds unquoted Snowflake/Oracle schema names to uppercase, matching
Bruin's schema convention; quote case-sensitive names explicitly in schema keys.
Qualified table keys containing quoted components must already use catalog case
for their unquoted components. The native API takes actual stored catalog names.
Absent/empty dialect selects generic SQL. `postgres` maps to `postgresql`, and
`tsql` to `sqlserver`; names are trimmed and lowercased.

## Output contract and limits

- `parse_error`: empty only when native parsing succeeds; request errors and
  unsupported dialects also appear here. Parsing uses `gosqlx.ParseWithDialect`.
- Schema decoding and normalization run only in the lineage stage, after native
  parsing and reference extraction. Unsupported shapes (arrays, mixed types/objects)
  and normalization collisions produce `lineage_error` with `schema_status: invalid`,
  without changing native results. SQL parse failures leave schema `not_evaluated`.
- `tables`: sorted native `ExtractTableDependencies` results: physical references
  with lexical CTE scoping and write targets included. CTAS returns source and
  destination; CTE aliases and table-function names are not physical tables.
  No extra filtering/reconstruction occurs in the probe.
- `columns`: ordered output columns, each with `name`, `type`, and resolved
  physical `upstream` references; stars expand against schema/scope. Duplicate
  output names remain distinct by position. `non_selected_columns` groups
  predicate/join/group/order/QUALIFY/PREWHERE dependencies by physical column
  name. Schema/query/control errors return no columns. Unsupported output
  expressions retain positions, healthy siblings, and per-column `error` values;
  `lineage_partial` is true and `lineage_error` explicitly reports partial analysis.
  `lineage_warnings` reports unknown types and unavailable catalog information. See
  [native API limits](../../pkg/sql/lineage/README.md).
- `native.table_references` and `native.column_references`: native
  `gosqlx.QualifiedName` objects (`Schema`, `Table`, `Name`), sorted by `String()`.
  Column references mix selected and predicate/join/group/order dependencies.
  Qualifiers may be aliases, and there is no physical-source resolution,
  output-column association, clause attribution, or inferred type. Wildcards
  are not schema-expanded. This is not a separate non-selected lineage result.
- `native.statement_types`: Go AST statement type names in statement order,
  not a read-only or single-select decision.
- `tables_error` and `native_error`: extraction/panic failures, separate from
  parsing success. Recoverable panics are isolated per native stage and query.
  Fatal runtime failures such as OOM or stack overflow are not recoverable.
- `dialect_status`: `native`, `native_generic`, or `unsupported`.
  Invalid requests use `not_evaluated`. `native_dialect` records the actual option.
  Native registration does not imply complete syntax coverage. DuckDB, Spark,
  Databricks, Trino, Athena, Fabric, Doris and StarRocks now have explicit native
  options and tested grammar/casing paths. Unknown names are rejected without parsing.
- `parse_ns`: native tokenization and parsing wall time; excludes JSON decoding
  and AST cleanup. `tables_ns`: native extraction plus sorting/string mapping
  over the already parsed AST. `native_columns_ns`: column-reference extraction
  plus sorting. `lineage_ns` measures schema decoding/folding and `lineage.Analyze`,
  including a separate native parse that preserves identifier quotes, scope
  analysis, and cleanup. Skipped stages have zero timings. Request decoding,
  serialization and process startup are not included; first-call initialization
  can affect timing.

Malformed JSON, SQL errors, and unsupported dialects produce one response and
do not stop the batch. Blank lines produce invalid-request responses. The final
line need not end in a newline. Lines are not subject to Scanner's 64 KiB limit;
the process holds each whole line in memory. I/O failures exit nonzero on stderr.

## Original native Bruin-operation audit (before this experiment)

The following records the base HEAD, not the new experimental lineage package,
query extraction, parser extensions or physical-dependency collector.

| Operation | Native support at inspected HEAD |
| --- | --- |
| Schema-aware output-column lineage | **Absent.** ExtractColumns/ExtractColumnsQualified collect references, not lineage. |
| Predicate/join/group/order dependencies | **Partial.** ExtractColumnsQualified mixes references from these clauses with selected references. No distinct non-selected, resolved dependency graph. |
| Type inference | **Absent for output expressions.** `pkg/schema` stores declared DataType, validates table/column existence and simple INSERT literal categories; this is not SQLGlot-style inferred output typing. |
| Table renaming | **Partial.** `transform.ReplaceTable` supports SELECT/UPDATE/DELETE, not INSERT or DDL. It rewrites selected FROM/JOIN/expression locations; it does not cover all CTE, GROUP BY/HAVING, derived-table, or UPDATE FROM locations. |
| LIMIT insertion | **Yes, limited to SelectStatement.** `transform.SetLimit` inserts/replaces, and rejects non-SELECT statements, including SetOperation roots. `FormatSQLWithDialect` supplies TOP/FETCH/LIMIT rendering. |
| Single-select detection | **AST building blocks only.** Statement count and concrete types are exposed; no dedicated semantic classifier, including a policy for set operations or writing CTEs. |
| Read-only classification | **No dedicated native classifier.** A SELECT root alone is not a safety decision. |
| Transpilation | **Limited.** `transpiler.Transpile` has registered rewrites only for MySQL→PostgreSQL, PostgreSQL→MySQL, PostgreSQL→SQLite. Other pairs silently parse/reformat without semantic rewrites. |
| CTE editing | **AST building blocks only.** `WalkCTEs`, `WithClause.CTEs`, `CommonTableExpr`, and general rewriting; no dedicated CTE add/remove/replace helper. |
| Time freezing | **No dedicated native API.** Would require custom expression rewriting. |

Audit sources: `pkg/gosqlx/{gosqlx,extract,tree,walkers}.go`,
`pkg/sql/keywords/dialect.go`, `pkg/schema/{validator,constraints}.go`,
`pkg/transform/{tables,limit,transform}.go`, and
`pkg/transpiler/{transpiler,dialect_rules}.go`.

## Inspected revision and experiment

Module: `github.com/ajitpratap0/GoSQLX` (the fork keeps the upstream module name).
HEAD: [31b5b1ce22bcf220819bc2f99f253e7b1ffdaeb3](https://github.com/karakanb/gosqlx/commit/31b5b1ce22bcf220819bc2f99f253e7b1ffdaeb3).
Latest changelog version: 1.14.0; this checkout has no exact version tag.
`go version -m bin/gosqlx-lineage-probe` records
`v0.0.0-20260730063804-31b5b1ce22bc+dirty`; dirty reflects this uncommitted harness.
The feature adds no module dependencies. The provided binary is Linux amd64.

## Tested boundary and intentional incompatibilities

The original 71-case corpus now yields **68/68 valid parses and table matches**;
all three invalid controls fail, with no unsupported dialects or panic errors.
The earlier implementation yielded 51/68 parses and 49/68 table matches.

| Check | Earlier subset | Current raw reference | Independently reviewed expectations |
| --- | ---: | ---: | ---: |
| Successful lineage analyses | 49 | 65 | 65 eligible |
| Output source agreement | 46/65 | 58/65 | 65/65, including types |
| Output sources and types | 44/65 | 57/65 | 65/65 |
| Non-selected dependencies | 46/65 | 62/65 | 65/65 |
| Original column/type fixtures | 27/33 | 29/33 | 33/33 |
| Original dependency fixtures | 34/44 | 44/44 | 44/44 |

Raw failures are retained. `expectations.py` contains separate, reviewable
corrections; it is test data, never used by the Go implementation:

- Seven queries repeat output names. Bruin's name-keyed SQLGlot calls assign
  the first name's source to later columns. Go retains distinct positions/sources.
- Quoted PostgreSQL `OrderID` has declared INT type; the baseline reports UNKNOWN.
- QUALIFY/PREWHERE predicates are dependencies; Python drops them in three cases.
- Two original Snowflake fixture types are stale: MIN_BY's string argument has
  TEXT type; CURRENT_TIMESTAMP is TIMESTAMPLTZ. SQLGlot 30.13.0 agrees with Go.

The expanded runner imports the supplied Python/Go fixtures and both pipeline
fixtures, adds the dynamic 101-branch UNION, wide UNION, positional duplicate
outputs and schema-error isolation cases. It reports **92/92 parses**, **28/28
table expectations**, **64/64 eligible analyses**, **58/58 dependency fixtures**,
**3/3 additional positional cases**, and **53/53 reviewed column/type cases**.
Raw column fixtures remain 48/50 (two duplicate-name bugs). Pipeline dependency
sets are raw 5/6, reviewed 6/6: one fixture names `people.updated_at` as a source,
although its SQL and column fixture specify `people.created_at AS updated_at`.
All six expected missing/invalid-schema errors preserve parsing/table results.
Independent Go tests additionally check adversarial scopes, stars, structured
fields, recursive CTEs, set precedence, quoting, query extraction and pool ownership.

Per-output failure isolation is now implemented and tested through public SQL,
including nested/CTE/UNION/scalar/IN/ARRAY propagation, preserved output positions
and healthy siblings. Ambiguity, missing schema and failed control inputs still
fail the whole analysis. Partial results are not counted as successful analyses.
No broad SQLGlot equivalence follows from these fixtures: unavailable UDF bodies,
configurable collations and complete database coercion systems are information
limits. See the native API README. CTAS/INSERT queries require an explicit
`lineage.ExtractQuery` call before analysis; the probe does not silently unwrap
them. Multiple statements are supported by parsing/tables, not single-query lineage.

The baseline is immutable. `compare.py` uses the original sorted fixture contract
for raw comparisons; reviewed duplicate-name cases and new independent tests
compare **positions**, not sets. `reference.py` needs SQLGlot 30.13.0 and pytest
only for offline reference evaluation. The binary itself is Go-only.

## Post-milestone differential ledger

`gap_cases.py` adds 31 persistent cases with independent positional expectations.
`gap_report.py` retains every input, native/Python result, diagnostic and raw
agreement flag. All 31 parse/status checks, 31 table comparisons and 25 independent
output/type/dependency checks pass. These include one intentionally partial output
case; the other six assert explicit analysis errors, not successful lineage.
Raw Python output/type agreement is **16/31**, dependency agreement **12/31**.
Neither denominator drops unsupported cases or incorrect reference outputs.

Implemented since the earlier milestone:

- Mixed comma/explicit JOIN association with separate ON and LATERAL scopes.
- Scalar static PIVOT/UNPIVOT: schema order, aliases, aggregate types, DEFAULT ON
  NULL, implicit groups, pivot-key and null-filter controls.
- MATCH_RECOGNIZE partition/measures output schemas, implicit pattern variables,
  hierarchy/sample clauses, snapshot reads and CHANGES metadata columns.
- Regex/bitwise and quantified comparison inputs; literal/cast naming and string
  function type propagation; explicit partial output diagnostics.

Intentional differences are backed by input SQL, schemas and vendor documentation:
Python drops PIVOT/hierarchy/null-filter controls, generates wrong aliased pivot
headings, treats MATCH_RECOGNIZE like an unchanged input schema, omits CHANGES
metadata fields, and loses one ANY operand. It also accepts an illegal cross-comma
ON reference. These are **not raw parity**. Dynamic PIVOT requires actual data;
unknown table functions require output signatures; custom operators require
bodies. Scalar ClickHouse WITH and ARRAY JOIN examples fail lineage in Python too.
The ledger is a tested boundary, not an exhaustive list of all SQLGlot syntax.
