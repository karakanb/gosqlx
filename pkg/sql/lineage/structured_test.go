package lineage_test

import (
	"reflect"
	"testing"

	"github.com/ajitpratap0/GoSQLX/pkg/sql/keywords"
	"github.com/ajitpratap0/GoSQLX/pkg/sql/lineage"
)

func TestConstructedFieldOrigins(t *testing.T) {
	schema := lineage.Schema{
		"t": {{Name: "x", Type: "INT"}, {Name: "y", Type: "TEXT"}, {Name: "i", Type: "INT"}},
		"u": {{Name: "z", Type: "BIGINT"}, {Name: "w", Type: "TEXT"}},
	}
	for _, tc := range []struct {
		sql, typ string
		refs     []lineage.Source
	}{
		{"SELECT q.s.a AS result FROM (SELECT STRUCT(x AS a,y AS b) AS s FROM t) q", "INT", []lineage.Source{{Table: "t", Column: "x"}}},
		{"SELECT (STRUCT(x AS a,y AS b)).a AS result FROM t", "INT", []lineage.Source{{Table: "t", Column: "x"}}},
		{"SELECT (ARRAY[STRUCT(x AS a,y AS b)])[i].a AS result FROM t", "INT", []lineage.Source{{Table: "t", Column: "i"}, {Table: "t", Column: "x"}}},
		{"SELECT q.s.a AS result FROM (SELECT STRUCT(x AS a,y AS b) AS s FROM t UNION ALL SELECT STRUCT(z AS renamed,w AS other) FROM u) q", "BIGINT", []lineage.Source{{Table: "t", Column: "x"}, {Table: "u", Column: "z"}}},
		{"SELECT (CASE WHEN i>0 THEN STRUCT(x AS a,y AS b) ELSE STRUCT(2 AS a,'other' AS b) END).a AS result FROM t", "INT", []lineage.Source{{Table: "t", Column: "i"}, {Table: "t", Column: "x"}}},
		{"SELECT (IF(i>0,STRUCT(x AS a,y AS b),STRUCT(2 AS a,'other' AS b))).a AS result FROM t", "INT", []lineage.Source{{Table: "t", Column: "i"}, {Table: "t", Column: "x"}}},
		{"SELECT (ARRAY_AGG(STRUCT(x AS a,y AS b)) FILTER(WHERE i>0))[1].a AS result FROM t", "INT", []lineage.Source{{Table: "t", Column: "i"}, {Table: "t", Column: "x"}}},
	} {
		t.Run(tc.sql, func(t *testing.T) {
			r := analyze(t, tc.sql, schema, keywords.DialectBigQuery)
			want := []lineage.Column{{Name: "result", Type: tc.typ, Upstream: tc.refs}}
			if !reflect.DeepEqual(r.Columns, want) {
				t.Fatalf("got %+v want %+v", r.Columns, want)
			}
		})
	}
}

func TestPhysicalStructStarOrder(t *testing.T) {
	r := analyze(t, "SELECT payload.* FROM events", lineage.Schema{"events": {{Name: "payload", Type: "STRUCT<z TEXT, a INT>"}}}, keywords.DialectBigQuery)
	want := []lineage.Column{column("z", "TEXT", "events", "payload"), column("a", "INT", "events", "payload")}
	if !reflect.DeepEqual(r.Columns, want) {
		t.Fatalf("got %+v want %+v", r.Columns, want)
	}
	schema := lineage.Schema{"events": {{Name: "payload", Type: `STRUCT<"Odd Name" TEXT, "Case" INT>`}}}
	r = analyze(t, `SELECT payload."Case", payload."Odd Name" FROM events`, schema, keywords.DialectPostgreSQL)
	want = []lineage.Column{column("Case", "INT", "events", "payload"), column("Odd Name", "TEXT", "events", "payload")}
	if !reflect.DeepEqual(r.Columns, want) {
		t.Fatalf("quoted fields: got %+v want %+v", r.Columns, want)
	}
}

func TestExtractQuotedQueryRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		sql     string
		dialect keywords.SQLDialect
		schema  lineage.Schema
		want    lineage.Column
	}{
		{`CREATE VIEW v AS SELECT "T"."Odd""Name" AS "Output" FROM "T"`, keywords.DialectPostgreSQL, lineage.Schema{"T": {{Name: `Odd"Name`, Type: "TEXT"}}}, column("Output", "TEXT", "T", `Odd"Name`)},
		{`INSERT INTO target SELECT [T].[Mixed Case] AS [Output] FROM [T]`, keywords.DialectSQLServer, lineage.Schema{"T": {{Name: "Mixed Case", Type: "INT"}}}, column("output", "INT", "t", "mixed case")},
		{"CREATE TABLE target AS SELECT `T`.`x` AS `Output` FROM `T`", keywords.DialectBigQuery, lineage.Schema{"T": {{Name: "x", Type: "INT"}}}, column("output", "INT", "T", "x")},
		{`SELECT '__gosqlx_quoted_1_x' AS "Output"`, keywords.DialectPostgreSQL, nil, column("Output", "VARCHAR", "", "")},
	} {
		t.Run(tc.sql, func(t *testing.T) {
			q, err := lineage.ExtractQuery(tc.sql, tc.dialect)
			if err != nil {
				t.Fatal(err)
			}
			r := analyze(t, q, tc.schema, tc.dialect)
			if !reflect.DeepEqual(r.Columns, []lineage.Column{tc.want}) {
				t.Fatalf("query %s produced %+v", q, r.Columns)
			}
		})
	}
}
