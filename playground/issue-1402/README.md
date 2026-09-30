# Issue 1402: grain options are lost on reactivation after passivation

https://github.com/Tochemey/goakt/issues/1402

Grain options (mailbox capacity, idle timeout, reentrancy, ...) belong to
the `GrainOf` call that activates the grain, not to the grain kind. Once
the grain passivated, the next bare send (`AskGrain` or `TellGrain` with
the identity) reactivates it with the package defaults: an unbounded
mailbox and the default idle timeout.

The sample activates a grain with a mailbox of one and a short idle
timeout, checks that a third message is refused while the grain is busy,
lets the grain passivate, reactivates it with a bare send and checks the
same thing again.

## Actual (before the fix)

`WithGrainDefaults` does not exist before the fix, so to run the sample
against `main` pass the two options to `GrainOf` instead. The first
activation is bounded, the reactivation is not:

```
first activation: a third message is refused, the mailbox is bounded
FAIL: after passivation a bare send reactivated the grain with an unbounded mailbox
```

## Expected (after the fix)

```
first activation: a third message is refused, the mailbox is bounded
PASS: after passivation a bare send reactivated the grain with the kind's bounded mailbox
```

## Run

```bash
go run ./playground/issue-1402
```

`TestGrainDefaults_ABareSendAfterPassivationKeepsTheKindsOptions` and
`TestGrainDefaults_ARemoteActivationKeepsTheKindsOptions` in `actor` cover
the local and the remote activation in the test suite.
