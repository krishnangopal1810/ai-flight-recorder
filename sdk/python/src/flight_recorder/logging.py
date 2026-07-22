from __future__ import annotations

import logging
import threading
from collections.abc import Mapping
from dataclasses import dataclass, field
from typing import Any


@dataclass
class LoggingRuntime:
    logger: logging.Logger
    provider: Any
    handler: logging.Handler
    _closed: bool = False
    _lock: threading.Lock = field(default_factory=threading.Lock, repr=False)

    def force_flush(self, timeout_millis: int = 30_000) -> bool:
        with self._lock:
            if self._closed:
                return True
            return bool(self.provider.force_flush(timeout_millis=timeout_millis))

    def shutdown(self) -> None:
        with self._lock:
            if self._closed:
                return
            self.logger.removeHandler(self.handler)
            self.provider.shutdown()
            self._closed = True


def install_otlp_logging(
    logger: logging.Logger,
    *,
    resource: Any | None = None,
    endpoint: str | None = None,
    headers: Mapping[str, str] | None = None,
    level: int = logging.INFO,
) -> LoggingRuntime:
    """Attach an OTLP handler to one standard-library logger.

    The OpenTelemetry SDK and exporter are optional dependencies. Install them
    with ``ai-flight-recorder[logs]`` before calling this function.
    """

    try:
        from opentelemetry.exporter.otlp.proto.http._log_exporter import OTLPLogExporter
        from opentelemetry.sdk._logs import LoggerProvider, LoggingHandler
        from opentelemetry.sdk._logs.export import BatchLogRecordProcessor
    except ImportError as exc:  # pragma: no cover - exercised without optional dependencies.
        raise RuntimeError(
            "OTLP logging requires the ai-flight-recorder[logs] optional dependencies"
        ) from exc

    for existing in logger.handlers:
        if getattr(existing, "_flight_otlp_handler", False):
            raise RuntimeError("this logger already has a Flight Recorder OTLP handler")

    exporter_options: dict[str, Any] = {}
    if endpoint is not None:
        exporter_options["endpoint"] = _logs_endpoint(endpoint)
    if headers is not None:
        exporter_options["headers"] = dict(headers)
    exporter = OTLPLogExporter(**exporter_options)
    provider_options = {"resource": resource} if resource is not None else {}
    provider = LoggerProvider(**provider_options)
    provider.add_log_record_processor(BatchLogRecordProcessor(exporter))
    handler = LoggingHandler(level=level, logger_provider=provider)
    setattr(handler, "_flight_otlp_handler", True)
    logger.addHandler(handler)
    return LoggingRuntime(logger=logger, provider=provider, handler=handler)


def _logs_endpoint(endpoint: str) -> str:
    normalized = endpoint.rstrip("/")
    if normalized.endswith("/v1/logs"):
        return normalized
    return normalized + "/v1/logs"
