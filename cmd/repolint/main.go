// Command repolint enforces configurable Go source policies.
package main

import (
	"cmp"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"slices"
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
	stats           *runStats
	output          io.Writer
}

const version = "0.2.0"

type runStats struct {
	Files   int
	Changed int
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	var opts options
	stats := runStats{}
	opts.stats = &stats
	opts.output = stdout
	flags := flag.NewFlagSet("repolint", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.BoolVar(&opts.fix, "fix", false, "apply configured fixes")
	flags.BoolVar(&opts.diff, "diff", false, "show a patch without writing files (requires --fix)")
	flags.BoolVar(&opts.allowStructural, "allow-structural", false, "allow configured error-domain edits (requires --fix)")
	flags.StringVar(&opts.fixRules, "fix-rules", "", "only fix these comma-separated rules")
	flags.StringVar(&opts.configPath, "config", "", "read configuration from this file")
	quiet := flags.Bool("quiet", false, "omit the summary")
	showVersion := flags.Bool("version", false, "print the version")
	flags.Usage = func() {
		_, writeErr := fmt.Fprint(stderr, "Usage: repolint [options] [path ...]\n\nCheck Go source files. Defaults to the current directory.\n\n")
		if writeErr != nil {
			return
		}
		flags.PrintDefaults()
	}
	if parseErr := flags.Parse(args); parseErr != nil {
		if errors.Is(parseErr, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *showVersion {
		if _, writeErr := fmt.Fprintf(stdout, "repolint %s\n", version); writeErr != nil {
			return 2
		}
		return 0
	}
	if !opts.fix && (opts.diff || opts.allowStructural || opts.fixRules != "") {
		return printFailure(stderr, failure("--diff, --allow-structural, and --fix-rules require --fix", nil))
	}
	roots := flags.Args()
	if len(roots) == 0 {
		roots = []string{"."}
	}
	var diagnostics []diagnostic
	var lintErrors []error
	for _, root := range roots {
		lintErr := lintRoot(root, opts, func(d diagnostic) { diagnostics = append(diagnostics, d) })
		if lintErr != nil {
			lintErrors = append(lintErrors, lintErr)
		}
	}
	slices.SortFunc(diagnostics, func(a, b diagnostic) int {
		if order := strings.Compare(a.Position.Filename, b.Position.Filename); order != 0 {
			return order
		}
		if order := cmp.Compare(a.Position.Offset, b.Position.Offset); order != 0 {
			return order
		}
		return strings.Compare(a.Rule, b.Rule)
	})
	for _, d := range diagnostics {
		if _, writeErr := fmt.Fprintf(stderr, "%s: %s: %s\n", displayPosition(d.Position), d.Rule, d.Message); writeErr != nil {
			return 2
		}
		if d.Suggestion != "" {
			if _, writeErr := fmt.Fprintf(stderr, "  help: %s\n", d.Suggestion); writeErr != nil {
				return 2
			}
		}
	}
	for _, lintErr := range lintErrors {
		if _, writeErr := fmt.Fprintf(stderr, "repolint: %v\n", lintErr); writeErr != nil {
			return 2
		}
	}
	if !*quiet && len(lintErrors) == 0 {
		summary := fmt.Sprintf("repolint: %d %s in %d %s", len(diagnostics), plural(len(diagnostics), "issue"), stats.Files, plural(stats.Files, "file"))
		if opts.fix {
			action := "fixed"
			if opts.diff {
				action = "would fix"
			}
			summary += fmt.Sprintf("; %s %d %s", action, stats.Changed, plural(stats.Changed, "file"))
		}
		if _, writeErr := fmt.Fprintln(stderr, summary); writeErr != nil {
			return 2
		}
	}
	if len(lintErrors) > 0 {
		return 2
	}
	if len(diagnostics) > 0 {
		return 1
	}
	return 0
}

func printFailure(output io.Writer, err error) int {
	_, writeErr := fmt.Fprintf(output, "repolint: %v\n", err)
	if writeErr != nil {
		return 2
	}
	return 2
}

func plural(count int, word string) string {
	if count == 1 {
		return word
	}
	return word + "s"
}

func displayPosition(position token.Position) string {
	path := position.Filename
	if cwd, cwdErr := os.Getwd(); cwdErr == nil {
		if rel, relErr := filepath.Rel(cwd, path); relErr == nil {
			path = rel
		}
	}
	return fmt.Sprintf("%s:%d:%d", filepath.ToSlash(path), position.Line, position.Column)
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
	if opts.stats != nil {
		opts.stats.Files++
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
