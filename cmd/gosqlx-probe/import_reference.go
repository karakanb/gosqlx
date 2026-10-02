//go:build ignore

// Export literal Bruin Go test cases without linking Bruin or running Python.
// Usage: go run cmd/gosqlx-probe/import_reference.go -- reference/parser_test.go
package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
)

func literal(e ast.Expr) (any, error) {
	switch n := e.(type) {
	case *ast.BasicLit:
		if n.Kind == token.STRING {
			return strconv.Unquote(n.Value)
		}
		return strconv.Atoi(n.Value)
	case *ast.Ident:
		switch n.Name {
		case "nil":
			return nil, nil
		case "true":
			return true, nil
		case "false":
			return false, nil
		}
	case *ast.UnaryExpr:
		if n.Op == token.AND {
			return literal(n.X)
		}
	case *ast.BinaryExpr:
		l, e1 := literal(n.X)
		r, e2 := literal(n.Y)
		if ls, ok := l.(string); ok && e1 == nil && e2 == nil && n.Op == token.ADD {
			if rs, ok := r.(string); ok {
				return ls + rs, nil
			}
		}
	case *ast.CompositeLit:
		m := map[string]any{}
		a := []any{}
		for _, e := range n.Elts {
			if kv, ok := e.(*ast.KeyValueExpr); ok {
				var key string
				if id, ok := kv.Key.(*ast.Ident); ok {
					key = id.Name
				} else {
					k, err := literal(kv.Key)
					if err != nil {
						return nil, err
					}
					key = fmt.Sprint(k)
				}
				v, err := literal(kv.Value)
				if err != nil {
					return nil, err
				}
				m[key] = v
			} else {
				v, err := literal(e)
				if err != nil {
					return nil, err
				}
				a = append(a, v)
			}
		}
		if len(m) > 0 {
			return m, nil
		}
		return a, nil
	}
	return nil, fmt.Errorf("nonliteral %T", e)
}

func main() {
	path := os.Args[len(os.Args)-1]
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		panic(err)
	}
	out := []map[string]any{}
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		name := fn.Name.Name
		if name != "TestGetLineageForRunner" && name != "TestSqlParser_ExtractSelect" && !strings.HasPrefix(name, "TestSqlParser_GetTables") && name != "TestDorisDialectUsedTables" {
			continue
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			lit, ok := node.(*ast.CompositeLit)
			if !ok {
				return true
			}
			array, ok := lit.Type.(*ast.ArrayType)
			if !ok {
				return true
			}
			st, ok := array.Elt.(*ast.StructType)
			if !ok {
				return true
			}
			var fields []string
			for _, f := range st.Fields.List {
				for _, n := range f.Names {
					fields = append(fields, n.Name)
				}
			}
			for i, e := range lit.Elts {
				v, err := literal(e)
				if err != nil {
					out = append(out, map[string]any{"test": name, "index": i, "export_error": err.Error()})
					continue
				}
				m, ok := v.(map[string]any)
				if !ok {
					m = map[string]any{}
					vals, ok := v.([]any)
					if !ok {
						panic("invalid test literal")
					}
					for j, v := range vals {
						m[fields[j]] = v
					}
				}
				m["test"] = name
				m["index"] = i
				out = append(out, m)
			}
			return false
		})
	}
	e := json.NewEncoder(os.Stdout)
	e.SetIndent("", "  ")
	if err := e.Encode(out); err != nil {
		panic(err)
	}
}
