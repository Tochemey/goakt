# Contributions are welcome

The project adheres to [Semantic Versioning](https://semver.org) and [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/).
This repo uses [Earthly](https://earthly.dev/get-earthly).

There are two ways you can become a contributor:

1. Request to become a collaborator and then you can just open pull requests against the repository without forking it.
2. Follow these steps
   - Fork the repository
   - Create a feature branch
   - Set your docker credentials on your fork using the following secret names: `DOCKERHUB_USERNAME` and `DOCKERHUB_TOKEN`
   - Submit a [pull request](https://help.github.com/articles/using-pull-requests)

## Test & Linter

Prior to submitting a [pull request](https://help.github.com/articles/using-pull-requests), please run:

```bash
earthly +test
```
