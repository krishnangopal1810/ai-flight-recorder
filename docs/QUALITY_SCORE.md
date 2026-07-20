# Quality score

Verified by `make quality`. Grades are evidence-based and must link to deterministic checks.

| Domain | Grade | Evidence | Gap |
|---|---:|---|---|
| Harness | B | `make harness-check`, `make verify`, short `AGENTS.md`, structural/docs checks, isolated smoke artifacts, retained real-replay evidence | Remaining acceptance journeys are being added through the active completion audit |
| Capture | B | `make integration`, OTLP HTTP/gRPC conformance, partial acceptance, late-span assembly, policy, JCS/digest, retention and bundle tests | All GenAI/OpenInference kinds and representative p95 overhead still need end-to-end proof |
| Storage | B | `make integration`, shared SQLite/filesystem and PostgreSQL/S3 contract, idempotent writes, leases, refcounts, audit, atomic import | Crash/partial-write and compatibility proof remains incomplete |
| Replay | B | `make harness-check`, `make security`, real reference-agent substitution/no-fallback journey, preflight, credential allowlists, gateway/container controls | Live model routing, all-substituted LLM replay, and approved live sandbox remain unproven |
| Diff/evaluator/bisect | B | `make test`, named AFR regressions, ambiguity/confidence, evidence labels, repeated outcomes, monotonicity validation | Several AFR IDs currently prove lower-layer behavior rather than full staged journeys |
| SDKs | B | `make test`, Python and TypeScript suites, executable Python standard-OTLP reference agent, shared RFC 8785 fixture, exact matching, safe diagnostics | Executable equivalent TypeScript reference agent and incomplete-metadata journey remain unproven |
| Inspector | A- | `make e2e`, generated OpenAPI types, React unit test, retained DOM/trace/screenshot/video, and a live-stack smoke journey | Evaluator authoring and bisect remain CLI-first by design |
| Operations/releases | B | `make harness-check`, Compose, deterministic seed, backup/restore/failure runbook, tagged CLI/image/SBOM/signature workflow | Restore/upgrade/failure injection and signed release proof remain incomplete |
