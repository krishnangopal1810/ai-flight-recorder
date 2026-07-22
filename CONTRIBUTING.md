# Contributing to AI Flight Recorder

The best contribution starts with a failure that was difficult to reproduce. Tell us what the agent did, which dependency had moved by investigation time, and what evidence would have made the incident testable.

## Before you change code

1. Read the [agent map](AGENTS.md) and the relevant source of truth it links.
2. Check the [roadmap](ROADMAP.md) and open issues for overlapping work.
3. For a substantial change, open a focused issue before implementation and name the AFR requirement or acceptance scenario it advances.
4. Never weaken capture privacy, replay isolation, or evidence labeling to make a test pass.

Small documentation corrections and narrowly scoped `good first issue` changes do not need a design proposal.

## Make the change vertically

A complete change normally updates behavior, tests, documentation, generated contracts, and observability together. Keep pull requests short-lived and limited to one user-visible outcome.

```sh
make bootstrap
make test
make docs-check
```

Run `make architecture-check` after moving or adding packages. Run `make generate` after changing public CLI, API, or schema contracts. Before requesting review, run the narrowest relevant journey and then `make verify` when Docker and the full toolchain are available.

## Prove the safety claim

Changes touching capture or replay must retain these invariants:

- Capture policy runs before persistence, hashing, indexing, logs, metrics, or quarantine.
- A missing replay boundary fails closed instead of contacting a live dependency.
- Unknown and mutating boundaries are substituted or blocked by default.
- Replay containers receive no direct external network, production credentials, Docker socket, or host-writable mount.
- Evidence, observations, and hypotheses remain distinct.

Include the relevant AFR test IDs and retained evidence path in the pull request. See [Security](docs/SECURITY.md) and the [acceptance matrix](docs/ACCEPTANCE.md).

## Pull request checklist

- The change has one clear user outcome.
- Tests fail before the fix and pass after it when applicable.
- Public behavior and limitations are documented honestly.
- Generated files are current.
- No secret, customer content, or production identifier appears in fixtures, screenshots, logs, or history.
- The pull request explains how to reproduce the evidence.

By participating, you agree to the [Code of Conduct](CODE_OF_CONDUCT.md). Security vulnerabilities belong in a [private advisory](SECURITY.md), never a public issue.
