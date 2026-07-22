from __future__ import annotations

import inspect
import uuid
from collections.abc import Awaitable, Callable, Mapping
from typing import Any, TypeVar

from opentelemetry import trace
from opentelemetry.trace import Status, StatusCode, Tracer

from .boundary import _capture_content, _run_scope
from .telemetry import ensure_telemetry

T = TypeVar("T")


def record_run(
    logical_key: str,
    run_input: Any,
    call: Callable[..., T],
    *,
    tracer: Tracer | None = None,
    attributes: Mapping[str, str | bool | int | float] | None = None,
) -> T:
    """Run one application entrypoint in a root agent span and capture its result."""
    ensure_telemetry()
    active_tracer = tracer or trace.get_tracer("ai-flight-recorder", "0.1.0")
    with active_tracer.start_as_current_span(logical_key) as span:
        _annotate_root(span, logical_key, run_input, attributes)
        token = _run_scope.set(f"run:{uuid.uuid4().hex}")
        try:
            result = _invoke(call, run_input)
            _capture_content(span, "flight.output", result, "capture_run_output")
            span.set_status(Status(StatusCode.OK))
            return result
        except BaseException as exc:
            span.record_exception(exc)
            span.set_status(Status(StatusCode.ERROR, type(exc).__name__))
            raise
        finally:
            _run_scope.reset(token)


async def record_run_async(
    logical_key: str,
    run_input: Any,
    call: Callable[..., Awaitable[T]],
    *,
    tracer: Tracer | None = None,
    attributes: Mapping[str, str | bool | int | float] | None = None,
) -> T:
    """Asynchronous form of :func:`record_run`."""
    ensure_telemetry()
    active_tracer = tracer or trace.get_tracer("ai-flight-recorder", "0.1.0")
    with active_tracer.start_as_current_span(logical_key) as span:
        _annotate_root(span, logical_key, run_input, attributes)
        token = _run_scope.set(f"run:{uuid.uuid4().hex}")
        try:
            result = await _invoke(call, run_input)
            _capture_content(span, "flight.output", result, "capture_run_output")
            span.set_status(Status(StatusCode.OK))
            return result
        except BaseException as exc:
            span.record_exception(exc)
            span.set_status(Status(StatusCode.ERROR, type(exc).__name__))
            raise
        finally:
            _run_scope.reset(token)


def _annotate_root(
    span: trace.Span,
    logical_key: str,
    run_input: Any,
    attributes: Mapping[str, str | bool | int | float] | None,
) -> None:
    span.set_attribute("flight.capture", True)
    span.set_attribute("flight.logical_key", logical_key)
    span.set_attribute("flight.operation.kind", "agent")
    span.set_attribute("openinference.span.kind", "AGENT")
    span.set_attribute("gen_ai.operation.name", "invoke_agent")
    for key, value in (attributes or {}).items():
        span.set_attribute(key, value)
    _capture_content(span, "flight.input", run_input, "capture_run_input")


def _invoke(call: Callable[..., T], run_input: Any) -> T:
    try:
        accepts_input = len(inspect.signature(call).parameters) > 0
    except (TypeError, ValueError):
        accepts_input = False
    if accepts_input:
        return call(run_input)
    return call()
