package lineage

import "github.com/ajitpratap0/GoSQLX/pkg/sql/ast"

func (a *analyzer) windowDependencies(window *ast.WindowSpec, s *scope) []Source {
	seen := map[string]bool{}
	lookup := func(name string) *ast.WindowSpec {
		key := a.key(a.ident(name))
		if seen[key] {
			a.fail("cyclic named window %q", name)
			return nil
		}
		seen[key] = true
		w := s.windows[key]
		if w == nil {
			a.fail("unknown named window %q", name)
		}
		return w
	}
	if window.Name != "" {
		window = lookup(window.Name)
	}
	var refs []Source
	partition, order, frame := false, false, false
	for window != nil && (a.err == nil || isUnsupported(a.err)) {
		if len(window.PartitionBy) > 0 {
			if partition {
				a.fail("inherited window redeclares PARTITION BY")
			}
			partition = true
			refs = sources(refs, a.expressions(window.PartitionBy, s).refs)
		}
		if len(window.OrderBy) > 0 {
			if order {
				a.fail("inherited window redeclares ORDER BY")
			}
			order = true
			for _, ob := range window.OrderBy {
				refs = sources(refs, a.expr(ob.Expression, s).refs)
			}
		}
		if f := window.FrameClause; f != nil {
			if frame {
				a.fail("inherited window redeclares frame")
			}
			frame = true
			refs = sources(refs, a.expr(f.Start.Value, s).refs)
			if f.End != nil {
				refs = sources(refs, a.expr(f.End.Value, s).refs)
			}
		}
		if window.BaseName == "" {
			break
		}
		window = lookup(window.BaseName)
	}
	return refs
}
