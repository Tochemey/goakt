---
title: Installation
description: Add GoAkt v4 to your Go module.
sidebarTitle: "📦 Installation"
---

## Requirements

- **Go 1.26 or higher** — GoAkt v4 requires Go 1.26+ for its type system and runtime features.

## Add the dependency

Add GoAkt v4 to your module:

```bash
go get github.com/tochemey/goakt/v4
```

The `v4` module path ensures you receive v4.x releases. The framework follows semantic versioning; patch releases within
v4 are backward compatible.

## Verify installation

Import the package and ensure it compiles:

```go
import "github.com/tochemey/goakt/v4/actor"
```

If your project builds without errors, the installation is complete.

## Next steps

- [First Actor](actor/first-actor) — Create your first actor and send messages
- [Migration from v3.x](migration-v3) — If upgrading from v3
