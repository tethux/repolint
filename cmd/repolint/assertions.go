package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
)

// fixAssertions wraps eligible expensive assertion expression statements in:
//
//	if assert.Enabled {
//		...
//	}
//
// Only direct expression statements are rewritten. More complicated
// expressions are left diagnostic-only.
func fixAssertions(file *ast.File, fset *token.FileSet, settings ...RuleConfig) bool {
	rule := assertionSettings(settings...)
	rule.source = file
	rule.positions = fset
	settings = []RuleConfig{rule}
	changed := false

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}

		if fixStatementList(&fn.Body.List, false, settings...) {
			changed = true
		}
	}

	return changed
}

func fixStatementList(stmts *[]ast.Stmt, guarded bool, settings ...RuleConfig) bool {
	changed := false
	list := *stmts

	for i, stmt := range list {
		switch value := stmt.(type) {
		case *ast.ExprStmt:
			if guarded {
				continue
			}

			call, ok := value.X.(*ast.CallExpr)
			if !ok || !fixableAssertion(call, settings...) {
				continue
			}

			list[i] = &ast.IfStmt{
				Cond: assertionGuard(settings...),
				Body: &ast.BlockStmt{
					List: []ast.Stmt{value},
				},
			}

			changed = true

		case *ast.IfStmt:
			bodyGuarded := guarded || isAssertEnabled(value.Cond, settings...)

			if fixStatementList(&value.Body.List, bodyGuarded, settings...) {
				changed = true
			}

			switch elseNode := value.Else.(type) {
			case *ast.BlockStmt:
				if fixStatementList(&elseNode.List, guarded, settings...) {
					changed = true
				}

			case *ast.IfStmt:
				if fixIfStatement(elseNode, guarded, settings...) {
					changed = true
				}
			}

		case *ast.ForStmt:
			if fixStatementList(&value.Body.List, guarded, settings...) {
				changed = true
			}

		case *ast.RangeStmt:
			if fixStatementList(&value.Body.List, guarded, settings...) {
				changed = true
			}

		case *ast.SwitchStmt:
			for _, clauseNode := range value.Body.List {
				clause, ok := clauseNode.(*ast.CaseClause)
				if !ok {
					continue
				}

				if fixStatementList(&clause.Body, guarded, settings...) {
					changed = true
				}
			}

		case *ast.TypeSwitchStmt:
			for _, clauseNode := range value.Body.List {
				clause, ok := clauseNode.(*ast.CaseClause)
				if !ok {
					continue
				}

				if fixStatementList(&clause.Body, guarded, settings...) {
					changed = true
				}
			}

		case *ast.SelectStmt:
			for _, clauseNode := range value.Body.List {
				clause, ok := clauseNode.(*ast.CommClause)
				if !ok {
					continue
				}

				if fixStatementList(&clause.Body, guarded, settings...) {
					changed = true
				}
			}

		case *ast.LabeledStmt:
			if fixStatement(&value.Stmt, guarded, settings...) {
				changed = true
			}
		}
	}

	*stmts = list
	return changed
}

func fixIfStatement(stmt *ast.IfStmt, guarded bool, settings ...RuleConfig) bool {
	changed := false

	bodyGuarded := guarded || isAssertEnabled(stmt.Cond, settings...)

	if fixStatementList(&stmt.Body.List, bodyGuarded, settings...) {
		changed = true
	}

	switch elseNode := stmt.Else.(type) {
	case *ast.BlockStmt:
		if fixStatementList(&elseNode.List, guarded, settings...) {
			changed = true
		}

	case *ast.IfStmt:
		if fixIfStatement(elseNode, guarded, settings...) {
			changed = true
		}
	}

	return changed
}

func fixStatement(stmt *ast.Stmt, guarded bool, settings ...RuleConfig) bool { //nolint:gocritic // the interface value must be replaced in place
	switch value := (*stmt).(type) {
	case *ast.ExprStmt:
		if guarded {
			return false
		}

		call, ok := value.X.(*ast.CallExpr)
		if !ok || !fixableAssertion(call, settings...) {
			return false
		}

		*stmt = &ast.IfStmt{
			Cond: assertionGuard(settings...),
			Body: &ast.BlockStmt{
				List: []ast.Stmt{value},
			},
		}

		return true

	case *ast.BlockStmt:
		return fixStatementList(&value.List, guarded, settings...)

	case *ast.IfStmt:
		return fixIfStatement(value, guarded, settings...)

	case *ast.ForStmt:
		return fixStatementList(&value.Body.List, guarded, settings...)

	case *ast.RangeStmt:
		return fixStatementList(&value.Body.List, guarded, settings...)

	default:
		return false
	}
}

func lintAssertions(
	fset *token.FileSet,
	node ast.Node,
	guarded bool,
	report reporter,
	settings ...RuleConfig,
) {
	if node == nil {
		return
	}
	if file, ok := node.(*ast.File); ok {
		original := report
		report = func(d diagnostic) {
			if !ignored(file, fset, file.Pos()+token.Pos(d.Position.Offset-fset.Position(file.Pos()).Offset), d.Rule) {
				original(d)
			}
		}
	}

	if ifStmt, ok := node.(*ast.IfStmt); ok {
		lintAssertions(fset, ifStmt.Init, guarded, report, settings...)
		lintAssertions(fset, ifStmt.Cond, guarded, report, settings...)

		bodyGuarded := guarded || isAssertEnabled(ifStmt.Cond, settings...)
		lintAssertions(fset, ifStmt.Body, bodyGuarded, report, settings...)

		lintAssertions(fset, ifStmt.Else, guarded, report, settings...)

		return
	}

	if call, ok := node.(*ast.CallExpr); ok &&
		isExpensiveAssertion(call.Fun, settings...) &&
		!guarded {

		name := selectorNameOrIdent(call.Fun)

		report(diagnostic{
			Rule:     ruleExpensiveAssertion,
			Position: fset.Position(call.Pos()),
			Message: fmt.Sprintf(
				"expensive assertion %q runs without a %s guard",
				name, assertionSettings(settings...).Guard,
			),
			Suggestion: fmt.Sprintf(
				"run repolint with --fix, or wrap the call in `if %s { %s(...) }`",
				assertionSettings(settings...).Guard, name,
			),
		})
	}

	ast.Inspect(node, func(child ast.Node) bool {
		if child == nil || child == node {
			return true
		}

		lintAssertions(fset, child, guarded, report, settings...)
		return false
	})
}

func isAssertEnabled(expr ast.Expr, settings ...RuleConfig) bool {
	switch value := expr.(type) {
	case *ast.Ident:
		return value.Name == assertionSettings(settings...).Guard
	case *ast.ParenExpr:
		return isAssertEnabled(value.X, settings...)

	case *ast.SelectorExpr:
		ident, ok := value.X.(*ast.Ident)

		return ok &&
			ident.Name+"."+value.Sel.Name == assertionSettings(settings...).Guard

	case *ast.BinaryExpr:
		return value.Op == token.LAND &&
			(isAssertEnabled(value.X, settings...) ||
				isAssertEnabled(value.Y, settings...))

	default:
		return false
	}
}

func isExpensiveAssertion(expr ast.Expr, settings ...RuleConfig) bool {
	name := selectorNameOrIdent(expr)
	for _, pattern := range assertionSettings(settings...).Calls {
		if matched, _ := filepath.Match(pattern, name); matched {
			return true
		}
	}
	return false
}

func selectorName(expr ast.Expr) string {
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return ""
	}

	return selector.Sel.Name
}

func assertionSettings(settings ...RuleConfig) RuleConfig {
	if len(settings) > 0 {
		return settings[0]
	}
	return defaultConfig().Rules[ruleExpensiveAssertion]
}

func assertionGuard(settings ...RuleConfig) ast.Expr {
	expr, _ := parser.ParseExpr(assertionSettings(settings...).Guard)
	return expr
}

func selectorNameOrIdent(expr ast.Expr) string {
	if ident, ok := expr.(*ast.Ident); ok {
		return ident.Name
	}
	return selectorName(expr)
}

func fixableAssertion(call *ast.CallExpr, settings ...RuleConfig) bool {
	rule := assertionSettings(settings...)
	return isExpensiveAssertion(call.Fun, settings...) && (rule.source == nil || !ignored(rule.source, rule.positions, call.Pos(), ruleExpensiveAssertion))
}
