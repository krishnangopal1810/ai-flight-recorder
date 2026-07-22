import { SpanStatusCode, trace } from "@opentelemetry/api";
import { SeverityNumber } from "@opentelemetry/api-logs";
import { OTLPTraceExporter } from "@opentelemetry/exporter-trace-otlp-http";
import { detectResources, envDetector, resourceFromAttributes } from "@opentelemetry/resources";
import { BatchSpanProcessor } from "@opentelemetry/sdk-trace-base";
import { NodeTracerProvider } from "@opentelemetry/sdk-trace-node";
import { boundary, registerEntrypoint, setRunProvenance } from "@ai-flight-recorder/sdk";
import { createOTLPLogging } from "@ai-flight-recorder/sdk/logging";
import canonicalize from "canonicalize";

const NORMAL_PROJECT_ID = "reference-replay-typescript";
const DIAGNOSTIC_PROJECT_ID = "reference-replay-typescript-diagnostic";
const SERVICE_NAME = "typescript-reference-agent";
const ENTRYPOINT = ["node", "/app/dist/agent.js"];
const RECORDED_POLICY = { rule: "refunds within 30 days" };
const RECORDED_RETRIEVAL = { documents: [{id: "refund-policy", content: "grounded policy evidence"}] };
const RECORDED_MCP = { uri: "policy://refund", limit_days: 30 };
const RECORDED_TOOL = { eligible: true };
const TOOL_DEFINITIONS = [{type: "function", name: "check_refund_eligibility", description: "Check whether a refund request is within policy"}];
const MODEL_HOST = "fake-deps";
const MODEL_ENDPOINT = `http://${MODEL_HOST}:9099/v1/chat/completions`;

interface AgentInput { policy_id: string }
interface PolicyResponse { rule: string }
interface RetrievalResponse { documents: Array<{id: string; content: string}> }
interface MCPResponse { uri: string; limit_days: number }
interface ToolResponse { eligible: boolean }
interface ModelResponse { response: { choices: Array<{ message: { content: string } }> } }

function canonical(value: unknown): string {
  const encoded = canonicalize(value);
  if (encoded === undefined) throw new Error("value is not canonical JSON");
  return encoded;
}

function configureTracing(projectId: string) {
  const endpoint = `${requiredEnv("OTEL_EXPORTER_OTLP_ENDPOINT").replace(/\/$/, "")}/v1/traces`;
  const token = process.env.FLIGHT_OTLP_TOKEN;
  const resource = detectResources({ detectors: [envDetector] }).merge(resourceFromAttributes({
      "service.name": SERVICE_NAME,
      "flight.project.id": projectId,
      "flight.capture": true,
      "flight.sdk.language": "typescript",
    }));
  const provider = new NodeTracerProvider({
    resource,
    spanProcessors: [new BatchSpanProcessor(new OTLPTraceExporter({
      url: endpoint,
      headers: token ? { Authorization: `Bearer ${token}` } : {},
    }))],
  });
  provider.register();
  return {provider, resource};
}

async function readInput(): Promise<AgentInput> {
  const chunks: Buffer[] = [];
  for await (const chunk of process.stdin) chunks.push(Buffer.from(chunk));
  return JSON.parse(Buffer.concat(chunks).toString("utf8")) as AgentInput;
}

function requiredEnv(name: string): string {
  const value = process.env[name];
  if (!value) throw new Error(`${name} is required`);
  return value;
}

async function run(): Promise<number> {
  const input = await readInput();
  const diagnosticFixture = process.env.FLIGHT_METADATA_FAILURE_FIXTURE === "1";
  const {provider, resource} = configureTracing(diagnosticFixture ? DIAGNOSTIC_PROJECT_ID : NORMAL_PROJECT_ID);
  const logging = createOTLPLogging({
    resource,
    endpoint: requiredEnv("OTEL_EXPORTER_OTLP_ENDPOINT"),
    headers: process.env.FLIGHT_OTLP_TOKEN ? {Authorization: `Bearer ${process.env.FLIGHT_OTLP_TOKEN}`} : {},
    scopeName: "ai-flight-recorder.reference-typescript",
    scopeVersion: "1.0",
  });
  const tracer = trace.getTracer("ai-flight-recorder.reference-typescript", "1.0");
  let callbackInvoked = false;
  let modelCallbackInvoked = false;
  let retrievalCallbackInvoked = false;
  let mcpCallbackInvoked = false;
  let toolCallbackInvoked = false;
  let exitCode = 0;

  await tracer.startActiveSpan("answer-policy", async root => {
    logging.logger.emit({severityNumber: SeverityNumber.INFO, severityText: "INFO", body: "answer policy started", attributes: {policy_id: input.policy_id}});
    root.setAttribute("openinference.span.kind", "AGENT");
    root.setAttribute("flight.logical_key", "agent.answer");
    root.setAttribute("input.value", canonical(input));
    setRunProvenance({
      projectId: diagnosticFixture ? DIAGNOSTIC_PROJECT_ID : NORMAL_PROJECT_ID,
      commitSha: "a".repeat(40),
    });
    if (diagnosticFixture) {
      registerEntrypoint([1n as unknown as string]);
    } else {
      registerEntrypoint(ENTRYPOINT, {
        image: process.env.FLIGHT_REFERENCE_IMAGE,
        modelHosts: [MODEL_HOST],
        inputMode: "stdin_json",
        outputMode: "stdout_json",
      });
    }

    const policyRequest: Record<string, string> = { policy_id: input.policy_id };
    if (process.env.FLIGHT_REPLAY_MODEL === "mismatch-request") policyRequest.policy_id = "changed";

    try {
      const retrieved = await tracer.startActiveSpan("policy-retrieval", async span => {
        span.setAttribute("openinference.span.kind", "RETRIEVER");
        try {
          return await boundary<RetrievalResponse>({query: input.policy_id}, async () => {
            retrievalCallbackInvoked = true;
            if (process.env.FLIGHT_RECORDING_FIXTURE === "1") return RECORDED_RETRIEVAL;
            throw new Error("recorded retrieval dependency is unavailable");
          }, {logicalKey: "retriever.policy", boundaryClass: "retriever", sideEffect: "read_only", replayPolicy: "substitute", parentLogicalKey: "agent.answer"});
        } finally { span.end(); }
      });

      const mcpResult = await tracer.startActiveSpan("policy-resource", async span => {
        span.setAttribute("flight.operation.kind", "mcp");
        try {
          return await boundary<MCPResponse>({uri: "policy://refund"}, async () => {
            mcpCallbackInvoked = true;
            if (process.env.FLIGHT_RECORDING_FIXTURE === "1") return RECORDED_MCP;
            throw new Error("recorded MCP dependency is unavailable");
          }, {logicalKey: "mcp.policy", boundaryClass: "mcp", sideEffect: "read_only", replayPolicy: "substitute", parentLogicalKey: "agent.answer"});
        } finally { span.end(); }
      });

      const toolResult = await tracer.startActiveSpan("eligibility-tool", async span => {
        span.setAttribute("openinference.span.kind", "TOOL");
        logging.logger.emit({severityNumber: SeverityNumber.WARN, severityText: "WARN", body: "checking refund eligibility", attributes: {days_since_purchase: 12}});
        try {
          return await boundary<ToolResponse>({days_since_purchase: 12, limit_days: 30}, async () => {
            toolCallbackInvoked = true;
            if (process.env.FLIGHT_RECORDING_FIXTURE === "1") return RECORDED_TOOL;
            throw new Error("recorded tool dependency is unavailable");
          }, {logicalKey: "tool.eligibility", boundaryClass: "tool", sideEffect: "read_only", replayPolicy: "substitute", parentLogicalKey: "agent.answer"});
        } finally { span.end(); }
      });

      const policy = await tracer.startActiveSpan("policy-lookup", async span => {
        span.setAttribute("openinference.span.kind", "TOOL");
        logging.logger.emit({severityNumber: SeverityNumber.ERROR, severityText: "ERROR", body: "reference conformance error record", attributes: {expected: true}});
        try {
          return await boundary<PolicyResponse>(policyRequest, async () => {
            callbackInvoked = true;
            if (process.env.FLIGHT_RECORDING_FIXTURE === "1") return RECORDED_POLICY;
            throw new Error("recorded API dependency is unavailable");
          }, {
            logicalKey: "api.policy",
            boundaryClass: "api",
            sideEffect: "read_only",
            replayPolicy: "substitute",
            parentLogicalKey: "agent.answer",
          });
        } finally {
          span.end();
        }
      });

      const modelRequest = {
        model: "flight-fake-v1",
        messages: [{ role: "user", content: input.policy_id }],
      };
      const modelResponse = await tracer.startActiveSpan("model-call", async span => {
        span.setAttribute("openinference.span.kind", "LLM");
        span.setAttribute("gen_ai.tool.definitions", canonical(TOOL_DEFINITIONS));
        try {
          return await boundary<ModelResponse>(modelRequest, async () => {
            modelCallbackInvoked = true;
            const endpoint = process.env.FLIGHT_REPLAY_MODEL === "substitute-only"
              ? "http://unavailable.invalid/v1/chat/completions"
              : MODEL_ENDPOINT;
            const response = await fetch(endpoint, {
              method: "POST",
              headers: { "Content-Type": "application/json" },
              body: canonical(modelRequest),
            });
            if (!response.ok) throw new Error(`model request failed with ${response.status}`);
            return await response.json() as ModelResponse;
          }, {
            logicalKey: "llm.answer",
            boundaryClass: "llm",
            sideEffect: "read_only",
            replayPolicy: "live",
            parentLogicalKey: "agent.answer",
          });
        } finally {
          span.end();
        }
      });

      const result = {
        answer: `${policy.rule}; ${modelResponse.response.choices[0]?.message.content ?? ""}`,
        evidence: retrieved.documents[0]?.content ?? "",
        limit_days: mcpResult.limit_days,
        eligible: toolResult.eligible,
      };
      root.setAttribute("flight.reference.model_callback_invoked", modelCallbackInvoked);
      root.setAttribute("flight.reference.retrieval_callback_invoked", retrievalCallbackInvoked);
      root.setAttribute("flight.reference.mcp_callback_invoked", mcpCallbackInvoked);
      root.setAttribute("flight.reference.tool_callback_invoked", toolCallbackInvoked);
      root.setAttribute("flight.reference.api_callback_invoked", callbackInvoked);
      root.setAttribute("output.value", canonical(result));
      process.stdout.write(`${canonical(result)}\n`);
    } catch (error) {
      logging.logger.emit({severityNumber: SeverityNumber.ERROR, severityText: "ERROR", body: "boundary replay failed", attributes: {error_type: error instanceof Error ? error.name : "unknown"}});
      const result = {
        error: error instanceof Error ? error.message : "unknown boundary error",
        live_callback_invoked: callbackInvoked,
      };
      root.setAttribute("output.value", canonical(result));
      root.setAttribute("flight.reference.model_callback_invoked", modelCallbackInvoked);
      root.setAttribute("flight.reference.retrieval_callback_invoked", retrievalCallbackInvoked);
      root.setAttribute("flight.reference.mcp_callback_invoked", mcpCallbackInvoked);
      root.setAttribute("flight.reference.tool_callback_invoked", toolCallbackInvoked);
      root.setAttribute("flight.reference.api_callback_invoked", callbackInvoked);
      root.setStatus({ code: SpanStatusCode.ERROR, message: "required replay boundary did not match" });
      process.stdout.write(`${canonical(result)}\n`);
      exitCode = 17;
    } finally {
      root.end();
    }
  });

  await logging.forceFlush();
  await logging.shutdown();
  await provider.forceFlush();
  await provider.shutdown();
  return exitCode;
}

process.exitCode = await run();
