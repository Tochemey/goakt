# Unreleased

## 🔧 Fixes

- **Release passivated actor and grain references from the passivation heap** ([#1317](https://github.com/Tochemey/goakt/pull/1317)). Clear popped heap slots so deactivated participants are no longer retained by the heap backing array and can be reclaimed by the garbage collector.
