# Dependencies

Dependency injection allows you to attach runtime dependencies to actors at spawn time. This is particularly useful for testing, configuration, and resource management.

## Table of Contents

- 🤔 [What are Dependencies?](#what-are-dependencies)
- 💡 [Why Use Dependency Injection?](#why-use-dependency-injection)
- 🚀 [Basic Usage](#basic-usage)
- 💡 [Example](#comprehensive-example)
- 📦 [Multiple Dependencies](#multiple-dependencies)
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

- **At spawn:** Pass dependencies with **actor.WithDependencies(dep1, dep2, ...)**.
- **In PreStart:** Call **ctx.Dependencies()** (returns a slice of `interface{}`); type-assert to your types (e.g. `deps[0].(*sql.DB)`) and store in the actor.
- Keeps initialization out of constructors and makes testing easy (inject mocks).

## Example (concept)

- Create a struct (e.g. `AppDependencies`) holding DB, HTTP client, config, cache; construct it in main or a setup function.
- Spawn with **WithDependencies(deps)**. In PreStart, **ctx.Dependencies()[0].(*AppDependencies)** and store; use deps in Receive.
- Do not close shared resources in PostStop. For tests, spawn with **WithDependencies(mockDB, mockCache)**.

## Multiple Dependencies

- Pass several deps: **WithDependencies(db, cache, logger)**.
- **ctx.Dependencies()** returns the slice in the same order; type-assert each (e.g. `deps[0].(*sql.DB)`) and assign to actor fields.

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