package lineage

import (
	"fmt"
	"strings"

	"github.com/ajitpratap0/GoSQLX/pkg/models"
	"github.com/ajitpratap0/GoSQLX/pkg/sql/ast"
	"github.com/ajitpratap0/GoSQLX/pkg/sql/keywords"
	"github.com/ajitpratap0/GoSQLX/pkg/sql/parser"
	"github.com/ajitpratap0/GoSQLX/pkg/sql/tokenizer"
)

// parseSQL retains quoted identifier spelling in the string-based native AST.
// Callers own the returned tree and must release it. This is the native grammar,
// not a second SQL parser.
func parseSQL(sql string, dialect keywords.SQLDialect, spellings map[string]string) (*ast.AST, error) {
	if dialect == "" {
		dialect = keywords.DialectGeneric
	}
	if !keywords.IsValidDialect(string(dialect)) {
		return nil, fmt.Errorf("unsupported dialect %q", dialect)
	}
	tk, err := tokenizer.NewWithDialect(dialect)
	if err != nil {
		return nil, err
	}
	defer tokenizer.PutTokenizer(tk)
	tokens, err := tk.Tokenize([]byte(sql))
	if err != nil {
		return nil, err
	}
	prefix := "__gosqlx_quoted_"
	for strings.Contains(sql, prefix) {
		prefix += "_"
	}
	for i := range tokens {
		t := &tokens[i].Token
		if t.Quote == '"' && (dialect == keywords.DialectBigQuery || dialect == keywords.DialectMySQL || dialect == keywords.DialectMariaDB || dialect == keywords.DialectSpark || dialect == keywords.DialectDatabricks || dialect == keywords.DialectDoris || dialect == keywords.DialectStarRocks) {
			t.Type = models.TokenTypeSingleQuotedString
		} else if t.Quote == '"' || t.Quote == '`' || t.Quote == '[' {
			if spellings == nil {
				t.Value = `"` + strings.ReplaceAll(t.Value, `"`, `""`) + `"`
			} else {
				// The SQL renderer expects catalog names, not quoted spellings.
				// Collision-free identifiers round-trip delimited tokens without
				// changing shared AST rendering or re-parsing SQL with regexes.
				key := fmt.Sprintf("%s%d_x", prefix, i)
				close := string(t.Quote)
				if t.Quote == '[' {
					close = "]"
				}
				spellings[key] = string(t.Quote) + strings.ReplaceAll(t.Value, close, close+close) + close
				t.Value = key
			}
			// Private token values already carry delimiters (or placeholders).
			// Clause renderers must not wrap them a second time.
			t.Quote = 0
			if t.Word != nil {
				word := *t.Word
				word.QuoteStyle = 0
				t.Word = &word
			}
		}
	}
	p := parser.NewParser(parser.WithDialect(string(dialect)))
	defer p.Release()
	return p.ParseFromModelTokens(tokens)
}

// ExtractQuery unwraps the query in one CREATE TABLE/VIEW AS or INSERT SELECT,
// or accepts a query directly. SELECT INTO is reduced to a read. Other writes,
// modifying CTEs and multiple statements are rejected. The returned SQL uses
// the AST's canonical rendering, not a dialect transpiler or execution-safety
// guarantee (function bodies can have side effects).
func ExtractQuery(sql string, dialect keywords.SQLDialect) (string, error) {
	spellings := map[string]string{}
	tree, err := parseSQL(sql, dialect, spellings)
	if err != nil {
		return "", err
	}
	defer ast.ReleaseAST(tree)
	if len(tree.Statements) != 1 {
		return "", fmt.Errorf("query extraction requires one statement")
	}
	stmt := tree.Statements[0]
	switch s := stmt.(type) {
	case *ast.CreateTableStatement:
		stmt = s.Query
	case *ast.CreateViewStatement:
		stmt = s.Query
	case *ast.CreateMaterializedViewStatement:
		stmt = s.Query
	case *ast.InsertStatement:
		stmt = s.Query
		if s.With != nil {
			var with **ast.WithClause
			switch q := stmt.(type) {
			case *ast.SelectStatement:
				with = &q.With
			case *ast.SetOperation:
				with = &q.With
			}
			if with == nil || *with != nil {
				return "", fmt.Errorf("cannot attach INSERT WITH to query")
			}
			*with, s.With = s.With, nil
		}
	}
	if _, ok := stmt.(ast.QueryExpression); !ok {
		return "", fmt.Errorf("statement has no extractable query")
	}
	var validate func(ast.Node) error
	validate = func(n ast.Node) error {
		if s, ok := n.(ast.Statement); ok {
			switch s.(type) {
			case ast.QueryExpression, *ast.WithClause, *ast.CommonTableExpr, *ast.TableReference, *ast.WindowSpec, *ast.WindowFrame:
			default:
				return fmt.Errorf("write in query: %T", s)
			}
		}
		if q, ok := n.(*ast.SelectStatement); ok {
			q.Into = ""
			if dialect == keywords.DialectFabric {
				// Fabric requires explicit case-preserving output aliases when
				// extracting CTE queries for Bruin's schema qualification step.
				for i, expr := range q.Columns {
					if id, ok := expr.(*ast.Identifier); ok && id.Name != "*" {
						q.Columns[i] = &ast.AliasedExpression{Expr: id, Alias: id.Name}
					}
				}
			}
		}
		for _, child := range n.Children() {
			if err := validate(child); err != nil {
				return err
			}
		}
		return nil
	}
	if err := validate(stmt); err != nil {
		return "", err
	}
	rendered := (ast.AST{Statements: []ast.Statement{stmt}}).SQL()
	for key, spelling := range spellings {
		rendered = strings.ReplaceAll(rendered, key, spelling)
	}
	return rendered, nil
}
