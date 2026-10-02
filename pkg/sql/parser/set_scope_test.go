package parser_test

import (
	"strings"
	"testing"

	"github.com/ajitpratap0/GoSQLX/pkg/gosqlx"
	"github.com/ajitpratap0/GoSQLX/pkg/sql/ast"
	"github.com/ajitpratap0/GoSQLX/pkg/sql/keywords"
)

func TestJoinLeftDoesNotOwnFromSubquery(t *testing.T) {
	tree, err := gosqlx.Parse("SELECT * FROM (SELECT x FROM a) q JOIN b ON q.x=b.y")
	if err != nil {
		t.Fatal(err)
	}
	defer ast.ReleaseAST(tree)
	q, ok := tree.Statements[0].(*ast.SelectStatement)
	if !ok || len(q.From) != 1 || len(q.Joins) != 1 {
		t.Fatalf("unexpected tree %+v", tree)
	}
	if q.From[0].Subquery == nil || q.Joins[0].Left.Subquery != nil || q.Joins[0].Left.Alias != "q" {
		t.Fatalf("JOIN must retain identity but not share owned FROM pointers: %+v", q.Joins[0].Left)
	}
}

func TestSetOrderLimitRoundTrip(t *testing.T) {
	for _, sql := range []string{
		"(SELECT x FROM a ORDER BY x LIMIT 2) UNION ALL (SELECT y FROM b LIMIT 1) ORDER BY 1 LIMIT 3",
		"SELECT x FROM a UNION SELECT y FROM b INTERSECT SELECT z FROM c ORDER BY 1 LIMIT 2",
		"(SELECT x FROM a UNION SELECT y FROM b) INTERSECT SELECT z FROM c",
	} {
		t.Run(sql, func(t *testing.T) {
			tree, err := gosqlx.ParseWithDialect(sql, keywords.DialectPostgreSQL)
			if err != nil {
				t.Fatal(err)
			}
			defer ast.ReleaseAST(tree)
			rendered := tree.SQL()
			next, err := gosqlx.ParseWithDialect(rendered, keywords.DialectPostgreSQL)
			if err != nil {
				t.Fatal(err)
			}
			defer ast.ReleaseAST(next)
			if next.SQL() != rendered {
				t.Fatalf("unstable roundtrip: %s -> %s", rendered, next.SQL())
			}
		})
	}
}

func TestSetBranchBoundaries(t *testing.T) {
	for _, sql := range []string{
		"SELECT x FROM a LIMIT 1 UNION SELECT y FROM b",
		"SELECT x FROM a UNION SELECT y FROM b ORDER BY y UNION SELECT z FROM c",
		strings.Repeat("(", 150) + "SELECT 1" + strings.Repeat(")", 150),
	} {
		tree, err := gosqlx.ParseWithDialect(sql, keywords.DialectPostgreSQL)
		if tree != nil {
			ast.ReleaseAST(tree)
		}
		if err == nil {
			t.Fatalf("accepted invalid/deep set query %s", sql)
		}
	}
}
