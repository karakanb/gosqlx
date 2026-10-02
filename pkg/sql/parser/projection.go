package parser

import (
	"github.com/ajitpratap0/GoSQLX/pkg/models"
	"github.com/ajitpratap0/GoSQLX/pkg/sql/ast"
	"strings"
)

func (p *Parser) parseStarModifiers(id *ast.Identifier) (ast.Expression, error) {
	word := strings.ToUpper(p.currentToken.Token.Value)
	if word != "EXCEPT" && word != "EXCLUDE" && word != "REPLACE" {
		return id, nil
	}
	// EXCEPT SELECT is a set operation, not a projection modifier.
	if word == "EXCEPT" && p.peekToken().Token.Type != models.TokenTypeLParen {
		return id, nil
	}
	switch p.dialect {
	case "bigquery", "duckdb", "snowflake", "spark", "databricks", "clickhouse":
	default:
		return id, nil
	}
	star := &ast.StarExpression{Table: id.Table}
	if word == "EXCEPT" || word == "EXCLUDE" {
		p.advance()
		paren := p.matchType(models.TokenTypeLParen)
		if word == "EXCEPT" && !paren {
			return nil, p.expectedError("( after star EXCEPT")
		}
		for {
			if !p.isIdentifier() {
				return nil, p.expectedError("excluded column name")
			}
			star.Exclude = append(star.Exclude, p.currentToken.Token.Value)
			p.advance()
			if !paren || !p.matchType(models.TokenTypeComma) {
				break
			}
		}
		if paren && !p.matchType(models.TokenTypeRParen) {
			return nil, p.expectedError(") after excluded columns")
		}
	}
	if strings.EqualFold(p.currentToken.Token.Value, "REPLACE") {
		p.advance()
		if !p.matchType(models.TokenTypeLParen) {
			return nil, p.expectedError("( after star REPLACE")
		}
		for {
			e, err := p.parseExpression()
			if err != nil {
				return nil, err
			}
			if !p.matchType(models.TokenTypeAs) || !p.isIdentifier() {
				return nil, p.expectedError("AS replacement column")
			}
			star.Replace = append(star.Replace, &ast.AliasedExpression{Expr: e, Alias: p.currentToken.Token.Value})
			p.advance()
			if !p.matchType(models.TokenTypeComma) {
				break
			}
		}
		if !p.matchType(models.TokenTypeRParen) {
			return nil, p.expectedError(") after replacements")
		}
	}
	return star, nil
}
