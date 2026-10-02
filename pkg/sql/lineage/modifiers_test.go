package lineage_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/ajitpratap0/GoSQLX/pkg/formatter"
	"github.com/ajitpratap0/GoSQLX/pkg/gosqlx"
	"github.com/ajitpratap0/GoSQLX/pkg/sql/ast"
	"github.com/ajitpratap0/GoSQLX/pkg/sql/keywords"
	"github.com/ajitpratap0/GoSQLX/pkg/sql/lineage"
)

func TestCommaJoinAssociation(t *testing.T) {
	schema := lineage.Schema{"a": {{Name: "id", Type: "INT"}}, "b": {{Name: "id", Type: "INT"}}, "c": {{Name: "id", Type: "INT"}, {Name: "value", Type: "TEXT"}}}
	for _, sql := range []string{
		"SELECT a.id, c.value FROM a, b JOIN c ON b.id=c.id",
		"SELECT a.id, c.value FROM b JOIN c ON b.id=c.id, a",
		"SELECT a.id, c.value FROM a CROSS JOIN b JOIN c ON a.id=c.id",
		"SELECT a.id, c.value FROM a, b JOIN LATERAL (SELECT a.id AS value) c ON b.id>0",
	} {
		t.Run(sql, func(t *testing.T) {
			tree, err := gosqlx.ParseWithDialect(sql, keywords.DialectPostgreSQL)
			if err != nil {
				t.Fatal(err)
			}
			defer ast.ReleaseAST(tree)
			expected := []lineage.Column{column("id", "INT", "a", "id"), column("value", "TEXT", "c", "value")}
			if strings.Contains(sql, "LATERAL") {
				expected[1] = column("value", "INT", "a", "id")
			}
			for _, rendered := range []string{sql, tree.SQL(), formatter.FormatStatement(tree.Statements[0], ast.FormatOptions{})} {
				r := analyze(t, rendered, schema, keywords.DialectPostgreSQL)
				if !reflect.DeepEqual(r.Columns, expected) {
					t.Fatalf("%s: %+v", rendered, r)
				}
			}
		})
	}
	for _, sql := range []string{
		"SELECT a.id FROM a, b JOIN c ON a.id=c.id",
		"SELECT a.id FROM b JOIN c ON a.id=c.id, a",
		"SELECT a.id FROM a, b JOIN c ON id=c.id",
	} {
		if r, err := lineage.Analyze(sql, schema, keywords.DialectPostgreSQL); err == nil || r != nil {
			t.Fatalf("illegal ON scope accepted: %s %+v", sql, r)
		}
	}
}

func TestPivotUnpivotColumns(t *testing.T) {
	schema := lineage.Schema{"SALES": {{Name: "PRODUCT", Type: "VARCHAR"}, {Name: "QUARTER", Type: "VARCHAR"}, {Name: "AMOUNT", Type: "INT"}}}
	for _, tc := range []struct {
		sql  string
		want []lineage.Column
	}{
		{"SELECT * FROM sales PIVOT(SUM(amount) FOR quarter IN ('Q1', 'Q2')) AS p", []lineage.Column{column("PRODUCT", "VARCHAR", "SALES", "PRODUCT"), column("'Q1'", "BIGINT", "SALES", "AMOUNT"), column("'Q2'", "BIGINT", "SALES", "AMOUNT")}},
		{"SELECT * FROM sales PIVOT(AVG(amount) AS avg FOR quarter IN ('Q1' AS first, 'Q2' AS second)) AS p", []lineage.Column{column("PRODUCT", "VARCHAR", "SALES", "PRODUCT"), column("FIRST_AVG", "DOUBLE", "SALES", "AMOUNT"), column("SECOND_AVG", "DOUBLE", "SALES", "AMOUNT")}},
		{"SELECT p.* FROM sales PIVOT(MAX(amount) FOR quarter IN ('Q1' AS \"MiX\", 'Q2' AS last)) AS p", []lineage.Column{column("PRODUCT", "VARCHAR", "SALES", "PRODUCT"), column("MiX", "INT", "SALES", "AMOUNT"), column("LAST", "INT", "SALES", "AMOUNT")}},
	} {
		t.Run(tc.sql, func(t *testing.T) {
			r := analyze(t, tc.sql, schema, keywords.DialectSnowflake)
			if !reflect.DeepEqual(r.Columns, tc.want) {
				t.Fatalf("got %+v want %+v", r.Columns, tc.want)
			}
			deps := []lineage.Dependency{{Name: "PRODUCT", Upstream: []lineage.Source{{Table: "SALES", Column: "PRODUCT"}}}, {Name: "QUARTER", Upstream: []lineage.Source{{Table: "SALES", Column: "QUARTER"}}}}
			if !reflect.DeepEqual(r.NonSelectedColumns, deps) {
				t.Fatalf("controls: %+v", r.NonSelectedColumns)
			}
		})
	}
	uSchema := lineage.Schema{"SALES": {{Name: "PRODUCT", Type: "VARCHAR"}, {Name: "Q1", Type: "INT"}, {Name: "Q2", Type: "INT"}}}
	refs := []lineage.Source{{Table: "SALES", Column: "Q1"}, {Table: "SALES", Column: "Q2"}}
	want := []lineage.Column{column("PRODUCT", "VARCHAR", "SALES", "PRODUCT"), {Name: "QUARTER", Type: "VARCHAR", Upstream: refs}, {Name: "AMOUNT", Type: "INT", Upstream: refs}}
	for _, modifier := range []string{"", "INCLUDE NULLS ", "EXCLUDE NULLS "} {
		r := analyze(t, "SELECT * FROM sales UNPIVOT "+modifier+"(amount FOR quarter IN(q1 AS 'first',q2 AS 'second')) AS p", uSchema, keywords.DialectSnowflake)
		if !reflect.DeepEqual(r.Columns, want) {
			t.Fatalf("unpivot %s: %+v", modifier, r.Columns)
		}
		if (len(r.NonSelectedColumns) == 0) != (modifier == "INCLUDE NULLS ") {
			t.Fatalf("unpivot null controls: %+v", r)
		}
	}
}

func TestModifierQueryExtraction(t *testing.T) {
	schema := lineage.Schema{"SALES": {{Name: "PRODUCT", Type: "VARCHAR"}, {Name: "QUARTER", Type: "VARCHAR"}, {Name: "AMOUNT", Type: "INT"}}}
	for _, tc := range []struct {
		sql  string
		want []lineage.Column
	}{
		{`SELECT p.* FROM sales AS s PIVOT(SUM(s.amount) FOR quarter IN ('Q1' AS "MiX", 'Q2' AS later)) AS p`, []lineage.Column{column("PRODUCT", "VARCHAR", "SALES", "PRODUCT"), column("MiX", "BIGINT", "SALES", "AMOUNT"), column("LATER", "BIGINT", "SALES", "AMOUNT")}},
		{`SELECT * FROM sales PIVOT(SUM(amount) FOR quarter IN ('Q1','Q2') DEFAULT ON NULL (0)) p`, []lineage.Column{column("PRODUCT", "VARCHAR", "SALES", "PRODUCT"), column("'Q1'", "BIGINT", "SALES", "AMOUNT"), column("'Q2'", "BIGINT", "SALES", "AMOUNT")}},
		{`SELECT * FROM sales MATCH_RECOGNIZE (PARTITION BY product ORDER BY quarter MEASURES FIRST(A.amount) AS first_amount ONE ROW PER MATCH AFTER MATCH SKIP PAST LAST ROW PATTERN (A+)) AS m`, []lineage.Column{column("PRODUCT", "VARCHAR", "SALES", "PRODUCT"), column("FIRST_AMOUNT", "INT", "SALES", "AMOUNT")}},
		{`SELECT * FROM sales CHANGES(INFORMATION => DEFAULT) AT(OFFSET => -60) END(OFFSET => -1)`, []lineage.Column{column("PRODUCT", "VARCHAR", "SALES", "PRODUCT"), column("QUARTER", "VARCHAR", "SALES", "QUARTER"), column("AMOUNT", "INT", "SALES", "AMOUNT"), {Name: "METADATA$ACTION", Type: "VARCHAR", Upstream: []lineage.Source{}}, {Name: "METADATA$ISUPDATE", Type: "BOOLEAN", Upstream: []lineage.Source{}}, {Name: "METADATA$ROW_ID", Type: "VARCHAR", Upstream: []lineage.Source{}}}},
	} {
		t.Run(tc.sql, func(t *testing.T) {
			extracted, err := lineage.ExtractQuery("CREATE TABLE result AS "+tc.sql, keywords.DialectSnowflake)
			if err != nil {
				t.Fatal(err)
			}
			queries := []string{tc.sql, extracted}
			// The public AST does not preserve general identifier quotes;
			// ExtractQuery owns that preservation. Exercise both renderers for
			// unquoted modifiers, including their non-lineage syntax.
			if !strings.Contains(tc.sql, `"`) {
				tree, err := gosqlx.ParseWithDialect(tc.sql, keywords.DialectSnowflake)
				if err != nil {
					t.Fatal(err)
				}
				defer ast.ReleaseAST(tree)
				queries = append(queries, tree.SQL(), formatter.FormatStatement(tree.Statements[0], ast.FormatOptions{}))
			}
			for _, sql := range queries {
				if strings.Contains(tc.sql, "DEFAULT ON NULL") && !strings.Contains(sql, "DEFAULT ON NULL") {
					t.Fatalf("lost default during rendering: %s", sql)
				}
				if got := analyze(t, sql, schema, keywords.DialectSnowflake); !reflect.DeepEqual(got.Columns, tc.want) {
					t.Fatalf("%s: %+v, want %+v", sql, got.Columns, tc.want)
				}
			}
		})
	}
	for _, values := range []string{"ANY ORDER BY quarter", "SELECT quarter FROM quarters"} {
		sql := "SELECT * FROM sales PIVOT(SUM(amount) FOR quarter IN (" + values + "))"
		extracted, err := lineage.ExtractQuery(sql, keywords.DialectSnowflake)
		if err != nil {
			t.Fatal(err)
		}
		for _, query := range []string{sql, extracted} {
			if got, err := lineage.Analyze(query, schema, keywords.DialectSnowflake); got != nil || err == nil || !strings.Contains(err.Error(), "dynamic PIVOT requires data values") {
				t.Fatalf("data-dependent schema invented: %s %+v %v", query, got, err)
			}
		}
	}
	for _, value := range []string{"amount", "quarter", "missing"} {
		sql := "SELECT * FROM sales PIVOT(SUM(amount) FOR quarter IN ('Q1') DEFAULT ON NULL (" + value + "))"
		if got, err := lineage.Analyze(sql, schema, keywords.DialectSnowflake); got != nil || err == nil {
			t.Fatalf("invalid default input: %s %+v %v", sql, got, err)
		}
	}
	if got, err := lineage.Analyze("SELECT * FROM sales MATCH_RECOGNIZE (ONE ROW PER MATCH PATTERN (A+))", schema, keywords.DialectSnowflake); got != nil || err == nil || !strings.Contains(err.Error(), "no output columns") {
		t.Fatalf("accepted empty pattern schema: %+v %v", got, err)
	}
}
