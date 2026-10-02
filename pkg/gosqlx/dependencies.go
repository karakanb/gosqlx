package gosqlx

import (
	"sort"
	"strings"

	"github.com/ajitpratap0/GoSQLX/pkg/sql/ast"
	"github.com/ajitpratap0/GoSQLX/pkg/sql/keywords"
)

// ExtractTableDependencies returns sorted physical table references, including
// write targets. Unlike the legacy ExtractTables collector, CTE names and
// table-function names are not physical tables. USE context applies in statement
// order (SQL Server defaults an omitted schema to dbo).
func ExtractTableDependencies(tree *ast.AST, dialect keywords.SQLDialect) []string {
	result := map[string]bool{}
	key := func(name string, quoted bool) string {
		switch dialect {
		case keywords.DialectPostgreSQL, keywords.DialectRedshift:
			if quoted {
				return name
			}
		case keywords.DialectSnowflake, keywords.DialectOracle:
			if quoted {
				return name
			}
			return strings.ToUpper(name)
		case keywords.DialectClickHouse:
			return name
		}
		return strings.ToLower(name)
	}
	database := ""
	add := func(name string) {
		if name == "" {
			return
		}
		if database != "" && (dialect == keywords.DialectSQLServer || dialect == keywords.DialectFabric) {
			switch strings.Count(name, ".") {
			case 0:
				name = database + ".dbo." + name
			case 1:
				name = database + "." + name
			}
		}
		result[name] = true
	}
	var walk func(ast.Node, map[string]bool)
	var with func(*ast.WithClause, map[string]bool) map[string]bool
	with = func(w *ast.WithClause, outer map[string]bool) map[string]bool {
		env := map[string]bool{}
		for k, v := range outer {
			env[k] = v
		}
		if w == nil {
			return env
		}
		for _, c := range w.CTEs {
			name := key(c.Name, c.NameQuoted)
			if w.Recursive {
				env[name] = true
			}
			walk(c.Statement, env)
			walk(c.ScalarExpr, env)
			env[name] = true
		}
		return env
	}
	walk = func(node ast.Node, env map[string]bool) {
		if node == nil {
			return
		}
		switch n := node.(type) {
		case *ast.SelectStatement:
			if n == nil {
				return
			}
			add(n.Into)
			env = with(n.With, env)
		case *ast.SetOperation:
			env = with(n.With, env)
		case *ast.WithClause:
			return // Already visited in the owning query's lexical scope.
		case *ast.JoinClause:
			walk(&n.Right, env) // Left is synthetic and/or shared with FROM.
			walk(n.Condition, env)
			return
		case *ast.TableReference:
			if n.TableFunc == nil && n.Subquery == nil && n.Query == nil && !env[key(n.Name, n.NameQuoted)] {
				add(n.Name)
			}
		case *ast.InsertStatement:
			add(n.TableName)
			env = with(n.With, env)
		case *ast.UpdateStatement:
			add(n.TableName)
			env = with(n.With, env)
		case *ast.DeleteStatement:
			add(n.TableName)
			env = with(n.With, env)
		case *ast.CreateTableStatement:
			add(n.Name)
		case *ast.CreateViewStatement:
			add(n.Name)
		case *ast.CreateMaterializedViewStatement:
			add(n.Name)
		case *ast.UseStatement:
			database = n.Name
			return
		}
		for _, child := range node.Children() {
			walk(child, env)
		}
	}
	if tree != nil {
		for _, stmt := range tree.Statements {
			walk(stmt, nil)
		}
	}
	out := make([]string, 0, len(result))
	for name := range result {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
