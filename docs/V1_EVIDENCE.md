# V1 requirement evidence audit

This is the requirement-by-requirement completion ledger for the active V1 plan. “Partial” means code or a lower-layer test exists but does not yet prove the complete PRD acceptance condition. Requirements move to “proven” only after the linked deterministic evidence runs at the same scope as the requirement.

| Requirement | Current evidence | Status | Missing proof or behavior |
|---|---|---:|---|
| AFR-CAP-001 | `make integration` sends one opted-in reference trace through OTLP/HTTP and gRPC with agent, prompt, retriever, LLM, MCP, tool, and evaluator operations; it proves descendant payloads, four boundaries, an artifact, policy evidence, evaluator payload references, identical transport-independent identity, and verified bundle import | Proven | — |
| AFR-CAP-002 | `make test` proves OTLP decoding and equivalent Python/TypeScript emission for source, commit, dirty worktree, build/container, runtime, dependency-lock, feature-flag, environment, and entrypoint facts; a table test mutates every identity/provenance/model component and requires a changed capsule root plus exact diff evidence; absent or invalid facts are named without fabricated values | Proven | — |
| AFR-CAP-003 | `make integration`, policy/capture tests | Partial | Full content classes, typed serialization/limit omissions, offline replay |
| AFR-CAP-004 | `make e2e` proves deterministic compatible-minor bundle round trips, nested unknown-field preservation, identity-stable offline playback, and atomic unsupported-major rejection with no metadata or blobs; `make integration` applies the same preservation contract to SQLite/filesystem and PostgreSQL/S3-compatible storage | Proven | — |
| AFR-ING-001 | `make integration` covers OpenInference agent, prompt, retriever, LLM, tool, and evaluator kinds, the Flight MCP extension, OTel GenAI LLM metadata, producer schema URLs, Python/TypeScript convention parity, and mixed valid/malformed partial success over both OTLP/HTTP and gRPC | Proven | — |
| AFR-ING-002 | `make harness-check` executes equivalent Python and TypeScript standard-OTLP agents, compares their logical capsules, replays both through their native SDKs, and proves a TypeScript metadata failure remains payload-free and nonfatal while marking replay incomplete | Proven | — |
| AFR-ING-003 | `make test`, completeness unit tests | Partial | Boundary-by-boundary playback/replay/diff/bisect reports through API, CLI, and UI |
| AFR-SEC-001 | `make security`, local seeded-secret store scan | Partial | Hosted DB/object store, bundles, indexes, logs, metrics, traces, diagnostics, and quarantine scan |
| AFR-SEC-002 | architecture telemetry lint | Partial | Captured operational telemetry egress scan and exporter fail-closed behavior |
| AFR-SEC-003 | configuration and auth tests | Partial | Non-loopback refusal, native TLS, proxy TLS, unauthenticated hosted deployment journey |
| AFR-SEC-004 | repository retention tests | Partial | Project scheduling, all limit actions/classes, deletion audit, unsafe-accounting preservation |
| AFR-PLY-001 | playback service and inspector tests | Partial | Network-disabled proof of zero application processes and outbound requests with corrupt evidence |
| AFR-RPL-001 | `make harness-check` records and replays the Python reference entrypoint through OCI, verifies the immutable plan digest, source linkage, exact candidate, and preflight contracts | Proven | — |
| AFR-RPL-002 | `make harness-check` proves a declared live model route with the API substituted, a second all-substituted replay with no egress attachment, and an unmatched request that never invokes its live callback | Proven | — |
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
