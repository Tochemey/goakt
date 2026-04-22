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
3. Commit your changes using a **Conventional Commit** message. See [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/)
4. Submit a [pull request](https://help.github.com/articles/using-pull-requests) from your fork to the `main` branch of the original repository.
5. Follow the instructions in the `playground` package to leave a working sample in case it is a bug.

### Previewing Documentation

To preview documentation changes locally, install [Mint CLI](https://mintlify.com/docs) and run:

```bash
mint dev
```
