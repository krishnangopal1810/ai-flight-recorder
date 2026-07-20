from __future__ import annotations

import json
import os
import sys
from typing import Any
from urllib.request import Request, urlopen

import rfc8785
from flight_recorder import BoundaryError, boundary, register_entrypoint, set_run_provenance
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
MODEL_HOST = "fake-deps"
MODEL_ENDPOINT = f"http://{MODEL_HOST}:9099/v1/chat/completions"


def canonical(value: Any) -> str:
    return rfc8785.dumps(value).decode("utf-8")


def configure_tracing() -> TracerProvider:
    endpoint = os.environ["OTEL_EXPORTER_OTLP_ENDPOINT"].rstrip("/") + "/v1/traces"
    token = os.environ.get("FLIGHT_OTLP_TOKEN")
    headers = {"Authorization": f"Bearer {token}"} if token else None
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
    provider.add_span_processor(
        BatchSpanProcessor(OTLPSpanExporter(endpoint=endpoint, headers=headers, timeout=5))
    )
    trace.set_tracer_provider(provider)
    return provider


def main() -> int:
    root_input = json.load(sys.stdin)
    provider = configure_tracing()
    tracer = trace.get_tracer("ai-flight-recorder.reference-python", "1.0")
    exit_code = 0
    callback_invoked = False
    model_callback_invoked = False

    with tracer.start_as_current_span("answer-policy") as root:
        root.set_attribute("openinference.span.kind", "AGENT")
        root.set_attribute("flight.logical_key", "agent.answer")
        root.set_attribute("input.value", canonical(root_input))
        set_run_provenance(project_id=PROJECT_ID, commit_sha="a" * 40)
        register_entrypoint(
            ENTRYPOINT,
            image=os.environ.get("FLIGHT_REFERENCE_IMAGE"),
            model_hosts=[MODEL_HOST],
            input_mode="stdin_json",
            output_mode="stdout_json",
        )

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
            with tracer.start_as_current_span("policy-lookup") as policy_span:
                policy_span.set_attribute("openinference.span.kind", "TOOL")
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
                "model": "flight-fake-v1",
                "messages": [{"role": "user", "content": root_input["policy_id"]}],
            }

            def model_dependency() -> dict[str, Any]:
                nonlocal model_callback_invoked
                model_callback_invoked = True
                endpoint = MODEL_ENDPOINT
                if os.environ.get("FLIGHT_REPLAY_MODEL") == "substitute-only":
                    endpoint = "http://unavailable.invalid/v1/chat/completions"
                request = Request(
                    endpoint,
                    data=canonical(model_request).encode("utf-8"),
                    headers={"Content-Type": "application/json"},
                    method="POST",
                )
                with urlopen(request, timeout=2) as response:
                    return json.load(response)

            with tracer.start_as_current_span("model-call") as model_span:
                model_span.set_attribute("openinference.span.kind", "LLM")
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
            result = {"answer": f"{policy['rule']}; {model_content}"}
            root.set_attribute(
                "flight.reference.model_callback_invoked", model_callback_invoked
            )
            root.set_attribute("output.value", canonical(result))
            print(canonical(result))
        except BoundaryError as error:
            result = {
                "error": str(error),
                "live_callback_invoked": callback_invoked,
            }
            root.set_attribute("output.value", canonical(result))
            root.set_attribute(
                "flight.reference.model_callback_invoked", model_callback_invoked
            )
            root.set_status(Status(StatusCode.ERROR, "required replay boundary did not match"))
            print(canonical(result))
            exit_code = 17

    if not provider.force_flush(timeout_millis=5_000):
        return 18
    provider.shutdown()
    return exit_code


if __name__ == "__main__":
    raise SystemExit(main())
