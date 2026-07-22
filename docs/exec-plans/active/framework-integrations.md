# Framework-native OpenAI and LangChain integrations

## Objective

Deliver AFR-ING-005 as explicit replay-safe adapters for the official OpenAI SDK and LangChain in Python and TypeScript without replacing the generic boundary contract or weakening replay isolation.

## Traceability

AFR-ING-002, AFR-ING-003, AFR-ING-005, AFR-SEC-001, AFR-RPL-001–004; AFR-T05, AFR-T06, AFR-T08, AFR-T09.

## Decisions

- Support OpenAI Responses and Chat Completions, including parse helpers, synchronous/asynchronous Python, asynchronous TypeScript, and exact ordered streams. Realtime, WebSocket multi-agent, background polling, embeddings, images, audio, files, and batches remain outside this slice and fail through the existing unsupported-boundary path.
- Expose explicit wrappers and subpath imports. Do not monkey-patch clients, LangChain, transports, or global providers.
- Preserve Python official Pydantic response/event types through a local allowlist and TypeScript structural types. Preserve LangChain messages, message chunks, documents, tool results, and supported commands without importing a recorded class name.
- Record stream event order, content, termination, and an available final response. Do not record event timing. Closed, abandoned, and errored streams are incomplete for exact replay.
- Prefer a unique canonical request digest before occurrence. Partition repeated-call counters by root run or trace, and fail when identical concurrent evidence remains ambiguous.
- Emit OpenInference span kinds and OpenTelemetry GenAI operation/provider/model/usage attributes. Reuse an installed tracer provider; bootstrap standard OTLP only in a Flight capture environment with no provider.
- Publish-ready wheels, source distributions, and npm tarballs are in scope. External registry publication is not.

## Progress

- [x] Refactor both SDKs around a reusable boundary session while retaining the existing public boundary APIs.
- [x] Add root-run helpers, context-local nested-integration guards, trace/run-scoped occurrences, and generic ordered stream envelopes.
- [x] Add official OpenAI wrappers for Responses, Chat Completions, parse helpers, create streams, and helper streams.
- [x] Add LangChain chat-model, retriever, tool, and modern agent-middleware wrappers with safe tool defaults.
- [x] Add standard telemetry attributes, opt-in OTLP bootstrap, optional Python extras, npm subpaths/optional peers, and frozen dependency locks.
- [x] Add official-client loopback tests, offline no-network substitution tests, structured output tests, typed LangChain codec tests, and stream-order tests in both languages.
- [x] Update the SDK contract, public quickstart, PRD traceability, release clean-install smoke, license input graph, and active V1 plan.

## Validation evidence

- `uv run --project sdk/python ruff check sdk/python/src sdk/python/tests`
- `uv run --project sdk/python mypy sdk/python/src`
- `uv run --project sdk/python pytest -q sdk/python/tests`
- `npm run build --prefix sdk/typescript`
- `npm test --prefix sdk/typescript`
- `make docs-check`, `make license-check`, and the repository blocking gates in `make verify`
