# Unreleased

## Fixes

- **`ActorOf`, `ActorExists` and `Kill` no longer panic on an actor that is leaving the system.** A name lookup that found the actor's node just before the actor left the tree read a cleared PID and dereferenced it in `PID.IsStopping`, which ended the process with a nil pointer panic. It showed up when actors were stopped and looked up at the same time, for example while an application stopped. A cleared PID is now answered like any actor that is stopping: `ActorOf` and `Kill` return `ErrActorNotFound` and `ActorExists` returns `false`. ([#1447](https://github.com/Tochemey/goakt/pull/1447))
