# Development Setup

## Clone and prepare

```bash
git clone https://github.com/your-username/goakt.git
cd goakt
go mod tidy
```

## Build and test

We use [Earthly](https://earthly.dev/get-earthly) for reproducible builds:

```bash
earthly +test
```

This runs the full test suite in a consistent environment. Run it before submitting a pull request.

## Local Go commands

You can also use standard Go commands:

```bash
go build ./...
go test ./...
```

Tests that require infrastructure (Consul, etcd) use testcontainers and need Docker.
