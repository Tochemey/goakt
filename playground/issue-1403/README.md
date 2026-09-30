# Issue 1403: cluster store path is hard-coded under the home directory

https://github.com/Tochemey/goakt/issues/1403

A cluster node keeps its peer state in a BoltDB file under
`$HOME/.goakt/cluster`, and nothing let a caller choose another place. A pod
with a read-only root filesystem, a test sandbox without a home directory
(Bazel), or any process whose home is not writable could not start a cluster
node at all.

The sample points `HOME` at a read-only directory, as such a pod sees it,
and starts a one-node cluster twice: once as before, and once with the store
directory set to a writable place.

## Actual (before the fix)

The first start fails and there is no way around it:

```
cluster: unable to create boltdb directory: mkdir /.../home/.goakt: permission denied
```

`WithStoreDir` does not exist before the fix, so the sample does not build
against `main`; the first half of it is what a caller was left with.

## Expected (after the fix)

```
as expected, the node cannot start with its store under a read-only home: cluster: unable to create boltdb directory: mkdir /.../home/.goakt: permission denied
PASS: the node started with its cluster store in the directory the caller chose
```

## Run

```bash
go run ./playground/issue-1403
```

Run it as a user that cannot write to a `0555` directory (not root), or the
first half cannot show the failure. `TestClusterConfig_WithStoreDirStartsWithoutAHomeDirectory`
in `actor` covers the same case in the test suite.
