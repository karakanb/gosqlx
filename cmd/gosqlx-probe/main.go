// gosqlx-probe is an offline JSON-lines adapter for native GoSQLX APIs.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/ajitpratap0/GoSQLX/pkg/gosqlx"
	"github.com/ajitpratap0/GoSQLX/pkg/sql/ast"
	"github.com/ajitpratap0/GoSQLX/pkg/sql/keywords"
	"github.com/ajitpratap0/GoSQLX/pkg/sql/lineage"
)

type request struct {
	Query   *string         `json:"query"`
	Dialect string          `json:"dialect"`
	Schema  json.RawMessage `json:"schema"`
}

type nativeOutput struct {
	TableReferences  []gosqlx.QualifiedName `json:"table_references"`
	ColumnReferences []gosqlx.QualifiedName `json:"column_references"`
	StatementTypes   []string               `json:"statement_types"`
}

type response struct {
	ParseError         string               `json:"parse_error"`
	Tables             []string             `json:"tables"`
	Columns            []lineage.Column     `json:"columns"`
	NonSelectedColumns []lineage.Dependency `json:"non_selected_columns"`
	LineageWarnings    []string             `json:"lineage_warnings"`
	LineageError       string               `json:"lineage_error"`
	LineagePartial     bool                 `json:"lineage_partial"`
	ParseNS            int64                `json:"parse_ns"`
	TablesNS           int64                `json:"tables_ns"`
	LineageNS          int64                `json:"lineage_ns"`
	DialectStatus      string               `json:"dialect_status"`
	NativeDialect      string               `json:"native_dialect"`
	SchemaStatus       string               `json:"schema_status"`
	TablesSemantics    string               `json:"tables_semantics"`
	TablesError        string               `json:"tables_error"`
	Native             nativeOutput         `json:"native"`
	NativeColumnsNS    int64                `json:"native_columns_ns"`
	NativeError        string               `json:"native_error"`
}

func newResponse() response {
	return response{
		Tables: []string{}, Columns: []lineage.Column{}, LineageError: "not analyzed",
		NonSelectedColumns: []lineage.Dependency{}, LineageWarnings: []string{},
		DialectStatus: "not_evaluated", SchemaStatus: "not_evaluated",
		TablesSemantics: "physical_references_including_write_targets",
		Native: nativeOutput{
			TableReferences: []gosqlx.QualifiedName{}, ColumnReferences: []gosqlx.QualifiedName{},
			StatementTypes: []string{},
		},
	}
}

func dialectFor(name string) (keywords.SQLDialect, string) {
	name = strings.ToLower(strings.TrimSpace(name))
	switch name {
	case "":
		name = "generic"
	case "postgres":
		name = "postgresql"
	case "tsql":
		name = "sqlserver"
	}
	if !keywords.IsValidDialect(name) {
		return "", "unsupported"
	}
	if name == "generic" {
		return keywords.DialectGeneric, "native_generic"
	}
	return keywords.SQLDialect(name), "native"
}

// measure isolates recoverable native panics without pretending a failed stage succeeded.
func measure(fn func() error) (ns int64, err error) {
	start := time.Now()
	defer func() {
		ns = time.Since(start).Nanoseconds()
		if p := recover(); p != nil {
			err = fmt.Errorf("panic: %v", p)
		}
	}()
	err = fn()
	return
}

func processLine(line []byte) (out response) {
	out = newResponse()
	defer func() {
		if p := recover(); p != nil {
			out.NativeError = fmt.Sprintf("query panic: %v", p)
		}
	}()
	var in request
	if err := json.Unmarshal(line, &in); err != nil {
		out.ParseError = "invalid request: " + err.Error()
		return
	}
	if in.Query == nil {
		out.ParseError = "invalid request: query must be a string"
		return
	}
	dialect, status := dialectFor(in.Dialect)
	out.NativeDialect, out.DialectStatus = string(dialect), status
	if status == "unsupported" {
		out.ParseError = fmt.Sprintf("unsupported dialect: %q", in.Dialect)
		return
	}
	var tree *ast.AST
	var err error
	out.ParseNS, err = measure(func() error {
		var parseErr error
		tree, parseErr = gosqlx.ParseWithDialect(*in.Query, dialect)
		return parseErr
	})
	if tree != nil {
		defer ast.ReleaseAST(tree)
	}
	if err != nil {
		out.ParseError = err.Error()
		return
	}
	for _, stmt := range tree.Statements {
		out.Native.StatementTypes = append(out.Native.StatementTypes, fmt.Sprintf("%T", stmt))
	}
	out.TablesNS, err = measure(func() error {
		refs := gosqlx.ExtractTablesQualified(tree)
		sort.Slice(refs, func(i, j int) bool { return refs[i].String() < refs[j].String() })
		out.Native.TableReferences = refs
		out.Tables = gosqlx.ExtractTableDependencies(tree, dialect)
		return nil
	})
	if err != nil {
		out.TablesError = err.Error()
	}
	out.NativeColumnsNS, err = measure(func() error {
		refs := gosqlx.ExtractColumnsQualified(tree)
		sort.Slice(refs, func(i, j int) bool { return refs[i].String() < refs[j].String() })
		out.Native.ColumnReferences = refs
		return nil
	})
	if err != nil {
		out.NativeError = err.Error()
	}
	out.LineageNS, err = measure(func() error {
		out.SchemaStatus = "invalid"
		var schema lineage.Schema
		if len(in.Schema) > 0 && string(in.Schema) != "null" {
			if err := json.Unmarshal(in.Schema, &schema); err != nil {
				return fmt.Errorf("invalid schema: %w", err)
			}
		}
		// Bruin schema keys are identifier spellings, not necessarily stored catalog
		// names. Its Snowflake/Oracle schemas conventionally use lowercase unquoted
		// keys. Fold those here; the native API itself expects actual catalog names.
		if dialect == keywords.DialectSnowflake || dialect == keywords.DialectOracle {
			normalized := lineage.Schema{}
			for name, fields := range schema {
				fold := func(s string) string {
					if strings.HasPrefix(s, `"`) && strings.HasSuffix(s, `"`) {
						return strings.ReplaceAll(s[1:len(s)-1], `""`, `"`)
					}
					return strings.ToUpper(s)
				}
				// Preserve explicitly quoted qualified table components. Mixed-case
				// unquoted components in such keys must already be catalog-normalized.
				if !strings.Contains(name, `"`) {
					name = strings.ToUpper(name)
				}
				if _, exists := normalized[name]; exists {
					return fmt.Errorf("ambiguous schema after dialect normalization")
				}
				for i := range fields {
					fields[i].Name = fold(fields[i].Name)
				}
				normalized[name] = fields
			}
			schema = normalized
		}
		out.SchemaStatus = "used_for_lineage"
		result, err := lineage.Analyze(*in.Query, schema, dialect)
		if err != nil {
			return err
		}
		out.Columns = result.Columns
		out.NonSelectedColumns = result.NonSelectedColumns
		out.LineageWarnings = result.Warnings
		out.LineagePartial = result.Partial
		if result.Partial {
			return fmt.Errorf("partial analysis: inspect column errors")
		}
		return nil
	})
	out.LineageError = ""
	if err != nil {
		out.LineageError = err.Error()
	}
	return
}

func run(input io.Reader, output io.Writer) error {
	reader := bufio.NewReader(input)
	encoder := json.NewEncoder(output)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			if writeErr := encoder.Encode(processLine(line)); writeErr != nil {
				return writeErr
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
