"""Thin OpenTelemetry metadata and controlled-replay SDK."""

from .boundary import BoundaryError, boundary, boundary_async, mcp_boundary, openai_boundary
from .metadata import (
    attach_artifact,
    declare_sandbox,
    record_environment,
    register_entrypoint,
    set_run_provenance,
)

__all__ = [
    "BoundaryError",
    "attach_artifact",
    "boundary",
    "boundary_async",
    "declare_sandbox",
    "mcp_boundary",
    "openai_boundary",
    "record_environment",
    "register_entrypoint",
    "set_run_provenance",
]
