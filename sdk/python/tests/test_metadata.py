from flight_recorder import metadata


class RecordingSpan:
    def __init__(self):
        self.attributes = {}

    def set_attribute(self, key, value):
        self.attributes[key] = value


def test_run_provenance_emits_canonical_dependency_and_runtime_facts(monkeypatch):
    span = RecordingSpan()
    monkeypatch.setattr(metadata.trace, "get_current_span", lambda: span)

    metadata.set_run_provenance(
        project_id="support",
        runtime="python:3.13",
        dependency_lock={"uv.lock": "sha256:bbb", "pyproject.toml": "sha256:aaa"},
        feature_flags={"retrieval": "v2"},
    )

    assert span.attributes["flight.runtime"] == "python:3.13"
    assert (
        span.attributes["flight.dependency_lock"]
        == '{"pyproject.toml":"sha256:aaa","uv.lock":"sha256:bbb"}'
    )
    assert span.attributes["flight.feature_flags"] == '{"retrieval":"v2"}'
