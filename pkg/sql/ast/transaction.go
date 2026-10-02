package ast

// TransactionStatement represents BEGIN, COMMIT or ROLLBACK. It contributes no
// physical table references and is not a query for column-lineage purposes.
type TransactionStatement struct{ Command string }

func (*TransactionStatement) statementNode()         {}
func (t *TransactionStatement) TokenLiteral() string { return t.Command }
func (*TransactionStatement) Children() []Node       { return nil }
func (t *TransactionStatement) SQL() string          { return t.Command }
