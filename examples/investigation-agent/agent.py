from __future__ import annotations

import json
import os
import sys
from typing import Any
from urllib.request import Request, urlopen

import rfc8785
from flight_recorder import boundary, register_entrypoint, set_run_provenance
from opentelemetry import trace
from opentelemetry.exporter.otlp.proto.http.trace_exporter import OTLPSpanExporter
from opentelemetry.sdk.resources import Resource
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import BatchSpanProcessor
from opentelemetry.trace import Status, StatusCode


PROJECT_ID = "refund-operations"
SERVICE_NAME = "refund-review-agent"
MODEL_HOST = "fake-deps"
MODEL_ENDPOINT = f"http://{MODEL_HOST}:9099/v1/chat/completions"
POLICY = {"refund_window_days": 30, "maximum_amount_usd": 500}


def canonical(value: Any) -> str:
    return rfc8785.dumps(value).decode("utf-8")


def configure_tracing() -> TracerProvider:
    endpoint = os.environ["OTEL_EXPORTER_OTLP_ENDPOINT"].rstrip("/") + "/v1/traces"
    token = os.environ.get("FLIGHT_OTLP_TOKEN")
    provider = TracerProvider(
        resource=Resource.create(
            {
                "service.name": SERVICE_NAME,
                "flight.project.id": PROJECT_ID,
                "flight.capture": True,
                "flight.sdk.language": "python",
            }
        )
    )
    headers = {"Authorization": f"Bearer {token}"} if token else None
    provider.add_span_processor(
        BatchSpanProcessor(OTLPSpanExporter(endpoint=endpoint, headers=headers, timeout=5))
    )
    trace.set_tracer_provider(provider)
    return provider


def model_decision(request_body: dict[str, Any]) -> dict[str, Any]:
    request = Request(
        MODEL_ENDPOINT,
        data=canonical(request_body).encode("utf-8"),
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    with urlopen(request, timeout=3) as response:
        envelope = json.load(response)
    content = envelope["response"]["choices"][0]["message"]["content"]
    return json.loads(content)


def main() -> int:
    incident = json.load(sys.stdin)
    provider = configure_tracing()
    tracer = trace.get_tracer("refund-review-agent", "1.0")
    model_variant = os.environ.get("FLIGHT_REPLAY_MODEL", "baseline-conservative")

    with tracer.start_as_current_span("review-refund-request") as root:
        root.set_attribute("openinference.span.kind", "AGENT")
        root.set_attribute("flight.logical_key", "agent.refund-review")
        root.set_attribute("input.value", canonical(incident))
        set_run_provenance(project_id=PROJECT_ID, commit_sha="d" * 40)
        register_entrypoint(
            ["python", "/app/agent.py"],
            image=os.environ.get("FLIGHT_REPLAY_IMAGE"),
            model_hosts=[MODEL_HOST],
            input_mode="stdin_json",
            output_mode="stdout_json",
        )

        with tracer.start_as_current_span("retrieve-refund-policy") as policy_span:
            policy_span.set_attribute("openinference.span.kind", "RETRIEVER")
            policy = boundary(
                "retriever.refund-policy",
                {"policy": "refunds"},
                lambda: POLICY,
                boundary_class="retriever",
                side_effect="read_only",
                replay_policy="substitute",
                parent_logical_key="agent.refund-review",
            )

        eligibility_request = {
            "days_since_purchase": incident["days_since_purchase"],
            "amount_usd": incident["amount_usd"],
            "refund_window_days": policy["refund_window_days"],
            "maximum_amount_usd": policy["maximum_amount_usd"],
        }
        with tracer.start_as_current_span("check-refund-eligibility") as tool_span:
            tool_span.set_attribute("openinference.span.kind", "TOOL")
            eligibility = boundary(
                "tool.refund-eligibility",
                eligibility_request,
                lambda: {
                    "eligible": (
                        eligibility_request["days_since_purchase"]
                        <= eligibility_request["refund_window_days"]
                        and eligibility_request["amount_usd"]
                        <= eligibility_request["maximum_amount_usd"]
                    )
                },
                boundary_class="tool",
                side_effect="read_only",
                replay_policy="substitute",
                parent_logical_key="agent.refund-review",
            )

        instruction = (
            "Follow the retrieved policy exactly. Approve every eligible request."
            if model_variant == "policy-grounded"
            else "Be conservative and minimize refunds even when a request is eligible."
        )
        model_request = {
            "model": "flight-fake-v1",
            "messages": [
                {
                    "role": "system",
                    "content": f"refund-decision {model_variant}: {instruction}",
                },
                {
                    "role": "user",
                    "content": canonical({"incident": incident, "eligibility": eligibility}),
                },
            ],
        }
        with tracer.start_as_current_span("refund-decision") as model_span:
            model_span.set_attribute("openinference.span.kind", "LLM")
            model_span.set_attribute("gen_ai.request.model", "flight-fake-v1")
            decision = boundary(
                "llm.refund-decision",
                model_request,
                lambda effective_request: model_decision(effective_request),
                boundary_class="llm",
                side_effect="read_only",
                replay_policy="live",
                parent_logical_key="agent.refund-review",
            )
            if eligibility["eligible"] and decision["decision"] != "approve":
                model_span.set_status(
                    Status(StatusCode.ERROR, "eligible refund was denied by the model")
                )

        result = {
            "request_id": incident["request_id"],
            "eligible": eligibility["eligible"],
            "decision": decision["decision"],
            "reason": decision["reason"],
        }
        root.set_attribute("output.value", canonical(result))
        if eligibility["eligible"] and decision["decision"] != "approve":
            root.set_status(Status(StatusCode.ERROR, "eligible refund was denied"))
        print(canonical(result))

    if not provider.force_flush(timeout_millis=5_000):
        return 18
    provider.shutdown()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
