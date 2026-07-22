from __future__ import annotations

import atexit
import json
import os
import sys
import threading
from dataclasses import dataclass
from typing import Any
from urllib.parse import unquote

from opentelemetry import trace


@dataclass
class TelemetryRuntime:
    """Handle for flushing an SDK-owned provider; existing providers remain application-owned."""

    provider: Any
    owned: bool = False

    def force_flush(self, timeout_millis: int = 30_000) -> bool:
        callback = getattr(self.provider, "force_flush", None)
        return bool(callback(timeout_millis) if callable(callback) else True)

    def shutdown(self) -> None:
        if self.owned:
            callback = getattr(self.provider, "shutdown", None)
            if callable(callback):
                callback()


_lock = threading.Lock()
_runtime: TelemetryRuntime | None = None


def configure_telemetry(
    *,
    endpoint: str | None = None,
    headers: dict[str, str] | None = None,
    service_name: str | None = None,
) -> TelemetryRuntime:
    """Install an OTLP trace provider only when the application has not installed one."""
    global _runtime
    with _lock:
        if _runtime is not None:
            return _runtime
        current = trace.get_tracer_provider()
        if current.__class__.__name__ != "ProxyTracerProvider":
            _runtime = TelemetryRuntime(current, owned=False)
            return _runtime

        try:
            from opentelemetry.exporter.otlp.proto.http.trace_exporter import OTLPSpanExporter
            from opentelemetry.sdk.resources import Resource
            from opentelemetry.sdk.trace import TracerProvider
            from opentelemetry.sdk.trace.export import BatchSpanProcessor
        except ImportError as exc:
            raise RuntimeError(
                "telemetry bootstrap requires ai-flight-recorder[integrations] or "
                "ai-flight-recorder[telemetry]"
            ) from exc

        attributes = _resource_attributes()
        attributes.setdefault("service.name", service_name or "ai-application")
        provider = TracerProvider(resource=Resource.create(attributes))
        exporter = OTLPSpanExporter(
            endpoint=endpoint or _trace_endpoint(),
            headers=headers or _trace_headers(),
        )
        provider.add_span_processor(BatchSpanProcessor(exporter))
        trace.set_tracer_provider(provider)
        _runtime = TelemetryRuntime(provider, owned=True)
        atexit.register(_runtime.shutdown)
        return _runtime


def ensure_telemetry() -> TelemetryRuntime | None:
    """Safely bootstrap OTLP only inside a Flight Recorder capture environment."""
    if not _capture_environment():
        return None
    try:
        return configure_telemetry()
    except Exception as exc:
        print(
            json.dumps(
                {
                    "level": "warning",
                    "code": "flight_sdk_metadata",
                    "operation": "configure_telemetry",
                    "error_type": type(exc).__name__,
                }
            ),
            file=sys.stderr,
        )
        return None


def _capture_environment() -> bool:
    if os.environ.get("FLIGHT_PROJECT_ID"):
        return True
    value = os.environ.get("FLIGHT_CAPTURE", "").lower()
    if value in {"1", "true", "yes"}:
        return True
    return _resource_attributes().get("flight.capture", "").lower() == "true"


def _resource_attributes() -> dict[str, str]:
    result: dict[str, str] = {}
    for item in os.environ.get("OTEL_RESOURCE_ATTRIBUTES", "").split(","):
        if "=" not in item:
            continue
        key, value = item.split("=", 1)
        if key.strip():
            result[unquote(key.strip())] = unquote(value.strip())
    return result


def _trace_endpoint() -> str | None:
    signal = os.environ.get("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT")
    if signal:
        return signal
    base = os.environ.get("OTEL_EXPORTER_OTLP_ENDPOINT")
    if not base:
        return None
    return base if base.rstrip("/").endswith("/v1/traces") else base.rstrip("/") + "/v1/traces"


def _trace_headers() -> dict[str, str] | None:
    encoded = os.environ.get("OTEL_EXPORTER_OTLP_TRACES_HEADERS") or os.environ.get(
        "OTEL_EXPORTER_OTLP_HEADERS"
    )
    if not encoded:
        return None
    result: dict[str, str] = {}
    for item in encoded.split(","):
        if "=" in item:
            key, value = item.split("=", 1)
            result[unquote(key.strip())] = unquote(value.strip())
    return result
