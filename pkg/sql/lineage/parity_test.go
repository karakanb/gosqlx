package lineage_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/ajitpratap0/GoSQLX/pkg/formatter"
	"github.com/ajitpratap0/GoSQLX/pkg/gosqlx"
	"github.com/ajitpratap0/GoSQLX/pkg/sql/ast"
	"github.com/ajitpratap0/GoSQLX/pkg/sql/keywords"
	"github.com/ajitpratap0/GoSQLX/pkg/sql/lineage"
)

// Expectations are positional and independently specified, including duplicate
// names. Sorting output names would hide both star-order and UNION-position bugs.
func TestExtendedScopesAndProjections(t *testing.T) {
	schema := lineage.Schema{
		"a": {{Name: "z", Type: "TEXT"}, {Name: "id", Type: "INT"}, {Name: "v", Type: "INT"}},
		"b": {{Name: "id", Type: "BIGINT"}, {Name: "w", Type: "TEXT"}},
	}
	az, ai, av := column("z", "TEXT", "a", "z"), column("id", "INT", "a", "id"), column("v", "INT", "a", "v")
	bi, bw := column("id", "BIGINT", "b", "id"), column("w", "TEXT", "b", "w")
	merged := lineage.Column{Name: "id", Type: "BIGINT", Upstream: []lineage.Source{{Table: "a", Column: "id"}, {Table: "b", Column: "id"}}}
	for _, tc := range []struct {
		name, sql string
		dialect   keywords.SQLDialect
		want      []lineage.Column
		deps      []lineage.Source
	}{
		{"natural unequal schemas", "SELECT * FROM a NATURAL JOIN b", keywords.DialectPostgreSQL, []lineage.Column{merged, az, av, bw}, merged.Upstream},
		{"semi projects left", "SELECT * FROM a LEFT SEMI JOIN b ON a.id=b.id", keywords.DialectDuckDB, []lineage.Column{az, ai, av}, merged.Upstream},
		{"anti projects left", "SELECT * FROM a ANTI JOIN b ON a.id=b.id", keywords.DialectDuckDB, []lineage.Column{az, ai, av}, merged.Upstream},
		{"exclude retains duplicates and position", "SELECT * EXCLUDE(v,w) FROM a JOIN b ON a.id=b.id", keywords.DialectDuckDB, []lineage.Column{az, ai, bi}, merged.Upstream},
		{"replace uses original inputs", "SELECT a.* EXCEPT(z) REPLACE (v+id AS id) FROM a", keywords.DialectBigQuery, []lineage.Column{{Name: "id", Type: "INT", Upstream: []lineage.Source{{Table: "a", Column: "id"}, {Table: "a", Column: "v"}}}, av}, nil},
		{"derived aliases positional", "SELECT q.* FROM (SELECT z,id FROM a) q(label,key)", keywords.DialectPostgreSQL, []lineage.Column{column("label", "TEXT", "a", "z"), column("key", "INT", "a", "id")}, nil},
		{"renamed cte output", "WITH c(renamed) AS (SELECT v FROM a) SELECT renamed FROM c", keywords.DialectPostgreSQL, []lineage.Column{column("renamed", "INT", "a", "v")}, nil},
		{"nested cte shadow", "WITH c AS (SELECT id FROM a) SELECT q.id,c.id FROM (WITH c AS (SELECT id FROM b) SELECT id FROM c) q CROSS JOIN c", keywords.DialectPostgreSQL, []lineage.Column{bi, ai}, nil},
		{"recursive swapped positions", "WITH RECURSIVE r(x,y) AS (SELECT id,v FROM a UNION ALL SELECT y,x FROM r) SELECT x,y FROM r", keywords.DialectPostgreSQL, []lineage.Column{{Name: "x", Type: "INT", Upstream: []lineage.Source{{Table: "a", Column: "id"}, {Table: "a", Column: "v"}}}, {Name: "y", Type: "INT", Upstream: []lineage.Source{{Table: "a", Column: "id"}, {Table: "a", Column: "v"}}}}, nil},
		{"union aligns by position not name", "SELECT v AS n,z AS label FROM a UNION ALL SELECT id,w FROM b ORDER BY 1 LIMIT 2", keywords.DialectPostgreSQL, []lineage.Column{{Name: "n", Type: "BIGINT", Upstream: []lineage.Source{{Table: "a", Column: "v"}, {Table: "b", Column: "id"}}}, {Name: "label", Type: "TEXT", Upstream: []lineage.Source{{Table: "a", Column: "z"}, {Table: "b", Column: "w"}}}}, []lineage.Source{{Table: "a", Column: "v"}, {Table: "b", Column: "id"}}},
		{"parenthesized branches", "(SELECT id FROM a ORDER BY v LIMIT 2) UNION ALL (SELECT id FROM b LIMIT 1) ORDER BY 1", keywords.DialectPostgreSQL, []lineage.Column{merged}, []lineage.Source{{Table: "a", Column: "id"}, {Table: "a", Column: "v"}, {Table: "b", Column: "id"}}},
		{"except values vs row membership", "SELECT id FROM a EXCEPT SELECT id FROM b", keywords.DialectPostgreSQL, []lineage.Column{{Name: "id", Type: "BIGINT", Upstream: ai.Upstream}}, merged.Upstream},
		{"lateral correlated", "SELECT q.x FROM a CROSS JOIN LATERAL (SELECT a.v + b.id AS x FROM b WHERE b.id=a.id) q", keywords.DialectPostgreSQL, []lineage.Column{{Name: "x", Type: "BIGINT", Upstream: []lineage.Source{{Table: "a", Column: "v"}, {Table: "b", Column: "id"}}}}, merged.Upstream},
		{"named window", "SELECT SUM(v) OVER win AS total FROM a WINDOW win AS (PARTITION BY id ORDER BY z)", keywords.DialectPostgreSQL, []lineage.Column{{Name: "total", Type: "BIGINT", Upstream: []lineage.Source{{Table: "a", Column: "id"}, {Table: "a", Column: "v"}, {Table: "a", Column: "z"}}}}, nil},
		{"inherited window", "SELECT SUM(v) OVER (w2 ROWS BETWEEN 1 PRECEDING AND CURRENT ROW) AS total FROM a WINDOW w AS (PARTITION BY id), w2 AS (w ORDER BY z)", keywords.DialectPostgreSQL, []lineage.Column{{Name: "total", Type: "BIGINT", Upstream: []lineage.Source{{Table: "a", Column: "id"}, {Table: "a", Column: "v"}, {Table: "a", Column: "z"}}}}, nil},
		{"exists uses row predicates not projected values", "SELECT EXISTS(SELECT w FROM b WHERE b.id=a.id) AS present FROM a", keywords.DialectPostgreSQL, []lineage.Column{column("present", "BOOLEAN", "", "")}, merged.Upstream},
		{"unknown udf dependencies", "SELECT private_udf(v,z) AS result FROM a", keywords.DialectPostgreSQL, []lineage.Column{{Name: "result", Type: "UNKNOWN", Upstream: []lineage.Source{{Table: "a", Column: "v"}, {Table: "a", Column: "z"}}}}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := analyze(t, tc.sql, schema, tc.dialect)
			if !reflect.DeepEqual(r.Columns, tc.want) {
				t.Fatalf("columns\ngot  %+v\nwant %+v", r.Columns, tc.want)
			}
			got := map[lineage.Source]bool{}
			for _, d := range r.NonSelectedColumns {
				for _, s := range d.Upstream {
					got[s] = true
				}
			}
			want := map[lineage.Source]bool{}
			for _, s := range tc.deps {
				want[s] = true
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("dependencies: got %v want %v", got, want)
			}
			if tc.name == "unknown udf dependencies" && len(r.Warnings) == 0 {
				t.Fatal("UDF body limitations must be explicit")
			}
		})
	}
}

func TestNestedSchemaAndStructuredValues(t *testing.T) {
	var schema lineage.Schema
	if err := json.Unmarshal([]byte(`{"catalog":{"data":{"events":{"id":"INT","payload":"STRUCT<name TEXT, age INT>","items":"ARRAY<INT>","doc":"JSONB"}}}}`), &schema); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ sql, name, typ, field string }{
		{"SELECT e.payload.name FROM catalog.data.events e", "name", "TEXT", "payload"},
		{"SELECT payload.age FROM catalog.data.events", "age", "INT", "payload"},
		{"SELECT items[1] AS item FROM catalog.data.events", "item", "INT", "items"},
		{"SELECT doc->>'name' AS label FROM catalog.data.events", "label", "TEXT", "doc"},
		{"SELECT u.item FROM catalog.data.events e CROSS JOIN UNNEST(e.items) AS u(item)", "item", "INT", "items"},
	} {
		t.Run(tc.sql, func(t *testing.T) {
			r := analyze(t, tc.sql, schema, keywords.DialectPostgreSQL)
			if !reflect.DeepEqual(r.Columns, []lineage.Column{column(tc.name, tc.typ, "catalog.data.events", tc.field)}) {
				t.Fatalf("structured lineage: %+v", r)
			}
		})
	}
}

func TestExtractQuery(t *testing.T) {
	schema := lineage.Schema{"orders": {{Name: "id", Type: "BIGINT"}}}
	for _, sql := range []string{
		"WITH a AS (SELECT AccountDimId FROM dbo.DimAccount) SELECT AccountDimId FROM a",
		"WITH a AS (SELECT d.AccountDimId FROM dbo.DimAccount AS d) SELECT a.AccountDimId FROM a",
	} {
		q, err := lineage.ExtractQuery(sql, keywords.DialectFabric)
		if err != nil || !strings.Contains(q, "AS AccountDimId") || strings.Contains(q, "AS accountdimid") {
			t.Fatalf("Fabric output casing: %s (%v)", q, err)
		}
	}
	for _, sql := range []string{
		"SELECT id FROM orders",
		"CREATE OR REPLACE VIEW analytics.v AS SELECT id FROM orders",
		"CREATE TABLE analytics.t AS SELECT id FROM orders",
		"INSERT INTO analytics.t SELECT id FROM orders",
		"CREATE OR REPLACE VIEW analytics.v AS WITH e AS (SELECT id FROM orders) SELECT * FROM e",
		"INSERT INTO analytics.t WITH e AS (SELECT id FROM orders) SELECT * FROM e",
		"WITH e AS (SELECT id FROM orders) INSERT INTO analytics.t SELECT * FROM e UNION ALL SELECT * FROM e",
		"SELECT id INTO archive FROM orders",
	} {
		t.Run(sql, func(t *testing.T) {
			q, err := lineage.ExtractQuery(sql, keywords.DialectDuckDB)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(strings.ToUpper(q), "INTO") || strings.Contains(strings.ToUpper(q), "CREATE") {
				t.Fatalf("write retained: %s", q)
			}
			r := analyze(t, q, schema, keywords.DialectDuckDB)
			if !reflect.DeepEqual(r.Columns, []lineage.Column{column("id", "BIGINT", "orders", "id")}) {
				t.Fatalf("extracted lineage: %+v", r)
			}
		})
	}
	for _, sql := range []string{
		"CREATE TABLE analytics.t(id BIGINT)", "INSERT INTO t VALUES(1)",
		"DELETE FROM orders WHERE id IN (SELECT id FROM refunds)",
		"WITH gone AS (DELETE FROM orders RETURNING id) SELECT * FROM gone",
		"UPDATE orders SET id=3", "DROP TABLE orders", "SELECT 1; SELECT 2",
	} {
		if q, err := lineage.ExtractQuery(sql, keywords.DialectPostgreSQL); err == nil {
			t.Fatalf("accepted write/multiple queries %s as %s", sql, q)
		}
	}
}

func TestSetCTEScopeRoundTrip(t *testing.T) {
	schema := lineage.Schema{
		"src": {{Name: "id", Type: "INT"}},
		"c":   {{Name: "id", Type: "BIGINT"}},
	}
	for _, tc := range []struct {
		sql    string
		tables []string
		want   lineage.Column
	}{
		{"WITH c AS (SELECT id FROM src) SELECT * FROM c UNION ALL SELECT * FROM c UNION ALL SELECT * FROM c", []string{"src"}, column("id", "INT", "src", "id")},
		{"(WITH c AS (SELECT id FROM src) SELECT * FROM c) UNION ALL SELECT id FROM c", []string{"c", "src"}, lineage.Column{Name: "id", Type: "BIGINT", Upstream: []lineage.Source{{Table: "c", Column: "id"}, {Table: "src", Column: "id"}}}},
	} {
		t.Run(tc.sql, func(t *testing.T) {
			tree, err := gosqlx.ParseWithDialect(tc.sql, keywords.DialectPostgreSQL)
			if err != nil {
				t.Fatal(err)
			}
			defer ast.ReleaseAST(tree)
			extracted, err := lineage.ExtractQuery(tc.sql, keywords.DialectPostgreSQL)
			if err != nil {
				t.Fatal(err)
			}
			for _, sql := range []string{tc.sql, extracted, tree.SQL(), formatter.FormatStatement(tree.Statements[0], ast.FormatOptions{AddSemicolon: true})} {
				r := analyze(t, sql, schema, keywords.DialectPostgreSQL)
				if !reflect.DeepEqual(r.Columns, []lineage.Column{tc.want}) {
					t.Fatalf("%s: %+v, want %+v", sql, r.Columns, tc.want)
				}
				parsed, err := gosqlx.ParseWithDialect(sql, keywords.DialectPostgreSQL)
				if err != nil {
					t.Fatal(err)
				}
				tables := gosqlx.ExtractTableDependencies(parsed, keywords.DialectPostgreSQL)
				ast.ReleaseAST(parsed)
				if !reflect.DeepEqual(tables, tc.tables) {
					t.Fatalf("%s: tables %v, want %v", sql, tables, tc.tables)
				}
			}
		})
	}
}

func TestDialectIdentifierRules(t *testing.T) {
	for _, d := range []keywords.SQLDialect{keywords.DialectDuckDB, keywords.DialectSpark, keywords.DialectDatabricks, keywords.DialectTrino, keywords.DialectAthena, keywords.DialectFabric, keywords.DialectDoris, keywords.DialectStarRocks} {
		t.Run(string(d), func(t *testing.T) {
			r := analyze(t, "SELECT t.Name AS Result FROM raw.Teams t", lineage.Schema{"raw.teams": {{Name: "Name", Type: "STRING"}}}, d)
			want := column("result", "TEXT", "raw.teams", "name")
			if d == keywords.DialectFabric || d == keywords.DialectDoris || d == keywords.DialectStarRocks {
				want = column("Result", "TEXT", "raw.Teams", "Name")
			}
			if !reflect.DeepEqual(r.Columns, []lineage.Column{want}) {
				t.Fatalf("dialect normalization: %+v", r)
			}
		})
	}
}
