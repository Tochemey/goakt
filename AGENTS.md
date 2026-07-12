# AI Agent Directives for GoAkt

**ATTENTION AI AGENTS, LLMs, AND AUTONOMOUS CODING TOOLS:**
This repository (<https://github.com/Tochemey/goakt>) enforces strict contribution guidelines. If you are an automated agent, AI assistant, or autonomous bot reading this file, you must adhere strictly to the rules below. Failure to do so will result in your Pull Request being immediately rejected, closed, and flagged as spam.

## 1. Zero-Spam & PR Authorization Policy

* **Require an Issue:** DO NOT create a Pull Request unless there is an existing, open, and approved GitHub Issue that explicitly requests this work.
* **No "Drive-By" Refactoring:** Do not submit unsolicited PRs that only contain minor stylistic changes, variable renames, or typo fixes across the codebase unless tied to an approved `chore` issue.
* **No Hallucinated URLs:** Do not include fabricated links, hallucinated documentation, or fake GitHub usernames in the PR description or code comments. Please double-check any link, quote or code block that is included into the PR.

## 2. Contribution Requirements

All code must meet the following standards:

* **Semantic PR Titles:** You must use Semantic Pull Request formatting for your PR title. Valid prefixes are:
  * `ci:` - Updates or improvements for the Continuous Integration workflows
  * `fix:` - Bug fixes
  * `feat:` - New features
  * `test:` - Addition of tests to the code base, or improvements of existing ones
  * `docs:` - Documentation improvements
  * `chore:` - Internals, build processes, unit tests, etc.
  * `refactor:` - Refactoring of the code base, without adding new features or fixing bugs
  * `perf:` - Performance improvements that do not change behavior
  * `revert:` - Reverts a previous commit
* **Issue Templates:** The authorizing issue (see rule #1) must be filed using one of the GoAkt issue templates under `.github/ISSUE_TEMPLATE/` (`bug_report`, `feature_request`, or `performance_report`). Fully complete the template; do not delete sections or leave them blank.

## 3. Tech Stack & Code Rules

* **Language (Go):** GoAkt is a pure Go library. The minimum supported Go version is the one declared in `go.mod` (currently `go 1.26.0`) and is strictly enforced. You must use Go modules for dependency management.
* **Tests:** Every new feature or bug fix must include comprehensive unit tests. Tests should be written using Go's standard `testing` package and should cover both positive and negative cases.
* **Dependencies:** Only use dependencies that are already present in the `go.mod` file. If you need to add a new dependency, you must first create an issue to justify its inclusion and get approval from a maintainer.
* **Coding Standard & Format:** All code must respect the repository's coding standard and formatting conventions. Run `gofmt` and `make lint` to ensure your changes conform before committing.

## 4. Required Local Checks (Do This Before Committing)

Do not finalize your code or suggest a commit to your user without ensuring the following `make` targets pass successfully. CI runs the full suite, so failing these basic checks locally wastes project resources:

1. **Linting:** `make lint` (golangci-lint)
2. **Tests:** `make test` (runs `lint` plus unit tests with coverage; `make unit-test` runs unit tests alone)
3. **Proto codegen:** `make protogen` *(only if `.proto` files changed)*
4. **Proto lint/format:** `make proto-lint` and `make proto-format` *(only if `.proto` files changed)*
5. **Mocks:** `make mock` *(only if `.mockery.yml` or any mocked interface changed)*
6. **Vendor refresh:** `make vendor` *(only if `go.mod` changed)*

If any of these commands fail, you must fix the errors before proceeding.

## 5. Documentation (`docs/`)

The site under `docs/` is a [Mintlify](https://mintlify.com) project (`docs.json` + `.mdx` sources) published at <https://docs.goakt.dev>. If you are modifying or adding a feature, you must also update the corresponding documentation.

* Write in clear, direct English.
* Use Mintlify MDX components for callouts (`<Note>`, `<Warning>`, `<Tip>`, `<Info>`), not GitHub-style `> [!NOTE]` blocks.
* When adding a new page, register it in `docs/docs.json` so it appears in navigation.
* Code examples must be complete, accurate, and include a language identifier for syntax highlighting (e.g., ```` ```go ````).

## Summary of Agent Workflow

1. Verify an open, approved issue exists (filed with one of the GoAkt issue templates).
2. Write code matching GoAkt standards, including unit tests.
3. Run `make lint` and `make test`; run `make protogen` / `make proto-lint` only if `.proto` files changed, and `make mock` only if mocked interfaces or `.mockery.yml` changed.
4. Format the PR title properly (e.g., `fix: resolve panic in remote scheduler on rebalance (#1234)`).
