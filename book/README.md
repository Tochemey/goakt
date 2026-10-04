# GoAkt from the Inside

A book that explains how GoAkt is implemented: its data structures, algorithms, concurrency and invariants, at a level of detail the user documentation does not cover. It is for maintainers and contributors. The user documentation is in [`docs/`](../docs) and is published at <https://docs.goakt.dev>.

## Start here

- [Architecture](architecture.md): the whole system on a few pages, its main data flows and the reasons behind its design. Read it before the chapters; it points to the chapter that covers each part in depth.
- [Chapters](#chapters): one subsystem each, in reading order.
- [Migrating from v3 to v4](migration/v3-to-v4.md): what changed for users between the two major versions.

## How the book is written

- **The code is the source.** Every chapter describes what the code does.
- **Code is referred to by name, not by line.** A statement names the function, method, type or constant it rests on, and the file: `PID.tryPassivation` in `actor/pid.go`. Names survive edits that move lines.
- **A guarantee names its test.** Something is called a guarantee only when a test in the repository enforces it, and the guarantee names that test function and its file. Anything else is listed under "Implementation details (may change)" or "Behaviours to know".
- **Rationale comes from the maintainers.** The reasons for a design are taken from code comments and from the maintainers. They are not guessed.

Every chapter starts with a table of contents.

A chapter is verified as it is written: every statement in it is checked against the code, and every guarantee against the test it names, before it is marked **verified**. When the code changes, the chapters that describe the changed code are checked again before their status is kept.

## Keeping the book true

The book goes stale in two ways, and each has its defence.

**A name it cites is renamed or removed.** `make book-check` runs four checks, and the `book` job of the pull-request workflow (`.github/workflows/pr.yml`) runs the same four on every pull request that changes the book or any Go file:

| Check | What it fails on |
|---|---|
| `book/tools/checknames.py` | a "`Name` in `path/file.go`" whose name is not declared in that file (tests named under Guarantees included), or a cited path that does not exist |
| `book/tools/checklinks.py` | a link to a missing page or heading |
| `book/tools/linkrefs.py --check` | a "Chapter N" or "§N.k" reference that is not linked, or that names a missing chapter or section |
| `book/tools/mermaid-check.mjs` | a Mermaid diagram that does not parse |

**The code changes behaviour under the same names.** No check catches this, so the review must. `make book-affected` (with `BASE=<revision>`, default `origin/main`) lists the chapters that cite a file changed since that revision; the `book` job writes the same list to the pull request's job summary. Reread each listed chapter against the change.

Two scripts help when editing: `python3 book/tools/linkrefs.py` turns new chapter and section references into links, and `python3 book/tools/toc.py` regenerates every chapter's `## Contents` list.

## Chapters

| Chapter | Title | Status |
|---|---|---|
| [chap-01](chapters/chap-01.md) | What GoAkt Is | verified |
| [chap-02](chapters/chap-02.md) | Building, Running and Testing the Code | verified |
| [chap-03](chapters/chap-03.md) | The Actor System | verified |
| [chap-04](chapters/chap-04.md) | Spawning and the PID | verified |
| [chap-05](chapters/chap-05.md) | Messaging | verified |
| [chap-06](chapters/chap-06.md) | Mailboxes | verified |
| [chap-07](chapters/chap-07.md) | Dispatch | verified |
| [chap-08](chapters/chap-08.md) | The Receive Context | verified |
| [chap-09](chapters/chap-09.md) | Supervision and Death Watch | verified |
| [chap-10](chapters/chap-10.md) | Passivation and Eviction | verified |
| [chap-11](chapters/chap-11.md) | Scheduling, Routers, Event Stream, Pub/Sub | verified |
| [chap-12](chapters/chap-12.md) | Extensions, Dependencies, Observability, Logging | verified |
| [chap-13](chapters/chap-13.md) | Grains: Model, Identity and Activation | verified |
| [chap-14](chapters/chap-14.md) | Grains: the Runtime | verified |
| [chap-15](chapters/chap-15.md) | Remoting: the Transport | verified |
| [chap-16](chapters/chap-16.md) | Remoting: the Remote Client | verified |
| [chap-17](chapters/chap-17.md) | Remoting: the Remote Server and the `remote` Package | verified |
| [chap-18](chapters/chap-18.md) | TLS and the Standalone Client | verified |
| [chap-19](chapters/chap-19.md) | Clustering: Membership and Discovery | verified |
| [chap-20](chapters/chap-20.md) | Clustering: the Cluster Core | verified |
| [chap-21](chapters/chap-21.md) | Clustering: Placement, Singletons and Relocation | verified |
| [chap-22](chapters/chap-22.md) | Multi-Datacenter | verified |
| [chap-23](chapters/chap-23.md) | Reliable Delivery | verified |
| [chap-24](chapters/chap-24.md) | Distributed Data (CRDTs) | verified |
| [chap-25](chapters/chap-25.md) | Streams | verified |
| [chap-26](chapters/chap-26.md) | Circuit Breaker, Memory, Testkit | verified |
| [chap-27](chapters/chap-27.md) | Testing GoAkt | verified |
