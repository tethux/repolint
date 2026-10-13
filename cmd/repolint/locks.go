package main

import (
	"go/ast"
	"go/token"
	"maps"
	"strings"
)

type lockKey struct {
	Name   string
	Object *ast.Object
}
type lockState map[lockKey]int

type lockWalker struct {
	fset        *token.FileSet
	file        *ast.File
	imports     map[string]string
	report      reporter
	rule        RuleConfig
	blocking    bool
	guardObject *ast.Object
}

func lockIdentity(fset *token.FileSet, expr ast.Expr) lockKey {
	key := lockKey{Name: renderExpr(fset, expr)}
	for {
		switch value := ast.Unparen(expr).(type) {
		case *ast.Ident:
			key.Object = value.Obj
			return key
		case *ast.SelectorExpr:
			expr = value.X
		case *ast.StarExpr:
			expr = value.X
		default:
			return key
		}
	}
}

func lintLockedCalls(fset *token.FileSet, file *ast.File, report reporter, rule RuleConfig) {
	w := lockWalker{fset: fset, file: file, imports: importAliases(file), report: report, rule: rule}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil || matchesCall(fn.Name.Name, rule.ExcludeFunctions) {
			continue
		}
		w.guardObject = guardBinding(file, fn, rule.Guard)
		w.block(fn.Body, lockState{})
	}
}

func guardBinding(file *ast.File, fn *ast.FuncDecl, guard string) *ast.Object {
	root, _, _ := strings.Cut(guard, ".")
	for _, fields := range []*ast.FieldList{fn.Recv, fn.Type.Params} {
		if fields != nil {
			for _, field := range fields.List {
				for _, name := range field.Names {
					if name.Name == root {
						return name.Obj
					}
				}
			}
		}
	}
	for _, decl := range file.Decls {
		if group, ok := decl.(*ast.GenDecl); ok {
			for _, spec := range group.Specs {
				if value, ok := spec.(*ast.ValueSpec); ok {
					for _, name := range value.Names {
						if name.Name == root {
							return name.Obj
						}
					}
				}
			}
		}
	}
	return nil
}

func intersectLocks(a, b lockState) lockState {
	result := lockState{}
	for key, count := range a {
		if count > 0 && b[key] > 0 {
			result[key] = min(count, b[key])
		}
	}
	return result
}

func (w *lockWalker) expression(expr ast.Expr, held lockState) {
	if expr == nil {
		return
	}
	ast.Inspect(expr, func(node ast.Node) bool {
		if literal, ok := node.(*ast.FuncLit); ok {
			w.block(literal.Body, lockState{})
			return false
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		name := cleanupCallName(call, w.imports)
		if matchesCall(name, w.rule.Calls) && !matchesCall(name, w.rule.ExcludeCalls) {
			protected := false
			for key, count := range held {
				if key.Name == w.rule.Guard && key.Object == w.guardObject && count > 0 {
					protected = true
				}
			}
			if !protected && !ignored(w.file, w.fset, call.Pos(), ruleNativeLock) {
				w.report(diagnostic{Rule: ruleNativeLock, Position: w.fset.Position(call.Pos()), End: w.fset.Position(call.End()), Message: "native call requires held lock " + w.rule.Guard, Suggestion: "hold the configured lock across the native operation"})
			}
		}
		selector, isSelector := call.Fun.(*ast.SelectorExpr)
		if isSelector && len(call.Args) == 0 {
			key := lockIdentity(w.fset, selector.X)
			switch selector.Sel.Name {
			case "Lock", "RLock":
				held[key]++
			case "Unlock", "RUnlock":
				delete(held, key)
			}
		}
		return true
	})
}

func (w *lockWalker) block(block *ast.BlockStmt, held lockState) (lockState, bool) {
	if block == nil {
		return held, true
	}
	for _, stmt := range block.List {
		var alive bool
		held, alive = w.statement(stmt, held)
		if !alive {
			return held, false
		}
	}
	return held, true
}

func (w *lockWalker) statement(stmt ast.Stmt, held lockState) (lockState, bool) {
	switch value := stmt.(type) {
	case *ast.ExprStmt:
		w.expression(value.X, held)
	case *ast.AssignStmt:
		for _, expr := range value.Rhs {
			w.expression(expr, held)
		}
		for _, lhs := range value.Lhs {
			key := lockIdentity(w.fset, lhs)
			for locked := range held {
				if key.Object != nil && key.Object == locked.Object {
					delete(held, locked)
				}
			}
		}
	case *ast.DeclStmt:
		ast.Inspect(value.Decl, func(node ast.Node) bool {
			if spec, ok := node.(*ast.ValueSpec); ok {
				for _, expr := range spec.Values {
					w.expression(expr, held)
				}
				return false
			}
			return true
		})
	case *ast.ReturnStmt:
		for _, expr := range value.Results {
			w.expression(expr, held)
		}
		return held, false
	case *ast.DeferStmt:
		if selector, ok := value.Call.Fun.(*ast.SelectorExpr); ok && (selector.Sel.Name == "Unlock" || selector.Sel.Name == "RUnlock") {
			return held, true
		}
		w.expression(value.Call, held)
	case *ast.GoStmt:
		w.expression(value.Call, lockState{})
	case *ast.BlockStmt:
		return w.block(value, held)
	case *ast.IfStmt:
		if value.Init != nil {
			held, _ = w.statement(value.Init, held)
		}
		w.expression(value.Cond, held)
		left, leftAlive := w.block(value.Body, maps.Clone(held))
		right, rightAlive := maps.Clone(held), true
		if value.Else != nil {
			right, rightAlive = w.statement(value.Else, right)
		}
		if !leftAlive {
			return right, rightAlive
		}
		if !rightAlive {
			return left, leftAlive
		}
		return intersectLocks(left, right), true
	case *ast.ForStmt:
		if value.Init != nil {
			held, _ = w.statement(value.Init, held)
		}
		w.expression(value.Cond, held)
		body, _ := w.block(value.Body, maps.Clone(held))
		if value.Post != nil {
			body, _ = w.statement(value.Post, body)
		}
		return intersectLocks(held, body), true
	case *ast.RangeStmt:
		w.expression(value.X, held)
		body, _ := w.block(value.Body, maps.Clone(held))
		return intersectLocks(held, body), true
	case *ast.SwitchStmt:
		if value.Init != nil {
			held, _ = w.statement(value.Init, held)
		}
		w.expression(value.Tag, held)
		for _, branch := range value.Body.List {
			if clause, ok := branch.(*ast.CaseClause); ok {
				for _, expr := range clause.List {
					w.expression(expr, held)
				}
				body, _ := w.block(&ast.BlockStmt{List: clause.Body}, maps.Clone(held))
				held = intersectLocks(held, body)
			}
		}
	case *ast.SelectStmt:
		for _, branch := range value.Body.List {
			if clause, ok := branch.(*ast.CommClause); ok {
				branchState := maps.Clone(held)
				if clause.Comm != nil {
					branchState, _ = w.statement(clause.Comm, branchState)
				}
				body, _ := w.block(&ast.BlockStmt{List: clause.Body}, branchState)
				held = intersectLocks(held, body)
			}
		}
	case *ast.SendStmt:
		w.expression(value.Chan, held)
		w.expression(value.Value, held)
	case *ast.LabeledStmt:
		return w.statement(value.Stmt, held)
	}
	return held, true
}
