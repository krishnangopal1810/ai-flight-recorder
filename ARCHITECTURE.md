# Architecture

AI Flight Recorder is a local-first causal regression debugger. The same domain services power the embedded CLI workflow and the single-tenant hosted service.

## Dependency model

```text
types -> config -> repository -> service -> runtime/API
                     ^              ^
                     |              |
                   providers -------+
```

- `types` owns validated domain values and public logical models.
- `config` parses files and environment variables into typed configuration.
- `repository` defines persistence contracts and their local/hosted implementations.
- `service` owns capture, policy, completeness, replay, diff, evaluator, and bisect behavior.
- `runtime` exposes CLI, REST, SSE, OTLP, and worker boundaries.
- `providers` contains explicit clocks, identifiers, OCI, Git, object storage, and telemetry adapters.

Cross-domain code communicates through exported service contracts. Runtime packages cannot import concrete repositories. Repository implementations cannot import services. External JSON, YAML, protobuf, database rows, and environment variables are validated at their boundary.

## Runtime data flow

1. OTLP spans enter the HTTP or gRPC runtime.
2. The capture policy sanitizes data in memory before persistence.
3. The capsule service stores post-policy payloads in content-addressed storage and finalizes an immutable manifest.
4. Playback reads verified evidence without starting application code.
5. Replay preflight creates an immutable plan, then launches an OCI-isolated application behind the Flight gateway.
6. Candidate telemetry becomes a linked capsule.
7. The diff service aligns logical trees and reports evidence, observations, and bounded hypotheses.

## Public compatibility

Run Capsule schema major versions are strict. Minor versions preserve unknown fields. REST lives under `/api/v1`; OTLP uses its native protocols. Capsule bundles are deterministic tar+zstd archives with RFC 8785-compatible canonical JSON and SHA-256 payload identities.

## More detail

- [Security](docs/SECURITY.md)
- [Reliability](docs/RELIABILITY.md)
- [Frontend](docs/FRONTEND.md)
- [Design documents](docs/design-docs/index.md)
- [Generated references](docs/generated/index.md)
