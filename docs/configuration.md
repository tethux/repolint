# Configuration

Repolint reads `.repolint.toml` above the selected path, stopping at the module root. `--config=path` selects another file. Paths stay relative to the repository, regardless of the working directory. Unknown keys and invalid settings fail with exit code 2.

## Files and scope

```toml
schema = 1

[files]
exclude_dirs = [".git", ".jj", "vendor", "node_modules", "build"]
exclude_generated = true

[rules.no-any]
enabled = true
paths = ["."]
exclude_dirs = ["internal/compat"]
include_tests = false
```

These file exclusions are the defaults. File-level exclusions accept names that match anywhere in the tree, or directory prefixes. Rule-level `paths` and `exclude_dirs` accept repository-relative file or directory prefixes. `"."` selects everything; `paths = []` selects nothing. Symlinks are skipped.

Omitted settings keep their defaults. Each rule supports `enabled`, `paths`, `exclude_dirs`, `include_tests`, and `fix`. Tests are excluded by default. `no-any` and `direct-error-comparison` are enabled by default; the other rules require opt-in.

## Error comparisons

```toml
[rules.direct-error-comparison]
error_packages = ["errs"]
sentinel_prefix = "Err"
```

Checks comparisons against imported categories with the configured prefix. Package names or full import-path suffixes are accepted; aliases are resolved. This rule and `no-any` are diagnostic only.

## Assertions

```toml
[rules.expensive-assertion]
enabled = true
calls = ["assertValid*", "assertInvariants*"]
guard = "assert.Enabled"
fix = "safe"
```

Call patterns match function or method names. Guards must be identifiers or package selectors. Conjunctions preserve a guard; disjunctions and else branches do not. The fixer wraps standalone calls when the guard is available in that file. Other expressions remain diagnostic only.

## Structured errors

```toml
[rules.structured-error]
enabled = true
error_package = "errs"
sentinel_prefix = "Err"
dialect = "constructors"
new_constructor = "New"
wrap_constructor = "Wrap"
fix = "structural"
generate_domains = false
constructor_roles = ["operation", "kind", "target"]
```

Checks runtime calls to `fmt.Errorf` and `errors.New`. Package-level `errors.New` declarations define stable categories and are allowed. The configured error package is exempt.

The constructor dialect reads categories and constructor signatures from `errors.go` in the error package. It supports string `operation`, `provider`, and `target` parameters, and error `kind` and `cause` parameters. `op`, `category`, and `err` are recognized aliases for those roles.

To use an operation struct instead:

```toml
[rules.structured-error]
enabled = true
dialect = "operation"
operation_type = "errs.Operation"
fix = "safe"
```

The configured type must provide `Op`, `Kind`, and `Cause` fields. Operation fixes only use existing categories and never generate an error package.

### Category mappings

Map exact source messages or format strings to existing category names:

```toml
[rules.structured-error.categories]
"open %q: %w" = "ErrIO"
"closed" = "ErrClosed"
```

A mapping lets either dialect fix a call with formatting arguments. The category must already exist in the selected domain. An unknown mapping remains diagnostic; it never causes a new category to be guessed. Formatting and wrapped causes are retained. Indexed formats, dynamic widths, multiple wrapped causes, and causes that are not the last argument remain diagnostic only.

Without a mapping, safe fixes require an exact, unambiguous existing category message and no formatting arguments. Constructor structural fixes can also match categories from existing usage or introduce a new category.

### Domain generation

`generate_domains = true` permits a missing constructor-based error package to be created with structural fixing enabled. Supported `constructor_roles` are `["operation", "kind", "target"]`, `["provider", "kind", "target"]`, or `["kind", "target"]`. Constructor names and the sentinel prefix are configurable.

## Applying fixes

`fix = "off"` disables edits. `"safe"` permits local rewrites. `"structural"` additionally permits category and error-domain edits when `--allow-structural` is supplied. `--fix-rules` further restricts the permitted rules.

Fixes are prepared in a temporary tree and syntax checked before writing. Source permissions are preserved, concurrent source changes are rejected, and earlier writes are restored if a later write fails. Excluded and generated files cannot be replaced. A selected subdirectory or file also limits where fixes may write.

`--fix --diff` prints unified patches with three lines of context and leaves the checkout untouched. Diagnostics describe the proposed result. Fix validation checks syntax; the consuming project's tests check types and behavior.
