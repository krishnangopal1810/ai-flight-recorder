from __future__ import annotations

import hashlib
import inspect
import json
import os
import sys
import threading
from collections.abc import (
    AsyncIterable,
    AsyncIterator,
    Awaitable,
    Callable,
    Iterable,
    Iterator,
    Mapping,
)
from contextvars import ContextVar
from dataclasses import dataclass
from pathlib import Path
from typing import Any, TypeVar, cast

import rfc8785
from opentelemetry import trace

T = TypeVar("T")
E = TypeVar("E")


class BoundaryError(RuntimeError):
    """Raised when controlled replay cannot safely match a required boundary."""


_plan_lock = threading.Lock()
_cached_plan: tuple[str, Mapping[str, Any]] | None = None
_occurrences: dict[tuple[str, str], int] = {}
_run_scope: ContextVar[str | None] = ContextVar("flight_run_scope", default=None)
_integration_depth: ContextVar[int] = ContextVar("flight_integration_depth", default=0)
_LIVE = object()


@dataclass(frozen=True)
class StreamTermination:
    """Stable stream termination evidence; elapsed timing is intentionally excluded."""

    kind: str
    error_type: str | None = None


class BoundarySession:
    """One boundary decision shared by values, typed adapters, and streams."""

    def __init__(
        self,
        logical_key: str,
        request: Any,
        *,
        boundary_class: str,
        side_effect: str = "unknown",
        replay_policy: str = "substitute",
        sandbox: str | None = None,
        parent_logical_key: str | None = None,
        ignore_paths: tuple[str, ...] = (),
        span: trace.Span | None = None,
    ) -> None:
        self.logical_key = logical_key
        self.boundary_class = boundary_class
        self.side_effect = side_effect
        self.replay_policy = replay_policy
        self.parent_logical_key = parent_logical_key
        self.ignore_paths = ignore_paths
        self.span = span or trace.get_current_span()
        self.plan = _load_plan()
        self.occurrence = _next_occurrence(logical_key, boundary_class, parent_logical_key)
        self.request, self.request_overridden = _effective_request(
            self.plan,
            logical_key,
            boundary_class,
            parent_logical_key,
            self.occurrence,
            request,
        )
        self.request_digest = _safe_digest(
            self.request, ignore_paths, self.plan is not None
        )
        _annotate(
            logical_key,
            boundary_class,
            side_effect,
            replay_policy,
            self.request_digest,
            sandbox,
            parent_logical_key,
            self.request,
            ignore_paths,
            span=self.span,
        )
        self.match = (
            _match(
                self.plan,
                logical_key,
                boundary_class,
                parent_logical_key,
                self.request_digest,
                self.occurrence,
            )
            if self.plan is not None
            else None
        )

    def action(self) -> object:
        """Return a recorded response or the private live-call sentinel."""
        if self.plan is None:
            return _LIVE
        if (
            self.match is not None
            and _substitution_allowed(self.plan, self.logical_key)
            and self.match.get("response_state", "captured") == "captured"
        ):
            return self.match.get("response")
        if self.replay_policy == "live" and _live_allowed(self.plan, self.logical_key):
            return _LIVE
        raise BoundaryError(
            f"unmatched_boundary: {self.logical_key} occurrence {self.occurrence}; "
            "live fallback is disabled"
        )

    def invoke(self, call: Callable[..., T]) -> T:
        return _invoke(call, self.request, self.request_overridden)

    async def invoke_async(self, call: Callable[..., Awaitable[T]]) -> T:
        return await _invoke(call, self.request, self.request_overridden)

    def capture_response(self, response: Any) -> None:
        _capture_response(response, span=self.span)


def boundary(
    logical_key: str,
    request: Any,
    call: Callable[..., T],
    *,
    boundary_class: str,
    side_effect: str = "unknown",
    replay_policy: str = "substitute",
    sandbox: str | None = None,
    parent_logical_key: str | None = None,
    ignore_paths: tuple[str, ...] = (),
) -> T:
    session = BoundarySession(
        logical_key,
        request,
        boundary_class=boundary_class,
        side_effect=side_effect,
        replay_policy=replay_policy,
        sandbox=sandbox,
        parent_logical_key=parent_logical_key,
        ignore_paths=ignore_paths,
    )
    action = session.action()
    response = session.invoke(call) if action is _LIVE else cast(T, action)
    session.capture_response(response)
    return response


async def boundary_async(
    logical_key: str,
    request: Any,
    call: Callable[..., Awaitable[T]],
    *,
    boundary_class: str,
    side_effect: str = "unknown",
    replay_policy: str = "substitute",
    sandbox: str | None = None,
    parent_logical_key: str | None = None,
    ignore_paths: tuple[str, ...] = (),
) -> T:
    session = BoundarySession(
        logical_key,
        request,
        boundary_class=boundary_class,
        side_effect=side_effect,
        replay_policy=replay_policy,
        sandbox=sandbox,
        parent_logical_key=parent_logical_key,
        ignore_paths=ignore_paths,
    )
    action = session.action()
    response = await session.invoke_async(call) if action is _LIVE else cast(T, action)
    session.capture_response(response)
    return response


def boundary_stream(
    logical_key: str,
    request: Any,
    call: Callable[..., Iterable[E]],
    *,
    boundary_class: str,
    side_effect: str = "unknown",
    replay_policy: str = "substitute",
    sandbox: str | None = None,
    parent_logical_key: str | None = None,
    ignore_paths: tuple[str, ...] = (),
    encode_event: Callable[[E], Any] = lambda value: value,
    decode_event: Callable[[Any], E] = lambda value: value,
    final_response: Callable[[], Any] | None = None,
) -> Iterator[E]:
    """Record or replay a synchronous stream as exact ordered typed events."""
    session = BoundarySession(
        logical_key,
        request,
        boundary_class=boundary_class,
        side_effect=side_effect,
        replay_policy=replay_policy,
        sandbox=sandbox,
        parent_logical_key=parent_logical_key,
        ignore_paths=ignore_paths,
    )
    action = session.action()
    if action is not _LIVE:
        return _replay_stream(action, decode_event)
    source = session.invoke(call)
    return _record_stream(source, session, encode_event, final_response)


def boundary_async_stream(
    logical_key: str,
    request: Any,
    call: Callable[..., AsyncIterable[E] | Awaitable[AsyncIterable[E]]],
    *,
    boundary_class: str,
    side_effect: str = "unknown",
    replay_policy: str = "substitute",
    sandbox: str | None = None,
    parent_logical_key: str | None = None,
    ignore_paths: tuple[str, ...] = (),
    encode_event: Callable[[E], Any] = lambda value: value,
    decode_event: Callable[[Any], E] = lambda value: value,
    final_response: Callable[[], Any] | None = None,
) -> AsyncIterator[E]:
    """Record or replay an asynchronous stream as exact ordered typed events."""
    session = BoundarySession(
        logical_key,
        request,
        boundary_class=boundary_class,
        side_effect=side_effect,
        replay_policy=replay_policy,
        sandbox=sandbox,
        parent_logical_key=parent_logical_key,
        ignore_paths=ignore_paths,
    )
    action = session.action()
    if action is not _LIVE:
        return _replay_async_stream(action, decode_event)

    async def iterate() -> AsyncIterator[E]:
        source = _invoke(call, session.request, session.request_overridden)
        if inspect.isawaitable(source):
            source = await source
        events: list[dict[str, Any]] = []
        captured = False
        try:
            async for event in source:
                events.append({"type": _event_type(event), "payload": encode_event(event)})
                yield event
        except GeneratorExit:
            captured = True
            session.capture_response(_stream_envelope(events, StreamTermination("closed")))
            raise
        except BaseException as exc:
            captured = True
            session.capture_response(
                _stream_envelope(
                    events, StreamTermination("error", type(exc).__name__)
                )
            )
            raise
        else:
            captured = True
            final = final_response() if final_response is not None else None
            session.capture_response(
                _stream_envelope(events, StreamTermination("completed"), final)
            )
        finally:
            if not captured:
                session.capture_response(_stream_envelope(events, StreamTermination("abandoned")))

    return iterate()


def openai_boundary(logical_key: str, request: Any, call: Callable[..., T]) -> T:
    return boundary(
        logical_key,
        request,
        call,
        boundary_class="llm",
        side_effect="read_only",
        replay_policy="live",
    )


def mcp_boundary(
    logical_key: str, request: Any, call: Callable[..., T], *, side_effect: str = "unknown"
) -> T:
    return boundary(
        logical_key,
        request,
        call,
        boundary_class="mcp",
        side_effect=side_effect,
        replay_policy="substitute",
    )


def _record_stream(
    source: Iterable[E],
    session: BoundarySession,
    encode_event: Callable[[E], Any],
    final_response: Callable[[], Any] | None,
) -> Iterator[E]:
    def iterate() -> Iterator[E]:
        events: list[dict[str, Any]] = []
        captured = False
        try:
            for event in source:
                events.append({"type": _event_type(event), "payload": encode_event(event)})
                yield event
        except GeneratorExit:
            captured = True
            session.capture_response(_stream_envelope(events, StreamTermination("closed")))
            close = getattr(source, "close", None)
            if callable(close):
                close()
            raise
        except BaseException as exc:
            captured = True
            session.capture_response(
                _stream_envelope(events, StreamTermination("error", type(exc).__name__))
            )
            raise
        else:
            captured = True
            final = final_response() if final_response is not None else None
            session.capture_response(
                _stream_envelope(events, StreamTermination("completed"), final)
            )
        finally:
            if not captured:
                session.capture_response(_stream_envelope(events, StreamTermination("abandoned")))

    return iterate()


def _replay_stream(value: Any, decode_event: Callable[[Any], E]) -> Iterator[E]:
    events, termination = _parse_stream_envelope(value)

    def iterate() -> Iterator[E]:
        for event in events:
            yield decode_event(event["payload"])
        _raise_for_replayed_termination(termination)

    return iterate()


def _replay_async_stream(value: Any, decode_event: Callable[[Any], E]) -> AsyncIterator[E]:
    events, termination = _parse_stream_envelope(value)

    async def iterate() -> AsyncIterator[E]:
        for event in events:
            yield decode_event(event["payload"])
        _raise_for_replayed_termination(termination)

    return iterate()


def _stream_envelope(
    events: list[dict[str, Any]], termination: StreamTermination, final: Any = None
) -> dict[str, Any]:
    envelope: dict[str, Any] = {
        "schema_version": "1",
        "kind": "stream",
        "events": events,
        "termination": {"kind": termination.kind},
    }
    if termination.error_type is not None:
        envelope["termination"]["error_type"] = termination.error_type
    if final is not None:
        envelope["final"] = final
    return envelope


def _parse_stream_envelope(
    value: Any,
) -> tuple[list[Mapping[str, Any]], Mapping[str, Any]]:
    if (
        not isinstance(value, Mapping)
        or value.get("schema_version") != "1"
        or value.get("kind") != "stream"
        or not isinstance(value.get("events"), list)
        or not isinstance(value.get("termination"), Mapping)
    ):
        raise BoundaryError("unsafe_replay: recorded stream envelope is malformed")
    events: list[Mapping[str, Any]] = []
    for event in value["events"]:
        if not isinstance(event, Mapping) or "type" not in event or "payload" not in event:
            raise BoundaryError("unsafe_replay: recorded stream event is malformed")
        events.append(event)
    termination = cast(Mapping[str, Any], value["termination"])
    if termination.get("kind") not in {"completed", "closed", "error", "abandoned"}:
        raise BoundaryError("unsafe_replay: recorded stream termination is unsupported")
    return events, termination


def _raise_for_replayed_termination(termination: Mapping[str, Any]) -> None:
    kind = termination.get("kind")
    if kind == "completed":
        return
    if kind == "error":
        error_type = termination.get("error_type", "IntegrationError")
        raise BoundaryError(f"replayed_stream_error: {error_type}")
    raise BoundaryError(f"incomplete_stream: recorded stream terminated as {kind}")


def _event_type(event: Any) -> str:
    if isinstance(event, Mapping) and isinstance(event.get("type"), str):
        return cast(str, event["type"])
    value = getattr(event, "type", None)
    if isinstance(value, str):
        return value
    return type(event).__name__


def _integration_active() -> bool:
    return _integration_depth.get() > 0


class _IntegrationScope:
    def __enter__(self) -> None:
        self._token = _integration_depth.set(_integration_depth.get() + 1)

    def __exit__(self, *_: object) -> None:
        _integration_depth.reset(self._token)


def _integration_scope() -> _IntegrationScope:
    return _IntegrationScope()


def _annotate(
    logical_key: str,
    boundary_class: str,
    side_effect: str,
    replay_policy: str,
    request_digest: str,
    sandbox: str | None,
    parent_logical_key: str | None,
    request: Any,
    ignore_paths: tuple[str, ...],
    *,
    span: trace.Span | None = None,
) -> None:
    try:
        target = span or trace.get_current_span()
        target.set_attribute("flight.logical_key", logical_key)
        target.set_attribute("flight.boundary.logical_key", logical_key)
        target.set_attribute("flight.boundary.class", boundary_class)
        if parent_logical_key is not None:
            target.set_attribute("flight.boundary.parent_logical_key", parent_logical_key)
        target.set_attribute("flight.boundary.side_effect", side_effect)
        target.set_attribute("flight.boundary.replay_policy", replay_policy)
        target.set_attribute("flight.boundary.request_digest", request_digest)
        target.set_attribute("flight.boundary.matching_mode", "exact")
        target.set_attribute("flight.boundary.ignore_paths", json.dumps(ignore_paths))
        _capture_content(target, "flight.input", request, "capture_boundary_request")
        if sandbox is not None:
            target.set_attribute("flight.boundary.sandbox", sandbox)
    except Exception as exc:
        _diagnostic("declare_boundary", exc)


def _capture_response(response: Any, *, span: trace.Span | None = None) -> None:
    try:
        _capture_content(
            span or trace.get_current_span(),
            "flight.output",
            response,
            "capture_boundary_response",
        )
    except Exception as exc:
        _diagnostic("capture_boundary_response", exc)


def _capture_content(span: trace.Span, attribute: str, value: Any, operation: str) -> None:
    try:
        span.set_attribute(attribute, _canonical(value))
    except Exception as exc:
        span.set_attribute(f"{attribute}.omission.reason", "serialization_failed")
        span.set_attribute(f"{attribute}.omission.size", _omission_size(value))
        span.set_attribute(f"{attribute}.omission.media_type", "application/json")
        _diagnostic(operation, exc)


def _omission_size(value: Any) -> int:
    if isinstance(value, str):
        return len(value.encode("utf-8"))
    if isinstance(value, (bytes, bytearray, memoryview)):
        return len(value)
    try:
        return len(json.dumps(value, separators=(",", ":"), ensure_ascii=False).encode())
    except Exception:
        return 0


def _load_plan() -> Mapping[str, Any] | None:
    path = os.environ.get("FLIGHT_REPLAY_PLAN")
    if not path:
        return None
    global _cached_plan
    with _plan_lock:
        if _cached_plan is None or _cached_plan[0] != path:
            try:
                plan = json.loads(Path(path).read_text())
            except (OSError, json.JSONDecodeError) as exc:
                raise BoundaryError("unsafe_replay: replay plan cannot be loaded") from exc
            _cached_plan = (path, plan)
        return _cached_plan[1]


def _match(
    plan: Mapping[str, Any],
    logical_key: str,
    boundary_class: str,
    parent_logical_key: str | None,
    request_digest: str,
    occurrence: int,
) -> Mapping[str, Any] | None:
    candidates: list[Mapping[str, Any]] = [
        item
        for item in plan.get("boundaries", [])
        if item.get("logical_key") == logical_key
        and item.get("boundary_class") == boundary_class
        and item.get("parent_logical_key", "") == (parent_logical_key or "")
    ]
    exact = [item for item in candidates if item.get("request_digest") == request_digest]
    if len(exact) == 1:
        return exact[0]
    repeated = [item for item in exact if item.get("occurrence", 0) == occurrence]
    if len(repeated) == 1:
        return repeated[0]
    if len(exact) > 1:
        raise BoundaryError(f"unmatched_boundary: ambiguous replay match for {logical_key}")
    return None


def _effective_request(
    plan: Mapping[str, Any] | None,
    logical_key: str,
    boundary_class: str,
    parent_logical_key: str | None,
    occurrence: int,
    request: Any,
) -> tuple[Any, bool]:
    if plan is None:
        return request, False
    candidates = [
        item
        for item in plan.get("boundaries", [])
        if item.get("logical_key") == logical_key
        and item.get("boundary_class") == boundary_class
        and item.get("parent_logical_key", "") == (parent_logical_key or "")
        and item.get("occurrence", 0) == occurrence
        and item.get("override_target") == "request"
        and "request_override" in item
    ]
    if len(candidates) > 1:
        raise BoundaryError(f"unmatched_boundary: ambiguous replay override for {logical_key}")
    if not candidates:
        return request, False
    return candidates[0]["request_override"], True


def _invoke(call: Callable[..., T], request: Any, override_required: bool) -> T:
    try:
        accepts_request = len(inspect.signature(call).parameters) > 0
    except (TypeError, ValueError):
        accepts_request = False
    if accepts_request:
        return call(request)
    if override_required:
        raise BoundaryError(
            "unsafe_replay: model input override requires a request-aware boundary callback"
        )
    return call()


def _live_allowed(plan: Mapping[str, Any], logical_key: str) -> bool:
    return logical_key in plan.get("live_boundaries", [])


def _substitution_allowed(plan: Mapping[str, Any], logical_key: str) -> bool:
    return logical_key in plan.get("substitutions", [])


def _next_occurrence(logical_key: str, boundary_class: str, parent_logical_key: str | None) -> int:
    boundary_key = f"{parent_logical_key or ''}\0{boundary_class}\0{logical_key}"
    occurrence_key = (_scope_key(), boundary_key)
    with _plan_lock:
        value = _occurrences.get(occurrence_key, 0)
        _occurrences[occurrence_key] = value + 1
        return value


def _scope_key() -> str:
    explicit = _run_scope.get()
    if explicit is not None:
        return explicit
    current_span = trace.get_current_span()
    get_context = getattr(current_span, "get_span_context", None)
    span_context = get_context() if callable(get_context) else None
    if span_context is not None and span_context.is_valid:
        return f"trace:{span_context.trace_id:032x}"
    try:
        import asyncio

        task = asyncio.current_task()
    except RuntimeError:
        task = None
    if task is not None:
        return f"task:{id(task)}"
    return f"thread:{threading.get_ident()}"


def _digest(value: Any, ignore_paths: tuple[str, ...] = ()) -> str:
    encoded = _canonical(_apply_ignores(value, ignore_paths)).encode()
    return "sha256:" + hashlib.sha256(encoded).hexdigest()


def _safe_digest(value: Any, ignore_paths: tuple[str, ...], replaying: bool) -> str:
    try:
        return _digest(value, ignore_paths)
    except Exception as exc:
        if replaying:
            raise BoundaryError("unmatched_boundary: request is not canonical JSON") from exc
        _diagnostic("boundary_digest", exc)
        return ""


def _diagnostic(operation: str, error: Exception) -> None:
    print(
        json.dumps(
            {
                "level": "warning",
                "code": "flight_sdk_metadata",
                "operation": operation,
                "error_type": type(error).__name__,
            }
        ),
        file=sys.stderr,
    )


def _canonical(value: Any) -> str:
    try:
        return rfc8785.dumps(value).decode("utf-8")
    except (TypeError, ValueError) as exc:
        raise BoundaryError("validation: value is not RFC 8785 JSON") from exc


def _apply_ignores(value: Any, paths: tuple[str, ...]) -> Any:
    if not paths:
        return value
    cloned = json.loads(json.dumps(value))
    for pointer in paths:
        if not pointer.startswith("/"):
            raise BoundaryError(f"unmatched_boundary: invalid JSON pointer {pointer}")
        parts = [item.replace("~1", "/").replace("~0", "~") for item in pointer[1:].split("/")]
        parent = cloned
        for part in parts[:-1]:
            if isinstance(parent, list):
                parent = parent[int(part)]
            elif isinstance(parent, dict):
                parent = parent.get(part)
            else:
                parent = None
            if parent is None:
                break
        if parent is None or not parts:
            continue
        if isinstance(parent, list):
            index = int(parts[-1])
            if 0 <= index < len(parent):
                parent.pop(index)
        elif isinstance(parent, dict):
            parent.pop(parts[-1], None)
    return cloned
