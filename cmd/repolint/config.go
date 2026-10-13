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
	Analysis AnalysisConfig        `toml:"analysis"`
	Schema   int                   `toml:"schema"`
	Files    FileConfig            `toml:"files"`
	Rules    map[string]RuleConfig `toml:"rules"`
}

type AnalysisConfig struct {
	BuildTags []string `toml:"build_tags"`
}

type FileConfig struct {
	ExcludeDirs      []string `toml:"exclude_dirs"`
	ExcludeGenerated bool     `toml:"exclude_generated"`
}

type RuleConfig struct {
	SchemaFiles            []string          `toml:"schema_files"`
	Headers                []string          `toml:"headers"`
	CDefinitions           []string          `toml:"c_definitions"`
	CppFiles               []string          `toml:"cpp_files"`
	GoFiles                []string          `toml:"go_files"`
	HeaderCopies           map[string]string `toml:"header_copies"`
	AllowCSymbols          []string          `toml:"allow_c_symbols"`
	RequireSignatures      bool              `toml:"require_signatures"`
	RequireReason          bool              `toml:"require_reason"`
	AllowAll               bool              `toml:"allow_all"`
	CheckUnused            bool              `toml:"check_unused"`
	Locks                  []string          `toml:"locks"`
	AssumeLockedFunctions  []string          `toml:"assume_locked_functions"`
	AllowContextParameters bool              `toml:"allow_context_parameters"`
	Sources                []string          `toml:"sources"`
	Allocators             map[string]string `toml:"allocators"`
	ExcludeCalls           []string          `toml:"exclude_calls"`
	ExcludeFunctions       []string          `toml:"exclude_functions"`
	TypeCheck              bool              `toml:"type_check"`
	Functions              []string          `toml:"functions"`
	ContextFactories       []string          `toml:"context_factories"`
	ContextArg             int               `toml:"context_arg"`
	Boundaries             []PackageBoundary `toml:"boundaries"`
	source                 *ast.File
	positions              *token.FileSet
	Enabled                bool              `toml:"enabled"`
	Paths                  []string          `toml:"paths"`
	ExcludeDirs            []string          `toml:"exclude_dirs"`
	IncludeTests           bool              `toml:"include_tests"`
	Fix                    string            `toml:"fix"`
	Calls                  []string          `toml:"calls"`
	Guard                  string            `toml:"guard"`
	ErrorPackage           string            `toml:"error_package"`
	ErrorPackages          []string          `toml:"error_packages"`
	SentinelPrefix         string            `toml:"sentinel_prefix"`
	Dialect                string            `toml:"dialect"`
	NewConstructor         string            `toml:"new_constructor"`
	WrapConstructor        string            `toml:"wrap_constructor"`
	OperationType          string            `toml:"operation_type"`
	GenerateDomains        bool              `toml:"generate_domains"`
	Categories             map[string]string `toml:"categories"`
	ConstructorRoles       []string          `toml:"constructor_roles"`
}

// PackageBoundary restricts imports from repository-relative source scopes.
type PackageBoundary struct {
	ExcludeDirs  []string `toml:"exclude_dirs"`
	Name         string   `toml:"name"`
	From         []string `toml:"from"`
	DenyImports  []string `toml:"deny_imports"`
	AllowImports []string `toml:"allow_imports"`
}

func defaultConfig() Config {
	return Config{Schema: 1, Files: FileConfig{ExcludeDirs: []string{".git", ".jj", "vendor", "node_modules", "build"}, ExcludeGenerated: true}, Rules: map[string]RuleConfig{
		ruleSchemaDrift:        {Paths: []string{"."}, Fix: "off", RequireSignatures: true},
		ruleSuppression:        {Paths: []string{"."}, Fix: "off", IncludeTests: true, RequireReason: true, CheckUnused: true},
		ruleBlockingLock:       {Paths: []string{"."}, Fix: "off", Locks: []string{"*"}},
		ruleSubprocessContext:  {Paths: []string{"."}, Fix: "off", Calls: []string{"os/exec.CommandContext"}, Functions: []string{"*"}, ContextFactories: []string{"context.WithTimeout", "context.WithDeadline"}, AllowContextParameters: true},
		ruleNativeLength:       {Paths: []string{"."}, Fix: "off", Sources: []string{"*.size", "*.payload_len"}},
		ruleNativeAllocation:   {Paths: []string{"."}, Fix: "off", Allocators: map[string]string{"C.CString": "C.free"}},
		ruleNativeLock:         {Paths: []string{"."}, Fix: "off", Calls: []string{"C.dyn_*"}, Guard: "runtimeMu"},
		ruleNoAny:              {Enabled: true, Paths: []string{"."}, Fix: "off"},
		rulePackageBoundaries:  {Paths: []string{"."}, Fix: "off"},
		ruleDiscardedCleanup:   {Paths: []string{"."}, Fix: "off", Calls: []string{"*.Release"}},
		ruleCleanupContext:     {Paths: []string{"."}, Fix: "off", Calls: []string{"*.Release"}, Functions: []string{"rollback"}},
		ruleStructuredError:    {Paths: []string{"."}, Fix: "off", ErrorPackage: "errs", SentinelPrefix: "Err", Dialect: "constructors", NewConstructor: "New", WrapConstructor: "Wrap", ConstructorRoles: []string{"operation", "kind", "target"}},
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
			Analysis AnalysisConfig `toml:"analysis"`
			Schema   int            `toml:"schema"`
			Files    FileConfig     `toml:"files"`
			Rules    struct {
				Assertions         RuleConfig `toml:"expensive-assertion"`
				Structured         RuleConfig `toml:"structured-error"`
				Comparison         RuleConfig `toml:"direct-error-comparison"`
				NoAny              RuleConfig `toml:"no-any"`
				Boundaries         RuleConfig `toml:"package-boundaries"`
				CleanupErrors      RuleConfig `toml:"no-discarded-cleanup-error"`
				CleanupContexts    RuleConfig `toml:"cleanup-context-policy"`
				NativeLocks        RuleConfig `toml:"native-call-lock"`
				NativeAllocations  RuleConfig `toml:"native-allocation"`
				NativeLengths      RuleConfig `toml:"native-length"`
				SubprocessContexts RuleConfig `toml:"subprocess-context"`
				BlockingLocks      RuleConfig `toml:"blocking-under-lock"`
				Suppressions       RuleConfig `toml:"suppression-policy"`
				SchemaDrift        RuleConfig `toml:"schema-drift"`
			} `toml:"rules"`
		}{Schema: cfg.Schema, Files: cfg.Files}
		wire.Rules.Assertions = cfg.Rules[ruleExpensiveAssertion]
		wire.Rules.Structured = cfg.Rules[ruleStructuredError]
		wire.Rules.Comparison = cfg.Rules[ruleErrorComparison]
		wire.Rules.NoAny = cfg.Rules[ruleNoAny]
		wire.Rules.Boundaries = cfg.Rules[rulePackageBoundaries]
		wire.Rules.CleanupErrors = cfg.Rules[ruleDiscardedCleanup]
		wire.Rules.CleanupContexts = cfg.Rules[ruleCleanupContext]
		wire.Rules.NativeLocks = cfg.Rules[ruleNativeLock]
		wire.Rules.NativeAllocations = cfg.Rules[ruleNativeAllocation]
		wire.Rules.NativeLengths = cfg.Rules[ruleNativeLength]
		wire.Rules.SubprocessContexts = cfg.Rules[ruleSubprocessContext]
		wire.Rules.BlockingLocks = cfg.Rules[ruleBlockingLock]
		wire.Rules.Suppressions = cfg.Rules[ruleSuppression]
		wire.Rules.SchemaDrift = cfg.Rules[ruleSchemaDrift]
		decodeErr := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields().Decode(&wire)
		if decodeErr != nil {
			return Config{}, "", failure("decode configuration", decodeErr)
		}
		cfg.Schema = wire.Schema
		cfg.Analysis = wire.Analysis
		cfg.Files = wire.Files
		cfg.Rules[ruleExpensiveAssertion] = wire.Rules.Assertions
		cfg.Rules[ruleStructuredError] = wire.Rules.Structured
		cfg.Rules[ruleErrorComparison] = wire.Rules.Comparison
		cfg.Rules[ruleNoAny] = wire.Rules.NoAny
		cfg.Rules[rulePackageBoundaries] = wire.Rules.Boundaries
		cfg.Rules[ruleDiscardedCleanup] = wire.Rules.CleanupErrors
		cfg.Rules[ruleCleanupContext] = wire.Rules.CleanupContexts
		cfg.Rules[ruleNativeLock] = wire.Rules.NativeLocks
		cfg.Rules[ruleNativeAllocation] = wire.Rules.NativeAllocations
		cfg.Rules[ruleNativeLength] = wire.Rules.NativeLengths
		cfg.Rules[ruleSubprocessContext] = wire.Rules.SubprocessContexts
		cfg.Rules[ruleBlockingLock] = wire.Rules.BlockingLocks
		cfg.Rules[ruleSuppression] = wire.Rules.Suppressions
		cfg.Rules[ruleSchemaDrift] = wire.Rules.SchemaDrift
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
		for _, patterns := range [][]string{rule.Calls, rule.Functions, rule.ExcludeCalls, rule.ExcludeFunctions, rule.Locks, rule.AssumeLockedFunctions, rule.Sources} {
			for _, pattern := range patterns {
				if pattern == "" {
					return failure("empty pattern for "+name, nil)
				}
				if _, matchErr := path.Match(pattern, "value"); matchErr != nil {
					return failure("invalid pattern for "+name, matchErr)
				}
			}
		}
		if !slices.Contains([]string{ruleNoAny, ruleStructuredError, ruleErrorComparison, ruleExpensiveAssertion, rulePackageBoundaries, ruleDiscardedCleanup, ruleCleanupContext, ruleNativeLock, ruleNativeAllocation, ruleNativeLength, ruleSubprocessContext, ruleBlockingLock, ruleSuppression, ruleSchemaDrift}, name) {
			return failure("unknown rule: "+name, nil)
		}
		if rule.Fix != "off" && rule.Fix != "safe" && rule.Fix != "structural" {
			return failure("invalid fix mode for "+name, nil)
		}
		if name == ruleSchemaDrift {
			if rule.Fix != "off" || (rule.Enabled && (len(rule.SchemaFiles) == 0 || len(rule.Headers) == 0 || len(rule.CDefinitions) == 0 || len(rule.CppFiles) == 0 || len(rule.GoFiles) == 0)) {
				return failure("schema-drift requires schema_files, headers, c_definitions, cpp_files, go_files, and fix=off", nil)
			}
			for _, patterns := range [][]string{rule.SchemaFiles, rule.Headers, rule.CDefinitions, rule.CppFiles, rule.GoFiles} {
				for _, pattern := range patterns {
					if !validPrefix(pattern) {
						return failure("invalid schema input path: "+pattern, nil)
					}
					if _, matchErr := filepath.Match(pattern, "file"); matchErr != nil {
						return failure("invalid schema glob", matchErr)
					}
				}
			}
			for source, target := range rule.HeaderCopies {
				if !validPrefix(source) || !validPrefix(target) {
					return failure("invalid header copy path", nil)
				}
			}
		}
		if name == ruleSuppression && rule.Fix != "off" {
			return failure("suppression-policy is diagnostic only", nil)
		}
		if name == ruleBlockingLock {
			if rule.Fix != "off" || len(rule.Locks) == 0 || (len(rule.AssumeLockedFunctions) > 0 && rule.Guard == "") {
				return failure("blocking-under-lock requires locks, fix=off, and a guard for caller-held locks", nil)
			}
			for _, pattern := range append(slices.Clone(rule.Locks), rule.Calls...) {
				if _, matchErr := path.Match(pattern, "value"); matchErr != nil {
					return failure("invalid blocking lock pattern", matchErr)
				}
			}
		}
		if name == ruleNativeLength {
			if rule.Fix != "off" || len(rule.Sources) == 0 {
				return failure("native-length requires sources and fix=off", nil)
			}
			for _, pattern := range rule.Sources {
				if _, matchErr := path.Match(pattern, "value"); matchErr != nil {
					return failure("invalid native length source", matchErr)
				}
			}
		}
		if name == ruleNativeAllocation {
			if rule.Fix != "off" || len(rule.Allocators) == 0 {
				return failure("native-allocation requires allocators and fix=off", nil)
			}
			for allocator, release := range rule.Allocators {
				for _, symbol := range []string{allocator, release} {
					prefix, function, found := splitCallSymbol(symbol)
					if !found || !validImportPrefix(prefix) || !validIdentifier(function) {
						return failure("invalid native allocation symbol: "+symbol, nil)
					}
				}
			}
		}
		if name == ruleNativeLock {
			if rule.Fix != "off" || len(rule.Calls) == 0 {
				return failure("native-call-lock requires calls and fix=off", nil)
			}
			if _, parseErr := parser.ParseExpr(rule.Guard); parseErr != nil || rule.Guard == "" {
				return failure("invalid native lock guard", parseErr)
			}
			for _, pattern := range append(slices.Clone(rule.Calls), rule.ExcludeCalls...) {
				if _, matchErr := path.Match(pattern, "call"); matchErr != nil {
					return failure("invalid native call pattern", matchErr)
				}
			}
		}
		if name == ruleDiscardedCleanup || name == ruleCleanupContext || name == ruleSubprocessContext {
			if rule.Fix != "off" {
				return failure(name+" is diagnostic only", nil)
			}
			if len(rule.Calls) == 0 {
				return failure(name+" requires cleanup calls", nil)
			}
			for _, pattern := range append(slices.Clone(rule.Calls), rule.Functions...) {
				if pattern == "" {
					return failure("empty cleanup pattern", nil)
				}
				if _, matchErr := path.Match(pattern, "call"); matchErr != nil {
					return failure("invalid cleanup pattern", matchErr)
				}
			}
			if name == ruleCleanupContext || name == ruleSubprocessContext {
				if rule.ContextArg < 0 || len(rule.Functions) == 0 || (rule.Enabled && len(rule.ContextFactories) == 0) {
					return failure("cleanup-context-policy requires functions, context_factories, and a nonnegative context_arg", nil)
				}
				for _, factory := range rule.ContextFactories {
					packagePath, function, found := splitCallSymbol(factory)
					if !found || !validImportPrefix(packagePath) || !validIdentifier(function) {
						return failure("context factory must be an exact import-path.Function symbol", nil)
					}
				}
			}
		}
		if name == rulePackageBoundaries {
			if rule.Fix != "off" {
				return failure("package-boundaries is diagnostic only", nil)
			}
			names := map[string]bool{}
			for _, boundary := range rule.Boundaries {
				if strings.TrimSpace(boundary.Name) == "" || names[boundary.Name] || len(boundary.From) == 0 || len(boundary.DenyImports) == 0 {
					return failure("boundaries require a unique name, from paths, and deny_imports", nil)
				}
				names[boundary.Name] = true
				for _, prefix := range append(slices.Clone(boundary.From), boundary.ExcludeDirs...) {
					if !validPrefix(prefix) || strings.ContainsAny(prefix, "*?[]") {
						return failure("invalid boundary source: "+prefix, nil)
					}
				}
				for _, prefix := range append(slices.Clone(boundary.DenyImports), boundary.AllowImports...) {
					if !validImportPrefix(prefix) {
						return failure("invalid boundary import: "+prefix, nil)
					}
				}
			}
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
			if !validIdentifier(rule.ErrorPackage) {
				return failure("error_package must be a package name", nil)
			}
			if !validIdentifier(rule.SentinelPrefix) {
				return failure("sentinel_prefix must be an identifier", nil)
			}
			for message, category := range rule.Categories {
				if message == "" || !validIdentifier(category) {
					return failure("categories must map nonempty messages to category identifiers", nil)
				}
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
	return name != "_" && parseErr == nil && selectorNameOrIdent(expr) == name && !strings.Contains(name, ".") && name != ""
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
