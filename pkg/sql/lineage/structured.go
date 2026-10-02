package lineage

import (
	"fmt"
	"github.com/ajitpratap0/GoSQLX/pkg/sql/ast"
	"strings"
)

// resolvePath disambiguates relation.column.field from column.field using the
// nearest lexical scope. A local relation shadows outer names even if its
// requested field is absent. Struct fields depend on the stored root column.
func (a *analyzer) resolvePath(id *ast.Identifier, s *scope) (Column, bool) {
	if id.Table == "" {
		return Column{}, false
	}
	parts := append(nameParts(id.Table), id.Name)
	for i := range parts {
		parts[i] = a.ident(parts[i])
	}
	for current := s; current != nil; current = current.parent {
		for width := len(parts) - 1; width > 0; width-- {
			qualifier := strings.Join(parts[:width], ".")
			for _, r := range current.relations {
				if !a.equal(r.name, qualifier) {
					continue
				}
				col, n := a.find(r.columns, parts[width])
				if n == 0 && r.open {
					col = a.inferredColumn(r, parts[width])
					n = 1
				}
				if n != 1 {
					a.fail("column %q resolves %d times in relation %q", parts[width], n, r.name)
					return Column{}, true
				}
				return a.fields(col, parts[width+1:]), true
			}
		}
		col, n := a.find(current.visible, parts[0])
		if n > 1 {
			a.fail("ambiguous struct column %q", parts[0])
			return Column{}, true
		}
		if n == 1 {
			return a.fields(col, parts[1:]), true
		}
	}
	return Column{}, false
}

func (a *analyzer) fields(col Column, path []string) Column {
	for _, name := range path {
		if col.fields != nil {
			next, n := a.find(col.fields, name)
			if n != 1 {
				a.fail("constructed field %q resolves %d times", name, n)
				return Column{}
			}
			col = next
			continue
		}
		fields := structFields(col.Type)
		found := false
		for _, field := range fields {
			if a.equal(field.Name, name) {
				col.Type = field.Type
				found = true
				break
			}
		}
		if !found {
			a.fail("unknown field %q in type %s", name, col.Type)
			return Column{}
		}
		col.Name = name
	}
	return col
}

func (a *analyzer) constructStruct(f *ast.FunctionCall, s *scope) value {
	v := value{fields: []Column{}}
	parts := []string{}
	for i := 0; i < len(f.Arguments); i++ {
		arg := f.Arguments[i]
		name := fmt.Sprintf("_field_%d", i)
		if strings.EqualFold(f.Name, "NAMED_STRUCT") {
			lit, ok := arg.(*ast.LiteralValue)
			if !ok || lit.Type != "string" || i+1 >= len(f.Arguments) {
				a.fail("NAMED_STRUCT requires name/value pairs")
				return value{}
			}
			name = fmt.Sprint(lit.Value)
			i++
			arg = f.Arguments[i]
		} else {
			switch e := arg.(type) {
			case *ast.AliasedExpression:
				name = a.ident(e.Alias)
			case *ast.Identifier:
				name = a.ident(e.Name)
			}
		}
		x := a.expr(arg, s)
		v.refs = sources(v.refs, x.refs)
		v.fields = append(v.fields, Column{Name: name, Type: x.typ, Upstream: sources(x.refs), fields: x.fields})
		parts = append(parts, typeFieldName(name)+" "+x.typ)
	}
	v.typ = "STRUCT<" + strings.Join(parts, ", ") + ">"
	return v
}

func (a *analyzer) fieldColumns(col Column) []Column {
	if col.fields != nil {
		return col.fields
	}
	var fields []Column
	for _, f := range structFields(col.Type) {
		name := f.Name
		if a.insensitive() && !a.preserveCase() {
			name = strings.ToLower(name)
		}
		fields = append(fields, Column{Name: name, Type: f.Type, Upstream: sources(col.Upstream)})
	}
	return fields
}

func (a *analyzer) mergeValues(left, right value) value {
	if left.typ == "NULL" {
		return right
	}
	if right.typ == "NULL" {
		return left
	}
	v := value{typ: commonType(left.typ, right.typ), refs: sources(left.refs, right.refs)}
	l := a.fieldColumns(Column{Type: left.typ, Upstream: left.refs, fields: left.fields})
	r := a.fieldColumns(Column{Type: right.typ, Upstream: right.refs, fields: right.fields})
	if len(l) == 0 && len(r) == 0 {
		return v
	}
	if len(l) != len(r) {
		return v
	}
	parts := []string{}
	for i := range l {
		f := a.mergeValues(value{l[i].Type, l[i].Upstream, l[i].fields}, value{r[i].Type, r[i].Upstream, r[i].fields})
		v.fields = append(v.fields, Column{Name: l[i].Name, Type: f.typ, Upstream: f.refs, fields: f.fields})
		parts = append(parts, typeFieldName(l[i].Name)+" "+f.typ)
	}
	if strings.HasPrefix(left.typ, "STRUCT<") && strings.HasPrefix(right.typ, "STRUCT<") {
		v.typ = "STRUCT<" + strings.Join(parts, ", ") + ">"
	}
	return v
}

func typeFieldName(name string) string {
	if strings.ContainsAny(name, " \t.,:\"`<>()") {
		return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
	}
	return name
}

// Index/CASE controls influence every projected field, not just the container.
func controlled(v value, refs []Source) value {
	v.refs = sources(v.refs, refs)
	if v.fields != nil {
		v.fields = append([]Column{}, v.fields...)
		for i, f := range v.fields {
			next := controlled(value{f.Type, f.Upstream, f.fields}, refs)
			v.fields[i].Upstream, v.fields[i].fields = next.refs, next.fields
		}
	}
	return v
}
