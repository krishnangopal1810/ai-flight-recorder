# SDK and `flight.*` semantic contract

The Python package `ai-flight-recorder` and TypeScript package `@ai-flight-recorder/sdk` are thin OpenTelemetry metadata and controlled-boundary libraries. Metadata failures emit payload-free `flight_sdk_metadata` diagnostics and never fail a production call. Once `FLIGHT_REPLAY_PLAN` is present, missing, malformed, or ambiguous replay evidence fails closed.

Equivalent APIs cover run provenance, argv entrypoint registration, artifact attachment, environment-fact hashing, sandbox declaration, synchronous/asynchronous boundaries, and OpenAI-compatible or MCP reference adapters. Both SDKs use RFC 8785 canonical JSON before SHA-256 request matching and share a golden cross-language digest fixture.

## Span namespace

| Attribute or event | Meaning |
|---|---|
| `flight.capture` | Explicit root opt-in. |
| `flight.project.id` | Project grouping boundary. |
| `flight.repository.url`, `flight.git.commit`, `flight.git.dirty_digest`, `flight.build.id` | Source and build provenance. |
| `flight.runtime`, `flight.dependency_lock`, `flight.feature_flags` | Explicit runtime identity plus canonical dependency-lock digests and feature flags. |
| `flight.entrypoint.command`, `.image`, `.input_mode`, `.output_mode`, `.model_hosts` | Replay entrypoint evidence. |
| `flight.logical_key` | Stable developer operation identity. |
| `flight.boundary.logical_key`, `.class`, `.parent_logical_key`, `.request_digest` | Producer boundary identity and aligned parent; the collector replaces the supplied digest with its post-policy canonical digest. |
| `flight.boundary.side_effect`, `.replay_policy`, `.sandbox` | Safety classification and declared execution policy. |
| `flight.boundary.matching_mode`, `.ignore_paths` | Canonical request comparison rules. |
| `flight.input`, `flight.output` | Capture-policy inputs; never product telemetry. |
| `flight.artifact` | Artifact event with logical identity, media type, digest, and bytes. |
| `flight.sandbox` | Named sandbox-to-destination declaration. |
| `flight.environment.<NAME>.digest` | One-way environment fact; never the value. |
| `flight.evaluation.id`, `.version`, `.score`, `.threshold` | Evaluator identity and scalar result metadata on an OpenInference `EVALUATOR` span. |

Boundary occurrence is counted by aligned parent, boundary class, and logical key. A substitution additionally requires the canonical request digest. The SDK emits that digest for immediate replay metadata, but the collector never trusts or persists a producer-computed pre-policy identity: it applies capture policy, applies the declared JSON-pointer ignores, canonicalizes with RFC 8785, and derives the stored digest itself. Neither SDK falls through to a live call when a match is missing or ambiguous.

## OTLP and OpenInference ingestion

The collector consumes the [official OpenInference span-kind taxonomy](https://arize-ai.github.io/openinference/spec/semantic_conventions.html) for agent, LLM, retriever, tool, evaluator, and prompt operations, plus the OTel GenAI operation name where supplied. `flight.operation.kind` provides explicit application kinds such as `mcp` that are not part of the OpenInference taxonomy. The producer's scope or resource schema URL is retained on every logical operation.

Operation input and output values, artifact bytes, and evaluator evidence cross the ordered capture-policy boundary and become content-addressed payload references. Evaluator identity, version, score, and threshold are metadata; evaluator input and evidence reuse the evaluator operation's payload references so content cannot bypass redaction, exclusion, or size limits. Field and exact artifact-class limits select the first configured keep, omit, or truncate action. A truncated reference keeps the digest and stored size of its bounded policy-safe evidence, adds `original_size` and `reason: payload_truncated`, and fails closed for replay paths that require the original value. Model provider, requested/reported model, invocation parameters, usage, token counts, and cost are associated from the LLM span and retained as policy-processed metadata.

The collector recognizes the current OpenTelemetry GenAI content attributes, including `gen_ai.input.messages`, `gen_ai.output.messages`, `gen_ai.system_instructions`, tool call arguments/results, retrieval content, and [`gen_ai.tool.definitions`](https://opentelemetry.io/docs/specs/semconv/registry/attributes/gen-ai/), together with OpenInference and `flight.*` input/output aliases. Tool definitions are stored as content-addressed payloads rather than manifest metadata. Python and TypeScript boundary wrappers emit `flight.input|output.omission.{reason,size,media_type}` when a value cannot be canonicalized; producer digests may also be supplied when safely calculable. The collector normalizes these into a payload reference with `state: omitted`, and a policy size limit records the post-boundary size without hashing or retaining rejected bytes. Omitted root or boundary evidence is surfaced through completeness and never falls through to a live dependency.

Run provenance accepts standard `container.id` and `process.runtime.*` resource attributes together with the documented `flight.*` metadata. Python `set_run_provenance` and TypeScript `setRunProvenance` emit equivalent canonical dependency-lock and feature-flag maps and may provide an explicit runtime identity. Missing `source_identity`, `commit_sha`, `build_or_container_identity`, `runtime`, `dependency_lock`, `feature_flags`, or `entrypoint` facts are listed in `provenance.missing`; invalid JSON or entrypoint argv is named separately and never guessed into a value. A clean worktree legitimately has no `dirty_digest` and is not labeled missing.

During replay, `FLIGHT_REPLAY_PLAN` points at the read-only envelope and `FLIGHT_WORKSPACE` points at ephemeral writable space. `FLIGHT_REPLAY_MODEL`, when set with `flight replay --model`, is the selected candidate model name. Credentials remain worker configuration and are only passed for environment names declared by `flight.yaml`.

## Executable Python reference

`examples/reference-python/agent.py` is the executable conformance agent used by `make harness-check`. Its image is built from `examples/reference-python/Dockerfile`, reads one JSON request from stdin, enriches standard OTLP spans through this SDK, and registers its exact argv entrypoint, immutable image ID, and model host. Recording mode supplies a deterministic API fixture and calls the deterministic model service. The first replay keeps only that model route live while the API callback points at an unavailable host. A second replay substitutes both boundaries, points the model callback at an unavailable host, and records that it was not invoked. A third replay changes the canonical API request and retains a failed candidate whose output proves the API callback was never invoked.

## Executable TypeScript reference

`examples/reference-typescript/agent.ts` mirrors the Python execution through the native TypeScript SDK and the OpenTelemetry JavaScript OTLP/HTTP JSON exporter. `make harness-check` compares both source capsules' root input/output digests and complete boundary identity, request, response, parent, and policy projections. It then runs the registered Node entrypoint in OCI with both boundaries substituted and verifies the linked exact candidate reports that the live model callback was not invoked.

The same executable has a deterministic metadata-failure fixture. It opts the root into capture, passes a non-JSON argv value to `registerEntrypoint`, and still returns the production answer. The retained warning contains only the operation and error type; the capsule remains playback-ready, reports `entrypoint_missing` for replay, and the replay API fails with the stable `incomplete_capsule` category.

OTLP/HTTP JSON encodes trace and span IDs as hexadecimal strings rather than the base64 representation used by standard protobuf JSON. The collector and trusted replay gateway normalize those IDs once at their transport boundaries before using the typed protobuf request.
