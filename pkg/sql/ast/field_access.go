package ast

// FieldAccess selects a field of an expression, for example items[1].name.
// Dotted identifiers remain Identifier nodes until schema resolution.
type FieldAccess struct {
	Expr Expression
	Name string
}

func (*FieldAccess) expressionNode()        {}
func (f *FieldAccess) TokenLiteral() string { return f.Name }
func (f *FieldAccess) Children() []Node     { return []Node{f.Expr} }
func (f *FieldAccess) SQL() string          { return "(" + exprSQL(f.Expr) + ")." + safeIdentifier(f.Name) }
