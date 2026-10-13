package main

import (
	"go/ast"
	"go/token"
)

func lintSubprocessContexts(fset *token.FileSet, file *ast.File, report reporter, rule RuleConfig, indexes ...*typeIndex) {
	imports := importAliases(file)
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if ok && cleanupCallName(call, imports) == "os/exec.Command" && !ignored(file, fset, call.Pos(), ruleSubprocessContext) {
			report(diagnostic{Rule: ruleSubprocessContext, Position: fset.Position(call.Pos()), End: fset.Position(call.End()), Message: "subprocess requires CommandContext", Suggestion: "propagate caller cancellation or bind a context with a deadline"})
		}
		return true
	})
	lintContextBindings(fset, file, report, rule, ruleSubprocessContext, rule.AllowContextParameters, indexes...)
}
