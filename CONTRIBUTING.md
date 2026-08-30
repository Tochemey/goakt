# Contributions are welcome

We welcome contributions! Whether you're fixing a bug, adding a new feature, or improving documentation, your help is appreciated.
This project adheres to [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/) to standardize commit messages and help automate releases.
All developer tooling runs inside Docker via the `Makefile`, so you do not need to install Go toolchain plugins, `buf`, `mockery`, `golangci-lint`, or `openssl` locally.

### Prerequisites

Before you start, make sure you have these installed:

* [Docker](https://docs.docker.com/get-started/get-docker/)
* `make` (pre-installed on macOS and most Linux distributions)
* [Mint CLI](https://mintlify.com/docs) — only if you plan to preview documentation changes

### Getting Started

1.  Fork the repository to your GitHub account.
2.  Clone your forked repository to your local machine:
    ```bash
    git clone https://github.com/your-username/goakt.git
    cd goakt
    ```
3.  Build the tools image once (subsequent runs reuse Docker's layer cache):
    ```bash
    make image
    ```

### Make targets

Run `make help` to list every target. The common ones are:

| Target           | Purpose                                              |
|------------------|------------------------------------------------------|
| `make test`      | Run lint and the full test suite                     |
| `make lint`      | Run `golangci-lint`                                  |
| `make unit-test` | Run tests with coverage (writes `coverage.out`)      |
| `make vendor`    | `go mod tidy` and `go mod vendor`                    |
| `make mock`      | Regenerate mocks under `mocks/`                      |
| `make protogen`  | Regenerate protobuf Go code                          |
| `make certs`     | Regenerate test TLS fixtures under `test/data/certs` |
| `make clean`     | Remove the tools image                               |

### Making Contributions

1. Make your changes in your fork.
2. Ensure your code adheres to the project's style and passes tests: `make test`.
3. Document user-visible changes in [`changelogs/unreleased.md`](changelogs/unreleased.md) (see [Changelog entries](#changelog-entries) below).
4. Commit your changes using a **Conventional Commit** message. See [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/)
5. Submit a [pull request](https://help.github.com/articles/using-pull-requests) from your fork to the `main` branch of the original repository.
6. Follow the instructions in the `playground` package to leave a working sample in case it is a bug or a new feature. This will help reviewers understand your changes and verify that they work as expected.

### Changelog entries

Document user-facing changes by creating or updating [`changelogs/unreleased.md`](changelogs/unreleased.md). Maintainers can only create or update `changelogs/unreleased.md` for pending release notes.

If `changelogs/unreleased.md` is missing, create it with this skeleton:

```markdown
# Unreleased

## ✨ Features

## 🔧 Fixes

## ⚡ Performance

## ⚠️ Behavior Changes

## 🗑️ Deprecations

## 📚 Documentation
```

Keep only the section headings that have entries. Add your note under the matching section using the same style as existing releases:

```markdown
## ✨ Features

- **Short title** ([#1234](https://github.com/Tochemey/goakt/issues/1234)). One or two sentences on what changed and why it matters to callers. Call out new APIs, options, or migration impact when relevant.
```

Guidelines:

* Update `changelogs/unreleased.md` in the same pull request as the code change.
* Link the authorizing GitHub issue (or PR) in the entry.
* Prefer clear, user-facing language over implementation detail.
* Purely internal refactors with no observable behavior change do not need a changelog entry.

### Contributor License Agreement (CLA)

GoAkt requires every contributor to sign our [Contributor License Agreement](CLA.md) before their pull request can be merged. This protects both you and the project, and only needs to be done once.

When you open your first pull request, the **CLA Assistant** bot (powered by [cla-assistant.io](https://cla-assistant.io)) will comment with a link to sign the CLA. Click the link, authenticate with GitHub, and confirm your acceptance. The signature covers all of your future contributions. The PR cannot be merged until the CLA check passes.

### Previewing Documentation

The documentation site lives under `docs/`. To preview changes locally, install [Mint CLI](https://mintlify.com/docs) and run:

```bash
cd docs
mint dev
```
