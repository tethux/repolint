package main

import (
	"go/ast"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
)

func lintDirectErrorComparisons(
	fset *token.FileSet,
	file *ast.File,
	report reporter,
	settings ...RuleConfig,
) {
	imports := importAliases(file)

	ast.Inspect(file, func(node ast.Node) bool {
		binary, ok := node.(*ast.BinaryExpr)
		if !ok || (binary.Op != token.EQL && binary.Op != token.NEQ) {
			return true
		}

		if !isStructuredSentinel(binary.X, imports, settings...) &&
			!isStructuredSentinel(binary.Y, imports, settings...) {
			return true
		}

		if ignored(file, fset, binary.Pos(), ruleErrorComparison) {
			return true
		}
		value, category := binary.X, binary.Y
		if isStructuredSentinel(binary.X, imports, settings...) {
			value, category = binary.Y, binary.X
		}
		replacement := "errors.Is(" + renderExpr(fset, value) + ", " + renderExpr(fset, category) + ")"
		if binary.Op == token.NEQ {
			replacement = "!" + replacement
		}

		report(diagnostic{
			Rule:       ruleErrorComparison,
			Position:   fset.Position(binary.Pos()),
			End:        fset.Position(binary.End()),
			Message:    "compare error categories with errors.Is",
			Suggestion: "replace with " + replacement + " to match wrapped errors",
		})

		return true
	})
}

func isStructuredSentinel(expr ast.Expr, imports map[string]string, settings ...RuleConfig) bool {
	rule := comparisonSettings(settings...)
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok || !strings.HasPrefix(selector.Sel.Name, rule.SentinelPrefix) {
		return false
	}

	pkg, ok := selector.X.(*ast.Ident)
	if !ok {
		return false
	}

	importPath, ok := imports[pkg.Name]
	if !ok {
		return false
	}
	for _, pkg := range rule.ErrorPackages {
		if importPath == pkg || strings.HasSuffix(importPath, "/"+pkg) {
			return true
		}
	}
	return false
}

func hasPathPrefix(path, prefix string) bool {
	return path == prefix ||
		strings.HasPrefix(path, prefix+"/")
}

func lintAny(
	fset *token.FileSet, file *ast.File, report reporter,
) {
	values := map[*ast.Ident]bool{}
	ast.Inspect(file, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.SelectorExpr:
			values[value.Sel] = true
		case *ast.Field:
			for _, name := range value.Names {
				values[name] = true
			}
		case *ast.KeyValueExpr:
			if ident, ok := value.Key.(*ast.Ident); ok {
				values[ident] = true
			}
		}
		return true
	})
	ast.Inspect(file, func(node ast.Node) bool {
		if node == nil {
			return true
		}

		if ignored(file, fset, node.Pos(), ruleNoAny) {
			return true
		}

		switch value := node.(type) {
		case *ast.Ident:
			if value.Name != "any" || value.Obj != nil || values[value] {
				return true
			}

			report(diagnostic{
				Rule:       ruleNoAny,
				Position:   fset.Position(value.Pos()),
				End:        fset.Position(value.End()),
				Message:    "replace any with a concrete type or interface",
				Suggestion: "use a concrete type or the smallest behavior-bearing interface",
			})

		case *ast.InterfaceType:
			if value.Methods != nil && len(value.Methods.List) != 0 {
				return true
			}

			report(diagnostic{
				Rule:       ruleNoAny,
				Position:   fset.Position(value.Pos()),
				End:        fset.Position(value.End()),
				Message:    "replace interface{} with a concrete type or interface",
				Suggestion: "use a concrete type or the smallest behavior-bearing interface",
			})
		}

		return true
	})
}

func lintErrorConstructors(
	fset *token.FileSet,
	file *ast.File,
	report reporter,
	settings ...RuleConfig,
) {
	imports := importAliases(file)
	sentinels := sentinelDeclarations(file)
	rule := defaultConfig().Rules[ruleStructuredError]
	if len(settings) > 0 {
		rule = settings[0]
	}

	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}

		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}

		pkg, ok := selector.X.(*ast.Ident)
		if !ok {
			return true
		}

		importPath, ok := imports[pkg.Name]
		if !ok {
			return true
		}

		if sentinels[call] || ignored(file, fset, call.Pos(), ruleStructuredError) {
			return true
		}

		switch {
		case importPath == "fmt" &&
			selector.Sel.Name == "Errorf":

			suggestion := "use the package's structured " + rule.ErrorPackage + "." + rule.NewConstructor + " constructor"
			if rule.Dialect == "operation" {
				suggestion = "use &" + rule.OperationType + " with a category and cause"
			}

			if errorfWrapsCause(call) && rule.Dialect != "operation" {
				suggestion = "format contains %w; use the package's structured " + rule.ErrorPackage + "." + rule.WrapConstructor + " constructor"
			}

			report(diagnostic{
				Rule:       ruleStructuredError,
				Position:   fset.Position(call.Pos()),
				End:        fset.Position(call.End()),
				Message:    "use a structured error instead of fmt.Errorf",
				Suggestion: suggestion,
			})

		case importPath == "errors" &&
			selector.Sel.Name == "New":

			report(diagnostic{
				Rule:       ruleStructuredError,
				Position:   fset.Position(call.Pos()),
				End:        fset.Position(call.End()),
				Message:    "use a structured error instead of errors.New",
				Suggestion: structuredSuggestion(rule),
			})
		}

		return true
	})
}

func errorfWrapsCause(call *ast.CallExpr) bool {
	if len(call.Args) == 0 {
		return false
	}

	lit, ok := call.Args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return false
	}

	formatString, err := strconv.Unquote(lit.Value)
	if err != nil {
		return false
	}

	return containsErrorWrapVerb(formatString)
}

// containsErrorWrapVerb detects an unescaped %w.
//
// %%w is literal text and must not count as an error-wrapping directive.
func containsErrorWrapVerb(formatString string) bool {
	for i := 0; i < len(formatString); i++ {
		if formatString[i] != '%' {
			continue
		}

		if i+1 >= len(formatString) {
			continue
		}

		if formatString[i+1] == '%' {
			i++
			continue
		}

		for j := i + 1; j < len(formatString); j++ {
			switch formatString[j] {
			case 'w':
				return true

			case 'v', 's', 'q', 'd', 'x', 'X', 'f', 'e', 'E',
				'g', 'G', 'o', 'O', 'c', 'p', 'T', 't', 'b',
				'U':
				i = j
				goto next
			}
		}

	next:
	}

	return false
}

func importAliases(file *ast.File) map[string]string {
	imports := make(map[string]string, len(file.Imports))

	for _, spec := range file.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}

		name := filepath.Base(importPath)

		if spec.Name != nil {
			name = spec.Name.Name
		}

		if name == "." || name == "_" {
			continue
		}

		imports[name] = importPath
	}

	return imports
}

// Stable package-level categories may use errors.New outside the error package.
func sentinelDeclarations(file *ast.File) map[*ast.CallExpr]bool {
	result := map[*ast.CallExpr]bool{}
	imports := importAliases(file)
	for _, decl := range file.Decls {
		group, ok := decl.(*ast.GenDecl)
		if !ok || group.Tok != token.VAR {
			continue
		}
		for _, spec := range group.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for _, expr := range value.Values {
				call, ok := expr.(*ast.CallExpr)
				if !ok {
					continue
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || selector.Sel.Name != "New" {
					continue
				}
				pkg, ok := selector.X.(*ast.Ident)
				if ok && imports[pkg.Name] == "errors" {
					result[call] = true
				}
			}
		}
	}
	return result
}
