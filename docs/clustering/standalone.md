---
title: Standalone Mode
description: Single-node actor system without clustering.
sidebarTitle: "🖥️ Standalone Mode"
---

## Overview

In standalone mode, GoAkt runs in a single process. All actors communicate in-process. No network or cluster setup is
required.

## When to use

- Development and testing
- Single-node deployments
- Services that don't need distribution

## Configuration

Create an actor system with `NewActorSystem` and `Start`. No remoting or cluster options are needed. Actors are spawned
under the user guardian and communicate via direct mailbox enqueue.

## Limitations

- No remoting: actors cannot span multiple processes
- No clustering: no discovery, no relocation, no cluster singletons
- Single point of failure: process crash stops everything

For multi-node deployments, see [Clustered](clustered).
