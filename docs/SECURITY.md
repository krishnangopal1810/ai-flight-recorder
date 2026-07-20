# Security

## Trust boundaries

Production telemetry is hostile input. Replay source code and evaluator containers are untrusted. Capsule contents are sensitive customer data. Operational telemetry is payload-free by construction.

## Required controls

- Ordered policy enforcement precedes all durable writes and observability.
- Strict-mode failures retain only policy-safe diagnostic metadata.
- Local mode binds loopback. Shared mode requires TLS and scoped bearer tokens.
- Replays run with the controls in the replay-isolation design document.
- Unknown and mutating boundaries are substituted or blocked unless a sandbox and explicit flag are present.
- Secrets are allowlisted per replay and never included in capsule manifests or logs.

Security tests scan files, databases, exports, logs, metrics, and quarantine records for seeded values.
