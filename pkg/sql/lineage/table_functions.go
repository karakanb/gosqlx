package lineage

import (
	"github.com/ajitpratap0/GoSQLX/pkg/sql/ast"
	"strings"
)

func arrayElement(typ string) string {
	typ = strings.TrimSpace(typ)
	upper := strings.ToUpper(typ)
	if strings.HasSuffix(typ, "[]") {
		return dataType(typ[:len(typ)-2])
	}
	if (strings.HasPrefix(upper, "ARRAY<") && strings.HasSuffix(typ, ">")) || (strings.HasPrefix(upper, "ARRAY(") && strings.HasSuffix(typ, ")")) {
		return dataType(typ[6 : len(typ)-1])
	}
	return "UNKNOWN"
}

// splitTypeFields splits a SQL type's field list, never nested type parameters.
func splitTypeFields(text string) []string {
	var out []string
	depth, start := 0, 0
	quoted := byte(0)
	for i := 0; i < len(text); i++ {
		c := text[i]
		if quoted != 0 {
			if c == quoted {
				quoted = 0
			}
			continue
		}
		switch c {
		case '"', '`':
			quoted = c
		case '<', '(', '[':
			depth++
		case '>', ')', ']':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, strings.TrimSpace(text[start:i]))
				start = i + 1
			}
		}
	}
	return append(out, strings.TrimSpace(text[start:]))
}

func structFields(typ string) []Field {
	upper := strings.ToUpper(typ)
	start := strings.IndexAny(typ, "<(")
	if start < 0 || len(typ) <= start+1 || (typ[len(typ)-1] != '>' && typ[len(typ)-1] != ')') || !(strings.HasPrefix(upper, "STRUCT") || strings.HasPrefix(upper, "ROW") || strings.HasPrefix(upper, "TUPLE")) {
		return nil
	}
	var fields []Field
	for _, text := range splitTypeFields(typ[start+1 : len(typ)-1]) {
		sep := strings.IndexAny(text, " :")
		if len(text) > 0 && (text[0] == '"' || text[0] == '`') {
			sep = -1
			for i := 1; i < len(text); i++ {
				if text[i] != text[0] {
					continue
				}
				if i+1 < len(text) && text[i+1] == text[0] {
					i++
					continue
				}
				sep = i + 1
				break
			}
		}
		if sep < 0 {
			return nil
		}
		name := text[:sep]
		if len(name) > 1 && (name[0] == '"' || name[0] == '`') {
			quote := string(name[0])
			name = strings.ReplaceAll(name[1:len(name)-1], quote+quote, quote)
		}
		fields = append(fields, Field{Name: name, Type: dataType(strings.TrimLeft(text[sep:], " :"))})
	}
	return fields
}

func (a *analyzer) tableFunction(ref ast.TableReference, s *scope) relation {
	f := ref.TableFunc
	name := strings.ToUpper(f.Name)
	r := relation{name: a.ident(ref.Alias)}
	if r.name == "" {
		r.name = a.ident(f.Name)
	}
	switch name {
	case "UNNEST", "EXPLODE", "POSEXPLODE":
		if len(f.Arguments) == 0 {
			a.fail("%s requires an array argument", name)
			return r
		}
		for _, arg := range f.Arguments {
			v := a.expr(arg, s)
			if name == "UNNEST" {
				a.dependency(v.refs)
			}
			typ := arrayElement(v.typ)
			fields := structFields(typ)
			if len(fields) > 0 {
				for _, field := range fields {
					col := a.fields(Column{Type: typ, Upstream: v.refs, fields: v.fields}, []string{field.Name})
					r.columns = append(r.columns, col)
				}
			} else if typ == "UNKNOWN" && name == "UNNEST" && len(ref.Columns) == 0 && a.dialect == "bigquery" {
				r.open, r.refs = true, sources(v.refs)
			} else {
				if name == "POSEXPLODE" {
					r.columns = append(r.columns, Column{Name: "pos", Type: "INT", Upstream: sources(v.refs)})
				}
				column := "col"
				if name == "UNNEST" {
					column = r.name
				}
				r.columns = append(r.columns, Column{Name: column, Type: typ, Upstream: sources(v.refs), fields: v.fields})
			}
		}
	case "GENERATE_SERIES":
		v := a.expressions(f.Arguments, s)
		r.columns = []Column{{Name: r.name, Type: v.typ, Upstream: sources(v.refs)}}
	case "FLATTEN":
		v := a.expressions(f.Arguments, s)
		for _, field := range []Field{{"seq", "BIGINT"}, {"key", "TEXT"}, {"path", "TEXT"}, {"index", "INT"}, {"value", "VARIANT"}, {"this", "VARIANT"}} {
			r.columns = append(r.columns, Column{Name: a.ident(field.Name), Type: field.Type, Upstream: sources(v.refs)})
		}
	default:
		a.fail("table function %q has unknown output schema", f.Name)
	}
	return r
}
