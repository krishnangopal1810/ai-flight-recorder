# AI Flight Recorder agent map

This repository is designed for agent-first development. Start here, then open only the documents relevant to the task.

## Commands

- `make bootstrap` — install project dependencies.
- `make generate` — regenerate checked-in API and schema references.
- `make build` — build Go binaries, SDKs, and inspector.
- `make test` — run fast unit tests.
- `make integration` — run storage and API integration tests.
- `make e2e` — run deterministic product journeys.
- `make security` — run privacy and replay-isolation checks.
- `make docs-check` — validate the repository knowledge base.
- `make quality` — generate the domain quality report.
- `make verify` — run every blocking local check.
- `make dev-up` / `make dev-down` — start or stop a worktree-isolated development stack.

## Sources of truth

- Product behavior: [ai-flight-recorder-prd.md](ai-flight-recorder-prd.md)
- Architecture and dependencies: [ARCHITECTURE.md](ARCHITECTURE.md)
- Golden principles: [docs/design-docs/core-beliefs.md](docs/design-docs/core-beliefs.md)
- Security invariants: [docs/SECURITY.md](docs/SECURITY.md)
- Reliability behavior: [docs/RELIABILITY.md](docs/RELIABILITY.md)
- Active execution plan: [docs/exec-plans/active/v1.md](docs/exec-plans/active/v1.md)
- API/schema references: [docs/generated/index.md](docs/generated/index.md)
- Technical debt: [docs/exec-plans/tech-debt-tracker.md](docs/exec-plans/tech-debt-tracker.md)

## Architecture rules

Go dependencies flow `types -> config -> repository -> service -> runtime`. Cross-cutting infrastructure enters through `providers`. HTTP handlers, CLI commands, and UI code never access storage directly. Parse external shapes once at the boundary and pass typed values internally. The inspector uses the generated API client.

Run `make architecture-check` after moving or adding packages. Its errors include remediation guidance.

## Non-negotiable safety invariants

- Apply capture policy before durable storage, hashing, indexing, logs, metrics, or quarantine.
- Never silently fall through from a missing replay boundary to a live dependency.
- Unknown and mutating boundaries are substituted or blocked by default.
- Replays have no direct external network, production credentials, Docker socket, or host-writable mounts.
- Evidence, computed observations, and hypotheses are distinct types and UI labels.
- Capsule content never appears in operational telemetry.

## Change discipline

Reference an active execution plan and AFR requirement/test IDs in substantial PRs. Update behavior, tests, docs, generated references, and observability together. If an agent repeatedly fails, improve the harness rather than retrying an underspecified prompt. Keep PRs vertical and short-lived; never weaken a safety gate to merge faster.
