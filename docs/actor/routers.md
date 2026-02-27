# Routers

## Concept

A **router** is an actor that fans out messages to a pool of routees using a routing strategy. You send to the router;
it forwards to one or more routees.

## Strategies

| Strategy       | Behavior                        |
|----------------|---------------------------------|
| **RoundRobin** | Rotate through routees in order |
| **Random**     | Pick a random routee            |
| **FanOut**     | Send to all routees             |

## When to use

- Load balancing across a pool of workers
- Parallel processing (fan-out)
- Scaling message throughput

## Configuration

Spawn a router with the desired strategy and routee count. The router manages the routee pool and applies the strategy
on each message.
