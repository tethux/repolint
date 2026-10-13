package main

import (
	"go/ast"
	"go/token"
	"path"
	"slices"
	"strings"
)

func splitCallSymbol(symbol string) (string, string, bool) {
	index := strings.LastIndexByte(symbol, '.')
	if index < 1 || index == len(symbol)-1 {
		return "", "", false
	}
	return symbol[:index], symbol[index+1:], true
}

func cleanupCallName(call *ast.CallExpr, imports map[string]string) string {
	switch fun := ast.Unparen(call.Fun).(type) {
	case *ast.Ident:
		return fun.Name
	case *ast.SelectorExpr:
		if pkg, ok := fun.X.(*ast.Ident); ok && pkg.Obj == nil {
			if importPath, found := imports[pkg.Name]; found {
				return importPath + "." + fun.Sel.Name
			}
		}
		return "receiver." + fun.Sel.Name
	}
	return ""
}

func matchesCall(name string, patterns []string) bool {
	for _, pattern := range patterns {
		matched, matchErr := path.Match(pattern, name)
		if matchErr == nil && matched {
			return true
		}
	}
	return false
}

func lintDiscardedCleanup(fset *token.FileSet, file *ast.File, report reporter, rule RuleConfig) {
	imports := importAliases(file)
	ast.Inspect(file, func(node ast.Node) bool {
		var exprs []ast.Expr
		switch value := node.(type) {
		case *ast.ExprStmt:
			exprs = []ast.Expr{value.X}
		case *ast.DeferStmt:
			exprs = []ast.Expr{value.Call}
		case *ast.GoStmt:
			exprs = []ast.Expr{value.Call}
		case *ast.AssignStmt:
			for index, lhs := range value.Lhs {
				ident, ok := lhs.(*ast.Ident)
				if !ok || ident.Name != "_" {
					continue
				}
				if len(value.Rhs) == len(value.Lhs) {
					exprs = append(exprs, value.Rhs[index])
				} else if len(value.Rhs) == 1 && index == len(value.Lhs)-1 {
					exprs = append(exprs, value.Rhs[0])
				}
			}
		case *ast.ValueSpec:
			for index, name := range value.Names {
				if name.Name != "_" {
					continue
				}
				if len(value.Values) == len(value.Names) {
					exprs = append(exprs, value.Values[index])
				} else if len(value.Values) == 1 && index == len(value.Names)-1 {
					exprs = append(exprs, value.Values[0])
				}
			}
		}
		for _, expr := range exprs {
			call, ok := ast.Unparen(expr).(*ast.CallExpr)
			if !ok || !matchesCall(cleanupCallName(call, imports), rule.Calls) || ignored(file, fset, call.Pos(), ruleDiscardedCleanup) {
				continue
			}
			report(diagnostic{
				Rule: ruleDiscardedCleanup, Position: fset.Position(call.Pos()), End: fset.Position(call.End()),
				Message:    "cleanup error is discarded",
				Suggestion: "return or join the cleanup error, or record it in a recovery mechanism",
			})
		}
		return true
	})
}

// Context provenance is limited to local bindings from exact approved factories.
// Reassignments, address escapes, and forwarding through helpers are rejected.
func lintCleanupContexts(fset *token.FileSet, file *ast.File, report reporter, rule RuleConfig) {
	lintContextBindings(fset, file, report, rule, ruleCleanupContext, false)
}

func lintContextBindings(fset *token.FileSet, file *ast.File, report reporter, rule RuleConfig, ruleName string, allowParameters bool) {
	imports := importAliases(file)
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil || !matchesCall(fn.Name.Name, rule.Functions) || matchesCall(fn.Name.Name, rule.ExcludeFunctions) {
			continue
		}
		bindings := map[*ast.Object]token.Pos{}
		invalid := map[*ast.Object]bool{}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			switch value := node.(type) {
			case *ast.AssignStmt:
				for index, lhs := range value.Lhs {
					ident, isIdent := lhs.(*ast.Ident)
					if !isIdent || ident.Obj == nil {
						continue
					}
					var factory *ast.CallExpr
					if index == 0 && len(value.Rhs) == 1 {
						factory, _ = ast.Unparen(value.Rhs[0]).(*ast.CallExpr)
					}
					if factory != nil && value.Tok == token.DEFINE && ident.Obj.Decl == value && slices.Contains(rule.ContextFactories, cleanupCallName(factory, imports)) {
						bindings[ident.Obj] = value.End()
					} else {
						invalid[ident.Obj] = true
					}
				}
			case *ast.UnaryExpr:
				if value.Op == token.AND {
					if ident, isIdent := ast.Unparen(value.X).(*ast.Ident); isIdent {
						invalid[ident.Obj] = true
					}
				}
			}
			return true
		})
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			call, isCall := node.(*ast.CallExpr)
			if !isCall || !matchesCall(cleanupCallName(call, imports), rule.Calls) || ignored(file, fset, call.Pos(), ruleName) {
				return true
			}
			approved := false
			if rule.ContextArg < len(call.Args) {
				if ident, isIdent := ast.Unparen(call.Args[rule.ContextArg]).(*ast.Ident); isIdent && ident.Obj != nil {
					bound, found := bindings[ident.Obj]
					approved = found && !invalid[ident.Obj] && bound < call.Pos()
					if allowParameters && !invalid[ident.Obj] {
						if field, ok := ident.Obj.Decl.(*ast.Field); ok {
							if selector, yes := field.Type.(*ast.SelectorExpr); yes && selector.Sel.Name == "Context" {
								if pkg, ok := selector.X.(*ast.Ident); ok && imports[pkg.Name] == "context" {
									approved = true
								}
							}
						}
					}
				}
			}
			if !approved {
				report(diagnostic{
					Rule: ruleName, Position: fset.Position(call.Pos()), End: fset.Position(call.End()),
					Message:    "call requires a context from an approved factory or permitted parameter",
					Suggestion: "bind the context with " + strings.Join(rule.ContextFactories, " or ") + " and defer its cancel function",
				})
			}
			return true
		})
	}
}
