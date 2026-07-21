from __future__ import annotations

import hashlib
import inspect
import json
import os
import sys
import threading
from collections.abc import Awaitable, Callable, Mapping
from pathlib import Path
from typing import Any, TypeVar

import rfc8785
from opentelemetry import trace

T = TypeVar("T")


class BoundaryError(RuntimeError):
    """Raised when controlled replay cannot safely match a required boundary."""


_plan_lock = threading.Lock()
_cached_plan: tuple[str, Mapping[str, Any]] | None = None
_occurrences: dict[str, int] = {}


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
    plan = _load_plan()
    occurrence = _next_occurrence(logical_key, boundary_class, parent_logical_key)
    effective_request, request_overridden = _effective_request(
        plan, logical_key, boundary_class, parent_logical_key, occurrence, request
    )
    request_digest = _safe_digest(effective_request, ignore_paths, plan is not None)
    _annotate(
        logical_key,
        boundary_class,
        side_effect,
        replay_policy,
        request_digest,
        sandbox,
        parent_logical_key,
        effective_request,
        ignore_paths,
    )
    if plan is None:
        response = _invoke(call, effective_request, False)
        _capture_response(response)
        return response
    match = _match(
        plan, logical_key, boundary_class, parent_logical_key, request_digest, occurrence
    )
    if (
        match is not None
        and _substitution_allowed(plan, logical_key)
        and match.get("response_state", "captured") == "captured"
    ):
        recorded_response = match.get("response")
        _capture_response(recorded_response)
        return recorded_response  # type: ignore[return-value]
    if replay_policy == "live" and _live_allowed(plan, logical_key):
        response = _invoke(call, effective_request, request_overridden)
        _capture_response(response)
        return response
    raise BoundaryError(
        f"unmatched_boundary: {logical_key} occurrence {occurrence}; live fallback is disabled"
    )


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
    plan = _load_plan()
    occurrence = _next_occurrence(logical_key, boundary_class, parent_logical_key)
    effective_request, request_overridden = _effective_request(
        plan, logical_key, boundary_class, parent_logical_key, occurrence, request
    )
    request_digest = _safe_digest(effective_request, ignore_paths, plan is not None)
    _annotate(
        logical_key,
        boundary_class,
        side_effect,
        replay_policy,
        request_digest,
        sandbox,
        parent_logical_key,
        effective_request,
        ignore_paths,
    )
    if plan is None:
        response = await _invoke(call, effective_request, False)
        _capture_response(response)
        return response
    match = _match(
        plan, logical_key, boundary_class, parent_logical_key, request_digest, occurrence
    )
    if (
        match is not None
        and _substitution_allowed(plan, logical_key)
        and match.get("response_state", "captured") == "captured"
    ):
        recorded_response = match.get("response")
        _capture_response(recorded_response)
        return recorded_response  # type: ignore[return-value]
    if replay_policy == "live" and _live_allowed(plan, logical_key):
        response = await _invoke(call, effective_request, request_overridden)
        _capture_response(response)
        return response
    raise BoundaryError(
        f"unmatched_boundary: {logical_key} occurrence {occurrence}; live fallback is disabled"
    )


def openai_boundary(logical_key: str, request: Any, call: Callable[[], T]) -> T:
    return boundary(
        logical_key,
        request,
        call,
        boundary_class="llm",
        side_effect="read_only",
        replay_policy="live",
    )


def mcp_boundary(
    logical_key: str, request: Any, call: Callable[[], T], *, side_effect: str = "unknown"
) -> T:
    return boundary(
        logical_key,
        request,
        call,
        boundary_class="mcp",
        side_effect=side_effect,
        replay_policy="substitute",
    )


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
) -> None:
    try:
        span = trace.get_current_span()
        span.set_attribute("flight.logical_key", logical_key)
        span.set_attribute("flight.boundary.logical_key", logical_key)
        span.set_attribute("flight.boundary.class", boundary_class)
        if parent_logical_key is not None:
            span.set_attribute("flight.boundary.parent_logical_key", parent_logical_key)
        span.set_attribute("flight.boundary.side_effect", side_effect)
        span.set_attribute("flight.boundary.replay_policy", replay_policy)
        span.set_attribute("flight.boundary.request_digest", request_digest)
        span.set_attribute("flight.boundary.matching_mode", "exact")
        span.set_attribute("flight.boundary.ignore_paths", json.dumps(ignore_paths))
        _capture_content(span, "flight.input", request, "capture_boundary_request")
        if sandbox is not None:
            span.set_attribute("flight.boundary.sandbox", sandbox)
    except Exception as exc:
        _diagnostic("declare_boundary", exc)


def _capture_response(response: Any) -> None:
    try:
        _capture_content(
            trace.get_current_span(),
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
    candidates = [
        item
        for item in plan.get("boundaries", [])
        if item.get("logical_key") == logical_key
        and item.get("boundary_class") == boundary_class
        and item.get("parent_logical_key", "") == (parent_logical_key or "")
        and item.get("occurrence", 0) == occurrence
    ]
    exact = [item for item in candidates if item.get("request_digest") == request_digest]
    if len(exact) == 1:
        return exact[0]
    if len(exact) > 1 or len(candidates) > 1:
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
    occurrence_key = f"{parent_logical_key or ''}\0{boundary_class}\0{logical_key}"
    with _plan_lock:
        value = _occurrences.get(occurrence_key, 0)
        _occurrences[occurrence_key] = value + 1
        return value


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
