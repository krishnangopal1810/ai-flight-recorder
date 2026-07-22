import asyncio
import hashlib
import json

import pytest

from flight_recorder import BoundaryError, boundary, boundary_async, record_run_async
from flight_recorder import boundary as boundary_module


def digest(value):
    raw = json.dumps(value, sort_keys=True, separators=(",", ":")).encode()
    return "sha256:" + hashlib.sha256(raw).hexdigest()


@pytest.fixture(autouse=True)
def reset_state(monkeypatch):
    monkeypatch.delenv("FLIGHT_REPLAY_PLAN", raising=False)
    boundary_module.__globals__["_cached_plan"] = None
    boundary_module.__globals__["_occurrences"].clear()


def test_live_call_without_replay():
    assert boundary(
        "weather",
        {"city": "Pune"},
        lambda: {"temp": 25},
        boundary_class="api",
        side_effect="read_only",
    ) == {"temp": 25}


def test_metadata_failure_never_fails_production_request(capsys, monkeypatch):
    class RecordingSpan:
        def __init__(self):
            self.attributes = {}

        def set_attribute(self, key, value):
            self.attributes[key] = value

    span = RecordingSpan()
    monkeypatch.setattr(boundary_module.__globals__["trace"], "get_current_span", lambda: span)
    marker = object()
    assert boundary("opaque", marker, lambda: "ok", boundary_class="api") == "ok"
    diagnostic = capsys.readouterr().err
    assert "flight_sdk_metadata" in diagnostic
    assert repr(marker) not in diagnostic
    assert span.attributes["flight.input.omission.reason"] == "serialization_failed"
    assert span.attributes["flight.input.omission.size"] == 0
    assert span.attributes["flight.input.omission.media_type"] == "application/json"


def test_substitutes_exact_recorded_boundary(tmp_path, monkeypatch):
    request = {"city": "Pune"}
    plan = {
        "substitutions": ["weather"],
        "boundaries": [
            {
                "logical_key": "weather",
                "boundary_class": "api",
                "parent_logical_key": "agent.root",
                "occurrence": 0,
                "request_digest": digest(request),
                "response": {"temp": 24},
            }
        ],
    }
    filename = tmp_path / "plan.json"
    filename.write_text(json.dumps(plan))
    monkeypatch.setenv("FLIGHT_REPLAY_PLAN", str(filename))
    assert boundary(
        "weather",
        request,
        lambda: pytest.fail("live call"),
        boundary_class="api",
        parent_logical_key="agent.root",
    ) == {"temp": 24}


def test_model_input_override_is_passed_to_request_aware_live_callback(tmp_path, monkeypatch):
    edited = {"messages": [{"role": "system", "content": "approve eligible refunds"}]}
    plan = {
        "live_boundaries": ["llm.decision"],
        "boundaries": [
            {
                "logical_key": "llm.decision",
                "boundary_class": "llm",
                "parent_logical_key": "agent.root",
                "occurrence": 0,
                "request_digest": digest(edited),
                "override_target": "request",
                "request_override": edited,
            }
        ],
    }
    filename = tmp_path / "plan.json"
    filename.write_text(json.dumps(plan))
    monkeypatch.setenv("FLIGHT_REPLAY_PLAN", str(filename))
    assert boundary(
        "llm.decision",
        {"messages": [{"role": "system", "content": "deny"}]},
        lambda effective_request: {"received": effective_request},
        boundary_class="llm",
        side_effect="read_only",
        replay_policy="live",
        parent_logical_key="agent.root",
    ) == {"received": edited}


def test_model_input_override_fails_closed_for_legacy_callback(tmp_path, monkeypatch):
    edited = {"prompt": "changed"}
    plan = {
        "live_boundaries": ["llm.decision"],
        "boundaries": [
            {
                "logical_key": "llm.decision",
                "boundary_class": "llm",
                "occurrence": 0,
                "request_digest": digest(edited),
                "override_target": "request",
                "request_override": edited,
            }
        ],
    }
    filename = tmp_path / "plan.json"
    filename.write_text(json.dumps(plan))
    monkeypatch.setenv("FLIGHT_REPLAY_PLAN", str(filename))
    with pytest.raises(BoundaryError, match="request-aware boundary callback"):
        boundary(
            "llm.decision",
            {"prompt": "original"},
            lambda: {"unexpected": True},
            boundary_class="llm",
            side_effect="read_only",
            replay_policy="live",
        )


def test_json_pointer_ignore_rule_is_deterministic():
    first = {"query": "hello", "request_id": "one"}
    second = {"query": "hello", "request_id": "two"}
    digest_with_ignore = boundary_module.__globals__["_digest"]
    assert digest_with_ignore(first, ("/request_id",)) == digest_with_ignore(
        second, ("/request_id",)
    )


def test_rfc8785_digest_is_cross_sdk_stable():
    digest_value = boundary_module.__globals__["_digest"]
    assert (
        digest_value({"b": 1.0, "a": "€"})
        == "sha256:2c6fbbec1f6bfa5ea5d93187c7f6960ae2fbc3f05c442ebb923b9a6777b85c76"
    )


def test_parent_or_kind_mismatch_never_substitutes(tmp_path, monkeypatch):
    request = {"city": "Pune"}
    plan = {
        "substitutions": ["weather"],
        "boundaries": [
            {
                "logical_key": "weather",
                "boundary_class": "tool",
                "parent_logical_key": "other",
                "occurrence": 0,
                "request_digest": digest(request),
                "response": {"temp": 24},
            }
        ],
    }
    filename = tmp_path / "plan.json"
    filename.write_text(json.dumps(plan))
    monkeypatch.setenv("FLIGHT_REPLAY_PLAN", str(filename))
    with pytest.raises(BoundaryError, match="live fallback is disabled"):
        boundary(
            "weather",
            request,
            lambda: pytest.fail("live call"),
            boundary_class="api",
            parent_logical_key="agent.root",
        )


def test_missing_boundary_never_falls_through(tmp_path, monkeypatch):
    filename = tmp_path / "plan.json"
    filename.write_text('{"boundaries": []}')
    monkeypatch.setenv("FLIGHT_REPLAY_PLAN", str(filename))
    with pytest.raises(BoundaryError, match="live fallback is disabled"):
        boundary(
            "payment",
            {"amount": 100},
            lambda: pytest.fail("mutation"),
            boundary_class="tool",
            side_effect="mutating",
        )


async def test_async_boundary_without_replay():
    async def call():
        await asyncio.sleep(0)
        return "ok"

    assert (
        await boundary_async("async", {}, call, boundary_class="api", side_effect="read_only")
        == "ok"
    )


def test_unique_digest_matches_before_occurrence(tmp_path, monkeypatch):
    first = {"id": "first"}
    second = {"id": "second"}
    plan = {
        "substitutions": ["lookup"],
        "boundaries": [
            {
                "logical_key": "lookup",
                "boundary_class": "api",
                "occurrence": 0,
                "request_digest": digest(first),
                "response": "first-result",
            },
            {
                "logical_key": "lookup",
                "boundary_class": "api",
                "occurrence": 1,
                "request_digest": digest(second),
                "response": "second-result",
            },
        ],
    }
    filename = tmp_path / "plan.json"
    filename.write_text(json.dumps(plan))
    monkeypatch.setenv("FLIGHT_REPLAY_PLAN", str(filename))
    assert boundary("lookup", second, lambda: pytest.fail(), boundary_class="api") == (
        "second-result"
    )


def test_duplicate_digest_and_occurrence_is_ambiguous(tmp_path, monkeypatch):
    request = {"id": "same"}
    recorded = {
        "logical_key": "lookup",
        "boundary_class": "api",
        "occurrence": 0,
        "request_digest": digest(request),
        "response": "result",
    }
    filename = tmp_path / "plan.json"
    filename.write_text(
        json.dumps({"substitutions": ["lookup"], "boundaries": [recorded, recorded]})
    )
    monkeypatch.setenv("FLIGHT_REPLAY_PLAN", str(filename))
    with pytest.raises(BoundaryError, match="ambiguous replay match"):
        boundary("lookup", request, lambda: pytest.fail(), boundary_class="api")


async def test_concurrent_root_runs_have_independent_occurrences(tmp_path, monkeypatch):
    request = {"id": "same"}
    filename = tmp_path / "plan.json"
    filename.write_text(
        json.dumps(
            {
                "substitutions": ["lookup"],
                "boundaries": [
                    {
                        "logical_key": "lookup",
                        "boundary_class": "api",
                        "occurrence": 0,
                        "request_digest": digest(request),
                        "response": "recorded",
                    }
                ],
            }
        )
    )
    monkeypatch.setenv("FLIGHT_REPLAY_PLAN", str(filename))

    async def run(_input):
        return await boundary_async(
            "lookup", request, lambda: pytest.fail(), boundary_class="api"
        )

    assert await asyncio.gather(
        record_run_async("root.one", {}, run),
        record_run_async("root.two", {}, run),
    ) == ["recorded", "recorded"]
