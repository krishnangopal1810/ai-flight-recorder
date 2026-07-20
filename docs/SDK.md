# SDK and `flight.*` semantic contract

The Python package `ai-flight-recorder` and TypeScript package `@ai-flight-recorder/sdk` are thin OpenTelemetry metadata and controlled-boundary libraries. Metadata failures emit payload-free `flight_sdk_metadata` diagnostics and never fail a production call. Once `FLIGHT_REPLAY_PLAN` is present, missing, malformed, or ambiguous replay evidence fails closed.

Equivalent APIs cover run provenance, argv entrypoint registration, artifact attachment, environment-fact hashing, sandbox declaration, synchronous/asynchronous boundaries, and OpenAI-compatible or MCP reference adapters. Both SDKs use RFC 8785 canonical JSON before SHA-256 request matching and share a golden cross-language digest fixture.

## Span namespace

| Attribute or event | Meaning |
|---|---|
| `flight.capture` | Explicit root opt-in. |
| `flight.project.id` | Project grouping boundary. |
| `flight.repository.url`, `flight.git.commit`, `flight.git.dirty_digest`, `flight.build.id` | Source and build provenance. |
| `flight.entrypoint.command`, `.image`, `.input_mode`, `.output_mode`, `.model_hosts` | Replay entrypoint evidence. |
| `flight.logical_key` | Stable developer operation identity. |
| `flight.boundary.logical_key`, `.class`, `.parent_logical_key`, `.request_digest` | Exact boundary identity and aligned parent. |
| `flight.boundary.side_effect`, `.replay_policy`, `.sandbox` | Safety classification and declared execution policy. |
| `flight.boundary.matching_mode`, `.ignore_paths` | Canonical request comparison rules. |
| `flight.input`, `flight.output` | Capture-policy inputs; never product telemetry. |
| `flight.artifact` | Artifact event with logical identity, media type, digest, and bytes. |
| `flight.sandbox` | Named sandbox-to-destination declaration. |
| `flight.environment.<NAME>.digest` | One-way environment fact; never the value. |

Boundary occurrence is counted by aligned parent, boundary class, and logical key. A substitution additionally requires the canonical request digest. JSON-pointer ignore rules are explicit and versioned; neither SDK falls through to a live call when a match is missing or ambiguous.

During replay, `FLIGHT_REPLAY_PLAN` points at the read-only envelope and `FLIGHT_WORKSPACE` points at ephemeral writable space. `FLIGHT_REPLAY_MODEL`, when set with `flight replay --model`, is the selected candidate model name. Credentials remain worker configuration and are only passed for environment names declared by `flight.yaml`.

## Executable Python reference

`examples/reference-python/agent.py` is the executable conformance agent used by `make harness-check`. Its image is built from `examples/reference-python/Dockerfile`, reads one JSON request from stdin, enriches standard OTLP spans through this SDK, and registers its exact argv entrypoint and immutable image ID. Recording mode supplies a deterministic API fixture. Replay mode points the live callback at an unavailable host, so the successful exact replay is possible only through recorded boundary substitution. A second replay changes the canonical request and retains a failed candidate whose output proves the callback was never invoked.
