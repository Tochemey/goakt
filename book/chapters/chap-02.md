# 2. Building, Running and Testing the Code

Verified against: `cf7a7c6d` and the uncommitted changes of branch `issue-1432` (2026-10-03): every statement checked against the code

## Contents

- [Everything runs in a container](#everything-runs-in-a-container)
  - [`vendor/` is not committed](#vendor-is-not-committed)
- [Running one test while you read](#running-one-test-while-you-read)
- [How the full suite is split](#how-the-full-suite-is-split)
- [Generated code](#generated-code)
- [Lint rules that affect how code reads](#lint-rules-that-affect-how-code-reads)

## Everything runs in a container

Every `make` target runs inside a Docker image built from `Dockerfile.tools`, so the only host requirements are Docker and Make (`Makefile`). The image pins the tools:

| Tool | Version | Source |
|---|---|---|
| Go | `golang:1.27.1-alpine` | `Dockerfile.tools` |
| golangci-lint | v2.13.2 | `Dockerfile.tools` |
| buf | v1.73.0 | `Dockerfile.tools` |
| mockery | v3.8.0 | `Dockerfile.tools` |
| protoc-gen-go | `latest` (unpinned) | `Dockerfile.tools` |

Two mismatches to be aware of:

- The container builds with Go 1.27.1, but CI installs Go 1.26.6 (`.github/workflows/pr.yml`) and `go.mod` declares 1.26.0. A local `make lint` therefore runs on a newer toolchain than CI does.
- `protoc-gen-go` is installed at `latest`, so regenerating protobuf code on two different days can produce different output.

### `vendor/` is not committed

`vendor` is in `.gitignore`. The tools image sets `GOFLAGS=-mod=vendor` (`Dockerfile.tools`) and golangci-lint is configured to read modules from `vendor/` (`.golangci.yml`). CI creates `vendor/` with `go mod tidy && go mod vendor` before it lints or tests (`.github/workflows/pr.yml`). On a fresh clone, run `make vendor` first (`Makefile`).

## Running one test while you read

The full suite is large (about 167,500 test lines), and the `actor` package alone takes most of its wall-clock time. While reading, run only the test you care about with a local Go 1.26+ toolchain:

```bash
go test -mod=vendor -race -count=1 -run '^TestActorSystem$/^When_already_started$' ./actor
```

Subtest names replace spaces with underscores. Use `-run '^TestName$'` to list subtests with `-v`.

## How the full suite is split

`make unit-test` (`Makefile`) calls `scripts/unit-test.sh`. It works in two phases:

1. **Compile.** One container compiles every test binary with the race detector but runs nothing (`go test -race -exec true -run '^$' ./...`, `scripts/unit-test.sh`). A compile error stops the run here.
2. **Shards.** Six shards run in parallel, each in its own fresh container (`scripts/unit-test.sh`). The Docker socket is shared with the containers because some tests start Consul and etcd containers of their own.

`scripts/test-shard.sh` defines the shards:

| Shard | What it runs | Source |
|---|---|---|
| `actor-0`, `actor-1`, `actor-2` | The `actor` package's top-level tests, sorted by name and dealt round-robin into three slices, run serially (`-p 1`) | `scripts/test-shard.sh` |
| `infra` | Packages that bind ports or start cluster, remoting or discovery infrastructure, run serially | `scripts/test-shard.sh` |
| `core` | Every other package, in parallel | `scripts/test-shard.sh` |
| `memory` | The `memory` package without the race detector | `scripts/test-shard.sh` |

The round-robin split matters when you add a test to `actor`: you do not choose its shard. It lands in whichever slice its name sorts into, and that can change which other tests it shares a process with.

CI runs the five race shards (`actor-0`, `actor-1`, `actor-2`, `infra` and `core`) as a job matrix (`.github/workflows/pr.yml`). It has no Linux `memory` shard. It adds a cross-compile of the module for seven OS/architecture targets, which the local script does not have. It also runs the local `memory` shard's command on real macOS and Windows runners instead of Linux, because the package's per-OS code can compile cleanly and still fail at runtime.

## Generated code

| What | Command | Notes |
|---|---|---|
| Protobuf (`internal/internalpb`, `test/data/testpb`) | `make protogen` (`Makefile`) | Generated with the **opaque API** (`buf.gen.yaml`) |
| Mocks (`mocks/`) | `make mock` | Six interfaces: `hash.Hasher`, `discovery.Provider`, `internal/cluster.Cluster`, `extension.Dependency` and `extension.Extension`, `internal/remoteclient.Client` (`.mockery.yml`) |

The opaque API changes how every protobuf message is used in this codebase. Generated messages have unexported fields, so code never builds them with struct literals. It uses setters and getters instead, for example `peerState.SetHost(x.Host())` (`actorSystem.preShutdown` in `actor/actor_system.go`). Expect this pattern wherever the implementation touches the wire.

## Lint rules that affect how code reads

`make lint` enables a small, fixed set of linters (`.golangci.yml`). Two of them shape every file you will read:

- **`goheader`** requires the exact MIT license header, including the year range `2022-2026`, at the top of every Go file (`.golangci.yml`).
- **`misspell`** runs with the US locale but whitelists British spellings (`.golangci.yml`). Comments say "behaviour", "serialise" and "cancelled", and the linter accepts them.

House style uses `x` as the method receiver name in most of the code (`func (x *actorSystem) Start(...)`). Some older types keep their own: `pid` on `PID` in `actor/pid.go`, `rctx` on `ReceiveContext` in `actor/receive_context.go`.
