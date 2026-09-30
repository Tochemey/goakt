# Issue 1402: grain options are lost on reactivation after passivation

https://github.com/Tochemey/goakt/issues/1402

Grain options (mailbox capacity, idle timeout, reentrancy, ...) belong to
the `GrainOf` call that activates the grain, not to the grain kind. Once
the grain passivated, the next bare send (`AskGrain` or `TellGrain` with
the identity) reactivates it with the package defaults: an unbounded
mailbox and the default idle timeout of two minutes.

The sample declares a different idle timeout for two grain kinds, a
short-lived and a long-lived one, activates one grain of each, lets the
short-lived one passivate, reactivates it with a bare send and checks that
each grain still follows its own kind's idle timeout: the short-lived one
passivates again, the long-lived one stays active.

## Actual (before the fix)

`WithGrainDefaultOptions` does not exist before the fix, so to run the
sample against `main` pass each kind's option to its `GrainOf` call
instead. The first activation follows the options, the reactivated grain
lives on for the package default:

```
first activation: the short-lived grain passivated, the long-lived grain is still active
FAIL: after passivation a bare send reactivated the short-lived grain with the package's idle timeout
```

## Expected (after the fix)

```
first activation: the short-lived grain passivated, the long-lived grain is still active
PASS: each grain kind keeps its own default options across a bare-send reactivation
```

## Run

```bash
go run ./playground/issue-1402
```

`TestGrainDefaults_ABareSendAfterPassivationKeepsTheKindsOptions` and
`TestGrainDefaults_ARemoteActivationKeepsTheKindsOptions` in `actor` cover
the local and the remote activation in the test suite.
