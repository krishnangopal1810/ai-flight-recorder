from __future__ import annotations

import base64
import hashlib
import json
import os
import sys
from collections.abc import Iterable, Mapping
from typing import Any

from opentelemetry import trace


def _safe(operation: str, callback: Any) -> None:
    try:
        callback(trace.get_current_span())
    except Exception as exc:  # Production requests must survive metadata failure.
        print(
            json.dumps(
                {
                    "level": "warning",
                    "code": "flight_sdk_metadata",
                    "operation": operation,
                    "error_type": type(exc).__name__,
                }
            ),
            file=sys.stderr,
        )


def set_run_provenance(
    *,
    project_id: str,
    repository_url: str | None = None,
    commit_sha: str | None = None,
    dirty_digest: str | None = None,
    build_id: str | None = None,
    runtime: str | None = None,
    dependency_lock: Mapping[str, str] | None = None,
    feature_flags: Mapping[str, Any] | None = None,
) -> None:
    def apply(span: trace.Span) -> None:
        span.set_attribute("flight.capture", True)
        span.set_attribute("flight.project.id", project_id)
        values = {
            "flight.repository.url": repository_url,
            "flight.git.commit": commit_sha,
            "flight.git.dirty_digest": dirty_digest,
            "flight.build.id": build_id,
            "flight.runtime": runtime,
        }
        for key, value in values.items():
            if value is not None:
                span.set_attribute(key, value)
        if feature_flags is not None:
            span.set_attribute("flight.feature_flags", _canonical(feature_flags))
        if dependency_lock is not None:
            span.set_attribute("flight.dependency_lock", _canonical(dependency_lock))

    _safe("set_run_provenance", apply)


def register_entrypoint(
    command: Iterable[str],
    *,
    image: str | None = None,
    model_hosts: Iterable[str] = (),
    input_mode: str = "stdin_json",
    output_mode: str = "stdout_json",
) -> None:
    argv = list(command)

    def apply(span: trace.Span) -> None:
        span.set_attribute("flight.entrypoint.command", json.dumps(argv, separators=(",", ":")))
        span.set_attribute("flight.entrypoint.input_mode", input_mode)
        span.set_attribute("flight.entrypoint.output_mode", output_mode)
        if image is not None:
            span.set_attribute("flight.entrypoint.image", image)
        span.set_attribute("flight.entrypoint.model_hosts", _canonical(list(model_hosts)))

    _safe("register_entrypoint", apply)


def attach_artifact(
    *, logical_name: str, artifact_type: str, version: str, content: bytes, media_type: str
) -> str:
    digest = "sha256:" + hashlib.sha256(content).hexdigest()

    def apply(span: trace.Span) -> None:
        span.add_event(
            "flight.artifact",
            {
                "flight.artifact.logical_name": logical_name,
                "flight.artifact.type": artifact_type,
                "flight.artifact.version": version,
                "flight.artifact.digest": digest,
                "flight.artifact.media_type": media_type,
                "flight.artifact.content_base64": base64.b64encode(content).decode("ascii"),
            },
        )

    _safe("attach_artifact", apply)
    return digest


def declare_sandbox(name: str, destination: str) -> None:
    _safe(
        "declare_sandbox",
        lambda span: span.add_event(
            "flight.sandbox",
            {"flight.sandbox.name": name, "flight.sandbox.destination": destination},
        ),
    )


def record_environment(names: Iterable[str]) -> None:
    def apply(span: trace.Span) -> None:
        for name in names:
            value = os.environ.get(name)
            if value is not None:
                span.set_attribute(
                    f"flight.environment.{name}.digest", hashlib.sha256(value.encode()).hexdigest()
                )

    _safe("record_environment", apply)


def _canonical(value: Any) -> str:
    return json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False)
