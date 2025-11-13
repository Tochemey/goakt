# Playground

A place to create minimal, runnable reproductions for GitHub issues and keep them as living samples once fixed. Each reproduction must compile, run, and clearly demonstrate the original failure and the verified fix.

## Goals
- Fast, deterministic repros for reported issues.
- Preserve working samples to prevent regressions.
- Make it easy to run, debug, and verify fixes **locally**.

## Folder layout
- One folder per issue: `issue-<number>`
  - Examples: `issue-1234`
- Keep all code and data needed to run the sample inside that folder.

Recommended structure inside an issue folder:
- `main.go`(use either a tiny main)
- `README.md` (context, steps, expected vs actual)

## Workflow
1. Create a new folder
   - `mkdir playground/issue-<number>`
2. Write a minimal reproduction
   - Favor simplest possible actor topology and smallest message set.
   - Avoid external dependencies unless the issue requires them.
3. Document the scenario
   - Describe expected vs actual behavior and how to observe it.
4. Verify the repro fails on current main
5. Fix the framework code outside of `playground/`
6. Update the sample to assert the fix
   - Keep it runnable and self-validating
7. Commit with a message referencing the issue (e.g., “fix: router panic (#1234)”)

## Running samples
- Run a main-based repro:
  - `go run ./playground/issue-<number>`

## Conventions
- Deterministic: seed any randomness; use short timeouts.
- Self-contained: no network/files unless essential. If needed, clean up resources.
- Small and focused: one behavior per repro.
- Clear logging: prefer concise logs; include minimal assertions.
- Cleanup: stop actors and await termination to avoid flakiness.
- Run all other playground issues folder to make sure your fix did not create any regression.

## Example

Minimal main.go template:
```go
// main.go
package main

import (
	"context"
	"fmt"

	"github.com/tochemey/goakt/v3/actor"
	"github.com/tochemey/goakt/v3/goaktpb"
	"github.com/tochemey/goakt/v3/log"
)

func main() {
	ctx := context.Background()

	fmt.Println("Starting actor system...")
	actorSystem, _ := actor.NewActorSystem("Test", actor.WithLogger(log.DiscardLogger))
	_ = actorSystem.Start(ctx)

	fmt.Println("\nSpawning actor...")
	pid, _ := actorSystem.Spawn(ctx, "actor1", &MyActor{}, actor.WithLongLived())

	fmt.Println("\nRestarting actor...")
	pid.Restart(ctx)

	fmt.Println("\nStopping actor system...")
	_ = actorSystem.Stop(ctx)
}

type MyActor struct{}

var _ actor.Actor = (*MyActor)(nil)

func (x *MyActor) PreStart(*actor.Context) error {
	fmt.Println("PreStart")
	return nil
}

func (x *MyActor) Receive(ctx *actor.ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
		fmt.Println("PostStart")
	default:
		ctx.Unhandled()
	}
}

func (x *MyActor) PostStop(*actor.Context) error {
	fmt.Println("PostStop")
	return nil
}
```

By keeping each sample minimal, deterministic, and runnable, we make regressions easier to catch and fixes easier to validate.
