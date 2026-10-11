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

		report(diagnostic{
			Rule:       ruleErrorComparison,
			Position:   fset.Position(binary.Pos()),
			Message:    "structured error categories must not be compared directly",
			Suggestion: "use errors.Is so wrapped OpError categories are matched",
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
				Message:    "any is forbidden in important production modules",
				Suggestion: "use a concrete type or the smallest behavior-bearing interface",
			})

		case *ast.InterfaceType:
			if value.Methods != nil && len(value.Methods.List) != 0 {
				return true
			}

			report(diagnostic{
				Rule:       ruleNoAny,
				Position:   fset.Position(value.Pos()),
				Message:    "interface{} is forbidden in important production modules",
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

		if ignored(file, fset, call.Pos(), ruleStructuredError) {
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
				Message:    "fmt.Errorf is forbidden in this package",
				Suggestion: suggestion,
			})

		case importPath == "errors" &&
			selector.Sel.Name == "New":

			report(diagnostic{
				Rule:       ruleStructuredError,
				Position:   fset.Position(call.Pos()),
				Message:    "errors.New is forbidden in this package",
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
