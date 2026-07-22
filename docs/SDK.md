# SDK and `flight.*` semantic contract

The Python package `ai-flight-recorder` and TypeScript package `@ai-flight-recorder/sdk` are thin OpenTelemetry metadata and controlled-boundary libraries. Metadata failures emit payload-free `flight_sdk_metadata` diagnostics and never fail a production call. Once `FLIGHT_REPLAY_PLAN` is present, missing, malformed, or ambiguous replay evidence fails closed.

Equivalent APIs cover run provenance, argv entrypoint registration, artifact attachment, environment-fact hashing, sandbox declaration, synchronous/asynchronous values and streams, and MCP, official OpenAI, and LangChain adapters. Both SDKs use RFC 8785 canonical JSON before SHA-256 request matching and share a golden cross-language digest fixture.

## Official OpenAI and LangChain integrations

The integrations are explicit wrappers: they do not monkey-patch OpenAI, LangChain, HTTP transports, or global telemetry providers. The generic SDK stays framework-free.

Install Python with only the integration you use, or both:

```bash
pip install 'ai-flight-recorder[openai]'
pip install 'ai-flight-recorder[langchain]'
pip install 'ai-flight-recorder[integrations]'
```

Wrap the existing official clients and place the application entrypoint in a root run span:

```python
from openai import OpenAI
from flight_recorder import record_run, wrap_openai

client = wrap_openai(OpenAI())

answer = record_run(
    "support.answer",
    {"question": "What is covered?"},
    lambda request: client.responses.create(
        model="gpt-5", input=request["question"]
    ),
)
```

`wrap_openai()` covers Responses and Chat Completions `create`, `parse`, helper streams, and `create(stream=True)` for `OpenAI` and `AsyncOpenAI`. Python replay reconstructs only locally allowlisted official SDK Pydantic classes; a recorded class name is never imported. Parsed values are restored with the `response_format` class supplied by the replaying application.

LangChain components are wrapped at their natural edges:

```python
from flight_recorder import (
    flight_recorder_middleware,
    wrap_chat_model,
    wrap_retriever,
    wrap_tool,
)

model = wrap_chat_model(chat_model, "support.model")
retriever = wrap_retriever(policy_retriever, "support.policy")
lookup = wrap_tool(customer_lookup, "support.lookup", side_effect="read_only")
middleware = [flight_recorder_middleware(tool_side_effects={"lookup": "read_only"})]
```

Unknown tools default to `side_effect="unknown"` and recorded substitution. A mutating tool needs an explicit live policy and named sandbox before the replay service can consider it.

TypeScript uses optional peer dependencies and dedicated subpaths:

```ts
import OpenAI from "openai";
import {recordRun} from "@ai-flight-recorder/sdk";
import {wrapOpenAI} from "@ai-flight-recorder/sdk/openai";
import {
  flightRecorderMiddleware,
  wrapChatModel,
  wrapRetriever,
  wrapTool,
} from "@ai-flight-recorder/sdk/langchain";

const client = wrapOpenAI(new OpenAI());
const answer = await recordRun("support.answer", {question: "What is covered?"},
  request => client.responses.create({model: "gpt-5", input: request.question}));
```

Streams are stored as schema-versioned ordered event arrays with `completed`, `closed`, `error`, or `abandoned` termination and an available final response. Wall-clock gaps are deliberately excluded. Only `completed` streams can replay as successful exact streams; other termination states fail closed after reproducing their recorded prefix.

Both integration layers set standard OpenInference and OpenTelemetry GenAI attributes. `configure_telemetry()` / `configureTelemetry()` explicitly installs OTLP tracing only when no application provider exists. The wrappers perform the same bootstrap automatically only when `FLIGHT_PROJECT_ID`, `FLIGHT_CAPTURE=true`, or `flight.capture=true` in `OTEL_RESOURCE_ATTRIBUTES` identifies a Flight Recorder capture. An existing provider is always reused and remains application-owned.

## Trace-correlated OTLP logs

Flight Recorder accepts standard OTLP Logs at `POST /v1/logs` and the OTLP gRPC `LogsService/Export` method. A log exported while an OpenTelemetry span is current carries the trace and span context assigned by the language SDK. The collector shows it under that operation; records with an empty or unknown span ID remain visible under Run logs. Log evidence is policy-processed and content-addressed, and it never participates in replay or diff decisions.

Python installs logging support only on the supplied standard-library logger. Install the optional dependencies with `pip install 'ai-flight-recorder[logs]'`, pass the same `Resource` used by the tracer, and explicitly flush or shut down during process teardown:

```python
import logging
from flight_recorder import install_otlp_logging

application_logger = logging.getLogger("agent")
log_runtime = install_otlp_logging(application_logger, resource=trace_resource)

with tracer.start_as_current_span("tool.lookup"):
    application_logger.info("lookup completed", extra={"customer.tier": "standard"})

log_runtime.force_flush()
log_runtime.shutdown()
```

TypeScript exposes the standard OpenTelemetry Logs API logger from the optional `@ai-flight-recorder/sdk/logging` subpath. The experimental OpenTelemetry log packages are optional peers; applications should pin the exact mutually compatible versions used by the reference agent.

```ts
import {SeverityNumber} from "@opentelemetry/api-logs";
import {createOTLPLogging} from "@ai-flight-recorder/sdk/logging";

const logRuntime = createOTLPLogging({resource: traceResource, scopeName: "agent"});
await tracer.startActiveSpan("tool.lookup", async span => {
  logRuntime.logger.emit({
    severityNumber: SeverityNumber.INFO,
    severityText: "INFO",
    body: "lookup completed",
    attributes: {"customer.tier": "standard"},
  });
  span.end();
});
await logRuntime.forceFlush();
await logRuntime.shutdown();
```

When `endpoint` and `headers` are omitted, both helpers use the standard OTLP environment variables, including the logs-specific endpoint/header variables. Supplying a collector base URL appends `/v1/logs`; a signal URL ending in `/v1/logs` is used unchanged. The helpers create isolated log providers and do not replace global trace or logging providers.

## Replay-ready setup contract

`flight record --project <id>` always merges `flight.capture=true` and `flight.project.id=<id>` into `OTEL_RESOURCE_ATTRIBUTES`. With `--replay-ready`, the CLI loads `flight.yaml`, validates or builds an immutable image, and exports two versioned inputs to the child process:

- `FLIGHT_PROJECT_ID` contains the non-secret project identity.
- `FLIGHT_ENTRYPOINT_JSON` contains schema version `1` and the typed entrypoint, including the immutable image, declared model hostnames, credential names, sandbox destinations, resource limits, and model-override capability. It never contains credential values.

Python `set_run_provenance()` / `register_entrypoint()` and TypeScript `setRunProvenance()` / `registerEntrypoint()` use those values when called without arguments. Explicit arguments remain backward compatible and take precedence.

Live model execution requires a live LLM boundary, hostname-only `model_hosts`, at least one declared credential name, and matching `FLIGHT_REPLAY_CREDENTIAL_<NAME>` worker configuration. Recorded substitution is the default and receives no model credentials or egress. A model change is offered only when `model_override` was recorded; the candidate must emit the requested value as `gen_ai.request.model` or the replay fails with `override_not_applied`.

## Span namespace

| Attribute or event | Meaning |
|---|---|
| `flight.capture` | Explicit root opt-in. |
| `flight.project.id` | Project grouping boundary. |
| `flight.repository.url`, `flight.git.commit`, `flight.git.dirty_digest`, `flight.build.id` | Source and build provenance. |
| `flight.runtime`, `flight.dependency_lock`, `flight.feature_flags` | Explicit runtime identity plus canonical dependency-lock digests and feature flags. |
| `flight.entrypoint.command`, `.image`, `.working_dir`, `.input_mode`, `.output_mode`, `.environment`, `.model_hosts`, `.model_override`, limits | Replay entrypoint evidence; environment contains names only. |
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

A controlled replay may replace exactly one recorded boundary response or one model request. Response overrides are substituted without calling the original dependency. Model-request overrides require a request-aware callback, for example `lambda effective_request: call_model(effective_request)` in Python or `effectiveRequest => callModel(effectiveRequest)` in TypeScript. Legacy zero-argument callbacks continue to work for recording and response substitution, but a model-input override fails closed instead of silently sending the original request.

## Python boundary examples

Every external edge declares its class, side-effect behavior, and replay policy. Recorded substitution is the default. Only an explicitly live, read-only boundary can receive egress during replay.

```python
from flight_recorder import boundary, mcp_boundary, openai_boundary

answer = openai_boundary(
    "llm.answer", model_request,
    lambda effective_request: call_model(effective_request),
)
documents = boundary(
    "retriever.policy", retrieval_request,
    lambda effective_request: retrieve(effective_request),
    boundary_class="retriever", side_effect="read_only", replay_policy="substitute",
)
lookup = boundary(
    "tool.customer", tool_request,
    lambda effective_request: call_tool(effective_request),
    boundary_class="tool", side_effect="read_only", replay_policy="substitute",
)
profile = boundary(
    "api.profile", api_request,
    lambda effective_request: call_api(effective_request),
    boundary_class="api", side_effect="read_only", replay_policy="substitute",
)
draft = mcp_boundary(
    "mcp.email.draft", mcp_request,
    lambda effective_request: call_mcp(effective_request),
    side_effect="read_only",
)

# A mutation stays blocked unless replay explicitly allows it and the named
# sandbox has an isolated destination in flight.yaml.
sent = boundary(
    "tool.email.send", send_request,
    lambda effective_request: send_email(effective_request),
    boundary_class="tool", side_effect="mutating", replay_policy="live",
    sandbox="mail-test",
)
```

## TypeScript boundary examples

```ts
import {boundary, mcpBoundary, openAIBoundary} from "@ai-flight-recorder/sdk";

const answer = await openAIBoundary(
  "llm.answer", modelRequest,
  effectiveRequest => callModel(effectiveRequest),
);
const documents = await boundary(retrievalRequest, effectiveRequest => retrieve(effectiveRequest), {
  logicalKey: "retriever.policy", boundaryClass: "retriever",
  sideEffect: "read_only", replayPolicy: "substitute",
});
const lookup = await boundary(toolRequest, effectiveRequest => callTool(effectiveRequest), {
  logicalKey: "tool.customer", boundaryClass: "tool",
  sideEffect: "read_only", replayPolicy: "substitute",
});
const profile = await boundary(apiRequest, effectiveRequest => callAPI(effectiveRequest), {
  logicalKey: "api.profile", boundaryClass: "api",
  sideEffect: "read_only", replayPolicy: "substitute",
});
const draft = await mcpBoundary(
  "mcp.email.draft", mcpRequest,
  effectiveRequest => callMCP(effectiveRequest),
  "read_only",
);
const sent = await boundary(sendRequest, effectiveRequest => sendEmail(effectiveRequest), {
  logicalKey: "tool.email.send", boundaryClass: "tool",
  sideEffect: "mutating", replayPolicy: "live", sandbox: "mail-test",
});
```

## Executable Python reference

`examples/reference-python/agent.py` is the executable conformance agent used by `make harness-check`. Its image is built from `examples/reference-python/Dockerfile`, reads one JSON request from stdin, enriches standard OTLP spans through this SDK, and registers its exact argv entrypoint, immutable image ID, and model host. Recording mode supplies a deterministic API fixture and calls the deterministic model service. The first replay keeps only that model route live while the API callback points at an unavailable host. A second replay substitutes both boundaries, points the model callback at an unavailable host, and records that it was not invoked. A third replay changes the canonical API request and retains a failed candidate whose output proves the API callback was never invoked.

## Executable TypeScript reference

`examples/reference-typescript/agent.ts` mirrors the Python execution through the native TypeScript SDK and the OpenTelemetry JavaScript OTLP/HTTP JSON exporter. `make harness-check` compares both source capsules' root input/output digests and complete boundary identity, request, response, parent, and policy projections. It then runs the registered Node entrypoint in OCI with both boundaries substituted and verifies the linked exact candidate reports that the live model callback was not invoked.

The same executable has a deterministic metadata-failure fixture. It opts the root into capture, passes a non-JSON argv value to `registerEntrypoint`, and still returns the production answer. The retained warning contains only the operation and error type; the capsule remains playback-ready, reports `entrypoint_missing` for replay, and the replay API fails with the stable `incomplete_capsule` category.

OTLP/HTTP JSON encodes trace and span IDs as hexadecimal strings rather than the base64 representation used by standard protobuf JSON. The collector and trusted replay gateway normalize those IDs once at their transport boundaries before using the typed protobuf request.
