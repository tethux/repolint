# repolint

Configurable Go source policies, extracted from [Tethux](https://github.com/tethux/tethux). Uses Go's AST and one TOML dependency. Repository CI stays in each consuming project.

## Run

Requires Go 1.26.4 or newer. Pin the tool in a consuming module:

```sh
go get -tool github.com/tethux/repolint/cmd/repolint@v0.1.0
go tool repolint .
```

For a one-off invocation:

```sh
go run github.com/tethux/repolint/cmd/repolint@v0.1.0 .
```

## Configuration

Repolint discovers `.repolint.toml` above the selected path, stopping at the module root. `--config=path` selects another configuration file. Paths are relative to the consuming repository, including when invoked from another directory. Unknown fields, rule names, dialects, and fix modes fail clearly.

Without configuration, `no-any` and `direct-error-comparison` are enabled for production Go files. Assertion and structured-error policies require opt-in. Generated files, symlinks, and `.git`, `.jj`, `vendor`, `node_modules`, and `build` directories are skipped.

```toml
schema = 1

[files]
exclude_dirs = [".git", ".jj", "vendor", "node_modules", "build"]
exclude_generated = true

[rules.no-any]
enabled = true
paths = ["."]
include_tests = false
fix = "off"

[rules.direct-error-comparison]
enabled = true
paths = ["."]
error_packages = ["errs"]
sentinel_prefix = "Err"

[rules.expensive-assertion]
enabled = true
paths = ["."]
calls = ["assertValid*", "assertInvariants*"]
guard = "assert.Enabled"
fix = "safe"

[rules.structured-error]
enabled = true
paths = ["internal"]
exclude_dirs = ["internal/compat"]
error_package = "errs"
dialect = "constructors"
new_constructor = "New"
wrap_constructor = "Wrap"
fix = "structural"
generate_domains = false
constructor_roles = ["operation", "kind", "target"]
```

`paths` and rule-level `exclude_dirs` are directory prefixes, with `"."` selecting everything. `include_tests` defaults to false. File-level `exclude_dirs` also accepts directory names that apply anywhere in the tree. Empty `paths` disables a rule's scope. Rule settings omitted from a table use their defaults; specify `enabled = true` when enabling a policy.

See [Tethux's configuration](examples/tethux.toml) and [Dynamips's configuration](examples/dynamips.toml). Dynamips checks structured errors in the bindings; the example programs can define their own sentinels.

## Rules

- `no-any` diagnoses the built-in `any` and empty interfaces. Local values, fields, and functions named `any` are allowed. Diagnostic only; choosing a replacement type requires intent.
- `direct-error-comparison` diagnoses `==` and `!=` against imported error categories. Configurable error package suffixes and sentinel prefix; import aliases are supported. Diagnostic only.
- `expensive-assertion` requires calls matching configured name patterns to be guarded by the configured identifier or package selector. Conjunctions preserve the guard; disjunctions and else branches do not. Fixes wrap standalone calls when the guard resolves in that file. Complex expressions remain diagnostic only.
- `structured-error` diagnoses `fmt.Errorf` and `errors.New` outside the configured error package. The `constructors` dialect learns the `New` and `Wrap` parameter roles and existing categories from `errors.go`. The `operation` dialect uses an error struct, configured with `operation_type = "errs.Operation"`, with `Op`, `Kind`, and `Cause` fields.

Suppress a rule for the next source line:

```go
//repolint:ignore structured-error -- compatibility boundary
return errors.New("legacy failure")
```

`all` suppresses every rule on that line. Fixes respect suppressions.

## Fixes

```sh
go tool repolint --fix .
go tool repolint --fix --diff .
go tool repolint --fix --fix-rules=expensive-assertion .
go tool repolint --fix --allow-structural .
```

Each rule sets `fix = "off"`, `"safe"`, or `"structural"`. Ordinary `--fix` permits only safe fixes. Structural fixes require both `fix = "structural"` in the configuration and `--allow-structural`. Rule filters further restrict permitted edits. Rules without a fixer stay diagnostic only.

Safe structured-error fixes require an exact existing category message and retain the cause. Calls with additional formatting arguments remain diagnostic only. Operation fixes never generate categories or error packages. Unsupported format verbs, indexed formats, multiple wrapped causes, and unknown constructor roles remain diagnostic only.

Constructor structural fixes can learn category usage, add sentinels, and optionally generate a missing error package when `generate_domains = true`. Generated constructors use `constructor_roles` and the configured constructor names. Review structural changes: assigning a domain category can change an error's meaning.

Fixes are planned in a temporary tree, checked for Go syntax, and applied with source permissions preserved. Changed files must remain inside the selected root. Excluded and generated files are never replaced. Writes use temporary files and roll back already applied edits if a later write fails. Concurrent source changes are rejected. `--diff` prints unified diffs without touching the repository. Diagnostics describe the planned result; unresolved diagnostics cause a nonzero exit status. Planning checks syntax, not full type correctness; run the consuming project's focused tests after fixes.

## Development

```sh
mise run fmt:go
mise run check
mise run build
```

The code and license come from Tethux; licensed under AGPL-3.0.
