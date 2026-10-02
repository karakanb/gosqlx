package ast

import "strings"

// StarExpression is a projection wildcard with EXCEPT/EXCLUDE and REPLACE
// modifiers. A plain star remains an Identifier for backwards compatibility.
type StarExpression struct {
	Table   string
	Exclude []string
	Replace []*AliasedExpression
}

func (*StarExpression) expressionNode()        {}
func (s *StarExpression) TokenLiteral() string { return "*" }
func (s *StarExpression) Children() []Node {
	nodes := make([]Node, 0, len(s.Replace))
	for _, r := range s.Replace {
		nodes = append(nodes, r)
	}
	return nodes
}
func (s *StarExpression) SQL() string {
	text := "*"
	if s.Table != "" {
		text = s.Table + ".*"
	}
	if len(s.Exclude) > 0 {
		text += " EXCEPT (" + strings.Join(s.Exclude, ", ") + ")"
	}
	if len(s.Replace) > 0 {
		parts := make([]string, len(s.Replace))
		for i, r := range s.Replace {
			parts[i] = r.SQL()
		}
		text += " REPLACE (" + strings.Join(parts, ", ") + ")"
	}
	return text
}

// UseStatement changes the database context for subsequent statements.
type UseStatement struct{ Name string }

func (*UseStatement) statementNode()       {}
func (*UseStatement) TokenLiteral() string { return "USE" }
func (*UseStatement) Children() []Node     { return nil }
func (u *UseStatement) SQL() string        { return "USE " + u.Name }
