# Public roadmap

AI Flight Recorder is working toward one promise: turn a production agent failure into a safe, controlled experiment that reveals the first meaningful divergence.

The roadmap is ordered by evidence, not calendar promises. Items move only when their acceptance journey and safety proof exist.

## Public alpha — prove the loop

- Docker-only refund incident from failed capture to successful comparison in under five minutes.
- Signed service, demo, and agent images with checksums and SBOMs.
- Onboarding evidence from unfamiliar production agent engineers.
- Public incident stories that identify missing capture or boundary integrations.
- Stable JSON output for demo automation and support diagnostics.

## Beta — prove it on other people's failures

- Pass the measured reproduction, diagnosis-time, divergence-precision, integration-time, side-effect, and overhead gates in [`docs/beta-gates.json`](docs/beta-gates.json).
- Validate Python and TypeScript package onboarding before publishing registry packages.
- Expand real framework and boundary examples from design-partner evidence.
- Complete restore, migration, performance, and long-running operational proof.
- Promote evaluator and revision-search journeys from experimental to evidence-backed.

## Later — widen the controlled world

- More language SDKs and framework adapters.
- Team workflows for reviewed incident capsules and reusable regression libraries.
- Richer alignment for inserted, removed, moved, and ambiguous operations.
- Deployment packaging beyond Docker after alpha onboarding data.

## Good first investigations

These are deliberately bounded contributions. Open an issue referencing the item before starting so a maintainer can confirm nobody else is working on it.

### Add a capture recipe for one agent framework

Choose one open-source framework, record a tiny tool-using agent through standard OTLP, and document the exact resource attributes and boundary placement. Acceptance: source under `examples/`, no network in tests, and a deterministic capsule assertion.

### Add Windows documentation for the demo launcher

Research Docker Desktop's named-pipe/socket constraints without weakening isolation. Acceptance: a tested PowerShell command or an explicit, evidence-backed unsupported note; no speculative instructions.

### Improve README media provenance checks

Extend the README checker to validate GIF duration and dimensions using a portable mechanism. Acceptance: deterministic tests for too-short, oversized, and missing static fallback cases.

### Add an incident-story documentation example

Write a sanitized example that separates immutable evidence, computed observations, and an unvalidated hypothesis. Acceptance: no real identifiers or secrets, under 500 words, and linked from the community guide.

## How priorities change

Concrete incident patterns outrank feature polls. Share yours in the **Incident Stories** Discussion category or use the incident issue form when there is a specific product gap. Security and data-loss work always preempts roadmap order.
