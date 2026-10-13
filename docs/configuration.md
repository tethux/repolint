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

## Package boundaries

```toml
[rules.package-boundaries]
enabled = true
include_tests = true

[[rules.package-boundaries.boundaries]]
name = "storage-is-independent"
from = ["storage"]
deny_imports = ["example.org/project/virt", "example.org/project/topology"]
allow_imports = ["example.org/project/virt/contract"]
```

`from` and `exclude_dirs` accept repository-relative file or directory prefixes. `deny_imports` matches the named import and its subpackages, with path-segment boundaries. `allow_imports` exempts exact imports only. Globs are not accepted. Policies have unique nonempty names and require both source scopes and denied imports. Import aliases, dot imports, and blank imports are checked. Tests require `include_tests = true`; generated-file exclusions still apply. This opt-in rule is diagnostic only.

## Cleanup policies

```toml
[rules.no-discarded-cleanup-error]
enabled = true
paths = ["virt"]
calls = ["*.Release", "os.Remove"]

[rules.cleanup-context-policy]
enabled = true
paths = ["virt"]
calls = ["*.Release"]
functions = ["rollback"]
context_factories = ["example.org/project/internal/cleanup.NewContext"]
context_arg = 0
```

Both opt-in rules are diagnostic only. Configure calls whose final result is an error. Imported function patterns use canonical import paths, so aliases cannot bypass a policy. Method calls use `receiver.Method`; `*.Release` selects any receiver's Release method within the configured paths. Patterns follow Go's `path.Match`, where `*` does not cross `/`. This is syntax analysis, not type resolution.

The error rule checks blank assignments, standalone calls, `defer`, and `go` statements. It does not prove that an assigned error is subsequently handled. A deferred closure that joins a cleanup error is allowed.

Set `type_check = true` on `no-discarded-cleanup-error` to report only calls whose final result implements `error`. Void methods are allowed. Package loading respects `[analysis] build_tags = ["debug"]`, the Go environment, and test inclusion. Type or package-loading failures are operational errors, never a silent syntax fallback. Fixes type-check planned edits through a Go overlay before writing. Native consumers need their normal cgo build dependencies available.

The context rule examines functions matching `functions`. Cleanup's `context_arg` must name a local variable declared by a configured factory in that function. Shadowed variables, reassignments, address escapes, and forwarded aliases are rejected conservatively. A direct `context.WithoutCancel` call is rejected. The factory's implementation must provide bounded, cancellation-independent cleanup; the rule trusts this contract and does not verify timeouts or cancellation of the factory's returned context. Helper forwarding and general control flow require a narrow, documented suppression. Rules retain the usual path, test, generated-file, and suppression behavior.

## Native ownership and locking

```toml
[rules.native-call-lock]
enabled = true
paths = ["internal/binding"]
calls = ["C.dyn_*"]
guard = "runtimeMu"
exclude_calls = []
exclude_functions = []

[rules.native-allocation]
enabled = true
paths = ["internal/binding"]
allocators = { "C.CString" = "C.free" }

[rules.native-length]
enabled = true
paths = ["internal/binding"]
sources = ["*.size", "*.payload_len", "written"]
exclude_functions = ["checkedNativeSize"]
```

The lock rule follows lexical `Lock`, `RLock`, `Unlock`, and `RUnlock` operations through blocks and branches. A deferred unlock keeps the lock active. A lock acquired on only one branch cannot protect a later call. Shadowing the configured lock does not satisfy the policy. Asynchronous functions must acquire their own lock. Helpers and complex interprocedural ownership remain explicit contracts, rather than inferred guarantees.

The allocation rule requires each configured allocator to bind a local variable and immediately defer the paired release. A direct argument or `unsafe.Pointer(variable)` is accepted. Release arguments are evaluated when the defer is registered. Container ownership, RAII wrappers, and intentional transfers require a scoped `exclude_functions` entry or exact-rule suppression with a reason; arbitrary escape analysis is not attempted.

The length rule reports configured native sources used in integer conversions, allocations, or slice bounds. Excluded checked helpers must validate integer range and buffer capacity, and copy borrowed data before its native lifetime ends. Source patterns are syntactic; forwarding through variables is not general taint analysis. All three rules are opt-in and diagnostic only.

## Subprocess contexts

```toml
[rules.subprocess-context]
enabled = true
type_check = true
context_factories = ["context.WithTimeout", "context.WithDeadline", "example.org/cli.Command.Context"]
allow_context_parameters = true
functions = ["*"]
```

`exec.Command` is rejected. `CommandContext` must receive a binding from an approved factory or, when permitted, a caller's `context.Context` parameter. Parameter aliases preserve cancellation provenance; unknown reassignment and address escapes are rejected conservatively. With type checking, method factories use exact `import/path.Type.Method` identities, so another type's similarly named method cannot qualify. Factories remain trusted contracts. Permitting a propagated caller context does not prove it has a deadline; use `allow_context_parameters = false` when every selected command requires a factory-created context.

## Blocking while locked

```toml
[rules.blocking-under-lock]
enabled = true
locks = ["*"]
calls = ["*.Wait"]
guard = "o.mu"
assume_locked_functions = ["broadcastLocked"]
```

The rule reports channel sends, receives, and configured calls while a selected lock is held. A `select` communication with a default branch is nonblocking. `assume_locked_functions` declares a caller-held `guard` for helpers whose precondition cannot be inferred locally. Use exact suppressions for bounded delivery invariants. `exclude_calls` and `exclude_functions` permit audited exceptions. This is a lexical check, not whole-program deadlock detection.

## Suppressions and required policies

```toml
[rules.suppression-policy]
enabled = true
include_tests = true
require_reason = true
allow_all = false
check_unused = true
```

Unknown rules, malformed directives, and attempts to suppress the audit itself fail. Reasons must follow `--`. Unused checks compare directives with diagnostics that would be emitted by the enabled rules without suppression. Generated-file and file exclusions still apply; a directive for a disabled rule is stale in that configuration.

`--require-rules=suppression-policy,package-boundaries` requires those rules to remain known, enabled, and nonempty in scope. This prevents disabling them or setting `paths = []` through configuration. Scope changes and exceptions still require review; this flag does not make a repository-controlled policy immutable.

## Binding schema drift

```toml
[rules.schema-drift]
enabled = true
schema_files = ["bindings/schema/*.yaml"]
headers = ["include/project/*.h"]
c_definitions = ["bindings/*.cpp", "common/runtime.c"]
cpp_files = ["modules/*.cppm"]
go_files = ["project.go"]
require_signatures = true
allow_c_symbols = ["project_bytes_release"]
calls = ["cmd_*"]
header_copies = { "include/project/api.h" = "internal/binding/api.h" }
```

Each YAML document uses `schema_version = 1` and `scope.required`, `scope.deferred`, and `scope.legacy_only` lists. Every required operation needs matching `operation`, `classification`, `c_name`, `cpp_name`, and `go_name` fields. Required scope entries cannot lack operations. Nonrequired C exports and public C symbols without contracts need exact `allow_c_symbols` exceptions.

Operations record `c_signature`, `cpp_signature`, and `go_signature` snapshots. C signatures include return and parameter types; declaration and definition signatures must also agree. C++ signatures include public member return types, parameters, and qualifiers. Go signatures use `(parameter types)(result types)` without parameter names. C++ names use `namespace::class::member`; Go names use `package.Function` or `package.Type.Method`.

The reader supports ordinary C prototypes, C definitions, `cfn name(...) -> type` definitions, and the project's C++ class declarations. Comments, strings, and raw strings are masked. This constrained surface reader does not replace a C/C++ compiler, validate implementation semantics, or prove ABI layout. Keep native compilation and conformance tests required. Contract metadata outside the checked surface may evolve without changing the checker.

Inputs use repository-relative file globs; empty matches and invalid schemas are operational failures. File reads are confined to the repository root. Header copies must match their canonical source byte-for-byte. `calls` forbids matching native dependencies in definition files. The repository-wide check runs when selecting the repository root; `paths` and `exclude_dirs` scope schema documents, while the explicit input lists select the other layers.

## Applying fixes

`fix = "off"` disables edits. `"safe"` permits local rewrites. `"structural"` additionally permits category and error-domain edits when `--allow-structural` is supplied. `--fix-rules` further restricts the permitted rules.

Fixes are prepared in a temporary tree and syntax checked before writing. Source permissions are preserved, concurrent source changes are rejected, and earlier writes are restored if a later write fails. Excluded and generated files cannot be replaced. A selected subdirectory or file also limits where fixes may write.

`--fix --diff` prints unified patches with three lines of context and leaves the checkout untouched. Diagnostics describe the proposed result. Fix validation checks syntax; the consuming project's tests check types and behavior.
