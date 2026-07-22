from __future__ import annotations

import logging

from flight_recorder.logging import _logs_endpoint, install_otlp_logging


def test_logs_endpoint_accepts_collector_or_signal_url() -> None:
    assert _logs_endpoint("http://collector:4318") == "http://collector:4318/v1/logs"
    assert _logs_endpoint("http://collector:4318/v1/logs/") == "http://collector:4318/v1/logs"


def test_install_attaches_only_to_the_supplied_logger() -> None:
    target = logging.getLogger("flight.logging.target")
    other = logging.getLogger("flight.logging.other")
    target.handlers.clear()
    other.handlers.clear()

    runtime = install_otlp_logging(target, endpoint="http://127.0.0.1:4318")
    try:
        assert runtime.handler in target.handlers
        assert runtime.handler not in other.handlers
        assert runtime.force_flush()
    finally:
        runtime.shutdown()

    assert runtime.handler not in target.handlers
