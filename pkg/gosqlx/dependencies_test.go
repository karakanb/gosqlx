package gosqlx_test

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/ajitpratap0/GoSQLX/pkg/gosqlx"
	"github.com/ajitpratap0/GoSQLX/pkg/sql/ast"
	"github.com/ajitpratap0/GoSQLX/pkg/sql/keywords"
)

func TestQualifiedExtractionLongUnion(t *testing.T) {
	branch := "SELECT t1.x,t2.y FROM (SELECT x FROM a) t1 JOIN (SELECT y FROM b) t2 ON t1.x=t2.y"
	tree, err := gosqlx.Parse(strings.Repeat(branch+" UNION ALL ", 100) + branch)
	if err != nil {
		t.Fatal(err)
	}
	defer ast.ReleaseAST(tree)
	tables := gosqlx.ExtractTablesQualified(tree)
	var names []string
	for _, ref := range tables {
		names = append(names, ref.String())
	}
	sort.Strings(names)
	if !reflect.DeepEqual(names, []string{"a", "b"}) {
		t.Fatalf("tables %v", names)
	}
	refs := gosqlx.ExtractColumnsQualified(tree)
	names = nil
	for _, ref := range refs {
		names = append(names, ref.String())
	}
	sort.Strings(names)
	if !reflect.DeepEqual(names, []string{"t1.x", "t2.y", "x", "y"}) {
		t.Fatalf("columns %v", names)
	}
}

func TestPhysicalDependencies(t *testing.T) {
	for _, tc := range []struct {
		sql     string
		dialect keywords.SQLDialect
		want    []string
	}{
		{`WITH "Stage" AS (SELECT id FROM src) SELECT * FROM stage CROSS JOIN "Stage"`, keywords.DialectPostgreSQL, []string{"src", "stage"}},
		{`WITH Stage AS (SELECT id FROM src) SELECT * FROM stage CROSS JOIN "Stage"`, keywords.DialectPostgreSQL, []string{"Stage", "src"}},
		{`WITH c AS (SELECT id FROM src) SELECT * FROM c UNION ALL SELECT * FROM c`, keywords.DialectPostgreSQL, []string{"src"}},
		{`SELECT * FROM c JOIN (WITH c AS (SELECT id FROM inner_src) SELECT * FROM c) q ON c.id=q.id`, keywords.DialectPostgreSQL, []string{"c", "inner_src"}},
		{`CREATE TABLE dst AS WITH c AS (SELECT id FROM src) SELECT * FROM c`, keywords.DialectPostgreSQL, []string{"dst", "src"}},
		{`SELECT id INTO archive FROM orders`, keywords.DialectSQLServer, []string{"archive", "orders"}},
		{`BEGIN TRANSACTION; CREATE TABLE dst AS SELECT id FROM src; COMMIT;`, keywords.DialectPostgreSQL, []string{"dst", "src"}},
		{`SELECT * FROM orders(nolock) o JOIN customers(nolock,nowait) c ON o.id=c.id`, keywords.DialectSQLServer, []string{"customers", "orders"}},
		{`SELECT * FROM orders o CROSS JOIN UNNEST(o.items) u`, keywords.DialectBigQuery, []string{"orders"}},
		{`SELECT * FROM sales PIVOT(SUM(amount) FOR quarter IN (SELECT quarter FROM quarters)) p`, keywords.DialectSnowflake, []string{"quarters", "sales"}},
		{`SELECT * FROM sales CHANGES(INFORMATION => DEFAULT) AT(OFFSET => -60) END(OFFSET => -1)`, keywords.DialectSnowflake, []string{"sales"}},
		{`SELECT * FROM sales MATCH_RECOGNIZE (MEASURES FIRST(A.amount) AS first_amount PATTERN (A+)) m`, keywords.DialectSnowflake, []string{"sales"}},
	} {
		t.Run(tc.sql, func(t *testing.T) {
			tree, err := gosqlx.ParseWithDialect(tc.sql, tc.dialect)
			if err != nil {
				t.Fatal(err)
			}
			defer ast.ReleaseAST(tree)
			if got := gosqlx.ExtractTableDependencies(tree, tc.dialect); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}
