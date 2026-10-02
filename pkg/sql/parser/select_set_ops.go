// Copyright 2026 GoSQLX Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package parser - select_set_ops.go
// Set operation parsing: UNION, INTERSECT, EXCEPT.

package parser

import (
	"fmt"
	"strings"

	goerrors "github.com/ajitpratap0/GoSQLX/pkg/errors"
	"github.com/ajitpratap0/GoSQLX/pkg/models"
	"github.com/ajitpratap0/GoSQLX/pkg/sql/ast"
	"github.com/ajitpratap0/GoSQLX/pkg/sql/keywords"
)

// parseSelectWithSetOperations parses SELECT statements that may have set operations.
// It supports UNION, UNION ALL, EXCEPT, and INTERSECT operations with proper left-associative parsing.
//
// Examples:
//
//	SELECT name FROM users UNION SELECT name FROM customers
//	SELECT id FROM orders UNION ALL SELECT id FROM invoices
//	SELECT product FROM inventory EXCEPT SELECT product FROM discontinued
//	SELECT a FROM t1 UNION SELECT b FROM t2 INTERSECT SELECT c FROM t3
func (p *Parser) parseSelectWithSetOperations() (ast.Statement, error) {
	// Parse the first SELECT statement
	leftStmt, err := p.parseSelectStatement()
	if err != nil {
		return nil, err
	}
	if left, ok := leftStmt.(*ast.SelectStatement); ok &&
		(p.isAnyType(models.TokenTypeUnion, models.TokenTypeExcept, models.TokenTypeIntersect) || p.isMinusSetOp()) &&
		(len(left.OrderBy) > 0 || left.Limit != nil || left.Offset != nil || left.Fetch != nil) {
		return nil, p.expectedError("parentheses around ordered or limited set branch")
	}

	return p.parseSetTail(leftStmt, 1)
}

func (p *Parser) parseSetTail(leftStmt ast.Statement, minPrecedence int) (ast.Statement, error) {
	for p.isAnyType(models.TokenTypeUnion, models.TokenTypeExcept, models.TokenTypeIntersect) ||
		p.isMinusSetOp() {
		precedence := 1
		if p.isType(models.TokenTypeIntersect) {
			precedence = 2
		}
		if precedence < minPrecedence {
			break
		}
		// Parse the set operation type
		operationLiteral := p.currentToken.Token.Value
		if p.isMinusSetOp() {
			operationLiteral = "EXCEPT" // normalize to canonical name
		}
		p.advance()

		// Check for ALL keyword
		all := false
		if p.isType(models.TokenTypeAll) {
			all = true
			p.advance()
		} else {
			p.matchType(models.TokenTypeDistinct)
		}

		// Parse the right-hand SELECT statement
		grouped := p.matchType(models.TokenTypeLParen)
		var rightStmt ast.Statement
		var err error
		if grouped {
			rightStmt, err = p.parseStatement()
			if err == nil && !p.matchType(models.TokenTypeRParen) {
				return nil, p.expectedError(") after set branch")
			}
		} else {
			if !p.matchType(models.TokenTypeSelect) {
				return nil, p.expectedError("SELECT after set operation")
			}
			rightStmt, err = p.parseSelectStatement()
			if right, ok := rightStmt.(*ast.SelectStatement); ok && err == nil &&
				(p.isAnyType(models.TokenTypeUnion, models.TokenTypeExcept, models.TokenTypeIntersect) || p.isMinusSetOp()) &&
				(len(right.OrderBy) > 0 || right.Limit != nil || right.Offset != nil || right.Fetch != nil) {
				return nil, p.expectedError("parentheses around ordered or limited set branch")
			}
		}
		if err != nil {
			return nil, goerrors.InvalidSetOperationError(
				operationLiteral,
				fmt.Sprintf("error parsing right SELECT: %v", err),
				p.currentLocation(),
				"",
			)
		}
		if !grouped {
			rightStmt, err = p.parseSetTail(rightStmt, precedence+1)
			if err != nil {
				return nil, err
			}
		}

		// Create the set operation with left as the accumulated result
		setOp := &ast.SetOperation{
			Left:     leftStmt,
			Operator: operationLiteral,
			All:      all,
			Right:    rightStmt,
		}
		if !grouped {
			switch right := rightStmt.(type) {
			case *ast.SelectStatement:
				setOp.OrderBy, setOp.Limit, setOp.Offset, setOp.Fetch = right.OrderBy, right.Limit, right.Offset, right.Fetch
				right.OrderBy, right.Limit, right.Offset, right.Fetch = nil, nil, nil, nil
			case *ast.SetOperation:
				setOp.OrderBy, setOp.Limit, setOp.Offset, setOp.Fetch = right.OrderBy, right.Limit, right.Offset, right.Fetch
				right.OrderBy, right.Limit, right.Offset, right.Fetch = nil, nil, nil, nil
			}
		}

		leftStmt = setOp // The result becomes the left side for any subsequent operations
	}

	if set, ok := leftStmt.(*ast.SetOperation); ok {
		var err error
		if p.isType(models.TokenTypeOrder) {
			set.OrderBy, err = p.parseOrderByClause()
			if err != nil {
				return nil, err
			}
		}
		if p.isType(models.TokenTypeLimit) || p.isType(models.TokenTypeOffset) {
			set.Limit, set.Offset, err = p.parseLimitOffsetClause()
			if err != nil {
				return nil, err
			}
		}
		if p.isType(models.TokenTypeFetch) {
			set.Fetch, err = p.parseFetchClause()
			if err != nil {
				return nil, err
			}
		}
	}
	return leftStmt, nil
}

// isMinusSetOp returns true if the current token is the Snowflake / Oracle
// MINUS keyword used as a set operator (synonym for EXCEPT). MINUS tokenizes
// as an identifier; gate by dialect so that MINUS as a legitimate column
// alias in other dialects is not hijacked.
func (p *Parser) isMinusSetOp() bool {
	if p.dialect != string(keywords.DialectSnowflake) &&
		p.dialect != string(keywords.DialectOracle) {
		return false
	}
	if !p.isIdentifier() {
		return false
	}
	return strings.EqualFold(p.currentToken.Token.Value, "MINUS")
}
