# V1 requirement evidence audit

This is the requirement-by-requirement completion ledger for the active V1 plan. “Partial” means code or a lower-layer test exists but does not yet prove the complete PRD acceptance condition. Requirements move to “proven” only after the linked deterministic evidence runs at the same scope as the requirement.

| Requirement | Current evidence | Status | Missing proof or behavior |
|---|---|---:|---|
| AFR-CAP-001 | `make integration`, capsule and OTLP tests | Partial | One opted-in full reference trace with LLM, retriever, MCP, tool, artifact, evaluation, and verified import |
| AFR-CAP-002 | `make test`, source identity tests | Partial | Every provenance component, explicit missing states, and comparison changes |
| AFR-CAP-003 | `make integration`, policy/capture tests | Partial | Full content classes, typed serialization/limit omissions, offline replay |
| AFR-CAP-004 | `make e2e`, bundle round trip | Partial | Compatible unknown-field preservation and atomic unsupported-major import |
| AFR-ING-001 | `make integration`, OTLP HTTP/gRPC agent fixture | Partial | All GenAI/OpenInference kinds, convention metadata, malformed mixed batches on both transports |
| AFR-ING-002 | `make test`, Python and TypeScript SDK suites | Partial | Executable equivalent reference agents exporting standard OTLP and incomplete diagnostics |
| AFR-ING-003 | `make test`, completeness unit tests | Partial | Boundary-by-boundary playback/replay/diff/bisect reports through API, CLI, and UI |
| AFR-SEC-001 | `make security`, local seeded-secret store scan | Partial | Hosted DB/object store, bundles, indexes, logs, metrics, traces, diagnostics, and quarantine scan |
| AFR-SEC-002 | architecture telemetry lint | Partial | Captured operational telemetry egress scan and exporter fail-closed behavior |
| AFR-SEC-003 | configuration and auth tests | Partial | Non-loopback refusal, native TLS, proxy TLS, unauthenticated hosted deployment journey |
| AFR-SEC-004 | repository retention tests | Partial | Project scheduling, all limit actions/classes, deletion audit, unsafe-accounting preservation |
| AFR-PLY-001 | playback service and inspector tests | Partial | Network-disabled proof of zero application processes and outbound requests with corrupt evidence |
| AFR-RPL-001 | replay coordinator unit tests | Partial | Real isolated reference entrypoint producing a linked candidate capsule and immutable plan |
| AFR-RPL-002 | substitution-envelope unit test | Partial | Offline non-model replay, declared live model route, all-substituted replay, unmatched no-fallback proof |
| AFR-RPL-003 | `make security`, uncached live OCI probe plus remote-daemon-safe volume/copy contract | Partial | Instrumented/uninstrumented DB, email, payment blocks and approved live sandbox |
| AFR-RPL-004 | outcome-classifier unit test | Partial | Three-or-more real trials with tolerances/evaluator through CLI and inspector |
| AFR-DIF-001 | synthetic diff unit tests | Partial | Insert/delete/move/ambiguity fixtures and layered fallback alignment |
| AFR-DIF-002 | synthetic AFR-T01–T03 tests | Partial | Curated prompt/retrieval/tool repositories with exact evidence and unknown-first-divergence case |
| AFR-DIF-003 | schemas and inspector labels | Partial | API/CLI/storage/UI separation plus hypothesis suppression/contradicting signals |
| AFR-EVL-001 | evaluator provider tests | Partial | Exported versioned evaluator failing source and passing fixed real replay outside the UI |
| AFR-CLI-001 | generated help and command unit paths | Partial | Account-free quickstart record/replay/diff under 30 minutes and every stable JSON error category |
| AFR-SVC-001 | Compose configuration and hosted contracts | Partial | Offline Docker deployment recording, playback, replay, diff, health/metrics, and durable failure acknowledgement |
| AFR-BIS-001 | bisect algorithm tests | Partial | Real Git fixture builds/replays every required revision, including flaky/non-monotonic/conditional cases |

## Acceptance scenarios

AFR-T01–T15 currently have queryable named regressions in `docs/ACCEPTANCE.md`. The rows above identify where those tests are narrower than their PRD scenarios. Each scenario will be promoted to proven only when its full journey, retained evidence, and exact reproduction command are linked here.
