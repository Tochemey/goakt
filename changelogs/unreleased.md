# Unreleased

## 🔧 Fixes

- **Replies to stashed Asks are no longer dropped after `Unstash` under concurrent load** ([#1312](https://github.com/Tochemey/goakt/issues/1312)). `Stash` and `Unstash` clone the in-flight message into a pooled receive context without clearing the late-reply guard, so a recycled context silently dropped the reply sent with `Response` and the caller failed with `request timed out`. The clone path now clears the guard, and the Ask callers no longer write the guard on a context already handed to the target's mailbox, which could drop an unrelated request's reply.
