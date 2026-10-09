# Unreleased

## Fixes

- **`PostStart` is delivered once the actor is fully started, registry record included.** The turn that delivers `PostStart` was scheduled while the PID was being built, before the actor was attached to the tree and, in a cluster, before its registry record was written. Until v4.5.6 `PostStart` went through the mailbox and the synchronous registry write won that race in practice; since v4.6.0 it has its own slot and usually ran first, so a handler that asked `ActorSystem.Partition` for its own name got 0, and `ActorExists` or a lookup from another node did not find the actor yet. The turn is now scheduled after the tree attachment and the registry write, as a restart already did, so a `PostStart` handler can rely on the registry. The one exception is a message that reaches the actor through a local name lookup in the brief window between its tree attachment and the registry write: the turn it triggers runs `PostStart` before the record exists. A spawn whose publication fails does not schedule `PostStart`.

## Documentation

- **Migration guide and dispatcher tuning in the user docs.** The v3 to v4 migration guide moved from the maintainers' book to the [documentation site](https://docs.goakt.dev/reference/migration-v3-to-v4), merged with the v4.0.0 API changes page. Tuning `WithThroughputBudget` and `WithDispatcherPoolSize` is now covered on the [actor system page](https://docs.goakt.dev/actor/actor-system#tuning-the-dispatcher). The Dispatcher Pool and Context Pools pages were folded into the maintainers' book; their old URLs redirect.
