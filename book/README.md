# GoAkt from the Inside

A book that explains how GoAkt is implemented: its data structures, algorithms, concurrency and invariants, at a level of detail the user documentation does not cover. It is for maintainers and contributors. The user documentation is in [`docs/`](../docs) and is published at <https://docs.goakt.dev>.

## Start here

- [Architecture](architecture.md): the whole system on a few pages, its main data flows and the reasons behind its design. Read it before the chapters; it points to the chapter that covers each part in depth.
- [Chapters](#chapters): one subsystem each, in reading order.
- [Migrating from v3 to v4](migration/v3-to-v4.md): what changed for users between the two major versions.

## How the book is written

- **The code is the source.** Every chapter describes what the code does at the commit named under its title.
- **Code is referred to by name, not by line.** A statement names the function, method, type or constant it rests on, and the file: `PID.tryPassivation` in `actor/pid.go`. Names survive edits that move lines.
- **A guarantee names its test.** Something is called a guarantee only when a test in the repository enforces it, and the guarantee names that test function and its file. Anything else is listed under "Implementation details (may change)" or "Behaviours to know".
- **Rationale comes from the maintainers.** The reasons for a design are taken from code comments and from the maintainers. They are not guessed.

Every chapter starts with a table of contents and ends with exercises that can be answered from the chapter.

A chapter is marked **verified** when every statement in it has been checked against the code at the state named under its title, and every guarantee against the test it names. When the code changes, the chapters that describe the changed code are checked again before their status is kept.

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
| chap-13 – chap-14 | Grains | planned |
| [chap-15](chapters/chap-15.md) | Remoting: the Transport | verified |
| chap-16 – chap-18 | Remote Client, Remote Server, TLS and the Standalone Client | planned |
| chap-19 – chap-22 | Clustering | planned |
| [chap-23](chapters/chap-23.md) | Reliable Delivery | verified |
| [chap-24](chapters/chap-24.md) | Distributed Data (CRDTs) | verified |
| [chap-25](chapters/chap-25.md) | Streams | verified |
| chap-26 | Circuit Breaker, Memory, Testkit | planned |
| chap-27 – chap-29 | Failure Modes, Testing GoAkt, Exercises | planned |
