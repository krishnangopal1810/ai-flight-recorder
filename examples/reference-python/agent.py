from __future__ import annotations

import json
import logging
import os
import sys
from typing import Any
from urllib.request import Request, urlopen

import rfc8785
from flight_recorder import (
    BoundaryError,
    boundary,
    install_otlp_logging,
    register_entrypoint,
    set_run_provenance,
)
from opentelemetry import trace
from opentelemetry.exporter.otlp.proto.http.trace_exporter import OTLPSpanExporter
from opentelemetry.sdk.resources import Resource
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import BatchSpanProcessor
from opentelemetry.trace import Status, StatusCode


PROJECT_ID = "reference-replay"
SERVICE_NAME = "python-reference-agent"
ENTRYPOINT = ["python", "/app/agent.py"]
RECORDED_POLICY = {"rule": "refunds within 30 days"}
RECORDED_RETRIEVAL = {
    "documents": [{"id": "refund-policy", "content": "grounded policy evidence"}]
}
RECORDED_MCP = {"uri": "policy://refund", "limit_days": 30}
RECORDED_TOOL = {"eligible": True}
TOOL_DEFINITIONS = [
    {
        "type": "function",
        "name": "check_refund_eligibility",
        "description": "Check whether a refund request is within policy",
    }
]
MODEL_HOST = "fake-deps"
MODEL_ENDPOINT = f"http://{MODEL_HOST}:9099/v1/chat/completions"


def canonical(value: Any) -> str:
    return rfc8785.dumps(value).decode("utf-8")


def configure_tracing() -> tuple[TracerProvider, Resource]:
    endpoint = os.environ["OTEL_EXPORTER_OTLP_ENDPOINT"].rstrip("/") + "/v1/traces"
    token = os.environ.get("FLIGHT_OTLP_TOKEN")
    headers = {"Authorization": f"Bearer {token}"} if token else None
    resource = Resource.create(
        {
            "service.name": SERVICE_NAME,
            "flight.project.id": PROJECT_ID,
            "flight.capture": True,
            "flight.sdk.language": "python",
        }
    )
    provider = TracerProvider(resource=resource)
    provider.add_span_processor(
        BatchSpanProcessor(OTLPSpanExporter(endpoint=endpoint, headers=headers, timeout=5))
    )
    trace.set_tracer_provider(provider)
    return provider, resource


def main() -> int:
    root_input = json.load(sys.stdin)
    provider, resource = configure_tracing()
    application_logger = logging.getLogger("ai-flight-recorder.reference-python")
    application_logger.setLevel(logging.INFO)
    log_runtime = install_otlp_logging(
        application_logger,
        resource=resource,
        endpoint=os.environ["OTEL_EXPORTER_OTLP_ENDPOINT"],
        headers={"Authorization": f"Bearer {os.environ['FLIGHT_OTLP_TOKEN']}"}
        if os.environ.get("FLIGHT_OTLP_TOKEN")
        else None,
    )
    tracer = trace.get_tracer("ai-flight-recorder.reference-python", "1.0")
    exit_code = 0
    callback_invoked = False
    model_callback_invoked = False
    retrieval_callback_invoked = False
    mcp_callback_invoked = False
    tool_callback_invoked = False
    effective_model = os.environ.get("FLIGHT_REPLAY_MODEL", "flight-fake-v1")

    with tracer.start_as_current_span("answer-policy") as root:
        application_logger.info("answer policy started", extra={"policy_id": root_input["policy_id"]})
        root.set_attribute("openinference.span.kind", "AGENT")
        root.set_attribute("flight.logical_key", "agent.answer")
        root.set_attribute("input.value", canonical(root_input))
        root.set_attribute("gen_ai.request.model", effective_model)
        set_run_provenance(project_id=PROJECT_ID, commit_sha="a" * 40)
        register_entrypoint(
            ENTRYPOINT,
            image=os.environ.get("FLIGHT_REFERENCE_IMAGE"),
            environment=["MODEL_TOKEN"],
            model_hosts=[MODEL_HOST],
            model_override=True,
            input_mode="stdin_json",
            output_mode="stdout_json",
        )

        retrieval_request = {"query": root_input["policy_id"]}

        def retrieval_dependency() -> dict[str, Any]:
            nonlocal retrieval_callback_invoked
            retrieval_callback_invoked = True
            if os.environ.get("FLIGHT_RECORDING_FIXTURE") == "1":
                return RECORDED_RETRIEVAL
            raise RuntimeError("recorded retrieval dependency is unavailable")

        mcp_request = {"uri": "policy://refund"}

        def mcp_dependency() -> dict[str, Any]:
            nonlocal mcp_callback_invoked
            mcp_callback_invoked = True
            if os.environ.get("FLIGHT_RECORDING_FIXTURE") == "1":
                return RECORDED_MCP
            raise RuntimeError("recorded MCP dependency is unavailable")

        tool_request = {"days_since_purchase": 12, "limit_days": 30}

        def tool_dependency() -> dict[str, bool]:
            nonlocal tool_callback_invoked
            tool_callback_invoked = True
            if os.environ.get("FLIGHT_RECORDING_FIXTURE") == "1":
                return RECORDED_TOOL
            raise RuntimeError("recorded tool dependency is unavailable")

        request = {"policy_id": root_input["policy_id"]}
        if os.environ.get("FLIGHT_REPLAY_MODEL") == "mismatch-request":
            request["policy_id"] = "changed"

        def dependency() -> dict[str, str]:
            nonlocal callback_invoked
            callback_invoked = True
            if os.environ.get("FLIGHT_RECORDING_FIXTURE") == "1":
                return RECORDED_POLICY
            with urlopen("http://unavailable.invalid/policy", timeout=1) as response:
                return json.load(response)

        result: dict[str, Any]
        try:
            with tracer.start_as_current_span("policy-retrieval") as retrieval_span:
                retrieval_span.set_attribute("openinference.span.kind", "RETRIEVER")
                retrieved = boundary(
                    "retriever.policy",
                    retrieval_request,
                    retrieval_dependency,
                    boundary_class="retriever",
                    side_effect="read_only",
                    replay_policy="substitute",
                    parent_logical_key="agent.answer",
                )

            with tracer.start_as_current_span("policy-resource") as mcp_span:
                mcp_span.set_attribute("flight.operation.kind", "mcp")
                mcp_result = boundary(
                    "mcp.policy",
                    mcp_request,
                    mcp_dependency,
                    boundary_class="mcp",
                    side_effect="read_only",
                    replay_policy="substitute",
                    parent_logical_key="agent.answer",
                )

            with tracer.start_as_current_span("eligibility-tool") as tool_span:
                tool_span.set_attribute("openinference.span.kind", "TOOL")
                application_logger.warning(
                    "checking refund eligibility", extra={"days_since_purchase": 12}
                )
                tool_result = boundary(
                    "tool.eligibility",
                    tool_request,
                    tool_dependency,
                    boundary_class="tool",
                    side_effect="read_only",
                    replay_policy="substitute",
                    parent_logical_key="agent.answer",
                )

            with tracer.start_as_current_span("policy-lookup") as policy_span:
                policy_span.set_attribute("openinference.span.kind", "TOOL")
                application_logger.error(
                    "reference conformance error record", extra={"expected": True}
                )
                policy = boundary(
                    "api.policy",
                    request,
                    dependency,
                    boundary_class="api",
                    side_effect="read_only",
                    replay_policy="substitute",
                    parent_logical_key="agent.answer",
                )

            model_request = {
                "model": effective_model,
                "messages": [{"role": "user", "content": root_input["policy_id"]}],
            }

            def model_dependency() -> dict[str, Any]:
                nonlocal model_callback_invoked
                model_callback_invoked = True
                request = Request(
                    MODEL_ENDPOINT,
                    data=canonical(model_request).encode("utf-8"),
                    headers={"Content-Type": "application/json"},
                    method="POST",
                )
                with urlopen(request, timeout=2) as response:
                    return json.load(response)

            with tracer.start_as_current_span("model-call") as model_span:
                model_span.set_attribute("openinference.span.kind", "LLM")
                model_span.set_attribute("gen_ai.request.model", effective_model)
                model_span.set_attribute("gen_ai.tool.definitions", canonical(TOOL_DEFINITIONS))
                model_response = boundary(
                    "llm.answer",
                    model_request,
                    model_dependency,
                    boundary_class="llm",
                    side_effect="read_only",
                    replay_policy="live",
                    parent_logical_key="agent.answer",
                )
            model_content = model_response["response"]["choices"][0]["message"]["content"]
            result = {
                "answer": f"{policy['rule']}; {model_content}",
                "evidence": retrieved["documents"][0]["content"],
                "limit_days": mcp_result["limit_days"],
                "eligible": tool_result["eligible"],
            }
            root.set_attribute(
                "flight.reference.model_callback_invoked", model_callback_invoked
            )
            root.set_attribute(
                "flight.reference.retrieval_callback_invoked",
                retrieval_callback_invoked,
            )
            root.set_attribute("flight.reference.mcp_callback_invoked", mcp_callback_invoked)
            root.set_attribute("flight.reference.tool_callback_invoked", tool_callback_invoked)
            root.set_attribute("flight.reference.api_callback_invoked", callback_invoked)
            root.set_attribute("output.value", canonical(result))
            print(canonical(result))
        except BoundaryError as error:
            application_logger.error("boundary replay failed", extra={"error_type": type(error).__name__})
            result = {
                "error": str(error),
                "live_callback_invoked": callback_invoked,
            }
            root.set_attribute("output.value", canonical(result))
            root.set_attribute(
                "flight.reference.model_callback_invoked", model_callback_invoked
            )
            root.set_attribute(
                "flight.reference.retrieval_callback_invoked",
                retrieval_callback_invoked,
            )
            root.set_attribute("flight.reference.mcp_callback_invoked", mcp_callback_invoked)
            root.set_attribute("flight.reference.tool_callback_invoked", tool_callback_invoked)
            root.set_attribute("flight.reference.api_callback_invoked", callback_invoked)
            root.set_status(Status(StatusCode.ERROR, "required replay boundary did not match"))
            print(canonical(result))
            exit_code = 17

    if not log_runtime.force_flush(timeout_millis=5_000):
        return 18
    log_runtime.shutdown()
    if not provider.force_flush(timeout_millis=5_000):
        return 19
    provider.shutdown()
    return exit_code


if __name__ == "__main__":
    raise SystemExit(main())
