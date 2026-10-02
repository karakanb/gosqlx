package lineage

import (
	"github.com/ajitpratap0/GoSQLX/pkg/sql/ast"
	"reflect"
	"strings"
)

func (a *analyzer) cteColumns(c *cte) []Column {
	if c.active {
		if !c.recursive || c.columns == nil {
			a.fail("recursive CTE %q requires a non-recursive anchor", c.node.Name)
		}
		return c.columns
	}
	if c.columns != nil {
		return c.columns
	}
	c.active = true
	defer func() { c.active = false }()
	rename := func(cols []Column) []Column {
		if len(c.node.Columns) > 0 {
			if len(c.node.Columns) != len(cols) {
				a.fail("CTE %q column alias count mismatch", c.node.Name)
				return nil
			}
			for i := range cols {
				cols[i].Name = a.ident(c.node.Columns[i])
			}
		}
		return cols
	}
	set, isSet := c.node.Statement.(*ast.SetOperation)
	if !c.recursive || !isSet {
		c.columns = rename(a.query(c.node.Statement, nil, c.env))
		return c.columns
	}
	if !strings.EqualFold(set.Operator, "UNION") {
		a.fail("recursive CTE requires UNION")
		return nil
	}
	anchor := set.Left
	for {
		nested, ok := anchor.(*ast.SetOperation)
		if !ok {
			break
		}
		anchor = nested.Left
	}
	c.columns = rename(a.query(anchor, nil, c.env))
	// Dependency sets are finite and monotone. Iterate to a fixed point so
	// recursive column permutations propagate all sources, not just one hop.
	for pass := 0; pass < 128 && a.err == nil; pass++ {
		next := rename(a.query(c.node.Statement, nil, c.env))
		if reflect.DeepEqual(next, c.columns) {
			return c.columns
		}
		c.columns = next
	}
	if a.err == nil {
		a.fail("recursive lineage did not converge within 128 iterations")
	}
	return nil
}
