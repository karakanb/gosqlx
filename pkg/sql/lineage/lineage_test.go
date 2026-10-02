package lineage_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/ajitpratap0/GoSQLX/pkg/sql/keywords"
	"github.com/ajitpratap0/GoSQLX/pkg/sql/lineage"
)

func analyze(t *testing.T, sql string, schema lineage.Schema, dialect keywords.SQLDialect) *lineage.Result {
	t.Helper()
	r, err := lineage.Analyze(sql, schema, dialect)
	if err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return r
}

func column(name, typ, table, field string) lineage.Column {
	refs := []lineage.Source{}
	if table != "" {
		refs = append(refs, lineage.Source{Table: table, Column: field})
	}
	return lineage.Column{Name: name, Type: typ, Upstream: refs}
}

func TestStarsAndUsing(t *testing.T) {
	schema := lineage.Schema{
		"left_t":  {{Name: "z", Type: "TEXT"}, {Name: "id", Type: "INT"}},
		"right_t": {{Name: "id", Type: "BIGINT"}, {Name: "extra", Type: "BOOLEAN"}, {Name: "tail", Type: "FLOAT"}},
	}
	leftZ := column("z", "TEXT", "left_t", "z")
	leftID := column("id", "INT", "left_t", "id")
	rightID := column("id", "BIGINT", "right_t", "id")
	extra := column("extra", "BOOLEAN", "right_t", "extra")
	tail := column("tail", "FLOAT", "right_t", "tail")
	merged := lineage.Column{Name: "id", Type: "BIGINT", Upstream: []lineage.Source{{Table: "left_t", Column: "id"}, {Table: "right_t", Column: "id"}}}
	for _, tc := range []struct {
		sql  string
		want []lineage.Column
		deps []lineage.Dependency
	}{
		{"SELECT * FROM left_t l JOIN right_t r ON l.id = r.id", []lineage.Column{leftZ, leftID, rightID, extra, tail}, []lineage.Dependency{{Name: "id", Upstream: merged.Upstream}}},
		{"SELECT * FROM left_t l JOIN right_t r USING (id)", []lineage.Column{merged, leftZ, extra, tail}, []lineage.Dependency{{Name: "id", Upstream: merged.Upstream}}},
		{"SELECT r.*, l.z FROM left_t l LEFT JOIN right_t r USING (id)", []lineage.Column{rightID, extra, tail, leftZ}, []lineage.Dependency{{Name: "id", Upstream: merged.Upstream}}},
		// A bare ON boolean is not USING and must not merge matching columns.
		{"SELECT * FROM left_t l JOIN right_t r ON extra", []lineage.Column{leftZ, leftID, rightID, extra, tail}, []lineage.Dependency{{Name: "extra", Upstream: extra.Upstream}}},
	} {
		t.Run(tc.sql, func(t *testing.T) {
			r := analyze(t, tc.sql, schema, keywords.DialectPostgreSQL)
			if !reflect.DeepEqual(r.Columns, tc.want) || !reflect.DeepEqual(r.NonSelectedColumns, tc.deps) {
				t.Fatalf("got %+v; want columns %+v deps %+v", r, tc.want, tc.deps)
			}
		})
	}
}

func TestScopesRenamingAndUnion(t *testing.T) {
	schema := lineage.Schema{
		"orders":  {{Name: "amount", Type: "DECIMAL"}, {Name: "id", Type: "INT"}},
		"archive": {{Name: "price", Type: "DECIMAL"}, {Name: "other_id", Type: "BIGINT"}},
	}
	for _, sql := range []string{
		"WITH stage AS (SELECT amount AS subtotal FROM orders) SELECT subtotal AS total FROM stage",
		"WITH stage(subtotal) AS (SELECT amount FROM orders) SELECT subtotal AS total FROM stage",
		"SELECT q.subtotal AS total FROM (SELECT amount AS subtotal FROM orders) q",
		"WITH unused AS (SELECT missing FROM absent), stage AS (SELECT amount FROM orders) SELECT amount AS total FROM stage",
	} {
		r := analyze(t, sql, schema, keywords.DialectPostgreSQL)
		if !reflect.DeepEqual(r.Columns, []lineage.Column{column("total", "DECIMAL", "orders", "amount")}) {
			t.Fatalf("%s: %+v", sql, r)
		}
	}
	r := analyze(t, "SELECT amount AS total, id AS output_key FROM orders UNION ALL SELECT price AS differently_named, other_id FROM archive", schema, keywords.DialectPostgreSQL)
	want := []lineage.Column{
		{Name: "total", Type: "DECIMAL", Upstream: []lineage.Source{{Table: "archive", Column: "price"}, {Table: "orders", Column: "amount"}}},
		{Name: "output_key", Type: "BIGINT", Upstream: []lineage.Source{{Table: "archive", Column: "other_id"}, {Table: "orders", Column: "id"}}},
	}
	if !reflect.DeepEqual(r.Columns, want) {
		t.Fatalf("UNION must align ordinal positions, not names: %+v", r)
	}
	r = analyze(t, "WITH stage AS (SELECT amount FROM orders) SELECT amount FROM stage UNION ALL SELECT amount FROM stage UNION ALL SELECT amount FROM stage", schema, keywords.DialectPostgreSQL)
	if !reflect.DeepEqual(r.Columns, []lineage.Column{column("amount", "DECIMAL", "orders", "amount")}) {
		t.Fatalf("WITH scope across UNION: %+v", r)
	}
}

func TestCorrelationAndShadowing(t *testing.T) {
	schema := lineage.Schema{
		"outer_t": {{Name: "id", Type: "INT"}, {Name: "outer_only", Type: "TEXT"}},
		"inner_t": {{Name: "parent_id", Type: "INT"}, {Name: "score", Type: "BIGINT"}},
	}
	r := analyze(t, "SELECT o.id, (SELECT MAX(i.score) FROM inner_t i WHERE i.parent_id = o.id) AS rating FROM outer_t o", schema, keywords.DialectPostgreSQL)
	want := []lineage.Column{column("id", "INT", "outer_t", "id"), column("rating", "BIGINT", "inner_t", "score")}
	deps := []lineage.Dependency{{Name: "id", Upstream: []lineage.Source{{Table: "outer_t", Column: "id"}}}, {Name: "parent_id", Upstream: []lineage.Source{{Table: "inner_t", Column: "parent_id"}}}}
	if !reflect.DeepEqual(r.Columns, want) || !reflect.DeepEqual(r.NonSelectedColumns, deps) {
		t.Fatalf("value vs predicate deps: %+v", r)
	}
	r = analyze(t, "SELECT (SELECT MAX(o.score) FROM inner_t o) AS rating FROM outer_t o", schema, keywords.DialectPostgreSQL)
	if !reflect.DeepEqual(r.Columns, []lineage.Column{column("rating", "BIGINT", "inner_t", "score")}) {
		t.Fatalf("local alias must shadow outer: %+v", r)
	}
	_, err := lineage.Analyze("SELECT (SELECT o.outer_only FROM inner_t o) AS bad FROM outer_t o", schema, keywords.DialectPostgreSQL)
	if err == nil || !strings.Contains(err.Error(), "resolves 0 times") {
		t.Fatalf("must not fall back through a shadowed alias: %v", err)
	}
}

func TestQuotedIdentifiersAndDialectFolding(t *testing.T) {
	schema := lineage.Schema{"Sales.Orders": {{Name: "OrderID", Type: "INT"}, {Name: "orderid", Type: "TEXT"}, {Name: `a"b`, Type: "BOOLEAN"}}}
	r := analyze(t, `SELECT o."OrderID" AS "ResultID", o.OrderID, o."a""b" AS "Q" FROM "Sales"."Orders" o`, schema, keywords.DialectPostgreSQL)
	want := []lineage.Column{column("ResultID", "INT", "Sales.Orders", "OrderID"), column("orderid", "TEXT", "Sales.Orders", "orderid"), column("Q", "BOOLEAN", "Sales.Orders", `a"b`)}
	if !reflect.DeepEqual(r.Columns, want) {
		t.Fatalf("quoted vs folded names: %+v", r)
	}
	r = analyze(t, `WITH "Stage"("Renamed") AS (SELECT "OrderID" FROM "Sales"."Orders") SELECT "Renamed" FROM "Stage"`, schema, keywords.DialectPostgreSQL)
	if !reflect.DeepEqual(r.Columns, []lineage.Column{column("Renamed", "INT", "Sales.Orders", "OrderID")}) {
		t.Fatalf("quoted CTE alias: %+v", r)
	}
	sf := lineage.Schema{"ORDERS": {{Name: "ID", Type: "INT"}, {Name: "id", Type: "TEXT"}}}
	r = analyze(t, `SELECT id, "id" AS "lower" FROM orders`, sf, keywords.DialectSnowflake)
	if !reflect.DeepEqual(r.Columns, []lineage.Column{column("ID", "INT", "ORDERS", "ID"), column("lower", "TEXT", "ORDERS", "id")}) {
		t.Fatalf("Snowflake case: %+v", r)
	}
	// Quoted literal dots must not be confused with qualification boundaries.
	r = analyze(t, `SELECT "a.b" FROM "weird.table"`, lineage.Schema{`"weird.table"`: {{Name: "a.b", Type: "INT"}}}, keywords.DialectPostgreSQL)
	if r.Columns[0].Name != "a.b" || r.Columns[0].Upstream[0].Table != "weird.table" {
		t.Fatalf("quoted dots: %+v", r)
	}
	for _, dialect := range []keywords.SQLDialect{keywords.DialectBigQuery, keywords.DialectMySQL} {
		r = analyze(t, `SELECT id FROM orders WHERE country = "UK"`, lineage.Schema{"orders": {{Name: "id", Type: "INT"}, {Name: "country", Type: "TEXT"}}}, dialect)
		if len(r.NonSelectedColumns) != 1 || r.NonSelectedColumns[0].Name != "country" {
			t.Fatalf("double-quoted string became identifier: %+v", r)
		}
	}
	r = analyze(t, "SELECT orders.* FROM `my-project.analytics.orders`", lineage.Schema{"my-project.analytics.orders": {{Name: "id", Type: "INT"}}}, keywords.DialectBigQuery)
	if !reflect.DeepEqual(r.Columns, []lineage.Column{column("id", "INT", "my-project.analytics.orders", "id")}) {
		t.Fatalf("BigQuery implicit table alias: %+v", r)
	}
}

func TestExpressionsAndClauseDependencies(t *testing.T) {
	schema := lineage.Schema{"orders": {{Name: "customer", Type: "TEXT"}, {Name: "amount", Type: "INT"}, {Name: "status", Type: "BOOLEAN"}}}
	r := analyze(t, "SELECT customer AS who, SUM(amount) AS total, CASE WHEN status THEN 1 ELSE 0 END AS active, COUNT(*) AS n FROM orders WHERE status GROUP BY 1, status HAVING SUM(amount) > 1 ORDER BY total", schema, keywords.DialectPostgreSQL)
	want := []lineage.Column{column("who", "TEXT", "orders", "customer"), column("total", "BIGINT", "orders", "amount"), column("active", "INT", "orders", "status"), column("n", "BIGINT", "", "")}
	if !reflect.DeepEqual(r.Columns, want) {
		t.Fatalf("expressions: %+v", r)
	}
	if len(r.NonSelectedColumns) != 3 || r.NonSelectedColumns[0].Name != "amount" || r.NonSelectedColumns[1].Name != "customer" || r.NonSelectedColumns[2].Name != "status" {
		t.Fatalf("clause dependencies: %+v", r)
	}
	r = analyze(t, "SELECT amount AS status FROM orders GROUP BY status ORDER BY status", schema, keywords.DialectPostgreSQL)
	if len(r.NonSelectedColumns) != 2 {
		t.Fatalf("GROUP uses input; ORDER uses output alias: %+v", r)
	}
	r = analyze(t, "SELECT ROW_NUMBER() OVER (PARTITION BY customer ORDER BY amount) AS rn FROM orders", schema, keywords.DialectPostgreSQL)
	if !reflect.DeepEqual(r.Columns[0].Upstream, []lineage.Source{{Table: "orders", Column: "amount"}, {Table: "orders", Column: "customer"}}) {
		t.Fatalf("window dependencies: %+v", r)
	}
	r = analyze(t, "SELECT amount FROM orders QUALIFY ROW_NUMBER() OVER (PARTITION BY customer ORDER BY amount) = 1", schema, keywords.DialectBigQuery)
	if len(r.NonSelectedColumns) != 2 {
		t.Fatalf("QUALIFY dependencies must not be dropped: %+v", r)
	}
}

func TestErrorsAreNotPartialSuccess(t *testing.T) {
	schema := lineage.Schema{"a": {{Name: "id", Type: "INT"}}, "b": {{Name: "id", Type: "INT"}}}
	for _, tc := range []struct{ sql, contains string }{
		{"SELECT id FROM a JOIN b ON a.id = b.id", "ambiguous column"},
		{"SELECT a.id, missing FROM a", "unresolved column"},
		{"SELECT * FROM absent", "missing schema"},
		{"SELECT absent.* FROM a", "unknown star qualifier"},
		{"SELECT id FROM a UNION SELECT id, id FROM b", "unequal column counts"},
		{"SELECT id FROM a UNION SELECT id FROM b ORDER BY missing", "unresolved column"},
		{"SELECT EXISTS (SELECT missing FROM b) FROM a", "unresolved column"},
		{"WITH c(x,y) AS (SELECT id FROM a) SELECT * FROM c", "column alias count"},
		{"WITH RECURSIVE c AS (SELECT id FROM c) SELECT * FROM c", "recursive CTE"},
		{"SELECT id FROM a; SELECT id FROM b", "exactly one query"},
		{"SELECT id FROM a GROUP BY 2", "ordinal"},
		{"SELECT id FROM a GROUP BY 0", "ordinal"},
		{"SELECT * FROM a x JOIN b x ON x.id = x.id", "duplicate relation alias"},
	} {
		t.Run(tc.sql, func(t *testing.T) {
			r, err := lineage.Analyze(tc.sql, schema, keywords.DialectPostgreSQL)
			if err == nil || !strings.Contains(err.Error(), tc.contains) || r != nil {
				t.Fatalf("got result=%+v err=%v, want %s", r, err, tc.contains)
			}
		})
	}
}

func TestSchemaOrderAndConcurrentIsolation(t *testing.T) {
	var schema lineage.Schema
	if err := json.Unmarshal([]byte(`{"t":{"z":"INT","a":"TEXT","m":"BOOLEAN"}}`), &schema); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 16; i++ {
		t.Run("parallel", func(t *testing.T) {
			t.Parallel()
			r := analyze(t, "SELECT * FROM t", schema, keywords.DialectPostgreSQL)
			if !reflect.DeepEqual(r.Columns, []lineage.Column{column("z", "INT", "t", "z"), column("a", "TEXT", "t", "a"), column("m", "BOOLEAN", "t", "m")}) {
				t.Fatalf("schema order lost: %+v", r)
			}
		})
	}
}

func TestUnknownTypesAndSchemaAmbiguity(t *testing.T) {
	r := analyze(t, "SELECT id FROM t", lineage.Schema{"t": {{Name: "id", Type: "custom_type"}}}, keywords.DialectPostgreSQL)
	if r.Columns[0].Type != "UNKNOWN" || len(r.Warnings) == 0 {
		t.Fatalf("unknown type must be explicit: %+v", r)
	}
	for _, schema := range []lineage.Schema{
		{"s1.t": {{Name: "id", Type: "INT"}}, "s2.t": {{Name: "id", Type: "TEXT"}}},
		{"t": {{Name: "id", Type: "INT"}, {Name: "id", Type: "TEXT"}}},
	} {
		r, err := lineage.Analyze("SELECT id FROM t", schema, keywords.DialectPostgreSQL)
		if err == nil || r != nil {
			t.Fatalf("ambiguous schema accepted: %+v", r)
		}
	}
}

func BenchmarkAnalyze(b *testing.B) {
	schema := lineage.Schema{"orders": {{Name: "id", Type: "INT"}, {Name: "amount", Type: "DECIMAL"}}}
	for b.Loop() {
		if _, err := lineage.Analyze("WITH c AS (SELECT * FROM orders) SELECT SUM(amount) AS total FROM c WHERE id > 3", schema, keywords.DialectPostgreSQL); err != nil {
			b.Fatal(err)
		}
	}
}
