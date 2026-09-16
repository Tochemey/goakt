# Unreleased

## 📦 Dependencies

- **Cluster engine upgraded to `github.com/tochemey/olric` v0.3.21.** The engine now builds on `hashicorp/memberlist` v0.7.0, which reports its metrics through `github.com/hashicorp/go-metrics` directly, so the deprecated `github.com/armon/go-metrics` fallback has left the dependency graph. The `exclude` block that kept `go mod tidy` away from its renamed tags is gone from `go.mod`, and the `hashicorpmetrics` build tag that CI, lint, and the unit-test script set to bypass the fallback is no longer needed and has been removed.
