package main

import (
	"go/ast"
	"go/token"
)

func lintNativeAllocations(fset *token.FileSet, file *ast.File, report reporter, rule RuleConfig) {
	imports := importAliases(file)
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil || matchesCall(fn.Name.Name, rule.ExcludeFunctions) {
			continue
		}
		owned := map[*ast.CallExpr]bool{}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			block, isBlock := node.(*ast.BlockStmt)
			if !isBlock {
				return true
			}
			for index, stmt := range block.List {
				var ident *ast.Ident
				var expr ast.Expr
				switch value := stmt.(type) {
				case *ast.AssignStmt:
					if len(value.Lhs) == 1 && len(value.Rhs) == 1 {
						ident, _ = value.Lhs[0].(*ast.Ident)
						expr = value.Rhs[0]
					}
				case *ast.DeclStmt:
					if group, yes := value.Decl.(*ast.GenDecl); yes && len(group.Specs) == 1 {
						if spec, isSpec := group.Specs[0].(*ast.ValueSpec); isSpec && len(spec.Names) == 1 && len(spec.Values) == 1 {
							ident = spec.Names[0]
							expr = spec.Values[0]
						}
					}
				}
				if ident == nil || ident.Obj == nil || ident.Name == "_" || expr == nil || index+1 == len(block.List) {
					continue
				}
				call, isCall := ast.Unparen(expr).(*ast.CallExpr)
				if !isCall {
					continue
				}
				release, configured := rule.Allocators[cleanupCallName(call, imports)]
				if !configured {
					continue
				}
				deferred, isDefer := block.List[index+1].(*ast.DeferStmt)
				if !isDefer || cleanupCallName(deferred.Call, imports) != release || len(deferred.Call.Args) != 1 {
					continue
				}
				argument := ast.Unparen(deferred.Call.Args[0])
				if conversion, isConversion := argument.(*ast.CallExpr); isConversion && cleanupCallName(conversion, imports) == "unsafe.Pointer" && len(conversion.Args) == 1 {
					argument = ast.Unparen(conversion.Args[0])
				}
				if released, isIdent := argument.(*ast.Ident); isIdent && released.Obj == ident.Obj {
					owned[call] = true
				}
			}
			return true
		})
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			call, isCall := node.(*ast.CallExpr)
			if !isCall || owned[call] {
				return true
			}
			release, configured := rule.Allocators[cleanupCallName(call, imports)]
			if configured && !ignored(file, fset, call.Pos(), ruleNativeAllocation) {
				report(diagnostic{Rule: ruleNativeAllocation, Position: fset.Position(call.Pos()), End: fset.Position(call.End()), Message: "native allocation requires an immediate deferred release", Suggestion: "bind the allocation and immediately defer " + release + " for that binding"})
			}
			return true
		})
	}
}
