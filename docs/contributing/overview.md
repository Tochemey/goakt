# Contributing Overview

We welcome contributions—bug fixes, features, and documentation improvements. This section helps collaborators get
started.

## Prerequisites

- [Docker](https://docs.docker.com/get-started/get-docker/)
- [Go](https://go.dev/doc/install)
- [Earthly](https://earthly.dev/get-earthly) for reproducible builds

## Before you start

1. Read the [Architecture Document](../../ARCHITECTURE.md) to understand the codebase
2. Check existing [issues](https://github.com/Tochemey/goakt/issues)
   and [discussions](https://github.com/Tochemey/goakt/discussions)
3. Follow [Conventional Commits](https://www.conventionalcommits.org/) for commit messages

## How to contribute

1. Fork the repository
2. Create a branch for your change
3. Make your changes; ensure tests pass: `earthly +test`
4. Commit with a conventional commit message
5. Open a pull request to the `main` branch
6. For bug fixes, leave a working sample in the `playground` package per its instructions

## Key documents

- [Development Setup](development-setup.md) — Clone, build, run tests
- [Testing Strategy](testing-strategy.md) — TestKit, mocks, integration tests
- [Extending GoAkt](extending.md) — Adding discovery, mailboxes, extensions
