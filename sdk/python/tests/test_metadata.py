from flight_recorder import metadata


class RecordingSpan:
    def __init__(self):
        self.attributes = {}
        self.events = []

    def set_attribute(self, key, value):
        self.attributes[key] = value

    def add_event(self, name, attributes):
        self.events.append((name, attributes))


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


def test_cli_environment_supplies_complete_entrypoint_without_secrets(monkeypatch):
    span = RecordingSpan()
    monkeypatch.setattr(metadata.trace, "get_current_span", lambda: span)
    monkeypatch.setenv("FLIGHT_PROJECT_ID", "support")
    monkeypatch.setenv(
        "FLIGHT_ENTRYPOINT_JSON",
        '{"schema_version":"1","entrypoint":{"command":["python","agent.py"],"image":"sha256:abc","working_dir":"/workspace","timeout":120000000000,"environment":["OPENAI_API_KEY"],"model_hosts":["api.openai.com"],"model_override":true,"sandboxes":{"mail":"https://mail.test.invalid"}}}',
    )
    monkeypatch.setenv("OPENAI_API_KEY", "must-not-be-recorded")

    metadata.set_run_provenance()
    metadata.register_entrypoint()

    assert span.attributes["flight.project.id"] == "support"
    assert span.attributes["flight.entrypoint.environment"] == '["OPENAI_API_KEY"]'
    assert span.attributes["flight.entrypoint.model_hosts"] == '["api.openai.com"]'
    assert span.attributes["flight.entrypoint.model_override"] is True
    assert "must-not-be-recorded" not in repr(span.attributes)
    assert span.events[0][1]["flight.sandbox.name"] == "mail"
