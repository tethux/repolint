# repolint

A Go linter for structured errors, assertion guards, and explicit types.

```sh
go get -tool github.com/tethux/repolint/cmd/repolint@v0.2.1
go tool repolint .
```

Requires Go 1.26.4 or newer. For a one-off check:

```sh
go run github.com/tethux/repolint/cmd/repolint@v0.2.1 .
```

## Rules

- `no-any`: use concrete types or focused interfaces instead of `any` and `interface{}`.
- `direct-error-comparison`: match error categories with `errors.Is`.
- `structured-error`: use the repository's error constructors or operation type.
- `expensive-assertion`: guard expensive checks with the configured condition.
- `package-boundaries`: restrict dependency direction with scoped import policies.
- `no-discarded-cleanup-error`: report ignored errors from configured cleanup calls.
- `cleanup-context-policy`: require approved context factories in rollback functions.
- `native-call-lock`: require a configured mutex around native calls.
- `native-allocation`: require immediate deferred release of native allocations.
- `native-length`: route native lengths through checked boundary helpers.
- `subprocess-context`: propagate cancellation or use approved context factories.
- `blocking-under-lock`: detect blocking channel operations and configured calls.
- `suppression-policy`: audit reasons, rule names, blanket exemptions, and stale directives.
- `schema-drift`: compare binding schemas with C, C++, and Go API surfaces.

The first two rules run by default. Enable the others in `.repolint.toml`:

```toml
schema = 1

[rules.structured-error]
enabled = true
paths = ["internal"]
error_package = "errs"
dialect = "constructors"
fix = "structural"
```

Paths are relative to the repository root. Tests and generated files are skipped by default. See the [configuration reference](docs/configuration.md) and the [Tethux](examples/tethux.toml) and [Dynamips](examples/dynamips.toml) examples.

## Fixes

```sh
go tool repolint --fix --diff .                 # preview
go tool repolint --fix .                        # safe fixes
go tool repolint --fix --allow-structural .     # error-domain changes
go tool repolint --fix --fix-rules=expensive-assertion .
```

Each rule permits `off`, `safe`, or `structural` fixes. Structural edits require both configuration permission and `--allow-structural`. `no-any` and `direct-error-comparison` report issues without rewriting code.

Fixes are staged, syntax checked, and applied with rollback on write failure. Run the project's tests after applying them.

To suppress a rule on the next line:

```go
//repolint:ignore structured-error -- compatibility boundary
return errors.New("legacy failure")
```

Diagnostics show the source span, help, and a command to preview configured fixes. Colors are automatic for terminal output; use `--color=always`, `--color=never`, or `NO_COLOR` to control them.

Use `all` to suppress every rule on that line. `--quiet` omits the summary; `--version` prints the installed version. Exit codes are `0` for clean source, `1` for remaining issues, and `2` for configuration or operational errors.

## Development

```sh
mise run fmt:go
mise run check
mise run build
```

MIT. Originally part of [Tethux](https://github.com/tethux/tethux).
