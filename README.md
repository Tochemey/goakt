# GoAkt

[![build](https://img.shields.io/github/actions/workflow/status/Tochemey/goakt/build.yml?branch=main)](https://github.com/Tochemey/goakt/actions/workflows/build.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/tochemey/goakt.svg)](https://pkg.go.dev/github.com/tochemey/goakt)
[![Go Report Card](https://goreportcard.com/badge/github.com/tochemey/goakt)](https://goreportcard.com/report/github.com/tochemey/goakt)
[![codecov](https://codecov.io/gh/Tochemey/goakt/graph/badge.svg?token=J0p9MzwSRH)](https://codecov.io/gh/Tochemey/goakt)
[![GitHub go.mod Go version](https://img.shields.io/github/go-mod/go-version/Tochemey/goakt)](https://go.dev/doc/install)
[![Awesome](https://awesome.re/badge.svg)](https://github.com/avelino/awesome-go)
[![OpenSSF Best Practices](https://www.bestpractices.dev/projects/9248/badge)](https://www.bestpractices.dev/projects/9248)



Distributed [Go](https://go.dev/) actor framework to build a reactive and distributed system in golang using
_**protocol buffers**_ as actor messages.

GoAkt is highly scalable and available when running in cluster mode. It comes with the necessary features require to
build a distributed actor-based system without sacrificing performance and reliability. With GoAkt, you can instantly create a fast, scalable, distributed system
across a cluster of computers.

If you are not familiar with the actor model, the blog post from Brian Storti [here](https://www.brianstorti.com/the-actor-model/) is an excellent and short introduction to the actor model.
Also, check the reference section at the end of the post for more material regarding the actor model.

## 💻 Installation
```shell
go get github.com/tochemey/goakt/v3
```

## 📚 Documentation

The complete Documentation can be found [here](https://tochemey.gitbook.io/goakt)

## 📝 Examples

Kindly check out the [examples](https://github.com/Tochemey/goakt-examples)' repository.

## 💪 Support

GoAkt is free and open source. If you need priority support on complex topics or request new features, please consider [sponsorship](https://github.com/sponsors/Tochemey).

## 🔒 Security

Applications using this library should routinely upgrade their **Go version** and refresh **dependencies** as needed to mitigate security vulnerabilities. **GoAkt** will make a best effort to keep dependencies current and perform vulnerability checks whenever necessary.

## 🌍 Community

You can join these groups and chat to discuss and ask GoAkt related questions on:

[![GitHub Discussions](https://img.shields.io/github/discussions/Tochemey/goakt)](https://github.com/Tochemey/goakt/discussions)
[![GitHub Issues or Pull Requests](https://img.shields.io/github/issues/Tochemey/goakt)](https://github.com/Tochemey/goakt/issues)

## 🤝 Contribution

We welcome contributions! Whether you're fixing a bug, adding a new feature, or improving documentation, your help is appreciated.
This project adheres to [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/) to standardize commit messages and help automate releases.
We use [Earthly](https://earthly.dev/get-earthly) for reproducible builds.

### Prerequisites

Before you start, make sure you have these installed:

* [Docker](https://docs.docker.com/get-started/get-docker/)
* [Go](https://go.dev/doc/install)

### Getting Started

1.  Fork the repository to your GitHub account.
2.  Clone your forked repository to your local machine:
    ```bash
    git clone https://github.com/your-username/goakt.git
    cd goakt
    ```
3.  Initialize and tidy up Go modules:
    ```bash
    go mod tidy
    ```
    This ensures all dependencies are correctly listed and downloaded.

### How to Contribute

1. Make your changes in your fork.
2. Ensure your code adheres to the project's style and passes tests `earthly +test`.
3. Commit your changes using a **Conventional Commit** message. See [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/)
4. Submit a [pull request](https://help.github.com/articles/using-pull-requests) from your fork to the `main` branch of the original repository.
5. Following the instructions in the `playground` package to leave a working sample code in case it is a bug you are fixing.

### Test & Linter

Prior to submitting a [pull request](https://help.github.com/articles/using-pull-requests), please run:

```bash
earthly +test
```

## 📦 In Production

This framework is used in production by the following projects/companies:

- [Baki Money](https://www.baki.money/): AI-powered Expense Tracking platform that turns receipts into stories you can actually understand.


## 💬 Feedback

Kindly use this [issue](https://github.com/Tochemey/goakt/issues/948) to give us your feedback that can help us enhance the framework.

## 📊 Benchmark

One can find the benchmark tests here: [Benchmark](./bench/)

## 💰 Sponsors

<!-- sponsors --><a href="https://github.com/andrew-werdna"><img src="https:&#x2F;&#x2F;github.com&#x2F;andrew-werdna.png" width="60px" alt="User avatar: Andrew Brown" /></a><!-- sponsors -->