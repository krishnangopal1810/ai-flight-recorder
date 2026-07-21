# Security

## Trust boundaries

Production telemetry is hostile input. Replay source code and evaluator containers are untrusted. Capsule contents are sensitive customer data. Operational telemetry is payload-free by construction.

## Required controls

- Ordered policy enforcement precedes all durable writes and observability.
- Boundary match identity is derived from the policy-processed canonical request, never from an untrusted producer's pre-policy digest.
- Strict-mode failures retain only policy-safe diagnostic metadata.
- Local mode binds every listener to loopback by default. Shared mode requires a random bootstrap token plus native TLS or a source-CIDR-restricted TLS proxy; HTTP and OTLP/gRPC enforce the same scoped bearer contract.
- Replays run with the controls in the replay-isolation design document.
- Playback is a separate passive service path. Structural checks forbid network, process-execution, and provider imports; a hostile-entrypoint acceptance test runs inside a read-only, non-root OCI container with `--network none` and records zero application processes and outbound requests.
- Only the trusted gateway may join the operator-selected OCI egress network, and only for plans containing declared live hosts; the untrusted application remains on its private internal network.
- Unknown and mutating boundaries are substituted or blocked unless a sandbox and explicit flag are present.
- Secrets are allowlisted per replay and never included in capsule manifests or logs.
- UI overrides are explicit replay-plan inputs, never in-place capsule mutations. The API accepts at most one JSON value under 1 MiB, applies capture policy before storing it, rejects any value that policy would omit, redact, or truncate, binds it to a recorded boundary ID, includes it in the immutable plan digest, and never emits the value in logs, metrics, or stage messages. Candidate capture must prove the edited request or response digest was applied before the UI labels it as the user's controlled change; a mismatch fails closed as an unmatched boundary.

The blocking AFR-T08 contract sends deterministic API-key, token, password, email-PII, exclusion, and strict-failure fixtures through capture policy. It scans SQLite/filesystem and PostgreSQL/S3-compatible stores, searchable identity rows, decompressed bundles, quarantine, returned diagnostics, structured service logs, and metrics for every seed. The live Compose proof is `scripts/privacy-smoke.sh`; backend-neutral security tests apply the same capture contract directly to both repositories. V1 emits local structured logs and Prometheus metrics but configures no outbound product-telemetry exporter; adding one requires an equivalent fail-closed allowlist and sink scan.

The AFR-SEC-003 contract starts the real service on non-loopback listeners with native TLS and with a terminating TLS proxy. It rejects cleartext, missing tokens, wrong scopes, untrusted proxy peers, spoofed forwarded-protocol headers, and direct proxy-backend HTTP. It exercises API, metrics, OTLP/HTTP, and OTLP/gRPC; the hosted variant uses PostgreSQL and S3-compatible storage when the contract services are available. Local repository tests require `0600` database/blob files and a `0700` blob root. `scripts/access-smoke.sh` retains the live rejection bodies and the TLS journey output.

The AFR-SEC-004 contract applies ordered field/artifact-class keep, omit, and truncate limits before hashing or persistence and carries typed omissions or truncation evidence into completeness. Backend-neutral tests expire short-lived projects on schedule, preserve shared CAS data until its final reference expires, create content-free deletion audits, and run capture concurrently with cleanup. Local cleanup is serialized with capsule writes; hosted cleanup and upload use the same digest-scoped PostgreSQL advisory lock and recheck the reference row before object deletion. Corrupt accounting rolls back the entire expiry transaction, while a phase-two storage failure retains the blob and returns a safe operational error for the scheduler to alert on.

The AFR-PLY-001 contract verifies the manifest/root from its declared digest tree without trusting blob availability, then reads each payload through digest-checking repository methods. Direct payload and strict capsule endpoints reject corrupt bytes. The partial playback projection exposes only a safe status and reference list for damaged objects; it never includes the unverified value. The live hosted proof deletes one S3-compatible object, overwrites another, and browser-verifies the remaining evidence and warnings.
