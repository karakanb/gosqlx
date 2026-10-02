package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/ajitpratap0/GoSQLX/pkg/sql/keywords"
	"github.com/ajitpratap0/GoSQLX/pkg/sql/lineage"
)

func probe(t *testing.T, query, dialect string, schema any) response {
	t.Helper()
	line, err := json.Marshal(map[string]any{"query": query, "dialect": dialect, "schema": schema})
	if err != nil {
		t.Fatal(err)
	}
	return processLine(line)
}

func TestNativeReferencesAndLineage(t *testing.T) {
	query := "SELECT u.name AS result FROM public.users u JOIN app.orders o ON u.id = o.user_id WHERE o.amount > 5 GROUP BY u.name, o.region ORDER BY o.created_at"
	out := probe(t, query, "postgres", map[string]any{
		"public.users": map[string]string{"name": "TEXT", "id": "INT"},
		"app.orders":   map[string]string{"user_id": "INT", "amount": "DECIMAL", "region": "TEXT", "created_at": "TIMESTAMP"},
	})
	if out.ParseError != "" || out.TablesError != "" || out.NativeError != "" {
		t.Fatalf("unexpected error: %+v", out)
	}
	if !reflect.DeepEqual(out.Tables, []string{"app.orders", "public.users"}) {
		t.Fatalf("tables = %v", out.Tables)
	}
	var refs []string
	for _, ref := range out.Native.ColumnReferences {
		refs = append(refs, ref.String())
	}
	// All clauses are mixed together; alias qualifiers must NOT become resolved tables.
	want := []string{"o.amount", "o.created_at", "o.region", "o.user_id", "u.id", "u.name"}
	if !reflect.DeepEqual(refs, want) {
		t.Fatalf("native columns = %v, want %v", refs, want)
	}
	wantColumns := []lineage.Column{{Name: "result", Type: "TEXT", Upstream: []lineage.Source{{Table: "public.users", Column: "name"}}}}
	if out.LineageError != "" || !reflect.DeepEqual(out.Columns, wantColumns) || out.LineageNS <= 0 {
		t.Fatalf("wrong output lineage: %+v", out)
	}
	if out.SchemaStatus != "used_for_lineage" || out.ParseNS <= 0 || out.TablesNS <= 0 || out.NativeColumnsNS <= 0 {
		t.Fatalf("incorrect status/timing: %+v", out)
	}
	withoutSchema := probe(t, query, "postgres", nil)
	if !reflect.DeepEqual(out.Native, withoutSchema.Native) {
		t.Fatal("schema must not change native references")
	}
	if withoutSchema.LineageError != "" || len(withoutSchema.LineageWarnings) == 0 || withoutSchema.Columns[0].Type != "UNKNOWN" {
		t.Fatal("explicit qualified references without schema must warn about unverified types")
	}
}

func TestNativeTableSemantics(t *testing.T) {
	for _, tc := range []struct {
		name, query string
		want        []string
	}{
		{"cte is not physical", "WITH c AS (SELECT id FROM public.users) SELECT id FROM c", []string{"public.users"}},
		{"insert target remains", "INSERT INTO dst SELECT id FROM src", []string{"dst", "src"}},
		{"ctas includes target and source", "CREATE TABLE dst AS SELECT id FROM src", []string{"dst", "src"}},
		{"derived table", "SELECT q.id FROM (SELECT id FROM base) q", []string{"base"}},
		{"constant", "SELECT 42", []string{}},
		{"wildcard", "SELECT * FROM users", []string{"users"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := probe(t, tc.query, "snowflake", map[string]any{})
			if out.ParseError != "" || out.TablesError != "" || out.NativeError != "" {
				t.Fatalf("unexpected error: %+v", out)
			}
			if !reflect.DeepEqual(out.Tables, tc.want) {
				t.Fatalf("tables = %v, want %v", out.Tables, tc.want)
			}
		})
	}
}

func TestDialectStatus(t *testing.T) {
	for _, dialect := range keywords.AllDialects() {
		out := probe(t, "SELECT 1", string(dialect), nil)
		if out.ParseError != "" || out.NativeDialect != string(dialect) {
			t.Fatalf("native dialect not used: %+v", out)
		}
		want := "native"
		switch dialect {
		case keywords.DialectGeneric:
			want = "native_generic"
		}
		if out.DialectStatus != want {
			t.Fatalf("%s: status %s, want %s", dialect, out.DialectStatus, want)
		}
	}
	for _, dialect := range []string{"unknown", "not-a-database"} {
		out := probe(t, "SELECT 1", dialect, nil)
		if out.DialectStatus != "unsupported" || out.ParseError == "" || out.ParseNS != 0 || out.NativeDialect != "" {
			t.Fatalf("must not silently fall back for %s: %+v", dialect, out)
		}
	}
	for _, tc := range []struct{ query, dialect, native string }{
		{"SELECT TOP 5 id FROM dbo.users", "tsql", "sqlserver"},
		{"SELECT name ILIKE 'a%' FROM users", " POSTGRES ", "postgresql"},
		{"SELECT 1", "", "generic"},
	} {
		out := probe(t, tc.query, tc.dialect, nil)
		if out.ParseError != "" || out.NativeDialect != tc.native {
			t.Fatalf("alias/dialect syntax: %+v", out)
		}
	}
}

func TestBatchContinuesAfterErrorsAndLongLines(t *testing.T) {
	long, err := json.Marshal(map[string]string{"query": "SELECT '" + strings.Repeat("a", 70_000) + "'"})
	if err != nil {
		t.Fatal(err)
	}
	input := "not json\n{\"query\":\"SELECT FROM\"}\n{\"query\":\"SELECT 1\",\"dialect\":\"unsupported\"}\n" + string(long) + "\n{\"query\":\"SELECT id FROM final_table\"}"
	var output bytes.Buffer
	if err := run(strings.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(&output)
	for i := 0; i < 5; i++ {
		var out response
		if err := decoder.Decode(&out); err != nil {
			t.Fatalf("line %d: %v", i, err)
		}
		if (out.ParseError != "") != (i < 3) {
			t.Fatalf("line %d: unexpected result %+v", i, out)
		}
		if out.Tables == nil || out.Columns == nil {
			t.Fatalf("line %d: arrays must not be null", i)
		}
		if i == 4 && !reflect.DeepEqual(out.Tables, []string{"final_table"}) {
			t.Fatalf("final unterminated line was not processed: %+v", out)
		}
	}
	if err := decoder.Decode(new(response)); err != io.EOF {
		t.Fatalf("extra output: %v", err)
	}
}

func TestInvalidRequest(t *testing.T) {
	for _, line := range []string{"", "null", "{}", `{"query":null}`, `{"query":1}`} {
		if out := processLine([]byte(line)); !strings.HasPrefix(out.ParseError, "invalid request:") {
			t.Fatalf("%q: %+v", line, out)
		}
	}
}

func TestSchemaErrorsDoNotPreventNativeAnalysis(t *testing.T) {
	for _, tc := range []struct {
		name, dialect, schema, errorPrefix string
	}{
		{"malformed nested schema", "postgres", `{"catalog":{"users":{"id":42}}}`, "invalid schema:"},
		{"unsupported shape", "postgres", `[]`, "invalid schema:"},
		{"normalization collision", "snowflake", `{"users":{"id":"INT"},"USERS":{"id":"INT"}}`, "ambiguous schema"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := probe(t, "SELECT id FROM users", tc.dialect, json.RawMessage(tc.schema))
			if out.ParseError != "" || out.TablesError != "" || out.NativeError != "" || out.DialectStatus != "native" {
				t.Fatalf("schema error leaked into native analysis: %+v", out)
			}
			if !reflect.DeepEqual(out.Tables, []string{"users"}) || len(out.Native.ColumnReferences) != 1 || out.Native.ColumnReferences[0].String() != "id" || len(out.Native.StatementTypes) != 1 {
				t.Fatalf("native results lost: %+v", out)
			}
			if out.ParseNS <= 0 || out.TablesNS <= 0 || out.NativeColumnsNS <= 0 || out.LineageNS <= 0 {
				t.Fatalf("stages were not measured: %+v", out)
			}
			if !strings.HasPrefix(out.LineageError, tc.errorPrefix) || out.SchemaStatus != "invalid" || len(out.Columns) != 0 || len(out.NonSelectedColumns) != 0 {
				t.Fatalf("schema error must fail only lineage, without partial output: %+v", out)
			}
		})
	}
	out := probe(t, "SELECT FROM", "postgres", json.RawMessage(`[]`))
	if out.ParseError == "" || strings.HasPrefix(out.ParseError, "invalid request:") || out.ParseNS <= 0 || out.LineageNS != 0 || out.SchemaStatus != "not_evaluated" {
		t.Fatalf("invalid SQL must be diagnosed before schema: %+v", out)
	}
}

func TestPartialOutputIsNotCompleteLineage(t *testing.T) {
	out := probe(t, "SELECT id,id OPERATOR(public.===) id AS bad,name FROM a", "postgres", json.RawMessage(`{"a":{"id":"int","name":"varchar"}}`))
	if out.ParseError != "" || out.TablesError != "" || !out.LineagePartial || !strings.Contains(out.LineageError, "partial analysis") || len(out.Columns) != 3 {
		t.Fatalf("partial result lost/classified as complete: %+v", out)
	}
	if out.Columns[1].Error == "" || out.Columns[0].Error != "" || out.Columns[2].Error != "" || out.Columns[2].Upstream[0].Column != "name" {
		t.Fatalf("wrong output isolation: %+v", out.Columns)
	}
}

func TestMeasureRecoversPanicsAndPreservesErrors(t *testing.T) {
	ns, err := measure(func() error { panic("test panic") })
	if ns <= 0 || err == nil || err.Error() != "panic: test panic" {
		t.Fatalf("panic not recorded: ns=%d err=%v", ns, err)
	}
	sentinel := errors.New("native failure")
	_, err = measure(func() error { return sentinel })
	if !errors.Is(err, sentinel) {
		t.Fatalf("lost native error: %v", err)
	}
	_, err = measure(func() error { return nil })
	if err != nil {
		t.Fatalf("operation after panic failed: %v", err)
	}
}
