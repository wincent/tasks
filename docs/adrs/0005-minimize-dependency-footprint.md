# ADR-0005: Minimize the dependency footprint

## Status

Accepted.

## Context

Every dependency adds code or tooling we have to trust, review, and keep up to date. Required native libraries and executables also make `tasks` harder to install and distribute. A small `go.mod` is useful, but it does not account for native libraries, subprocesses, or build and release tooling.

## Decision

Prefer the Go standard library and existing project code. For a small feature, prefer a simpler design or a small, testable implementation over a new dependency. Do not reimplement a complex standard merely to avoid an import. Add a dependency only when its benefit outweighs the security, maintenance, and installation costs of the direct dependency and its transitive dependencies. Obtain explicit maintainer approval before adding any dependency, including when an update introduces a new transitive dependency.

When proposing a dependency, record why existing code or a simpler feature is insufficient, what runs at build time or runtime, and what it adds to the dependency tree and supported platforms. Review the upstream source, license, and maintenance status. Keep feature-specific tools out of ordinary workflows: shared libgit2 is a core requirement for in-process Git operations, while Pi is required only for commands that launch it and the CLI can display Markdown without `glow`.

Pin Go modules to reviewed versions and commit their checksums. Review updates deliberately, monitor security advisories, and remove dependencies whose benefit no longer justifies their cost. Checksums verify the downloaded content, not whether that content is safe. Keep development and CI tooling separate from end-user prerequisites.

### Exception: Unicode-aware terminal headlines

Task numbers alone are hard to recognize when several runs finish around the same time. Showing the title makes run and merge headlines easier to identify, but a title must fit the available terminal columns. Counting bytes or runes does not measure display width: CJK characters can occupy two columns, and combining accents or joined emoji can form a single grapheme that should not be split. The standard library does not provide both terminal column widths and grapheme segmentation. We chose [`github.com/rivo/uniseg`](https://github.com/rivo/uniseg) over maintaining our own Unicode rules and tables or settling for incorrect truncation.

## Consequences

`uniseg` is pure Go with no transitive modules. Normal builds compile its source and checked-in tables without running its generators, and users have nothing extra to install. It is used for terminal presentation, not task storage or terminal control filtering. The trade-off is another upstream project to monitor and roughly 373 KiB in one measured macOS arm64 build compared with a rune-counting implementation, mostly Unicode tables. Compiling it into `tasks` still means trusting it with the CLI's privileges.
