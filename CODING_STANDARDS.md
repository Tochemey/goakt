# Coding Standards

GoAkt code follows [Effective Go](https://go.dev/doc/effective_go). This document adds the rules specific to this repository.

These rules apply to code you add or change. Do not rewrite existing code that predates a rule only to make it comply.

## Enforced by `make lint`

`make lint` runs golangci-lint with `.golangci.yml`. A change that fails it does not merge.

- **License header:** every `.go` file starts with the MIT license header defined under `goheader` in `.golangci.yml` (`Copyright (c) 2022-2026 GoAkt Team`). Copy it from an existing file.
- **Formatting:** `gofmt` and `goimports`.
- **Linters:** `govet`, `staticcheck`, `revive` (default rules), `gosec`, `gocyclo`, `misspell` and `whitespace`. Generated files and `mocks/` are excluded.
- **Context first:** `context.Context` is the first parameter of a function (revive `context-as-argument`). Test files are exempt.
- **Spelling:** `misspell` runs with the US locale, so it flags British spellings. The British forms listed under `misspell.ignore-rules` (for example `behaviour`, `initialise`, `signalled`, `serialise`) are allowed. To use another British form, add every inflection of it to that list.

## Code style: blank lines around multi-line blocks

Put a blank line before and after any multi-line statement block (a `{ }`-delimited body spanning more than one line — `for`, `if`, `switch`, `select`, function literals, composite literals used as statements).

Exceptions:

- No blank line when the block is the first or last statement in its enclosing block. The `whitespace` linter rejects a blank line directly after the opening brace or directly before the closing brace of a function, `if` or `for` body.
- A simple statement immediately before a block may be grouped with it (no blank line between) when it directly sets up the block's condition or subject (e.g. `schema := cfg.schema` before `if schema == nil`).

Single-line statements forming a related unit may stay grouped without blank lines. The blank line is only required at the boundary between a multi-line block and surrounding code.

Example:

```go
cfg := funcConfig{}

for _, o := range opts {
    o(&cfg)
}

schema := cfg.schema
if schema == nil && !hasExplicitSchema(opts) {
    var zero T
    reflector := &jsonschema.Reflector{
        Anonymous:      true,
        DoNotReference: true,
    }
    schema = reflector.Reflect(&zero)
}
```

## File layout

- Put every package-level declaration at the top of the file, after the imports: constants first, then variables and types. Do not declare a `const`, `var` or `type` between functions or methods.
- The exception is a set of constants of a named type (an enum): put its `const` block directly after the type declaration.

  ```go
  // Mode determines how an actor processes other messages while waiting
  // for an async response started via Request/RequestName.
  type Mode int

  const (
      // Off disables async requests for the actor.
      Off Mode = iota
      // AllowAll keeps processing all messages while awaiting a response.
      AllowAll
      // ...
  )
  ```

- Put exported functions and methods before unexported ones. Do not place an unexported method or helper between exported ones; put it in the file's unexported section, next to the unexported code it works with.

## Go library conventions

- Use `x` as the receiver name for methods introduced or changed by the implementation, unless the file already gives that type another receiver name. revive's `receiver-naming` rule rejects two receiver names for one type within a file, so a method added to `actor/replicator.go` keeps `r`. Across files a type can mix names: `PID` methods use `pid` in `actor/pid.go` and `x` in `actor/pid_companion.go`.
- Keep function signatures on one line. Do not vertically wrap parameter lists.
- Document every function and method, exported or unexported, with clear GoDoc that explains its job.
- Document struct fields when their purpose, ownership, correlation role, or invariant is not obvious. Explain why the field exists, not only its Go type.
- Use plain domain language. Define protocol terms precisely and avoid unexplained shorthand.
- Assert at compile time that a type implements an interface: `var _ Interface = (*Type)(nil)`.
- Put public error sentinels and shared error constructors in the `errors` package (`github.com/tochemey/goakt/v4/errors`, imported as `gerrors`). Do not add package-local error helpers. Follow its existing patterns:
  - A sentinel is a documented `ErrXxx = errors.New("...")` entry in the package's `var` block.
  - A constructor is named `NewErrXxx`. It adds context with `fmt.Errorf("... %w", ErrXxx)` or attaches a cause with `errors.Join(ErrXxx, err)`, so `errors.Is(err, gerrors.ErrXxx)` still matches.
  - A typed error that carries a cause (`PanicError`, `InternalError`, `SpawnError`, `RebalancingError`) implements `Unwrap`.
- Do not edit generated code by hand. Change the source and regenerate:
  - `internal/internalpb` and `test/data/testpb`: edit the `.proto` files under `protos/` and run `make protogen`.
  - `mocks/`: edit `.mockery.yml` or the mocked interface and run `make mock`.

## Testing

- All changed code paths must have focused tests that cover both positive and negative cases.
- Write tests with the standard `testing` package plus testify's `require` and `assert`. Group cases as subtests with `t.Run`.
- Every test file pairs with the implementation file it tests (`client.go` → `client_test.go`). Add a test to that file. Do not create a test file that has no matching implementation file.
- The one exception is the package's `mocks_test.go`. It holds the mock types, fixtures and shared helpers the package's tests use; do not put them at the bottom of the test file that uses them.
- To wait for an asynchronous condition, use `require.Eventually`. For a fixed delay, use `pause.For` from `internal/pause`, not `time.Sleep`.
- Tests that bind network ports get them from `Get` in `internal/net/dynaport.go` (imported as `dynaport`), not hard-coded numbers.
- No code coverage regression. If coverage drops, add tests. Codecov requires 85% on both the patch and the project (`codecov.yml`), and ignores `test`, `testkit`, `mocks` and `internal/internalpb`.
