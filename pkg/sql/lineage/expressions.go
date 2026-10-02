package lineage

import (
	"strings"

	"github.com/ajitpratap0/GoSQLX/pkg/sql/ast"
	"github.com/ajitpratap0/GoSQLX/pkg/sql/keywords"
)

type value struct {
	typ    string
	refs   []Source
	fields []Column
}

func (a *analyzer) expr(e ast.Expression, s *scope) value {
	if e == nil || (a.err != nil && !isUnsupported(a.err)) {
		return value{typ: "UNKNOWN"}
	}
	switch e := e.(type) {
	case *ast.AliasedExpression:
		return a.expr(e.Expr, s)
	case *ast.Identifier:
		if e.Name == "*" {
			a.fail("star outside projection or COUNT(*)")
			return value{}
		}
		col := a.resolve(e, s)
		if col.Error != "" {
			a.unsupported("input %s: %s", col.Name, col.Error)
		}
		return value{col.Type, col.Upstream, col.fields}
	case *ast.FieldAccess:
		v := a.expr(e.Expr, s)
		col := a.fields(Column{Type: v.typ, Upstream: v.refs, fields: v.fields}, []string{a.ident(e.Name)})
		return value{col.Type, col.Upstream, col.fields}
	case *ast.LiteralValue:
		switch strings.ToLower(e.Type) {
		case "null":
			return value{typ: "NULL"}
		case "bool", "boolean":
			return value{typ: "BOOLEAN"}
		case "int", "integer":
			return value{typ: "INT"}
		case "float":
			return value{typ: "DOUBLE"}
		case "string":
			return value{typ: "VARCHAR"}
		case "placeholder":
			return value{typ: "UNKNOWN"}
		}
		a.unsupported("unsupported literal type %q", e.Type)
	case *ast.BinaryExpression:
		l, r := a.expr(e.Left, s), a.expr(e.Right, s)
		if e.CustomOp != nil {
			a.unsupported("custom operator body is unavailable")
			return value{}
		}
		v := value{typ: commonType(l.typ, r.typ), refs: sources(l.refs, r.refs)}
		switch strings.ToUpper(e.Operator) {
		case "=", "!=", "<>", "<", ">", "<=", ">=", "AND", "OR", "IS", "IS NOT", "IS NULL", "IS NOT NULL", "LIKE", "NOT LIKE", "ILIKE", "NOT ILIKE", "IS DISTINCT FROM", "IS NOT DISTINCT FROM":
			v.typ = "BOOLEAN"
		case "+", "-", "*", "/", "%":
			if !numeric(l.typ) || !numeric(r.typ) {
				v.typ = "UNKNOWN"
			}
			if e.Operator == "/" && numeric(l.typ) && numeric(r.typ) {
				v.typ = "DOUBLE"
				switch a.dialect {
				case keywords.DialectPostgreSQL, keywords.DialectTrino, keywords.DialectAthena, keywords.DialectSQLServer, keywords.DialectFabric, keywords.DialectSQLite:
					if commonType(l.typ, r.typ) == "INT" || commonType(l.typ, r.typ) == "BIGINT" {
						v.typ = "BIGINT"
					}
				}
			}
		case "||":
			v.typ = "TEXT"
		case "->>", "#>>":
			v.typ = "TEXT"
		case "->", "#>", "#-":
			v.typ = l.typ
		case "@>", "<@", "?", "?|", "?&":
			v.typ = "BOOLEAN"
		case "~", "~*", "!~", "!~*", "~~", "~~*", "!~~", "!~~*":
			v.typ = "BOOLEAN"
		case "&", "|", "#", "<<", ">>":
			v.typ = commonType(l.typ, r.typ)
		default:
			a.unsupported("unsupported binary operator %q", e.Operator)
		}
		return v
	case *ast.UnaryExpression:
		v := a.expr(e.Expr, s)
		switch strings.ToUpper(e.Operator.String()) {
		case "NOT", "IS NULL", "IS NOT NULL", "IS TRUE", "IS FALSE", "IS NOT TRUE", "IS NOT FALSE":
			v.typ = "BOOLEAN"
		case "+", "-", "PRIOR":
		default:
			a.unsupported("unsupported unary operator %s", e.Operator.String())
		}
		return v
	case *ast.CastExpression:
		v := a.expr(e.Expr, s)
		v.typ = dataType(e.Type)
		return v
	case *ast.CaseExpression:
		controls := a.expr(e.Value, s).refs
		v := value{typ: "NULL"}
		for _, when := range e.WhenClauses {
			cond, result := a.expr(when.Condition, s), a.expr(when.Result, s)
			controls = sources(controls, cond.refs)
			v = a.mergeValues(v, result)
		}
		if e.ElseClause != nil {
			other := a.expr(e.ElseClause, s)
			v = a.mergeValues(v, other)
		}
		return controlled(v, controls)
	case *ast.FunctionCall:
		return a.function(e, s)
	case *ast.SubqueryExpression:
		cols := a.query(e.Subquery, s, s.ctes)
		if len(cols) != 1 {
			a.fail("scalar subquery must return one column")
			return value{}
		}
		if cols[0].Error != "" {
			a.unsupported("subquery output: %s", cols[0].Error)
		}
		return value{cols[0].Type, cols[0].Upstream, cols[0].fields}
	case *ast.ExistsExpression:
		a.query(e.Subquery, s, s.ctes)
		return value{typ: "BOOLEAN"}
	case *ast.AnyExpression:
		return a.quantified(e.Expr, e.Subquery, s)
	case *ast.AllExpression:
		return a.quantified(e.Expr, e.Subquery, s)
	case *ast.InExpression:
		v := a.expr(e.Expr, s)
		for _, item := range e.List {
			v.refs = sources(v.refs, a.expr(item, s).refs)
		}
		if e.Subquery != nil {
			cols := a.query(e.Subquery, s, s.ctes)
			if len(cols) != 1 {
				a.fail("IN subquery must return one column")
			}
			for _, c := range cols {
				if c.Error != "" {
					a.unsupported("IN subquery output: %s", c.Error)
				}
				v.refs = sources(v.refs, c.Upstream)
			}
		}
		v.typ = "BOOLEAN"
		return v
	case *ast.BetweenExpression:
		v, lo, hi := a.expr(e.Expr, s), a.expr(e.Lower, s), a.expr(e.Upper, s)
		return value{typ: "BOOLEAN", refs: sources(v.refs, lo.refs, hi.refs)}
	case *ast.ExtractExpression:
		v := a.expr(e.Source, s)
		v.typ = "INT"
		return v
	case *ast.SubstringExpression:
		v, start, length := a.expr(e.Str, s), a.expr(e.Start, s), a.expr(e.Length, s)
		return value{typ: "TEXT", refs: sources(v.refs, start.refs, length.refs)}
	case *ast.PositionExpression:
		return value{typ: "INT", refs: sources(a.expr(e.Substr, s).refs, a.expr(e.Str, s).refs)}
	case *ast.ListExpression:
		return a.expressions(e.Values, s)
	case *ast.TupleExpression:
		return a.expressions(e.Expressions, s)
	case *ast.RollupExpression:
		return a.expressions(e.Expressions, s)
	case *ast.CubeExpression:
		return a.expressions(e.Expressions, s)
	case *ast.GroupingSetsExpression:
		v := value{typ: "UNKNOWN"}
		for _, set := range e.Sets {
			v.refs = sources(v.refs, a.expressions(set, s).refs)
		}
		return v
	case *ast.IntervalExpression:
		return value{typ: "INTERVAL"}
	case *ast.NamedArgument:
		return a.expr(e.Value, s)
	case *ast.VariantPath:
		v := a.expr(e.Root, s)
		v.typ = "VARIANT"
		for _, seg := range e.Segments {
			v.refs = sources(v.refs, a.expr(seg.Index, s).refs)
		}
		return v
	case *ast.ArrayConstructorExpression:
		v := a.expressions(e.Elements, s)
		if e.Subquery != nil {
			cols := a.query(e.Subquery, s, s.ctes)
			if len(cols) != 1 {
				a.fail("ARRAY subquery must have one column")
				return value{}
			}
			if cols[0].Error != "" {
				a.unsupported("ARRAY subquery output: %s", cols[0].Error)
			}
			v = value{cols[0].Type, cols[0].Upstream, cols[0].fields}
		}
		v.typ = "ARRAY<" + v.typ + ">"
		return v
	case *ast.ArraySubscriptExpression:
		v := a.expr(e.Array, s)
		v.typ = arrayElement(v.typ)
		return controlled(v, a.expressions(e.Indices, s).refs)
	case *ast.ArraySliceExpression:
		v := a.expr(e.Array, s)
		return controlled(v, sources(a.expr(e.Start, s).refs, a.expr(e.End, s).refs))
	default:
		// Validate child inputs before isolating this node; an unsupported
		// expression must not hide an unresolved or ambiguous input column.
		for _, child := range e.Children() {
			if expr, ok := child.(ast.Expression); ok {
				a.expr(expr, s)
			}
		}
		a.unsupported("unsupported lineage expression %T", e)
	}
	return value{typ: "UNKNOWN"}
}

func (a *analyzer) expressions(exprs []ast.Expression, s *scope) value {
	v := value{typ: "NULL"}
	for _, expr := range exprs {
		next := a.expr(expr, s)
		v = a.mergeValues(v, next)
	}
	return v
}

func (a *analyzer) quantified(expr ast.Expression, query ast.Statement, s *scope) value {
	v := a.expr(expr, s)
	cols := a.query(query, s, s.ctes)
	if len(cols) != 1 {
		a.fail("quantified subquery must return one column")
		return value{}
	}
	if cols[0].Error != "" {
		a.unsupported("quantified subquery: %s", cols[0].Error)
	}
	return value{typ: "BOOLEAN", refs: sources(v.refs, cols[0].Upstream)}
}

func (a *analyzer) function(f *ast.FunctionCall, s *scope) value {
	name := strings.ToUpper(f.Name)
	if name == "STRUCT" || name == "ROW" || name == "NAMED_STRUCT" {
		return a.constructStruct(f, s)
	}
	v := value{typ: "NULL"}
	args := []value{}
	for i, arg := range f.Arguments {
		if id, ok := arg.(*ast.Identifier); ok && id.Name == "*" && id.Table == "" && name == "COUNT" {
			continue
		}
		if i == 0 && (name == "DATEADD" || name == "DATEDIFF" || name == "TIMESTAMPDIFF") {
			id, ok := arg.(*ast.Identifier)
			if !ok || !datePart(id.Name) {
				a.fail("unsupported DATEADD unit")
				return value{}
			}
			continue
		}
		next := a.expr(arg, s)
		args = append(args, next)
		v.refs = sources(v.refs, next.refs)
		v.typ = commonType(v.typ, next.typ)
	}
	firstType := "UNKNOWN"
	if len(args) > 0 {
		firstType = args[0].typ
	}
	switch name {
	case "COALESCE", "IFNULL", "NVL", "GREATEST", "LEAST":
		v = value{typ: "NULL"}
		for _, arg := range args {
			v = a.mergeValues(v, arg)
		}
		// Choosing a whole structured value also depends on its comparison
		// or nullness. Preserve these controls when a field is projected later.
		v = controlled(v, v.refs)
	case "IF", "IFF":
		if len(args) != 3 {
			a.fail("%s requires three arguments", name)
		} else {
			v = controlled(a.mergeValues(args[1], args[2]), args[0].refs)
		}
	case "DATEDIFF", "TIMESTAMPDIFF":
		v.typ = "INT"
	case "COUNT", "LENGTH", "CHAR_LENGTH", "ROW_NUMBER", "RANK", "DENSE_RANK", "MATCH_NUMBER", "LEVEL", "ROWNUM":
		v.typ = "BIGINT"
	case "CLASSIFIER":
		v.typ = "VARCHAR"
	case "AVG":
		v.typ = "DOUBLE"
	case "SUM":
		v.typ = firstType
		if firstType == "INT" || firstType == "SMALLINT" || firstType == "TINYINT" {
			v.typ = "BIGINT"
		}
		if firstType == "BIGINT" && a.dialect == keywords.DialectPostgreSQL {
			v.typ = "DECIMAL"
		}
	case "MIN", "MAX", "MIN_BY", "MAX_BY", "ARG_MIN", "ARG_MAX", "ROUND", "FLOOR", "CEIL", "CEILING", "ABS", "FIRST_VALUE", "LAST_VALUE", "NULLIF", "FIRST", "LAST", "PREV", "NEXT":
		v.typ = firstType
		if len(args) > 0 && args[0].fields != nil {
			v = controlled(args[0], v.refs)
		}
	case "LAG", "LEAD":
		v.typ = firstType
		if len(args) > 2 {
			v.typ = commonType(firstType, args[2].typ)
		}
		if len(args) > 0 && args[0].fields != nil {
			out := args[0]
			if len(args) > 2 {
				out = a.mergeValues(out, args[2])
			}
			v = controlled(out, v.refs)
		}
	case "UPPER", "LOWER", "TRIM", "LTRIM", "RTRIM", "SUBSTRING", "SUBSTR":
		v.typ = firstType
	case "CONCAT":
		v.typ = "TEXT"
	case "DATE", "CURRENT_DATE":
		v.typ = "DATE"
	case "CURRENT_TIMESTAMP":
		v.typ = "TIMESTAMP"
		if a.dialect == keywords.DialectSnowflake {
			v.typ = "TIMESTAMPLTZ"
		}
	case "CURRENT_TIME":
		v.typ = "TIME"
	case "DATE_FORMAT":
		v.typ = "VARCHAR"
	case "TO_TIMESTAMP", "DATE_TRUNC", "DATEADD", "NOW":
		v.typ = "UNKNOWN"
	case "JSON_EXTRACT", "JSON_QUERY", "PARSE_JSON":
		v.typ = "JSON"
	case "JSON_VALUE", "JSON_EXTRACT_SCALAR", "GET_JSON_OBJECT":
		v.typ = "VARCHAR"
	case "OFFSET", "ORDINAL", "SAFE_OFFSET", "SAFE_ORDINAL":
		v.typ = firstType
	case "ARRAY_AGG", "ARRAY":
		if len(args) > 0 {
			v.fields = args[0].fields
		}
		v.typ = "ARRAY<" + firstType + ">"
	default:
		v.typ = "UNKNOWN"
		a.warnings["function "+f.Name+": argument dependencies only; function body and return type unavailable"] = true
	}
	controls := sources(a.expressions(f.Parameters, s).refs, a.expr(f.Filter, s).refs)
	for _, order := range append(append([]ast.OrderByExpression{}, f.OrderBy...), f.WithinGroup...) {
		controls = sources(controls, a.expr(order.Expression, s).refs)
	}
	if f.Over != nil {
		controls = sources(controls, a.windowDependencies(f.Over, s))
	}
	return controlled(v, controls)
}

func datePart(s string) bool {
	switch strings.ToUpper(s) {
	case "YEAR", "QUARTER", "MONTH", "WEEK", "DAY", "HOUR", "MINUTE", "SECOND", "MILLISECOND", "MICROSECOND":
		return true
	}
	return false
}

func dataType(t string) string {
	raw := strings.TrimSpace(t)
	t = strings.ToUpper(strings.TrimSpace(t))
	if strings.HasSuffix(t, "[]") {
		return "ARRAY<" + dataType(raw[:len(raw)-2]) + ">"
	}
	if (strings.HasPrefix(t, "ARRAY<") && strings.HasSuffix(t, ">")) || (strings.HasPrefix(t, "ARRAY(") && strings.HasSuffix(t, ")")) {
		return "ARRAY<" + dataType(raw[6:len(raw)-1]) + ">"
	}
	if fields := structFields(raw); len(fields) > 0 {
		parts := make([]string, len(fields))
		for i, f := range fields {
			parts[i] = typeFieldName(f.Name) + " " + f.Type
		}
		return "STRUCT<" + strings.Join(parts, ", ") + ">"
	}
	if i := strings.Index(t, "("); i >= 0 {
		t = t[:i]
	}
	switch t {
	case "STR", "STRING", "TEXT":
		return "TEXT"
	case "INTEGER", "INT", "INT32":
		return "INT"
	case "INT64", "BIGINT":
		return "BIGINT"
	case "BOOL", "BOOLEAN":
		return "BOOLEAN"
	case "NUMERIC", "DECIMAL":
		return "DECIMAL"
	case "FLOAT64", "DOUBLE", "DOUBLE PRECISION":
		return "DOUBLE"
	case "FLOAT", "FLOAT32", "REAL":
		return "FLOAT"
	case "VARCHAR", "CHAR", "SMALLINT", "TINYINT", "DATE", "TIME", "TIMESTAMP", "TIMESTAMPTZ", "TIMESTAMPLTZ", "DATETIME", "INTERVAL", "NULL", "JSON", "JSONB", "VARIANT":
		return t
	}
	return "UNKNOWN"
}

func numeric(t string) bool {
	switch t {
	case "TINYINT", "SMALLINT", "INT", "BIGINT", "DECIMAL", "FLOAT", "DOUBLE":
		return true
	}
	return false
}

func commonType(a, b string) string {
	if a == "NULL" {
		return b
	}
	if b == "NULL" {
		return a
	}
	if a == b && a != "" {
		return a
	}
	if (a == "TEXT" && b == "VARCHAR") || (a == "VARCHAR" && b == "TEXT") {
		return "TEXT"
	}
	if numeric(a) && numeric(b) {
		for _, t := range []string{"DOUBLE", "FLOAT", "DECIMAL", "BIGINT", "INT", "SMALLINT", "TINYINT"} {
			if a == t || b == t {
				return t
			}
		}
	}
	return "UNKNOWN"
}
