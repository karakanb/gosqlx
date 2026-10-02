package lineage

import (
	"github.com/ajitpratap0/GoSQLX/pkg/sql/ast"
	"strings"
)

// Snapshot selection changes the rows, not the stored column origins. Its
// expressions are control dependencies; CHANGES has extra generated fields and
// therefore cannot use the unchanged base-table schema.
func (a *analyzer) temporal(t *ast.TimeTravelClause, s *scope, r *relation) {
	if t.Kind == "CHANGES" {
		info, ok := t.Named["INFORMATION"].(*ast.Identifier)
		if !ok || (strings.ToUpper(info.Name) != "DEFAULT" && strings.ToUpper(info.Name) != "APPEND_ONLY") {
			a.fail("CHANGES INFORMATION must be DEFAULT or APPEND_ONLY")
			return
		}
		if len(t.Chained) == 0 {
			a.fail("CHANGES requires a starting snapshot")
			return
		}
		r.columns = append(append([]Column{}, r.columns...),
			Column{Name: "METADATA$ACTION", Type: "VARCHAR", Upstream: []Source{}},
			Column{Name: "METADATA$ISUPDATE", Type: "BOOLEAN", Upstream: []Source{}},
			Column{Name: "METADATA$ROW_ID", Type: "VARCHAR", Upstream: []Source{}})
		a.warnings["change-tracking metadata has no user-column value sources"] = true
	}
	for name, e := range t.Named {
		if t.Kind == "CHANGES" && name == "INFORMATION" {
			continue
		}
		a.dependency(a.expr(e, s).refs)
	}
	for _, next := range t.Chained {
		a.temporal(next, s, r)
	}
}
