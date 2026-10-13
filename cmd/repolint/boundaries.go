package main

import (
	"go/ast"
	"go/token"
	"slices"
	"strconv"
	"strings"
)

func validImportPrefix(value string) bool {
	if value == "" || strings.ContainsAny(value, "*?[]\\ \t\r\n") || strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/") {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func lintPackageBoundaries(fset *token.FileSet, file *ast.File, rel string, report reporter, rule RuleConfig) {
	for _, spec := range file.Imports {
		importPath, unquoteErr := strconv.Unquote(spec.Path.Value)
		if unquoteErr != nil {
			continue
		}
		if ignored(file, fset, spec.Pos(), rulePackageBoundaries) {
			continue
		}
		for _, boundary := range rule.Boundaries {
			applies := false
			for _, prefix := range boundary.From {
				if matchesPrefix(rel, prefix) {
					applies = true
					break
				}
			}
			for _, prefix := range boundary.ExcludeDirs {
				if matchesPrefix(rel, prefix) {
					applies = false
					break
				}
			}
			if !applies || slices.Contains(boundary.AllowImports, importPath) {
				continue
			}
			for _, prefix := range boundary.DenyImports {
				if !hasPathPrefix(importPath, prefix) {
					continue
				}
				report(diagnostic{
					Rule: rulePackageBoundaries, Position: fset.Position(spec.Path.Pos()), End: fset.Position(spec.Path.End()),
					Message:    "import violates boundary " + boundary.Name + ": " + importPath,
					Suggestion: "depend on the shared contract or move this dependency to the owning adapter",
				})
				break
			}
		}
	}
}
