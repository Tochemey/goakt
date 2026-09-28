# Issue 1391: non-relocatable actor recovery during cluster convergence

Demonstration of the fix for [issue #1391](https://github.com/Tochemey/goakt/issues/1391), the follow-up to #1386.

## The issue

The reporter recovers named non-relocatable actors (`WithRelocationDisabled()`) after their owner node dies abruptly, from a durable source of truth, with this flow:

```text
ActorOf(name)
  -> found: send
  -> not found: SpawnOn(name)
  -> ErrActorAlreadyExists: ActorOf(name) again
```

After the #1386 fixes, [two scenarios](https://github.com/Tochemey/goakt/issues/1386#issuecomment-5875950438) still came up while the cluster converged after the crash.

### Scenario 1: a registry deadline that is not the caller's

A lookup returned `context.DeadlineExceeded` while the caller's own context was still alive:

```go
errors.Is(err, context.DeadlineExceeded) == true
ctx.Err() == nil
```

The timeout belonged to the registry's internal read, not to the operation deadline the caller supplied, and nothing in the error told the two apart.

### Scenario 2: `SpawnOn` contradicts `ActorOf`

`ActorOf` reported the departed owner's actor as not found, while `SpawnOn` for the same name returned `ErrActorAlreadyExists`, for about two seconds after the departure.

To get through both, the reporter reworked the flow:

```text
ActorOf(name)
  -> found: send
  -> not found / inconclusive registry deadline: SpawnOn(name)
  -> ErrActorAlreadyExists: resolve again
  -> if lookup now says not found: retry SpawnOn once
```

Before the fix, even that flow recovered a name only through its outer retry, 2.1 to 2.3 seconds after the departure: 41 lookup timeouts, none of them distinguishable from the caller's own deadline, and 239 `SpawnOn` conflicts that the next lookup contradicted, in one run of this sample.

### Why

Three things converge on their own clock after a crash. Membership drops the node at once. The registry's routing table drops it later (1.4 seconds in the runs behind this sample, while an in-flight push to the dead node timed out). And the registry refuses to **delete** a record while a copy of it lives on a node the routing table still lists, because a delete has no tombstone and the copy would come back. A spawn that met the dead node's claim deleted it first, so it failed until the routing table converged. Each refused delete also failed to release the name's lock, which is itself a delete, so the lock stayed held for its one-second lease and the next attempt failed on the lock instead.

## The fix

- **Scenario 1.** A registry read that runs out of its own timeout while the caller's context is still alive returns `ErrClusterRegistryTimeout`. It still matches `context.DeadlineExceeded`, so the reporter's check above keeps working, and a caller can now tell the registry's timeout from its own deadline.
- **Scenario 2.** A **write** does not have the delete's problem: the registry skips a departed replica and lets the write quorum decide, and a copy that comes back later loses to the newer record when it is merged. A spawn now writes its own record over the stale claim, fenced by the incarnation that claim carries, instead of deleting it first. A publication refused because another incarnation owns the name no longer takes the name's lock to find that out, so a refusal during the window leaves no lock behind.

## The flow after the fix

With both scenarios closed, a recovery flow needs neither the reporter's inconclusive-deadline test nor the second `SpawnOn`:

```text
ActorOf(name)
  -> found: send
  -> ErrClusterRegistryTimeout: the registry is converging, look the name up again
  -> ErrActorNotFound: SpawnOn(name)
       -> spawned: send
       -> ErrActorAlreadyExists: a live node took the name in between, ActorOf(name) and send
```

```go
import (
	"context"
	"errors"

	"github.com/tochemey/goakt/v4/actor"
	goakterrors "github.com/tochemey/goakt/v4/errors"
)

// wake returns the live actor named name, spawning it when no live node holds
// the name. A registry read that timed out while ctx is still alive is
// returned as it is: the lookup was inconclusive, so the caller retries it.
func wake(ctx context.Context, system actor.ActorSystem, name string, newActor func() actor.Actor) (*actor.PID, error) {
	pid, err := system.ActorOf(ctx, name)
	if err == nil {
		return pid, nil
	}

	if !errors.Is(err, goakterrors.ErrActorNotFound) {
		return nil, err // includes goakterrors.ErrClusterRegistryTimeout
	}

	pid, err = system.SpawnOn(ctx, name, newActor(), actor.WithRelocationDisabled())
	if err == nil {
		return pid, nil
	}

	if !errors.Is(err, goakterrors.ErrActorAlreadyExists) {
		return nil, err
	}

	return system.ActorOf(ctx, name)
}
```

The reporter's reworked flow keeps working unchanged; its extra steps no longer run. That flow is what the sample below runs.

## The sample

The sample runs two OS processes, because an abrupt crash cannot be simulated in-process. The parent process is the survivor, the child process (the same binary, selected through an environment variable) is the owner. Both join one cluster through static discovery with `WithReplicaCount(2)`, so the registry records survive the loss of the node that wrote them.

1. The owner spawns 64 actors with `WithRelocationDisabled()` and prints a ready line. The survivor resolves each of them remotely.
2. The survivor kills the owner with SIGKILL and waits until membership reports zero peers.
3. The survivor recovers all 64 names at once with the reporter's reworked flow. A call of the flow that fails is made again after 100 ms, the way a caller retrying delivery would, for up to 45 seconds per name. Every lookup timeout under a live caller context is counted, with whether it carries `ErrClusterRegistryTimeout` (scenario 1), and so is every `SpawnOn` conflict that the next lookup contradicts (scenario 2).
4. The survivor then checks that every name resolves to a non-relocatable actor on this node.

Run it with:

```bash
go run ./playground/issue-1391
```

Set `GOAKT_ISSUE_1391_VERBOSE=1` to have the survivor log at debug level to stderr.

## What it shows

```text
owner before crash: 64 non-relocatable actors, each resolved remotely by the survivor
owner process killed; survivor membership now reports zero peers
recovered 64/64 names with the reporter's flow in 273ms
calls of the flow made again after a failure: 0 (none)
names resolving to a non-relocatable actor on the survivor after the recovery: 64/64
registry read timeouts under a live caller context: 3, carrying ErrClusterRegistryTimeout: 3
SpawnOn reported a name as taken while ActorOf reported it free: 0
```

- Every name is recovered on the first call of the flow, within 300 ms of the departure, and resolves on the survivor afterwards.
- **Scenario 1.** The registry read timeouts still happen while the cluster converges, and every one of them carries `ErrClusterRegistryTimeout`.
- **Scenario 2.** `SpawnOn` never contradicts the lookup.

`SpawnOn` hands back the actor in the form its placement returned: with no other node left, the round-robin placement lands on the survivor itself through the remote path, so the returned reference reads as remote while the actor runs on this node; sending to it works either way, and `ActorOf` returns the local actor from then on.

The program exits with status 0. `SETUP FAILURE` with status 2 means the setup itself failed (ports, process start, membership never converging); rerun it in that case.
