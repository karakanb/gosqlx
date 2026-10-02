package lineage

import (
	"fmt"
	"github.com/ajitpratap0/GoSQLX/pkg/sql/ast"
	"strings"
)

type unsupportedExpression struct{ message string }

func (e *unsupportedExpression) Error() string { return e.message }
func isUnsupported(err error) bool             { _, ok := err.(*unsupportedExpression); return ok }
func (a *analyzer) unsupported(format string, args ...any) {
	if a.err == nil {
		a.err = &unsupportedExpression{fmt.Sprintf(format, args...)}
	}
}

// Only expression-analysis failures can be isolated. Resolution, schema, query
// shape and control-clause failures remain fatal, even after an unsupported node.
func (a *analyzer) projectionColumn(e ast.Expression, s *scope, position int) Column {
	prior := a.err
	if isUnsupported(prior) {
		a.err = nil
	}
	v := a.expr(e, s)
	col := Column{Name: a.outputName(e, position), Type: v.typ, Upstream: sources(v.refs), fields: v.fields}
	if isUnsupported(a.err) {
		col.Type, col.Upstream, col.fields, col.Error = "UNKNOWN", []Source{}, nil, a.err.Error()
		a.err = nil
	}
	if a.err == nil {
		a.err = prior
	}
	return col
}

func (a *analyzer) outputName(e ast.Expression, position int) string {
	switch e := e.(type) {
	case *ast.AliasedExpression:
		return a.ident(e.Alias)
	case *ast.Identifier:
		return a.ident(e.Name)
	case *ast.FieldAccess:
		return a.ident(e.Name)
	case *ast.CastExpression:
		return a.outputName(e.Expr, position)
	case *ast.LiteralValue:
		if e.Type == "int" || e.Type == "float" || e.Type == "string" {
			return fmt.Sprint(e.Value)
		}
	}
	return fmt.Sprintf("_col_%d", position)
}

func (a *analyzer) star(star *ast.StarExpression, s *scope) []Column {
	var cols []Column
	for _, r := range s.relations {
		if r.open && (star.Table == "" || a.equal(r.name, a.ident(star.Table))) {
			a.fail("missing schema for star expansion on %q", r.name)
		}
	}
	if star.Table == "" {
		if len(s.relations) == 0 {
			a.fail("star requires a FROM relation")
		}
		cols = append(cols, s.visible...)
	} else {
		found := false
		for _, r := range s.relations {
			if a.equal(r.name, a.ident(star.Table)) {
				cols = append(cols, r.columns...)
				found = true
			}
		}
		if !found {
			parts := nameParts(star.Table)
			var root Column
			var n int
			if len(parts) == 1 {
				root, n = a.find(s.visible, a.ident(parts[0]))
			} else {
				var ok bool
				root, ok = a.resolvePath(&ast.Identifier{Table: strings.Join(parts[:len(parts)-1], "."), Name: parts[len(parts)-1]}, s)
				if ok {
					n = 1
				}
			}
			if n != 1 {
				a.fail("unknown star qualifier %q", star.Table)
			} else {
				cols = append(cols, a.fieldColumns(root)...)
				if len(cols) == 0 {
					a.fail("star qualifier %q has no struct fields", star.Table)
				}
			}
		}
	}
	for _, raw := range star.Exclude {
		name := a.ident(raw)
		_, n := a.find(cols, name)
		if n == 0 {
			a.fail("excluded column %q does not exist", name)
		}
		filtered := []Column{}
		for _, c := range cols {
			if !a.equal(c.Name, name) {
				filtered = append(filtered, c)
			}
		}
		cols = filtered
	}
	seen := map[string]bool{}
	for _, replacement := range star.Replace {
		name := a.ident(replacement.Alias)
		_, n := a.find(cols, name)
		if n != 1 || seen[name] {
			a.fail("replacement column %q must resolve exactly once", name)
			continue
		}
		seen[name] = true
		col := a.projectionColumn(replacement, s, 0)
		for i := range cols {
			if a.equal(cols[i].Name, name) {
				cols[i] = col
			}
		}
	}
	return cols
}
