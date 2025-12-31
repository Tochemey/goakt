# Changelog

Changes since `v3.11.2`.

## Unreleased

### ✨ Features
- 🧭 `SpawnOn` now uses the system-wide default supervisor strategy configured via `WithDefaultSupervisor`.
- 🧭 Added `WithDefaultSupervisor` to configure the ActorSystem-wide default supervisor strategy.

### 🐛 Fixes
- 🧱 Grain activation flow revamped to prevent panics and duplicate activations.
- ♻️ Added recovery handling for Grain activation/deactivation failures.
- 🕒 `ScheduleOnce` now reliably triggers.
- 🧮 Actor count tracking fixed to avoid mismatch/underflow/overflow.

### ⚙️ Improvements & Refactors
- ⚖️ Cluster engine now emits topology change events resiliently when stable/healthy; added `WithClusterBalancerInterval` in `ClusterConfig`.
- 📦 Moved supervisor code into its own `supervisor` package (replace `actor` with `supervisor` in existing code).
- 🧵 Relocation avoids relocating child actors during the relocation process.
- 🧬 Relocation now preserves each actor's configured supervisor strategy.
- 🔁 Restart behavior revamped to restart the full child family tree.
- 🧰 Address and PID internals revamped; guardrails and ID utilities cleaned up.
- 🧵 Cluster singleton spawning made more resilient with stronger error handling.
- 🗄️ Cluster storage BoltDB file creation reworked.
- 📴 Reduced log noise during actor system shutdown.
- 🧹 General internal maintenance and cleanup.

### 🧪 Tests & Examples
- 🧷 Expanded test coverage across grains, remoting, cluster singletons, and supervision flows.
- 🧭 Added playground scenarios for regression coverage and issue reproduction (e.g., 1017, 1022, 1031, 1036).

### 📚 Documentation
- 📘 README updated with a full feature list.

### 📦 Dependencies
- ⬆️ Cluster engine dependency upgraded.
- 🔄 Routine dependency maintenance updates.
