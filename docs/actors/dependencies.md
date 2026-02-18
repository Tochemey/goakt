# Dependencies

Dependency injection allows you to attach runtime dependencies to actors at spawn time. This is particularly useful for testing, configuration, and resource management.

## Table of Contents

- 🤔 [What are Dependencies?](#what-are-dependencies)
- 💡 [Why Use Dependency Injection?](#why-use-dependency-injection)
- 🚀 [Basic Usage](#basic-usage)
- ✅ [Best Practices](#best-practices)
- 📋 [Summary](#summary)

---

## What are Dependencies?

A **Dependency** is a pluggable and serialisable interface that can be injected into an Actor to extend its functionality with custom or domain-specific behaviour. It provides a flexible mechanism for augmenting actors with capabilities beyond the core runtime.

## Why Use Dependency Injection?

Benefits include:
- **Testability**: Easy to inject mocks and test doubles
- **Flexibility**: Different configurations per environment
- **Isolation**: No global state or singletons
- **Explicit dependencies**: Clear what each actor needs
- **Resource management**: Shared resources across actors

## Basic Usage

- During the actor creation via `spawn` Pass dependencies with `WithDependencies(dep1, dep2, ...)` option.
- *In PreStart:* Call `ctx.Dependencies()`. This function will return the all the list of the actor's dependencies.
- Keeps initialization out of constructors and makes testing easy (inject mocks).

## Best Practices

### Do's ✅

1. **Use dependency injection for external resources**
2. **Inject mocks for testing**
3. **Share expensive resources** (connection pools, caches)
4. **Use typed dependencies** with helper functions
5. **Document required dependencies**. Optionally use a typed helper to centralize type-assertion and validation.

### Don'ts ❌

Don't use global variables; don't close shared resources in PostStop; don't ignore type-assertion errors; don't over-inject; don't create deps inside the actor (inject them at spawn).

## Summary

- **Dependencies** provide runtime values to actors
- **Inject** using `WithDependencies()` at spawn time
- **Access** via `ctx.Dependencies()` in PreStart
- **Use for** databases, clients, configs, mocks
- **Benefits**: testability, flexibility, isolation
- **Share** expensive resources across actors
- **Don't close** shared dependencies in PostStop
