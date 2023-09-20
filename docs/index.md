# Go-Akt

[![build](https://img.shields.io/github/actions/workflow/status/Tochemey/goakt/build.yml?branch=main)](https://github.com/Tochemey/goakt/actions/workflows/build.yml)
[![codecov](https://codecov.io/gh/Tochemey/goakt/branch/main/graph/badge.svg?token=J0p9MzwSRH)](https://codecov.io/gh/Tochemey/goakt)
[![Go Report Card](https://goreportcard.com/badge/github.com/tochemey/goakt)](https://goreportcard.com/report/github.com/tochemey/goakt)
[![GitHub release (with filter)](https://img.shields.io/github/v/release/tochemey/goakt)](https://github.com/Tochemey/goakt/releases)
[![GitHub tag (with filter)](https://img.shields.io/github/v/tag/tochemey/goakt)](https://github.com/Tochemey/goakt/tags)

Go-Akt is a distributed actor framework to build reactive and distributed system in golang using _**protocol buffers as actor messages**_.
GoAkt is highly scalable and available when running in cluster mode. It comes with the necessary features require to build a distributed actor-based system without sacrificing performance and reliability.

## Use Cases

- Event-Driven programming
- Event Sourcing and CQRS - [eGo](https://github.com/Tochemey/ego)
- Highly Available, Fault-Tolerant Distributed Systems
- Game Servers

## Installation

```bash
go get github.com/tochemey/goakt
```