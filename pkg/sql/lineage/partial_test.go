package lineage_test

import (
	"github.com/ajitpratap0/GoSQLX/pkg/sql/keywords"
	"github.com/ajitpratap0/GoSQLX/pkg/sql/lineage"
	"reflect"
	"testing"
)

func TestOutputFailureIsolation(t *testing.T) {
	schema := lineage.Schema{"a": {{Name: "id", Type: "INT"}, {Name: "name", Type: "VARCHAR"}}}
	for _, sql := range []string{
		"SELECT id, id OPERATOR(public.===) id AS bad, name FROM a WHERE id>0",
		"SELECT id, bad, name FROM (SELECT id, id OPERATOR(public.===) id AS bad, name FROM a) q WHERE id>0",
		"WITH q AS (SELECT id, id OPERATOR(public.===) id AS bad, name FROM a) SELECT id,bad,name FROM q WHERE id>0",
		"SELECT id, 1 AS bad,name FROM a WHERE id>0 UNION ALL SELECT id,id OPERATOR(public.===) id,name FROM a",
		"SELECT id, (SELECT id OPERATOR(public.===) id FROM a) AS bad,name FROM a WHERE id>0",
		"SELECT id, id IN (SELECT id OPERATOR(public.===) id FROM a) AS bad,name FROM a WHERE id>0",
		"SELECT id, ARRAY(SELECT id OPERATOR(public.===) id FROM a) AS bad,name FROM a WHERE id>0",
		"SELECT id, id = ANY(SELECT id OPERATOR(public.===) id FROM a) AS bad,name FROM a WHERE id>0",
	} {
		t.Run(sql, func(t *testing.T) {
			r := analyze(t, sql, schema, keywords.DialectPostgreSQL)
			if !r.Partial || len(r.Columns) != 3 || r.Columns[1].Error == "" || r.Columns[1].Name != "bad" || len(r.Columns[1].Upstream) != 0 {
				t.Fatalf("partial diagnostics/positions: %+v", r)
			}
			if !reflect.DeepEqual(r.Columns[0], column("id", "INT", "a", "id")) || !reflect.DeepEqual(r.Columns[2], column("name", "VARCHAR", "a", "name")) {
				t.Fatalf("healthy outputs lost: %+v", r)
			}
			if !reflect.DeepEqual(r.NonSelectedColumns, []lineage.Dependency{{Name: "id", Upstream: []lineage.Source{{Table: "a", Column: "id"}}}}) {
				t.Fatalf("healthy controls lost: %+v", r)
			}
		})
	}
	for _, sql := range []string{
		"SELECT id, (id OPERATOR(public.===) id) + missing FROM a",
		"SELECT id, id OPERATOR(public.===) missing AS bad FROM a",
		"SELECT id FROM a WHERE id OPERATOR(public.===) id",
		"SELECT id, id OPERATOR(public.===) id AS bad FROM a ORDER BY bad",
		"SELECT id, id OPERATOR(public.===) id AS bad FROM a ORDER BY 2",
		"SELECT id FROM a WHERE id IN (SELECT id OPERATOR(public.===) id FROM a)",
		"SELECT id FROM a EXCEPT SELECT id OPERATOR(public.===) id FROM a",
		"SELECT * FROM (SELECT id OPERATOR(public.===) id AS id FROM a) q JOIN a USING(id)",
		"SELECT SUM(id OPERATOR(public.===) id) OVER (ORDER BY missing) FROM a",
	} {
		if r, err := lineage.Analyze(sql, schema, keywords.DialectPostgreSQL); err == nil || r != nil {
			t.Fatalf("schema/control failure became partial success: %s %+v", sql, r)
		}
	}
}
