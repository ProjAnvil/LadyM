# Changelog

All notable changes to LadyM are documented in this file. The structure follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and releases use
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.10.0] - 2026-11-09

The `v0.9.0` tag was reserved during release validation but was not published as
a GitHub Release. It is retained unchanged; `v0.10.0` is the next available minor.

### Added

- Typed, evidence-backed claims with caller-attested verification, revision-based
  correction and rejection, source fingerprints, scopes and expiration.
- Opt-in evidence archival with best-effort credential redaction and atomic
  per-entry and per-workspace storage limits.
- Explicit source-grounded topic views, hybrid lexical/vector/graph retrieval,
  bounded bootstrap context and expiring task state.
- Opt-in persistent maintenance jobs with deduplication, fenced leases, crash
  recovery, cancellable provider calls and explicit byte/call/time budgets.
- Embedding identity checks and generation-fenced, resumable migrations with
  atomic activation, rollback, finalization and local recovery commands.
- Canonical capture identities, scoped source provenance and new HTTP, MCP, CLI,
  Go, Python, TypeScript and LangGraph integration surfaces.
- Offline learning/navigation evaluations and provenance-aware retrieval metrics.

### Changed

- Apply workspace, eligibility and type filters before retrieval truncation;
  refresh SQLite vector indexes after changes committed by other connections.
- Publish derived memories, provenance, retirement and progress atomically.
  Recursively invalidate dependent knowledge when sources change or disappear.
- Offline consolidation adds distinct facts rather than overwriting them based
  only on similarity; exact eligible duplicates remain `NOOP`.
- Recompute retrieval sufficiency after deep expansion and bound graph candidates.
- Retain LangGraph Q/A as observed episodic events rather than verified facts.
- Require explicit model, question limit and wall budget for live LLM evaluation.

### Fixed

- Concurrent API/worker initialization on an empty database could incorrectly
  report `embedding fingerprint mismatch` or start unnecessary migrations.
- Contextual PostgreSQL transactions could miss cancellation; contextual Engine
  views could lose shared working-memory buffers.
- Automatic migration finalization and resume could conflict with later
  generations or explicit rollback policy.
- Resource, topic and task identity collisions, stale fallback retrieval,
  duplicated provenance traversal and nullable Python claim conditions.
- Live-answer evaluation timeouts did not enforce a total in-flight wall deadline.
- Canonical identity test fixtures used an invalid Git discovery sentinel that
  was not portable across runner Git versions.

### Upgrade notes

Back up existing databases before upgrading. Existing databases with unidentified
vectors may require explicit embedding identity adoption or migration. New
`storage.Store` methods require updates to custom Store implementations.
Archiving, canonical capture and budgeted maintenance remain opt-in. Verification
is caller-attested, not independent tool execution or model self-certification.

Read the [full release notes](docs/releases/v0.10.0.md) before upgrading, including
the rollback-window write freeze and downgrade limitations.

Earlier release history is available in [GitHub Releases].

[Unreleased]: https://github.com/ProjAnvil/LadyM/compare/v0.10.0...HEAD
[0.10.0]: https://github.com/ProjAnvil/LadyM/compare/v0.8.4...v0.10.0
[GitHub Releases]: https://github.com/ProjAnvil/LadyM/releases
