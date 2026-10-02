# Issue 1408: nodes that miss each other at boot stay separate clusters

Sample for [issue #1408](https://github.com/Tochemey/goakt/issues/1408). It shows no defect in GoAkt. It shows what a discovery provider has to guarantee so that nodes booting at the same moment end up in one cluster.

## What happens at boot

A node uses its discovery provider when it boots: it starts listening for membership gossip, calls `Register`, then calls `DiscoverPeers` and joins the nodes it returns.

| `DiscoverPeers` returns                            | What the node does                                             |
|----------------------------------------------------|----------------------------------------------------------------|
| A list with at least one reachable node            | Joins it.                                                      |
| An empty list, or a list with only the node itself | Takes it as "I am alone" and forms a cluster of one.           |
| An error                                           | Retries once a second, ten times, then forms a cluster of one. |

With the default minimum peers quorum of 1 the node does not query the provider again after that. Two clusters that formed separately never merge, and every grain identity can then be active once per cluster.

## The two rules

Nodes that boot at the same moment form one cluster when the provider guarantees both of these:

1. **A node is visible before it reads.** Once `Register` has returned on a node, `DiscoverPeers` on the other nodes lists it.
2. **Reads are consistent.** `DiscoverPeers` returns every node whose `Register` returned before the call.

With both rules two nodes cannot both miss each other: if node A reads before node B is visible, node B reads after node A became visible, and joins it.

A provider that cannot guarantee both has to return an error while it lists no other node, so the join is retried. The sample does that with `requirePeers`, a wrapper of about twenty lines around any provider.

## Scenarios

Every scenario boots three nodes at the same moment with a quorum of 1, gives them ten seconds to find each other, and hits the counter grain `user-1` once through each node. One cluster answers `1, 2, 3`. Three clusters of one answer `1, 1, 1`, since each has its own activation.

The custom providers read from an in-memory registry. A node is added by `Register` and marked ready once its actor system has started, the way a readiness probe would.

| # | Provider                                             | Rules                   | Expected                               |
|---|------------------------------------------------------|-------------------------|----------------------------------------|
| 1 | The built-in NATS provider                           | both hold               | one cluster                            |
| 2 | Custom, lists every registered node                  | both hold               | one cluster                            |
| 3 | Custom, lists a node only once it is ready           | breaks rule 1           | three clusters                         |
| 4 | Custom, lists a node two seconds after it registered | breaks rule 2           | three clusters                         |
| 5 | The provider of scenario 4 wrapped in `requirePeers` | repaired by the retries | one cluster                            |
| 6 | The provider of scenario 3 wrapped in `requirePeers` | still breaks rule 1     | three clusters, each after ten seconds |

Scenario 6 is the limit of the wrapper: no node becomes ready while it is still retrying, so each one waits out the ten seconds and boots alone. Such a provider has to list nodes that are still booting.

Run it with:

```bash
go run ./playground/issue-1408
```

It takes about one minute. The sample exits with status 0 when every scenario ended as expected, with status 1 when one did not, and with status 2 when a node could not be set up.

## Output

```text
scenario 1: the built-in NATS provider
  the nodes booted in 1.1s
  clusters=1, user-1 answered [1 2 3] through the three nodes
  OK: 1 cluster(s), 1 activation(s) of the grain, as expected

scenario 2: a custom provider that follows both rules
  the nodes booted in 100ms
  clusters=1, user-1 answered [1 2 3] through the three nodes
  OK: 1 cluster(s), 1 activation(s) of the grain, as expected

scenario 3: a custom provider that lists a node only once it is ready
  the nodes booted in 100ms
  clusters=3, user-1 answered [1 1 1] through the three nodes
  OK: 3 cluster(s), 3 activation(s) of the grain, as expected

scenario 4: a custom provider whose view lags behind the registrations
  the nodes booted in 100ms
  clusters=3, user-1 answered [1 1 1] through the three nodes
  OK: 3 cluster(s), 3 activation(s) of the grain, as expected

scenario 5: the lagging provider wrapped in requirePeers
  the nodes booted in 3.2s
  clusters=1, user-1 answered [1 2 3] through the three nodes
  OK: 1 cluster(s), 1 activation(s) of the grain, as expected

scenario 6: the ready-only provider wrapped in requirePeers
  the nodes booted in 10.1s
  clusters=3, user-1 answered [1 1 1] through the three nodes
  OK: 3 cluster(s), 3 activation(s) of the grain, as expected

PASS: nodes that boot together form one cluster when the provider follows both rules or fails while it sees no peer
```

## What to do in a deployment

- Use a provider that follows both rules, or wrap it so it fails while it lists no other node. A node that is really alone then boots after ten seconds.
- Or set `WithMinimumPeersQuorum` to 2 or more. A node then serves nothing until it has that many members and queries the provider again every five seconds while it is below the quorum. A single node cannot start with that setting.

The documentation is in `docs/clustering/service-discovery.mdx`, section "Discovery happens at boot".
