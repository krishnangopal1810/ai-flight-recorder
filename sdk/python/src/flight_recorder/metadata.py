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
    project_id: str | None = None,
    repository_url: str | None = None,
    commit_sha: str | None = None,
    dirty_digest: str | None = None,
    build_id: str | None = None,
    runtime: str | None = None,
    dependency_lock: Mapping[str, str] | None = None,
    feature_flags: Mapping[str, Any] | None = None,
) -> None:
    def apply(span: trace.Span) -> None:
        effective_project_id = project_id or os.environ.get("FLIGHT_PROJECT_ID")
        if not effective_project_id:
            raise ValueError("project_id is required outside flight record")
        span.set_attribute("flight.capture", True)
        span.set_attribute("flight.project.id", effective_project_id)
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
    command: Iterable[str] | None = None,
    *,
    image: str | None = None,
    working_dir: str | None = None,
    dockerfile: str | None = None,
    context: str | None = None,
    model_hosts: Iterable[str] | None = None,
    environment: Iterable[str] | None = None,
    sandboxes: Mapping[str, str] | None = None,
    model_override: bool | None = None,
    input_mode: str | None = None,
    output_mode: str | None = None,
    timeout_seconds: float | None = None,
    cpu_limit: float | None = None,
    memory_limit_mb: int | None = None,
) -> None:
    def apply(span: trace.Span) -> None:
        defaults = _entrypoint_environment()
        argv = list(command) if command is not None else list(defaults.get("command", []))
        if not argv:
            raise ValueError("entrypoint command is required outside flight record")
        effective_image = image if image is not None else defaults.get("image")
        effective_working_dir = (
            working_dir if working_dir is not None else defaults.get("working_dir")
        )
        effective_dockerfile = dockerfile if dockerfile is not None else defaults.get("dockerfile")
        effective_context = context if context is not None else defaults.get("context")
        effective_hosts = (
            list(model_hosts) if model_hosts is not None else list(defaults.get("model_hosts", []))
        )
        effective_environment = (
            list(environment) if environment is not None else list(defaults.get("environment", []))
        )
        effective_sandboxes = (
            dict(sandboxes) if sandboxes is not None else dict(defaults.get("sandboxes", {}))
        )
        effective_model_override = (
            model_override
            if model_override is not None
            else bool(defaults.get("model_override", False))
        )
        effective_input_mode = input_mode or str(defaults.get("input_mode", "stdin_json"))
        effective_output_mode = output_mode or str(defaults.get("output_mode", "stdout_json"))
        timeout_ns = (
            int(timeout_seconds * 1_000_000_000)
            if timeout_seconds is not None
            else int(defaults.get("timeout", 0))
        )
        effective_cpu_limit = (
            cpu_limit if cpu_limit is not None else float(defaults.get("cpu_limit", 0))
        )
        effective_memory_limit = (
            memory_limit_mb
            if memory_limit_mb is not None
            else int(defaults.get("memory_limit_mb", 0))
        )
        span.set_attribute("flight.entrypoint.command", json.dumps(argv, separators=(",", ":")))
        span.set_attribute("flight.entrypoint.input_mode", effective_input_mode)
        span.set_attribute("flight.entrypoint.output_mode", effective_output_mode)
        if effective_image:
            span.set_attribute("flight.entrypoint.image", str(effective_image))
        for key, value in {
            "flight.entrypoint.working_dir": effective_working_dir,
            "flight.entrypoint.dockerfile": effective_dockerfile,
            "flight.entrypoint.context": effective_context,
        }.items():
            if value:
                span.set_attribute(key, str(value))
        if timeout_ns:
            span.set_attribute("flight.entrypoint.timeout_ns", timeout_ns)
        if effective_cpu_limit:
            span.set_attribute("flight.entrypoint.cpu_limit", effective_cpu_limit)
        if effective_memory_limit:
            span.set_attribute("flight.entrypoint.memory_limit_mb", effective_memory_limit)
        span.set_attribute("flight.entrypoint.environment", _canonical(effective_environment))
        span.set_attribute("flight.entrypoint.model_hosts", _canonical(effective_hosts))
        span.set_attribute("flight.entrypoint.model_override", effective_model_override)
        for name, destination in effective_sandboxes.items():
            span.add_event(
                "flight.sandbox",
                {"flight.sandbox.name": name, "flight.sandbox.destination": destination},
            )

    _safe("register_entrypoint", apply)


def _entrypoint_environment() -> Mapping[str, Any]:
    encoded = os.environ.get("FLIGHT_ENTRYPOINT_JSON")
    if not encoded:
        return {}
    value = json.loads(encoded)
    if not isinstance(value, dict) or value.get("schema_version") != "1":
        raise ValueError("unsupported FLIGHT_ENTRYPOINT_JSON")
    entrypoint = value.get("entrypoint")
    if not isinstance(entrypoint, dict):
        raise ValueError("FLIGHT_ENTRYPOINT_JSON has no entrypoint")
    return entrypoint


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
