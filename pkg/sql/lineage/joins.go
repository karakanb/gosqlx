package lineage

import (
	"github.com/ajitpratap0/GoSQLX/pkg/sql/ast"
	"strings"
)

func (a *analyzer) join(join ast.JoinClause, s, preceding *scope) {
	ref := join.Right
	if strings.Contains(join.Type, "APPLY") {
		ref.Lateral = true
	}
	input := s
	if ref.Lateral || ref.TableFunc != nil {
		// A lateral input can reference preceding comma items, whereas ON
		// resolves only within this explicit join chain (and outer scopes).
		visible := *s
		visible.relations = append(append([]relation{}, preceding.relations...), s.relations...)
		visible.visible = append(append([]Column{}, preceding.visible...), s.visible...)
		input = &visible
	}
	r := a.relation(ref, input)
	left := s.visible
	s.relations = append(s.relations, r)
	natural := strings.Contains(join.Type, "NATURAL")
	if join.Using || natural {
		var names []string
		if natural {
			for _, rel := range s.relations {
				if rel.open {
					a.fail("NATURAL JOIN requires complete schemas")
					return
				}
			}
			for _, col := range left {
				if _, n := a.find(r.columns, col.Name); n > 0 {
					names = append(names, col.Name)
				}
			}
		} else {
			switch cond := join.Condition.(type) {
			case *ast.Identifier:
				names = []string{a.ident(cond.Name)}
			case *ast.ListExpression:
				for _, e := range cond.Values {
					id, ok := e.(*ast.Identifier)
					if !ok {
						a.fail("unsupported USING expression %T", e)
						return
					}
					names = append(names, a.ident(id.Name))
				}
			default:
				a.fail("missing USING columns")
				return
			}
		}
		merged := []Column{}
		used := map[string]bool{}
		for _, name := range names {
			if used[name] {
				a.fail("duplicate USING column %q", name)
			}
			used[name] = true
			l, nl := a.find(left, name)
			right, nr := a.find(r.columns, name)
			if nl != 1 || nr != 1 {
				a.fail("USING column %q must resolve once on each side", name)
				return
			}
			if l.Error != "" || right.Error != "" {
				a.fail("USING column %q has failed input lineage", name)
				return
			}
			refs := sources(l.Upstream, right.Upstream)
			a.dependency(refs)
			merged = append(merged, Column{Name: name, Type: commonType(l.Type, right.Type), Upstream: refs})
		}
		for _, col := range append(append([]Column{}, left...), r.columns...) {
			if !used[col.Name] {
				merged = append(merged, col)
			}
		}
		s.visible = merged
	} else {
		s.visible = append(append([]Column{}, left...), r.columns...)
		a.dependency(a.expr(join.Condition, s).refs)
	}
	if strings.Contains(join.Type, "SEMI") || strings.Contains(join.Type, "ANTI") {
		// The right input filters rows but cannot contribute projected columns.
		s.visible = left
		s.relations = s.relations[:len(s.relations)-1]
	}
}
