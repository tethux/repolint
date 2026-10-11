// Command repolint enforces configurable Go source policies.
package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

const (
	ruleExpensiveAssertion = "expensive-assertion"
	ruleStructuredError    = "structured-error"
	ruleErrorComparison    = "direct-error-comparison"
	ruleNoAny              = "no-any"
)

type diagnostic struct {
	Rule       string
	Position   token.Position
	Message    string
	Suggestion string
}

type reporter func(diagnostic)

type options struct {
	fix             bool
	diff            bool
	allowStructural bool
	fixRules        string
	configPath      string
	cfg             Config
	base            string
}

func main() {
	var opts options

	flag.BoolVar(
		&opts.fix,
		"fix",
		false,
		"apply safe automatic fixes",
	)

	flag.BoolVar(&opts.diff, "diff", false, "preview permitted fixes without writing")
	flag.BoolVar(&opts.allowStructural, "allow-structural", false, "allow configured structural error fixes")
	flag.StringVar(&opts.fixRules, "fix-rules", "", "comma-separated rules to fix")
	flag.StringVar(&opts.configPath, "config", "", "configuration file (defaults to .repolint.toml)")
	flag.Usage = func() {
		// #nosec G705 -- this writes plain text to the CLI's terminal output.
		_, writeErr := fmt.Fprintf(
			flag.CommandLine.Output(),
			"Usage: %s [options] [path ...]\n\n"+
				"Enforces configurable Go source policies.\n\n"+
				"If no paths are supplied, the current directory is checked.\n\n"+
				"Suppression:\n"+
				"  //repolint:ignore RULE -- reason\n\n",
			filepath.Base(os.Args[0]),
		)
		if writeErr != nil {
			os.Exit(1)
		}

		flag.PrintDefaults()
	}

	flag.Parse()
	if !opts.fix && (opts.diff || opts.allowStructural || opts.fixRules != "") {
		_, writeErr := fmt.Fprintln(os.Stderr, "repolint: fix options require --fix")
		if writeErr != nil {
			os.Exit(1)
		}
		os.Exit(2)
	}

	roots := flag.Args()
	if len(roots) == 0 {
		roots = []string{"."}
	}

	failed := false

	report := func(d diagnostic) {
		failed = true

		_, writeErr := fmt.Fprintf(
			os.Stderr,
			"%s: [%s] %s\n",
			d.Position,
			d.Rule,
			d.Message,
		)
		if writeErr != nil {
			return
		}

		if d.Suggestion != "" {
			_, writeErr = fmt.Fprintf(os.Stderr, "  fix: %s\n", d.Suggestion)
			if writeErr != nil {
				return
			}
		}
	}

	for _, root := range roots {
		lintErr := lintRoot(root, opts, report)
		if lintErr != nil {
			_, writeErr := fmt.Fprintf(os.Stderr, "repolint: %v\n", lintErr)
			if writeErr != nil {
				failed = true
			}
			failed = true
		}
	}

	if failed {
		os.Exit(1)
	}
}

func lintRoot(root string, opts options, report reporter) error {
	cfg, base, configErr := loadConfig(root, opts.configPath)
	if configErr != nil {
		return configErr
	}
	opts.cfg = cfg
	opts.base = base
	if opts.fixRules != "" {
		for _, name := range strings.Split(opts.fixRules, ",") {
			if _, exists := cfg.Rules[name]; !exists {
				return failure("unknown fix rule: "+name, nil)
			}
		}
	}
	if opts.fix {
		return planFixes(root, opts, report)
	}
	return walkSource(root, opts, func(path string, src []byte) error { return lintSource(path, src, opts, report) })
}

func lintSource(path string, src []byte, opts options, report reporter) error {
	fset := token.NewFileSet()
	file, parseErr := parser.ParseFile(fset, path, src, parser.ParseComments)
	if parseErr != nil {
		return failure("parse "+path, parseErr)
	}
	if opts.cfg.Files.ExcludeGenerated && ast.IsGenerated(file) {
		return nil
	}
	rel, relErr := filepath.Rel(opts.base, path)
	if relErr != nil {
		return failure("resolve source path", relErr)
	}
	rel = filepath.ToSlash(rel)
	if rule := opts.cfg.Rules[ruleExpensiveAssertion]; rule.applies(rel) {
		lintAssertions(fset, file, false, report, rule)
	}
	if rule := opts.cfg.Rules[ruleStructuredError]; rule.applies(rel) && !isErrorPackage(rel, rule.ErrorPackage) {
		lintErrorConstructors(fset, file, report, rule)
	}
	if rule := opts.cfg.Rules[ruleErrorComparison]; rule.applies(rel) {
		lintDirectErrorComparisons(fset, file, report, rule)
	}
	if rule := opts.cfg.Rules[ruleNoAny]; rule.applies(rel) {
		lintAny(fset, file, report)
	}
	return nil
}

func isErrorPackage(path, name string) bool {
	return strings.Contains("/"+filepath.ToSlash(path)+"/", "/"+name+"/")
}

// ignored reports whether the nearest preceding comment contains:
//
//	//repolint:ignore RULE -- optional reason
//
// A suppression applies only to the immediately following source line.
func ignored(
	file *ast.File,
	fset *token.FileSet,
	pos token.Pos,
	rule string,
) bool {
	line := fset.Position(pos).Line

	for _, group := range file.Comments {
		endLine := fset.Position(group.End()).Line

		if endLine != line-1 {
			continue
		}

		for _, comment := range group.List {
			ignoredRule, ok := parseIgnoreDirective(comment.Text)
			if ok && (ignoredRule == rule || ignoredRule == "all") {
				return true
			}
		}
	}

	return false
}

func parseIgnoreDirective(comment string) (string, bool) {
	comment = strings.TrimSpace(comment)

	comment = strings.TrimPrefix(comment, "//")
	comment = strings.TrimSpace(comment)

	const prefix = "repolint:ignore"

	if !strings.HasPrefix(comment, prefix) {
		return "", false
	}

	rest := strings.TrimSpace(
		strings.TrimPrefix(comment, prefix),
	)

	if rest == "" {
		return "", false
	}

	rule, _, _ := strings.Cut(rest, " ")
	rule, _, _ = strings.Cut(rule, "--")

	rule = strings.TrimSpace(rule)

	if rule == "" {
		return "", false
	}

	return rule, true
}

func comparisonSettings(settings ...RuleConfig) RuleConfig {
	if len(settings) > 0 {
		return settings[0]
	}
	return defaultConfig().Rules[ruleErrorComparison]
}

func structuredSuggestion(rule RuleConfig) string {
	if rule.Dialect == "operation" {
		return "use &" + rule.OperationType + " with a category and cause"
	}
	return "use the package's structured " + rule.ErrorPackage + "." + rule.NewConstructor + " constructor"
}
