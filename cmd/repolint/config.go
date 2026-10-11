package main

import (
	"bytes"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Config defines repository-relative rule scopes and permitted fixes.
type Config struct {
	Schema int                   `toml:"schema"`
	Files  FileConfig            `toml:"files"`
	Rules  map[string]RuleConfig `toml:"rules"`
}

type FileConfig struct {
	ExcludeDirs      []string `toml:"exclude_dirs"`
	ExcludeGenerated bool     `toml:"exclude_generated"`
}

type RuleConfig struct {
	source           *ast.File
	positions        *token.FileSet
	Enabled          bool     `toml:"enabled"`
	Paths            []string `toml:"paths"`
	ExcludeDirs      []string `toml:"exclude_dirs"`
	IncludeTests     bool     `toml:"include_tests"`
	Fix              string   `toml:"fix"`
	Calls            []string `toml:"calls"`
	Guard            string   `toml:"guard"`
	ErrorPackage     string   `toml:"error_package"`
	ErrorPackages    []string `toml:"error_packages"`
	SentinelPrefix   string   `toml:"sentinel_prefix"`
	Dialect          string   `toml:"dialect"`
	NewConstructor   string   `toml:"new_constructor"`
	WrapConstructor  string   `toml:"wrap_constructor"`
	OperationType    string   `toml:"operation_type"`
	GenerateDomains  bool     `toml:"generate_domains"`
	ConstructorRoles []string `toml:"constructor_roles"`
}

func defaultConfig() Config {
	return Config{Schema: 1, Files: FileConfig{ExcludeDirs: []string{".git", ".jj", "vendor", "node_modules", "build"}, ExcludeGenerated: true}, Rules: map[string]RuleConfig{
		ruleNoAny:              {Enabled: true, Paths: []string{"."}, Fix: "off"},
		ruleStructuredError:    {Paths: []string{"."}, Fix: "off", ErrorPackage: "errs", Dialect: "constructors", NewConstructor: "New", WrapConstructor: "Wrap", ConstructorRoles: []string{"operation", "kind", "target"}},
		ruleErrorComparison:    {Enabled: true, Paths: []string{"."}, Fix: "off", ErrorPackages: []string{"errs"}, SentinelPrefix: "Err"},
		ruleExpensiveAssertion: {Paths: []string{"."}, Fix: "safe", Calls: []string{"assertValid*", "assertInvariants*"}, Guard: "assert.Enabled"},
	}}
}

func loadConfig(root, explicit string) (Config, string, error) {
	abs, resolveErr := filepath.Abs(root)
	if resolveErr != nil {
		return Config{}, "", failure("resolve root", resolveErr)
	}
	info, statErr := os.Stat(abs)
	if statErr != nil {
		return Config{}, "", failure("stat root", statErr)
	}
	if !info.IsDir() {
		abs = filepath.Dir(abs)
	}
	configPath := explicit
	base := abs
	if configPath == "" {
		for dir := abs; ; dir = filepath.Dir(dir) {
			candidate := filepath.Join(dir, ".repolint.toml")
			_, configErr := os.Stat(candidate)
			if configErr == nil {
				configPath = candidate
				base = dir
				break
			}
			if !errors.Is(configErr, os.ErrNotExist) {
				return Config{}, "", failure("find configuration", configErr)
			}
			if _, moduleErr := os.Stat(filepath.Join(dir, "go.mod")); moduleErr == nil {
				base = dir
				break
			}
			if filepath.Dir(dir) == dir {
				break
			}
		}
	} else {
		var configErr error
		configPath, configErr = filepath.Abs(configPath)
		if configErr != nil {
			return Config{}, "", failure("resolve configuration", configErr)
		}
		if moduleRoot, _, moduleErr := findModule(root); moduleErr == nil {
			base = moduleRoot
		}
	}
	cfg := defaultConfig()
	if configPath != "" {
		data, readErr := os.ReadFile(configPath)
		if readErr != nil {
			return Config{}, "", failure("read configuration", readErr)
		}
		wire := struct {
			Schema int        `toml:"schema"`
			Files  FileConfig `toml:"files"`
			Rules  struct {
				Assertions RuleConfig `toml:"expensive-assertion"`
				Structured RuleConfig `toml:"structured-error"`
				Comparison RuleConfig `toml:"direct-error-comparison"`
				NoAny      RuleConfig `toml:"no-any"`
			} `toml:"rules"`
		}{Schema: cfg.Schema, Files: cfg.Files}
		wire.Rules.Assertions = cfg.Rules[ruleExpensiveAssertion]
		wire.Rules.Structured = cfg.Rules[ruleStructuredError]
		wire.Rules.Comparison = cfg.Rules[ruleErrorComparison]
		wire.Rules.NoAny = cfg.Rules[ruleNoAny]
		decodeErr := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields().Decode(&wire)
		if decodeErr != nil {
			return Config{}, "", failure("decode configuration", decodeErr)
		}
		cfg.Schema = wire.Schema
		cfg.Files = wire.Files
		cfg.Rules[ruleExpensiveAssertion] = wire.Rules.Assertions
		cfg.Rules[ruleStructuredError] = wire.Rules.Structured
		cfg.Rules[ruleErrorComparison] = wire.Rules.Comparison
		cfg.Rules[ruleNoAny] = wire.Rules.NoAny
	}
	if validateErr := cfg.validate(); validateErr != nil {
		return Config{}, "", validateErr
	}
	return cfg, base, nil
}

func (cfg Config) validate() error {
	if cfg.Schema != 1 {
		return failure("configuration schema must be 1", nil)
	}
	for name, rule := range cfg.Rules {
		if !slices.Contains([]string{ruleNoAny, ruleStructuredError, ruleErrorComparison, ruleExpensiveAssertion}, name) {
			return failure("unknown rule: "+name, nil)
		}
		if rule.Fix != "off" && rule.Fix != "safe" && rule.Fix != "structural" {
			return failure("invalid fix mode for "+name, nil)
		}
		for _, prefix := range append(slices.Clone(rule.Paths), rule.ExcludeDirs...) {
			if !validPrefix(prefix) {
				return failure("invalid path for "+name+": "+prefix, nil)
			}
		}
		if name == ruleExpensiveAssertion {
			if _, parseErr := parser.ParseExpr(rule.Guard); parseErr != nil {
				return failure("invalid assertion guard", parseErr)
			}
			parts := strings.Split(rule.Guard, ".")
			if len(parts) > 2 || !validIdentifier(parts[0]) || (len(parts) == 2 && !validIdentifier(parts[1])) {
				return failure("guard must be an identifier or package selector", nil)
			}
			for _, pattern := range rule.Calls {
				if _, matchErr := path.Match(pattern, "call"); matchErr != nil {
					return failure("invalid assertion pattern", matchErr)
				}
			}
		}
		if name == ruleStructuredError {
			if rule.Dialect != "constructors" && rule.Dialect != "operation" {
				return failure("unsupported error dialect", nil)
			}
			if rule.GenerateDomains && rule.Dialect != "constructors" {
				return failure("domain generation requires constructor dialect", nil)
			}
			if rule.ErrorPackage == "" || strings.ContainsAny(rule.ErrorPackage, "/\\.") {
				return failure("error_package must be a package name", nil)
			}
			if !validIdentifier(rule.NewConstructor) || !validIdentifier(rule.WrapConstructor) {
				return failure("invalid error constructor name", nil)
			}
			for _, role := range rule.ConstructorRoles {
				if !slices.Contains([]string{"operation", "provider", "kind", "target"}, role) {
					return failure("unsupported constructor role: "+role, nil)
				}
			}
			if rule.GenerateDomains && !validGenerationRoles(rule.ConstructorRoles) {
				return failure("generated constructor_roles must be [operation, kind, target], [provider, kind, target], or [kind, target]", nil)
			}
			if rule.Dialect == "operation" && (!validOperationType(rule.OperationType)) {
				return failure("operation_type must be a package selector", nil)
			}
		}
	}
	for _, prefix := range cfg.Files.ExcludeDirs {
		if !validPrefix(prefix) {
			return failure("invalid excluded directory: "+prefix, nil)
		}
	}
	return nil
}

func validIdentifier(name string) bool {
	expr, parseErr := parser.ParseExpr(name)
	return parseErr == nil && selectorNameOrIdent(expr) == name && !strings.Contains(name, ".") && name != ""
}

func validPrefix(value string) bool {
	clean := filepath.ToSlash(filepath.Clean(value))
	return value != "" && clean != ".." && !strings.HasPrefix(clean, "../") && !filepath.IsAbs(value) && value != ".." && !strings.HasPrefix(filepath.ToSlash(value), "../") && !strings.Contains(filepath.ToSlash(value), "/../")
}

func matchesPrefix(file, prefix string) bool {
	prefix = strings.TrimSuffix(filepath.ToSlash(filepath.Clean(prefix)), "/")
	return prefix == "." || hasPathPrefix(filepath.ToSlash(file), prefix)
}

func (rule RuleConfig) applies(file string) bool {
	if !rule.Enabled || (!rule.IncludeTests && strings.HasSuffix(file, "_test.go")) {
		return false
	}
	for _, prefix := range rule.ExcludeDirs {
		if matchesPrefix(file, prefix) {
			return false
		}
	}
	for _, prefix := range rule.Paths {
		if matchesPrefix(file, prefix) {
			return true
		}
	}
	return false
}

func (cfg Config) excluded(file string) bool {
	for _, prefix := range cfg.Files.ExcludeDirs {
		if strings.Contains(prefix, "/") {
			if matchesPrefix(file, prefix) {
				return true
			}
		} else {
			for _, part := range strings.Split(filepath.ToSlash(file), "/") {
				if part == prefix {
					return true
				}
			}
		}
	}
	return false
}

func validOperationType(value string) bool {
	parts := strings.Split(value, ".")
	return len(parts) == 2 && validIdentifier(parts[0]) && validIdentifier(parts[1])
}

func validGenerationRoles(roles []string) bool {
	return slices.Equal(roles, []string{"operation", "kind", "target"}) || slices.Equal(roles, []string{"provider", "kind", "target"}) || slices.Equal(roles, []string{"kind", "target"})
}
