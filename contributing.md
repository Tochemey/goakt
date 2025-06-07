# Contributions are welcome

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

### Making Contributions

1.  Create a new branch.
2.  Make your changes.
3.  Ensure your code adheres to the project's style and passes tests `earthly +test`.
4.  Commit your changes with a **Conventional Commit** message (e.g., `feat: add new awesome feature` or `fix: resolve a critical bug`).
5.  Push your branch to your forked repository.
6.  Submit a [pull request](https://help.github.com/articles/using-pull-requests) from your branch to the `main` branch of the original repository.

### Test & Linter

Prior to submitting a [pull request](https://help.github.com/articles/using-pull-requests), please run:

```bash
earthly +test
```
