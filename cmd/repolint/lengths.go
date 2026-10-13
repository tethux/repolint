package main

import (
	"go/ast"
	"go/token"
	"slices"
)

func lintNativeLengths(fset *token.FileSet, file *ast.File, report reporter, rule RuleConfig) {
	imports := importAliases(file)
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil || matchesCall(fn.Name.Name, rule.ExcludeFunctions) {
			continue
		}
		check := func(expr ast.Expr) {
			if expr == nil {
				return
			}
			name := renderExpr(fset, ast.Unparen(expr))
			if call, isCall := ast.Unparen(expr).(*ast.CallExpr); isCall {
				name = cleanupCallName(call, imports)
			}
			if matchesCall(name, rule.Sources) && !ignored(file, fset, expr.Pos(), ruleNativeLength) {
				report(diagnostic{Rule: ruleNativeLength, Position: fset.Position(expr.Pos()), End: fset.Position(expr.End()), Message: "native length requires a checked conversion or copy helper", Suggestion: "validate integer range, buffer bounds, and ownership in an approved helper"})
			}
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			switch value := node.(type) {
			case *ast.CallExpr:
				ident, isIdent := ast.Unparen(value.Fun).(*ast.Ident)
				if isIdent && ident.Obj == nil && slices.Contains([]string{"int", "int32", "int64", "uint", "uint16", "uint32", "uint64"}, ident.Name) && len(value.Args) == 1 {
					check(value.Args[0])
				}
				if isIdent && ident.Obj == nil && ident.Name == "make" {
					for _, arg := range value.Args[1:] {
						check(arg)
					}
				}
			case *ast.SliceExpr:
				check(value.Low)
				check(value.High)
				check(value.Max)
			}
			return true
		})
	}
}
