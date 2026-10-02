package lineage

import (
	"strings"

	"github.com/ajitpratap0/GoSQLX/pkg/sql/ast"
	"github.com/ajitpratap0/GoSQLX/pkg/sql/keywords"
)

// pivot removes consumed aggregate/key columns, retaining implicit group keys
// in schema order, then appends the statically enumerated output columns.
func (a *analyzer) pivot(p *ast.PivotClause, r relation) relation {
	if p.Dynamic || p.ValueQuery != nil {
		a.fail("dynamic PIVOT requires data values unavailable in schema")
		return r
	}
	if r.open {
		a.fail("PIVOT requires a complete input schema")
		return r
	}
	s := &scope{relations: []relation{r}, visible: r.columns}
	key := a.resolve(&ast.Identifier{Name: p.PivotColumn}, s)
	if key.Error != "" {
		a.fail("PIVOT key has failed input lineage")
		return r
	}
	a.dependency(key.Upstream)
	agg := p.AggregateFunction
	suffix := ""
	if alias, ok := agg.(*ast.AliasedExpression); ok {
		suffix = "_" + a.ident(alias.Alias)
		agg = alias.Expr
	}
	if _, ok := agg.(*ast.FunctionCall); !ok {
		a.fail("PIVOT requires an aggregate function")
		return r
	}
	aggregate := a.projectionColumn(agg, s, 0)
	v := value{aggregate.Type, aggregate.Upstream, aggregate.fields}
	outputError := aggregate.Error
	consumed := map[string]bool{a.key(key.Name): true}
	var inputs func(ast.Node)
	inputs = func(n ast.Node) {
		if id, ok := n.(*ast.Identifier); ok && id.Name != "*" {
			consumed[a.key(a.ident(id.Name))] = true
		}
		for _, child := range n.Children() {
			inputs(child)
		}
	}
	inputs(agg)
	cols := []Column{}
	for _, col := range r.columns {
		if !consumed[a.key(col.Name)] {
			if col.Error != "" {
				a.fail("PIVOT grouping column %q has failed input lineage", col.Name)
			}
			cols = append(cols, col)
			a.dependency(col.Upstream) // implicit grouping
		}
	}
	if p.DefaultOnNull != nil {
		// Defaults may use grouping fields but cannot reference the consumed
		// pivot/aggregation inputs. Resolve in the post-grouping scope.
		grouped := r
		grouped.columns = cols
		fallback := a.projectionColumn(p.DefaultOnNull, &scope{relations: []relation{grouped}, visible: cols}, 0)
		v = a.mergeValues(v, value{fallback.Type, fallback.Upstream, fallback.fields})
		outputError = strings.TrimSpace(outputError + " " + fallback.Error)
	}
	if outputError != "" {
		v = value{typ: "UNKNOWN", refs: []Source{}}
	}
	values := p.Values
	if len(values) == 0 {
		for _, name := range p.InValues {
			values = append(values, ast.PivotValue{Name: name})
		}
	}
	for _, entry := range values {
		name := entry.Name
		quotedLiteral := strings.HasPrefix(name, "'") && strings.HasSuffix(name, "'")
		if entry.Alias != "" {
			name = a.ident(entry.Alias) + suffix
		} else if quotedLiteral {
			name = strings.ReplaceAll(name[1:len(name)-1], "''", "'") + suffix
			if a.dialect == keywords.DialectSnowflake {
				name = "'" + name + "'"
			}
		} else {
			name = a.ident(name) + suffix
		}
		cols = append(cols, Column{Name: name, Type: v.typ, Upstream: sources(v.refs), fields: v.fields, Error: outputError})
	}
	r.columns = cols
	return r
}

func (a *analyzer) unpivot(p *ast.UnpivotClause, r relation) relation {
	if r.open {
		a.fail("UNPIVOT requires a complete input schema")
		return r
	}
	s := &scope{relations: []relation{r}, visible: r.columns}
	entries := p.Columns
	if len(entries) == 0 {
		for _, name := range p.InColumns {
			entries = append(entries, ast.PivotValue{Name: name})
		}
	}
	used := map[string]bool{}
	v := value{typ: "NULL"}
	outputError := ""
	for _, entry := range entries {
		name := a.ident(entry.Name)
		if used[a.key(name)] {
			a.fail("duplicate UNPIVOT column %q", name)
		}
		used[a.key(name)] = true
		col := a.resolve(&ast.Identifier{Name: entry.Name}, s)
		outputError = strings.TrimSpace(outputError + " " + col.Error)
		v = a.mergeValues(v, value{col.Type, col.Upstream, col.fields})
	}
	cols := []Column{}
	for _, col := range r.columns {
		if !used[a.key(col.Name)] {
			cols = append(cols, col)
		}
	}
	if !p.IncludeNulls {
		if outputError != "" {
			a.fail("UNPIVOT null filter has failed input lineage")
		}
		a.dependency(v.refs)
	}
	nameType := "VARCHAR"
	if outputError != "" {
		v = value{typ: "UNKNOWN", refs: []Source{}}
		nameType = "UNKNOWN"
	}
	cols = append(cols,
		Column{Name: a.ident(p.NameColumn), Type: nameType, Upstream: sources(v.refs), Error: outputError},
		Column{Name: a.ident(p.ValueColumn), Type: v.typ, Upstream: sources(v.refs), fields: v.fields, Error: outputError})
	r.columns = cols
	return r
}
