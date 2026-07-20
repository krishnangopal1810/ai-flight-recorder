# Quality score

Verified by `make quality`. Grades are evidence-based and must link to deterministic checks.

| Domain | Grade | Evidence | Gap |
|---|---:|---|---|
| Harness | A | `make verify`, short `AGENTS.md`, structural/docs checks, worktree-derived stack and browser ports | External beta evidence remains intentionally separate |
| Capture | A | OTLP HTTP/gRPC conformance, partial acceptance, late-span assembly, policy, JCS/digest, retention and bundle tests | Representative p95 overhead awaits measured beta evidence |
| Storage | A- | Shared SQLite/filesystem and PostgreSQL/S3 contract, idempotent writes, leases, refcounts, audit, atomic import | Hosted contract runs in CI because the local OCI daemon is unavailable |
| Replay | A- | Preflight, exact boundary matching, credential allowlists, immutable builds, gateway/container controls, live CI isolation probe | Local live isolation evidence requires a running Docker/Podman daemon |
| Diff/evaluator/bisect | A | AFR-T01–T15, ambiguity/confidence, evidence labels, repeated outcomes, exhaustive monotonicity validation | Partner-repository bisect value is a beta metric |
| SDKs | A | Python 8 tests, TypeScript 7 tests, shared RFC 8785 fixture, kind/parent/occurrence matching, safe diagnostics | Broad framework auto-patching is deliberately out of V1 scope |
| Inspector | A | Generated OpenAPI types, React unit test and three Playwright playback/divergence/job journeys | Evaluator authoring and bisect remain CLI-first by design |
| Operations/releases | A- | Compose, deterministic seed, backup/restore/failure runbook, tagged CLI/image/SBOM/signature workflow | Release workflow receives its first production proof on the first approved tag |
