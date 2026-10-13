package main

import (
	"go/ast"
	"go/token"
	"strings"
)

func lintSuppressions(fset *token.FileSet, file *ast.File, report reporter, cfg Config, rule RuleConfig, raw []diagnostic) {
	for _, group := range file.Comments {
		for _, comment := range group.List {
			text := strings.TrimSpace(strings.TrimPrefix(comment.Text, "//"))
			if !strings.HasPrefix(text, "repolint:ignore") {
				continue
			}
			name, valid := parseIgnoreDirective(comment.Text)
			message := ""
			_, known := cfg.Rules[name]
			switch {
			case !valid:
				message = "malformed suppression directive"
			case name == ruleSuppression:
				message = "suppression-policy cannot suppress itself"
			case name != "all" && !known:
				message = "suppression names unknown rule " + name
			case name == "all" && !rule.AllowAll:
				message = "blanket suppression is forbidden; name the exact rule"
			default:
				_, reason, hasReason := strings.Cut(text, "--")
				if rule.RequireReason && (!hasReason || strings.TrimSpace(reason) == "") {
					message = "suppression requires a nonempty reason"
				}
			}
			if message == "" && rule.CheckUnused {
				used := false
				line := fset.Position(group.End()).Line + 1
				for _, d := range raw {
					if d.Position.Line == line && (d.Rule == name || name == "all") {
						used = true
						break
					}
				}
				if !used {
					message = "suppression does not silence an enabled diagnostic"
				}
			}
			if message != "" {
				report(diagnostic{Rule: ruleSuppression, Position: fset.Position(comment.Pos()), End: fset.Position(comment.End()), Message: message, Suggestion: "remove stale directives or use an exact rule with a concrete reason"})
			}
		}
	}
}
