package lineage

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/ajitpratap0/GoSQLX/pkg/sql/ast"
)

type relation struct {
	name    string
	columns []Column
	source  string
	open    bool     // Absent catalog: only explicit references can be resolved, never stars.
	refs    []Source // Value-table fields inherit the generator's column dependencies.
}
type scope struct {
	parent     *scope
	ctes       map[string]*cte
	relations  []relation
	visible    []Column
	outputs    []Column
	aliases    bool
	inputFirst bool
	projection bool
	windows    map[string]*ast.WindowSpec
}
type cte struct {
	node      *ast.CommonTableExpr
	env       map[string]*cte
	columns   []Column
	active    bool
	recursive bool
}

func (a *analyzer) with(with *ast.WithClause, inherited map[string]*cte) map[string]*cte {
	env := map[string]*cte{}
	for name, c := range inherited {
		env[name] = c
	}
	if with == nil {
		return env
	}
	seen := map[string]bool{}
	for _, node := range with.CTEs {
		if node.ScalarExpr != nil {
			a.fail("unsupported scalar CTE")
			continue
		}
		name := a.key(a.ident(node.Name))
		if seen[name] {
			a.fail("duplicate CTE %q", name)
		}
		seen[name] = true
		if existing := env[name]; existing != nil && existing.node == node {
			continue
		}
		binding := &cte{node: node, recursive: with.Recursive}
		env[name] = binding
		binding.env = map[string]*cte{}
		for key, value := range env {
			binding.env[key] = value
		}
	}
	return env
}

func (a *analyzer) query(stmt ast.Statement, parent *scope, env map[string]*cte) []Column {
	if a.err != nil && !isUnsupported(a.err) {
		return nil
	}
	a.depth++
	defer func() { a.depth-- }()
	if a.depth > 128 {
		a.fail("lineage scope depth exceeds 128")
		return nil
	}
	switch q := stmt.(type) {
	case *ast.SelectStatement:
		return a.selectQuery(q, parent, a.with(q.With, env))
	case *ast.SetOperation:
		env = a.with(q.With, env)
		left := a.query(q.Left, parent, env)
		right := a.query(q.Right, parent, env)
		if len(left) != len(right) {
			a.fail("set operation has unequal column counts (%d and %d)", len(left), len(right))
			return nil
		}
		out := append([]Column{}, left...)
		for i := range out {
			if left[i].Error != "" || right[i].Error != "" {
				if !strings.EqualFold(q.Operator, "UNION") {
					a.fail("set filter uses a failed output column")
					return nil
				}
				out[i].Type, out[i].Upstream, out[i].fields = "UNKNOWN", []Source{}, nil
				out[i].Error = strings.TrimSpace(left[i].Error + " " + right[i].Error)
				continue
			}
			if strings.EqualFold(q.Operator, "UNION") {
				v := a.mergeValues(value{left[i].Type, left[i].Upstream, left[i].fields}, value{right[i].Type, right[i].Upstream, right[i].fields})
				out[i].Type, out[i].Upstream, out[i].fields = v.typ, v.refs, v.fields
			} else {
				// INTERSECT/EXCEPT filter left-hand rows by right-hand values.
				// They do not generate output values from the right input.
				a.dependency(sources(left[i].Upstream, right[i].Upstream))
				out[i].Type = commonType(left[i].Type, right[i].Type)
			}
		}
		resultScope := &scope{outputs: out, aliases: true}
		for _, ob := range q.OrderBy {
			a.clause(ob.Expression, resultScope, true)
		}
		return out
	default:
		a.fail("unsupported lineage statement %T", stmt)
		return nil
	}
}

func (a *analyzer) relation(ref ast.TableReference, s *scope) relation {
	r := relation{name: a.ident(ref.Alias)}
	if ref.Subquery != nil || ref.Query != nil {
		if ref.Alias == "" {
			a.fail("derived table requires an alias")
		}
		var outer *scope
		if ref.Lateral {
			outer = s
		}
		var query ast.Statement = ref.Query
		if ref.Subquery != nil {
			query = ref.Subquery
		}
		r.columns = a.query(query, outer, s.ctes)
	} else if ref.TableFunc != nil {
		r = a.tableFunction(ref, s)
	} else {
		parts := a.tableName(ref.Name)
		if r.name == "" {
			r.name = parts[len(parts)-1]
			if a.insensitive() {
				r.name = strings.ToLower(r.name)
			}
		}
		binding := s.ctes[a.key(a.ident(ref.Name))]
		if len(parts) == 1 && binding != nil {
			r.columns = a.cteColumns(binding)
		} else {
			r.columns = a.table(ref.Name)
			r.source = strings.Join(a.tableName(ref.Name), ".")
			r.open = len(a.schema) == 0
		}
	}
	outputName := r.name
	if ref.ModifierInputAlias != "" {
		r.name = a.ident(ref.ModifierInputAlias)
	} else if ref.Name != "" && (ref.Pivot != nil || ref.Unpivot != nil || ref.MatchRecognize != nil) {
		parts := a.tableName(ref.Name)
		r.name = parts[len(parts)-1]
	}
	if ref.TimeTravel != nil {
		a.temporal(ref.TimeTravel, s, &r)
	}
	if ref.Pivot != nil {
		r = a.pivot(ref.Pivot, r)
	}
	if ref.Unpivot != nil {
		r = a.unpivot(ref.Unpivot, r)
	}
	if ref.MatchRecognize != nil {
		r = a.pattern(ref.MatchRecognize, r)
	}
	r.name = outputName
	if ref.ForSystemTime != nil {
		for _, e := range []ast.Expression{ref.ForSystemTime.Point, ref.ForSystemTime.Start, ref.ForSystemTime.End} {
			a.dependency(a.expr(e, s).refs)
		}
	}
	if len(ref.Columns) > 0 {
		if len(ref.Columns) > len(r.columns) {
			a.fail("table column alias count exceeds width")
			return r
		}
		r.columns = append([]Column{}, r.columns...)
		for i, name := range ref.Columns {
			r.columns[i].Name = a.ident(name)
		}
	}
	for _, existing := range s.relations {
		if a.equal(existing.name, r.name) {
			a.fail("duplicate relation alias %q", r.name)
		}
	}
	return r
}

func (a *analyzer) selectQuery(q *ast.SelectStatement, parent *scope, env map[string]*cte) []Column {
	if q.ArrayJoin != nil {
		a.fail("unsupported ARRAY JOIN")
		return nil
	}
	s := &scope{parent: parent, ctes: env, visible: []Column{}, windows: map[string]*ast.WindowSpec{}}
	for i := range q.Windows {
		name := a.key(a.ident(q.Windows[i].Name))
		if s.windows[name] != nil {
			a.fail("duplicate named window %q", name)
		}
		s.windows[name] = &q.Windows[i]
	}
	for i, ref := range q.From {
		r := a.relation(ref, s)
		group := &scope{parent: parent, ctes: env, relations: []relation{r}, visible: r.columns}
		for _, join := range q.Joins {
			if join.FromIndex == i {
				a.join(join, group, s)
			}
		}
		for _, joined := range group.relations[1:] {
			for _, prior := range s.relations {
				if a.equal(joined.name, prior.name) {
					a.fail("duplicate relation alias %q", joined.name)
				}
			}
		}
		s.relations = append(s.relations, group.relations...)
		s.visible = append(s.visible, group.visible...)
	}
	outputs := []Column{}
	s.projection = true
	for i, e := range q.Columns {
		if star, ok := e.(*ast.StarExpression); ok {
			outputs = append(outputs, a.star(star, s)...)
			continue
		}
		if id, ok := e.(*ast.Identifier); ok && id.Name == "*" {
			outputs = append(outputs, a.star(&ast.StarExpression{Table: id.Table}, s)...)
			continue
		}
		outputs = append(outputs, a.projectionColumn(e, s, i))
	}
	s.projection = false
	s.outputs = outputs
	for _, expr := range []ast.Expression{q.Where, q.PrewhereClause} {
		a.dependency(a.expr(expr, s).refs)
	}
	a.dependency(a.expr(q.StartWith, s).refs)
	if q.ConnectBy != nil {
		a.dependency(a.expr(q.ConnectBy.Condition, s).refs)
	}
	// Alias/ordinal rules vary by dialect. GROUP BY prefers input names over
	// output aliases; ORDER BY prefers output aliases. WHERE never sees aliases.
	s.aliases, s.inputFirst = true, true
	for _, e := range q.GroupBy {
		a.clause(e, s, true)
	}
	a.clause(q.Having, s, false)
	s.inputFirst = false
	for _, ob := range q.OrderBy {
		a.clause(ob.Expression, s, true)
	}
	a.clause(q.Qualify, s, false)
	for _, e := range q.DistinctOnColumns {
		a.clause(e, s, false)
	}
	return outputs
}

func (a *analyzer) clause(e ast.Expression, s *scope, ordinal bool) {
	if ordinal {
		if lit, ok := e.(*ast.LiteralValue); ok && strings.EqualFold(lit.Type, "int") {
			n, err := strconv.Atoi(fmt.Sprint(lit.Value))
			if err != nil || n < 1 || n > len(s.outputs) {
				a.fail("output ordinal %v out of range", lit.Value)
				return
			}
			if s.outputs[n-1].Error != "" {
				a.fail("control clause uses failed output: %s", s.outputs[n-1].Error)
			}
			a.dependency(s.outputs[n-1].Upstream)
			return
		}
	}
	a.dependency(a.expr(e, s).refs)
}

func (a *analyzer) find(cols []Column, name string) (Column, int) {
	var result Column
	count := 0
	for _, col := range cols {
		if a.equal(col.Name, name) {
			result = col
			count++
		}
	}
	return result, count
}

func (a *analyzer) resolve(id *ast.Identifier, s *scope) Column {
	if col, ok := a.resolvePath(id, s); ok {
		return col
	}
	name, table := a.ident(id.Name), a.ident(id.Table)
	for current := s; current != nil; current = current.parent {
		var col Column
		var n int
		if table != "" {
			for _, r := range current.relations {
				if a.equal(r.name, table) {
					col, n = a.find(r.columns, name)
					if n == 0 && r.open {
						return a.inferredColumn(r, name)
					}
					if n != 1 {
						a.fail("column %q resolves %d times in relation %q", name, n, table)
					}
					return col // A local alias shadows outer aliases even if the column is absent.
				}
			}
			continue
		}
		if current == s && current.aliases && !current.inputFirst {
			col, n = a.find(current.outputs, name)
		}
		if n == 0 {
			col, n = a.find(current.visible, name)
		}
		if n == 0 {
			for _, r := range current.relations {
				if r.open {
					col = a.inferredColumn(r, name)
					n++
				}
			}
		}
		if n == 0 && current == s && current.aliases {
			col, n = a.find(current.outputs, name)
		}
		if n > 1 {
			a.fail("ambiguous column %q", name)
			return Column{}
		}
		if n == 1 {
			return col
		}
	}
	a.fail("unresolved column %q (qualifier %q)", name, table)
	return Column{}
}

func (a *analyzer) inferredColumn(r relation, name string) Column {
	a.warnings["schema unavailable: explicit references resolved syntactically; column existence and types are unverified"] = true
	refs := r.refs
	if r.source != "" {
		refs = []Source{{Table: r.source, Column: name}}
	}
	return Column{Name: name, Type: "UNKNOWN", Upstream: sources(refs)}
}
