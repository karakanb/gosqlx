// Package lineage performs conservative, schema-aware SQL column analysis.
// It expands projection stars in result metadata without rewriting the SQL.
// Output value dependencies and non-selected (filter/join/group/order) dependencies
// are separate. Query/schema errors are fatal; unsupported output expressions
// retain their positions with Column.Error and Result.Partial diagnostics.
package lineage

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ajitpratap0/GoSQLX/pkg/sql/ast"
	"github.com/ajitpratap0/GoSQLX/pkg/sql/keywords"
)

// Field describes a stored column. Names are catalog names, not SQL expressions.
type Field struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// Schema maps qualified table names to columns in physical ordinal order.
// PostgreSQL, Redshift, Snowflake and Oracle catalog names are case-sensitive:
// supply actual stored names (usually lowercase for PostgreSQL, uppercase for
// Snowflake/Oracle). Table keys use dots for qualification; quote a component
// containing a literal dot. Column names are always literal catalog names.
// MySQL, SQLite, SQL Server and BigQuery lookups are case-insensitive here;
// database-specific collation/case settings are not modeled.
type Schema map[string][]Field

type Source struct {
	Table  string `json:"table"`
	Column string `json:"column"`
}

type Column struct {
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Upstream []Source `json:"upstream"`
	fields   []Column // Constructed STRUCT field origins; physical fields use root origins.
	Error    string   `json:"error,omitempty"` // Nonempty: this output could not be analyzed.
}

type Dependency struct {
	Name     string   `json:"name"`
	Upstream []Source `json:"upstream"`
}

type Result struct {
	Columns            []Column     `json:"columns"`
	NonSelectedColumns []Dependency `json:"non_selected_columns"`
	Partial            bool         `json:"partial"` // Inspect Column.Error; positions are preserved.
	// Warnings describe type inference and unavailable UDF/catalog information.
	// UNKNOWN types and argument-only UDF dependencies are never hidden.
	Warnings []string `json:"warnings"`
}

// Analyze parses one SELECT/set-operation query with native GoSQLX APIs and
// resolves output columns against schema. Each call owns its parser and scopes;
// callers may analyze concurrently provided schema is not concurrently mutated.
// DML/DDL must first be explicitly reduced with ExtractQuery. Query shape,
// schema resolution and control-clause failures return (nil, error). Unsupported
// output expressions instead return a result with Partial and Column.Error set.
// A nil error is not SQL validation or complete dialect/type-system equivalence:
// inspect Partial, column errors and Warnings as well.
func Analyze(sql string, schema Schema, dialect keywords.SQLDialect) (*Result, error) {
	if dialect == "" {
		dialect = keywords.DialectGeneric
	}
	tree, err := parseSQL(sql, dialect, nil)
	if err != nil {
		return nil, err
	}
	defer ast.ReleaseAST(tree)
	if len(tree.Statements) != 1 {
		return nil, fmt.Errorf("lineage requires exactly one query, got %d statements", len(tree.Statements))
	}
	a := analyzer{schema: schema, dialect: dialect, deps: map[Source]bool{}, warnings: map[string]bool{}}
	cols := a.query(tree.Statements[0], nil, nil)
	if a.err != nil {
		return nil, a.err
	}
	result := &Result{Columns: cols, NonSelectedColumns: []Dependency{}, Warnings: []string{}}
	byName := map[string][]Source{}
	for ref := range a.deps {
		byName[ref.Column] = append(byName[ref.Column], ref)
	}
	for name, refs := range byName {
		result.NonSelectedColumns = append(result.NonSelectedColumns, Dependency{name, sources(refs)})
	}
	sort.Slice(result.NonSelectedColumns, func(i, j int) bool { return result.NonSelectedColumns[i].Name < result.NonSelectedColumns[j].Name })
	for _, col := range cols {
		if col.Error != "" {
			result.Partial = true
		}
		if col.Type == "UNKNOWN" {
			a.warnings[fmt.Sprintf("type unknown for output %q", col.Name)] = true
		}
	}
	for w := range a.warnings {
		result.Warnings = append(result.Warnings, w)
	}
	sort.Strings(result.Warnings)
	return result, nil
}

type analyzer struct {
	schema   Schema
	dialect  keywords.SQLDialect
	err      error
	depth    int
	deps     map[Source]bool
	warnings map[string]bool
}

func (a *analyzer) fail(format string, args ...any) {
	if a.err == nil || isUnsupported(a.err) {
		a.err = fmt.Errorf(format, args...)
	}
}

func sources(groups ...[]Source) []Source {
	set := map[Source]bool{}
	for _, group := range groups {
		for _, ref := range group {
			set[ref] = true
		}
	}
	out := make([]Source, 0, len(set))
	for ref := range set {
		out = append(out, ref)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Table == out[j].Table {
			return out[i].Column < out[j].Column
		}
		return out[i].Table < out[j].Table
	})
	return out
}

func (a *analyzer) dependency(refs []Source) {
	for _, ref := range refs {
		a.deps[ref] = true
	}
}

func (a *analyzer) insensitive() bool {
	switch a.dialect {
	case keywords.DialectMySQL, keywords.DialectMariaDB, keywords.DialectSQLite, keywords.DialectSQLServer, keywords.DialectBigQuery, keywords.DialectGeneric,
		keywords.DialectDuckDB, keywords.DialectSpark, keywords.DialectDatabricks, keywords.DialectTrino, keywords.DialectAthena, keywords.DialectFabric, keywords.DialectDoris, keywords.DialectStarRocks:
		return true
	}
	return false
}

func (a *analyzer) equal(x, y string) bool {
	if a.insensitive() {
		return strings.EqualFold(x, y)
	}
	return x == y
}

func (a *analyzer) ident(name string) string {
	quoted := len(name) >= 2 && name[0] == '"' && name[len(name)-1] == '"'
	if quoted {
		name = strings.ReplaceAll(name[1:len(name)-1], `""`, `"`)
	}
	if a.insensitive() && !a.preserveCase() {
		return strings.ToLower(name)
	}
	if quoted || a.preserveCase() {
		return name
	}
	if a.dialect == keywords.DialectSnowflake || a.dialect == keywords.DialectOracle {
		return strings.ToUpper(name)
	}
	return strings.ToLower(name)
}

func (a *analyzer) preserveCase() bool {
	return a.dialect == keywords.DialectFabric || a.dialect == keywords.DialectDoris || a.dialect == keywords.DialectStarRocks || a.dialect == keywords.DialectClickHouse
}

func (a *analyzer) key(name string) string {
	if a.insensitive() {
		return strings.ToLower(name)
	}
	return name
}

// nameParts only splits identifier spelling, not SQL syntax. Quoted dots and
// escaped double quotes survive the native parser's qualified-name flattening.
func nameParts(name string) []string {
	parts := []string{}
	start, quoted := 0, false
	for i := 0; i < len(name); i++ {
		if name[i] == '"' {
			if quoted && i+1 < len(name) && name[i+1] == '"' {
				i++
				continue
			}
			quoted = !quoted
		} else if name[i] == '.' && !quoted {
			parts = append(parts, name[start:i])
			start = i + 1
		}
	}
	return append(parts, name[start:])
}

func unquote(name string) string {
	if len(name) >= 2 && name[0] == '"' {
		return strings.ReplaceAll(name[1:len(name)-1], `""`, `"`)
	}
	return name
}

func (a *analyzer) tableName(name string) []string {
	// BigQuery backticks can enclose an entire qualified path.
	if a.dialect == keywords.DialectBigQuery {
		parts := nameParts(name)
		if len(parts) == 1 {
			return strings.Split(unquote(name), ".")
		}
		for i := range parts {
			parts[i] = unquote(parts[i])
		}
		return parts
	}
	parts := nameParts(name)
	for i := range parts {
		parts[i] = a.ident(parts[i])
	}
	return parts
}

func (a *analyzer) table(ref string) []Column {
	if len(a.schema) == 0 {
		return nil
	}
	want := a.tableName(ref)
	var matches []string
	for name := range a.schema {
		parts := nameParts(name)
		if len(want) > len(parts) {
			continue
		}
		match := true
		for i := range want {
			if !a.equal(want[i], unquote(parts[len(parts)-len(want)+i])) {
				match = false
			}
		}
		if match {
			matches = append(matches, name)
		}
	}
	if len(matches) != 1 {
		if len(matches) == 0 {
			a.fail("missing schema for table %q", ref)
		} else {
			a.fail("ambiguous schema for table %q", ref)
		}
		return nil
	}
	cols := []Column{}
	seen := map[string]bool{}
	for _, field := range a.schema[matches[0]] {
		name := field.Name
		if a.insensitive() && !a.preserveCase() {
			name = strings.ToLower(name)
		}
		if seen[a.key(name)] {
			a.fail("duplicate schema column %q in %q", name, ref)
		}
		seen[a.key(name)] = true
		cols = append(cols, Column{Name: name, Type: dataType(field.Type), Upstream: []Source{{strings.Join(want, "."), name}}})
	}
	return cols
}
