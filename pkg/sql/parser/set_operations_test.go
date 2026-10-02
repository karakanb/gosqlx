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

package parser

import (
	"testing"

	"github.com/ajitpratap0/GoSQLX/pkg/sql/ast"
	"github.com/ajitpratap0/GoSQLX/pkg/sql/tokenizer"
)

func TestParser_SimpleUnion(t *testing.T) {
	sql := `SELECT name FROM users UNION SELECT name FROM customers`

	// Get tokenizer from pool
	tkz := tokenizer.GetTokenizer()
	defer tokenizer.PutTokenizer(tkz)

	// Tokenize SQL
	tokens, err := tkz.Tokenize([]byte(sql))
	if err != nil {
		t.Fatalf("Failed to tokenize: %v", err)
	}

	// Convert tokens for parser

	// Parse tokens
	parser := &Parser{}
	astObj, err := parser.ParseFromModelTokens(tokens)
	if err != nil {
		t.Fatalf("Failed to parse UNION: %v", err)
	}
	defer ast.ReleaseAST(astObj)

	// Verify we have a statement
	if len(astObj.Statements) == 0 {
		t.Fatal("No statements parsed")
	}

	// Verify it's a SetOperation
	setOp, ok := astObj.Statements[0].(*ast.SetOperation)
	if !ok {
		t.Fatalf("Expected SetOperation, got %T", astObj.Statements[0])
	}

	// Verify operation type
	if setOp.Operator != "UNION" {
		t.Errorf("Expected UNION operator, got %s", setOp.Operator)
	}

	// Verify not ALL
	if setOp.All {
		t.Error("Expected UNION (not UNION ALL)")
	}

	// Verify left and right are SELECT statements
	_, leftOk := setOp.Left.(*ast.SelectStatement)
	_, rightOk := setOp.Right.(*ast.SelectStatement)
	if !leftOk || !rightOk {
		t.Errorf("Expected both sides to be SELECT statements, got left=%T, right=%T", setOp.Left, setOp.Right)
	}
}

func TestParser_UnionAll(t *testing.T) {
	sql := `SELECT id FROM orders UNION ALL SELECT id FROM invoices`

	// Get tokenizer from pool
	tkz := tokenizer.GetTokenizer()
	defer tokenizer.PutTokenizer(tkz)

	// Tokenize SQL
	tokens, err := tkz.Tokenize([]byte(sql))
	if err != nil {
		t.Fatalf("Failed to tokenize: %v", err)
	}

	// Convert tokens for parser

	// Parse tokens
	parser := &Parser{}
	astObj, err := parser.ParseFromModelTokens(tokens)
	if err != nil {
		t.Fatalf("Failed to parse UNION ALL: %v", err)
	}
	defer ast.ReleaseAST(astObj)

	// Verify we have a statement
	if len(astObj.Statements) == 0 {
		t.Fatal("No statements parsed")
	}

	// Verify it's a SetOperation
	setOp, ok := astObj.Statements[0].(*ast.SetOperation)
	if !ok {
		t.Fatalf("Expected SetOperation, got %T", astObj.Statements[0])
	}

	// Verify operation type and ALL flag
	if setOp.Operator != "UNION" {
		t.Errorf("Expected UNION operator, got %s", setOp.Operator)
	}
	if !setOp.All {
		t.Error("Expected UNION ALL")
	}
}

func TestParser_Except(t *testing.T) {
	sql := `SELECT region FROM sales EXCEPT SELECT region FROM returns`

	// Get tokenizer from pool
	tkz := tokenizer.GetTokenizer()
	defer tokenizer.PutTokenizer(tkz)

	// Tokenize SQL
	tokens, err := tkz.Tokenize([]byte(sql))
	if err != nil {
		t.Fatalf("Failed to tokenize: %v", err)
	}

	// Convert tokens for parser

	// Parse tokens
	parser := &Parser{}
	astObj, err := parser.ParseFromModelTokens(tokens)
	if err != nil {
		t.Fatalf("Failed to parse EXCEPT: %v", err)
	}
	defer ast.ReleaseAST(astObj)

	// Verify we have a statement
	if len(astObj.Statements) == 0 {
		t.Fatal("No statements parsed")
	}

	// Verify it's a SetOperation
	setOp, ok := astObj.Statements[0].(*ast.SetOperation)
	if !ok {
		t.Fatalf("Expected SetOperation, got %T", astObj.Statements[0])
	}

	// Verify operation type
	if setOp.Operator != "EXCEPT" {
		t.Errorf("Expected EXCEPT operator, got %s", setOp.Operator)
	}
}

func TestParser_Intersect(t *testing.T) {
	sql := `SELECT product FROM inventory INTERSECT SELECT product FROM sales`

	// Get tokenizer from pool
	tkz := tokenizer.GetTokenizer()
	defer tokenizer.PutTokenizer(tkz)

	// Tokenize SQL
	tokens, err := tkz.Tokenize([]byte(sql))
	if err != nil {
		t.Fatalf("Failed to tokenize: %v", err)
	}

	// Convert tokens for parser

	// Parse tokens
	parser := &Parser{}
	astObj, err := parser.ParseFromModelTokens(tokens)
	if err != nil {
		t.Fatalf("Failed to parse INTERSECT: %v", err)
	}
	defer ast.ReleaseAST(astObj)

	// Verify we have a statement
	if len(astObj.Statements) == 0 {
		t.Fatal("No statements parsed")
	}

	// Verify it's a SetOperation
	setOp, ok := astObj.Statements[0].(*ast.SetOperation)
	if !ok {
		t.Fatalf("Expected SetOperation, got %T", astObj.Statements[0])
	}

	// Verify operation type
	if setOp.Operator != "INTERSECT" {
		t.Errorf("Expected INTERSECT operator, got %s", setOp.Operator)
	}
}

func TestParser_MultipleSetOperations(t *testing.T) {
	sql := `SELECT name FROM users UNION SELECT name FROM customers INTERSECT SELECT name FROM employees`

	// Get tokenizer from pool
	tkz := tokenizer.GetTokenizer()
	defer tokenizer.PutTokenizer(tkz)

	// Tokenize SQL
	tokens, err := tkz.Tokenize([]byte(sql))
	if err != nil {
		t.Fatalf("Failed to tokenize: %v", err)
	}

	// Convert tokens for parser

	// Parse tokens
	parser := &Parser{}
	astObj, err := parser.ParseFromModelTokens(tokens)
	if err != nil {
		t.Fatalf("Failed to parse multiple set operations: %v", err)
	}
	defer ast.ReleaseAST(astObj)

	// Verify we have a statement
	if len(astObj.Statements) == 0 {
		t.Fatal("No statements parsed")
	}

	// Verify it's a SetOperation (the outer one)
	outerSetOp, ok := astObj.Statements[0].(*ast.SetOperation)
	if !ok {
		t.Fatalf("Expected SetOperation, got %T", astObj.Statements[0])
	}

	// INTERSECT binds more tightly than UNION, regardless of textual order.
	if outerSetOp.Operator != "UNION" {
		t.Errorf("Expected outer operation to be UNION, got %s", outerSetOp.Operator)
	}

	// The right branch is the INTERSECT of the final two SELECTs.
	rightSetOp, ok := outerSetOp.Right.(*ast.SetOperation)
	if !ok {
		t.Errorf("Expected right side to be SetOperation, got %T", outerSetOp.Right)
	} else {
		if rightSetOp.Operator != "INTERSECT" {
			t.Errorf("Expected right operation to be INTERSECT, got %s", rightSetOp.Operator)
		}
	}

	// The left branch remains the first SELECT.
	_, leftOK := outerSetOp.Left.(*ast.SelectStatement)
	if !leftOK {
		t.Errorf("Expected left side to be SELECT statement, got %T", outerSetOp.Left)
	}
}

func TestParser_SetOperationWithCTE(t *testing.T) {
	sql := `WITH regional AS (SELECT region FROM sales) SELECT region FROM regional UNION SELECT region FROM returns`

	// Get tokenizer from pool
	tkz := tokenizer.GetTokenizer()
	defer tokenizer.PutTokenizer(tkz)

	// Tokenize SQL
	tokens, err := tkz.Tokenize([]byte(sql))
	if err != nil {
		t.Fatalf("Failed to tokenize: %v", err)
	}

	// Convert tokens for parser

	// Parse tokens
	parser := &Parser{}
	astObj, err := parser.ParseFromModelTokens(tokens)
	if err != nil {
		t.Fatalf("Failed to parse CTE with set operation: %v", err)
	}
	defer ast.ReleaseAST(astObj)

	// Verify we have a statement
	if len(astObj.Statements) == 0 {
		t.Fatal("No statements parsed")
	}

	// The result should be a SetOperation with a With clause
	setOp, ok := astObj.Statements[0].(*ast.SetOperation)
	if !ok {
		t.Fatalf("Expected SetOperation, got %T", astObj.Statements[0])
	}

	// Verify operation type
	if setOp.Operator != "UNION" {
		t.Errorf("Expected UNION operator, got %s", setOp.Operator)
	}

	// The leading WITH belongs to the whole set, not only the left branch.
	leftSelect, ok := setOp.Left.(*ast.SelectStatement)
	if !ok {
		t.Errorf("Expected left side to be SELECT statement, got %T", setOp.Left)
	} else {
		if leftSelect.With != nil {
			t.Error("WITH must not be scoped only to the left SELECT")
		}
	}
	if setOp.With == nil || len(setOp.With.CTEs) != 1 || setOp.With.CTEs[0].Name != "regional" {
		t.Fatalf("Expected regional CTE on set operation, got %+v", setOp.With)
	}
}
