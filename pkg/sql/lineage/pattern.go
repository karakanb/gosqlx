package lineage

import (
	"github.com/ajitpratap0/GoSQLX/pkg/sql/ast"
	"strings"
)

// Row-pattern variables identify rows of the same input, not physical tables.
// ONE ROW exposes partition keys and measures; ALL ROWS also exposes input fields.
func (a *analyzer) pattern(m *ast.MatchRecognizeClause, r relation) relation {
	if r.open {
		a.fail("MATCH_RECOGNIZE requires an input schema")
		return r
	}
	s := &scope{relations: []relation{r}, visible: r.columns}
	variables := map[string]bool{}
	for _, name := range m.PatternVariables {
		variables[a.key(a.ident(name))] = true
	}
	for _, def := range m.Definitions {
		name := a.key(a.ident(def.Name))
		if len(m.PatternVariables) > 0 && !variables[name] {
			a.fail("undefined pattern variable %q", name)
		}
		variables[name] = true
	}
	for name := range variables {
		if !a.equal(name, r.name) {
			s.relations = append(s.relations, relation{name: name, columns: r.columns})
		}
	}
	cols := []Column{}
	if strings.HasPrefix(m.RowsPerMatch, "ALL ROWS") {
		cols = append(cols, r.columns...)
	}
	for i, e := range m.PartitionBy {
		col := a.projectionColumn(e, s, i)
		if col.Error != "" {
			a.fail("unsupported MATCH_RECOGNIZE partition: %s", col.Error)
		}
		a.dependency(col.Upstream)
		if !strings.HasPrefix(m.RowsPerMatch, "ALL ROWS") {
			cols = append(cols, col)
		}
	}
	for _, order := range m.OrderBy {
		a.dependency(a.expr(order.Expression, s).refs)
	}
	for _, def := range m.Definitions {
		a.dependency(a.expr(def.Condition, s).refs)
	}
	for i, measure := range m.Measures {
		cols = append(cols, a.projectionColumn(&ast.AliasedExpression{Expr: measure.Expr, Alias: measure.Alias}, s, i))
	}
	if len(cols) == 0 {
		a.fail("MATCH_RECOGNIZE produces no output columns")
	}
	r.columns = cols
	return r
}
