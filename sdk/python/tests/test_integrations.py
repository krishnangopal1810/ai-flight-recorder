import json
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import pytest
from langchain_core.documents import Document
from langchain_core.messages import AIMessage, AIMessageChunk, HumanMessage
from openai import AsyncOpenAI, OpenAI
from pydantic import BaseModel

from flight_recorder import wrap_chat_model, wrap_openai, wrap_retriever, wrap_tool
from flight_recorder import boundary as boundary_function
from flight_recorder.openai import _encode_response, _json_value


RESPONSE = {
    "id": "resp_flight",
    "object": "response",
    "created_at": 1,
    "status": "completed",
    "error": None,
    "incomplete_details": None,
    "instructions": None,
    "max_output_tokens": None,
    "model": "gpt-flight",
    "output": [],
    "parallel_tool_calls": True,
    "previous_response_id": None,
    "reasoning": {"effort": None, "summary": None},
    "store": True,
    "temperature": 1,
    "text": {"format": {"type": "text"}},
    "tool_choice": "auto",
    "tools": [],
    "top_p": 1,
    "truncation": "disabled",
    "usage": {
        "input_tokens": 2,
        "input_tokens_details": {"cached_tokens": 0, "cache_write_tokens": 0},
        "output_tokens": 1,
        "output_tokens_details": {"reasoning_tokens": 0},
        "total_tokens": 3,
    },
}


def chat_response(content: str = "hello"):
    return {
        "id": "chat_flight",
        "object": "chat.completion",
        "created": 1,
        "model": "gpt-flight",
        "choices": [
            {
                "index": 0,
                "finish_reason": "stop",
                "logprobs": None,
                "message": {"role": "assistant", "content": content, "refusal": None},
            }
        ],
        "usage": {"prompt_tokens": 2, "completion_tokens": 1, "total_tokens": 3},
    }


def chat_chunk(content: str, finish_reason=None):
    return {
        "id": "chat_flight",
        "object": "chat.completion.chunk",
        "created": 1,
        "model": "gpt-flight",
        "choices": [
            {
                "index": 0,
                "delta": {"role": "assistant" if content else None, "content": content},
                "finish_reason": finish_reason,
                "logprobs": None,
            }
        ],
    }


@pytest.fixture
def openai_server():
    requests = []

    class Handler(BaseHTTPRequestHandler):
        def do_POST(self):
            length = int(self.headers.get("content-length", "0"))
            body = json.loads(self.rfile.read(length) or b"{}")
            requests.append((self.path, body))
            if self.path.endswith("/chat/completions") and body.get("stream"):
                chunks = [chat_chunk("hel"), chat_chunk("lo", "stop")]
                encoded = "".join(f"data: {json.dumps(chunk)}\n\n" for chunk in chunks)
                encoded += "data: [DONE]\n\n"
                self.send_response(200)
                self.send_header("content-type", "text/event-stream")
                self.end_headers()
                self.wfile.write(encoded.encode())
                return
            if self.path.endswith("/chat/completions"):
                content = '{"city":"Pune"}' if body.get("response_format") else "hello"
                response = chat_response(content)
            else:
                response = RESPONSE
            encoded = json.dumps(response).encode()
            self.send_response(200)
            self.send_header("content-type", "application/json")
            self.send_header("content-length", str(len(encoded)))
            self.end_headers()
            self.wfile.write(encoded)

        def log_message(self, *_):
            return

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        yield f"http://127.0.0.1:{server.server_port}/v1", requests
    finally:
        server.shutdown()
        thread.join()


@pytest.fixture(autouse=True)
def reset_boundary_state(monkeypatch):
    _reset_boundary_state(monkeypatch)


def _reset_boundary_state(monkeypatch):
    globals_ = boundary_function.__globals__
    globals_["_cached_plan"] = None
    globals_["_occurrences"].clear()
    monkeypatch.delenv("FLIGHT_REPLAY_PLAN", raising=False)
    monkeypatch.delenv("FLIGHT_PROJECT_ID", raising=False)
    monkeypatch.delenv("FLIGHT_CAPTURE", raising=False)


def write_plan(tmp_path, request, response, logical_key):
    digest = boundary_function.__globals__["_digest"](request)
    plan = tmp_path / "plan.json"
    plan.write_text(
        json.dumps(
            {
                "substitutions": [logical_key],
                "boundaries": [
                    {
                        "logical_key": logical_key,
                        "boundary_class": "llm",
                        "occurrence": 0,
                        "request_digest": digest,
                        "response": response,
                    }
                ],
            }
        )
    )
    return plan


def test_openai_responses_and_chat_use_official_types(openai_server):
    base_url, requests = openai_server
    client = wrap_openai(OpenAI(api_key="test", base_url=base_url))

    response = client.responses.create(model="gpt-flight", input="hello")
    chat = client.chat.completions.create(
        model="gpt-flight", messages=[{"role": "user", "content": "hello"}]
    )
    stream = client.chat.completions.create(
        model="gpt-flight",
        messages=[{"role": "user", "content": "hello"}],
        stream=True,
    )

    assert type(response).__name__ == "Response"
    assert type(chat).__name__ == "ChatCompletion"
    assert response.id == "resp_flight"
    assert chat.choices[0].message.content == "hello"
    assert [chunk.choices[0].delta.content for chunk in stream] == ["hel", "lo"]
    assert [path for path, _ in requests] == [
        "/v1/responses",
        "/v1/chat/completions",
        "/v1/chat/completions",
    ]


async def test_async_openai_client_is_preserved(openai_server):
    base_url, _ = openai_server
    client = wrap_openai(AsyncOpenAI(api_key="test", base_url=base_url))
    response = await client.responses.create(model="gpt-flight", input="hello")
    stream = await client.chat.completions.create(
        model="gpt-flight",
        messages=[{"role": "user", "content": "hello"}],
        stream=True,
    )
    assert type(response).__name__ == "Response"
    assert [chunk.choices[0].delta.content async for chunk in stream] == ["hel", "lo"]


def test_openai_replay_substitutes_without_network(tmp_path, monkeypatch):
    parameters = {"model": "gpt-flight", "input": "hello"}
    request = {
        "schema_version": "1",
        "integration": "openai",
        "operation": "responses.create",
        "parameters": _json_value(parameters),
    }
    encoded = {
        "schema_version": "1",
        "integration": "openai",
        "kind": "response",
        "surface": "responses",
        "type": "Response",
        "value": RESPONSE,
    }
    monkeypatch.setenv(
        "FLIGHT_REPLAY_PLAN",
        str(write_plan(tmp_path, request, encoded, "openai.responses.create")),
    )
    client = wrap_openai(OpenAI(api_key="test", base_url="http://127.0.0.1:1/v1"))

    response = client.responses.create(**parameters)

    assert type(response).__name__ == "Response"
    assert response.id == "resp_flight"


class City(BaseModel):
    city: str


def test_openai_structured_output_and_stream_replay(openai_server, tmp_path, monkeypatch):
    base_url, _ = openai_server
    client = wrap_openai(OpenAI(api_key="test", base_url=base_url))
    parameters = {
        "model": "gpt-flight",
        "messages": [{"role": "user", "content": "city"}],
        "response_format": City,
    }
    parsed = client.chat.completions.parse(**parameters)
    assert isinstance(parsed.choices[0].message.parsed, City)

    request = {
        "schema_version": "1",
        "integration": "openai",
        "operation": "chat.completions.parse",
        "parameters": _json_value(parameters),
    }
    _reset_boundary_state(monkeypatch)
    monkeypatch.setenv(
        "FLIGHT_REPLAY_PLAN",
        str(
            write_plan(
                tmp_path,
                request,
                _encode_response(parsed, "chat.completions"),
                "openai.chat.completions.parse",
            )
        ),
    )
    replay = wrap_openai(OpenAI(api_key="test", base_url="http://127.0.0.1:1/v1"))
    replayed = replay.chat.completions.parse(**parameters)
    assert isinstance(replayed.choices[0].message.parsed, City)

    _reset_boundary_state(monkeypatch)
    stream_parameters = {
        "model": "gpt-flight",
        "messages": [{"role": "user", "content": "hello"}],
        "stream": True,
    }
    stream_request = {
        "schema_version": "1",
        "integration": "openai",
        "operation": "chat.completions.create",
        "parameters": _json_value(stream_parameters),
    }
    events = [
        {
            "type": "ChatCompletionChunk",
            "payload": {
                "integration": "openai",
                "surface": "chat.completions",
                "type": "ChatCompletionChunk",
                "value": chunk,
            },
        }
        for chunk in [chat_chunk("hel"), chat_chunk("lo", "stop")]
    ]
    stream_envelope = {
        "schema_version": "1",
        "kind": "stream",
        "events": events,
        "termination": {"kind": "completed"},
    }
    monkeypatch.setenv(
        "FLIGHT_REPLAY_PLAN",
        str(
            write_plan(
                tmp_path,
                stream_request,
                stream_envelope,
                "openai.chat.completions.create",
            )
        ),
    )
    replay_stream = replay.chat.completions.create(**stream_parameters)
    assert [chunk.choices[0].delta.content for chunk in replay_stream] == ["hel", "lo"]


class FakeModel:
    def invoke(self, messages, config=None, **kwargs):
        assert isinstance(messages[0], HumanMessage)
        return AIMessage(content="answer", tool_calls=[{"name": "lookup", "args": {}, "id": "1"}])

    async def ainvoke(self, messages, config=None, **kwargs):
        return self.invoke(messages, config, **kwargs)

    def stream(self, messages, config=None, **kwargs):
        yield AIMessageChunk(content="an")
        yield AIMessageChunk(content="swer")

    async def astream(self, messages, config=None, **kwargs):
        for item in self.stream(messages, config, **kwargs):
            yield item


class FakeRetriever:
    def invoke(self, query, config=None, **kwargs):
        return [Document(page_content=f"policy:{query}", metadata={"source": "fixture"})]


class FakeTool:
    def invoke(self, request, config=None, **kwargs):
        return {"status": "ok", "request": request}


def test_langchain_component_wrappers_preserve_messages_documents_and_streams():
    model = wrap_chat_model(FakeModel())
    retriever = wrap_retriever(FakeRetriever())
    tool = wrap_tool(FakeTool(), side_effect="read_only")

    answer = model.invoke([HumanMessage(content="question")])
    chunks = list(model.stream([HumanMessage(content="question")]))
    documents = retriever.invoke("refunds")
    result = tool.invoke({"id": 1})

    assert isinstance(answer, AIMessage)
    assert all(isinstance(chunk, AIMessageChunk) for chunk in chunks)
    assert isinstance(documents[0], Document)
    assert result["status"] == "ok"


def test_nested_langchain_and_openai_wrappers_create_one_boundary(openai_server):
    base_url, _ = openai_server
    client = wrap_openai(OpenAI(api_key="test", base_url=base_url))

    class NestedModel:
        def invoke(self, messages, config=None, **kwargs):
            response = client.responses.create(model="gpt-flight", input="nested")
            return AIMessage(content=response.id)

    answer = wrap_chat_model(NestedModel()).invoke([HumanMessage(content="question")])
    occurrences = boundary_function.__globals__["_occurrences"]
    boundary_keys = [key[1] for key in occurrences]

    assert answer.content == "resp_flight"
    assert len(boundary_keys) == 1
    assert "langchain.model" in boundary_keys[0]


async def test_langchain_async_model_wrapper():
    model = wrap_chat_model(FakeModel())
    answer = await model.ainvoke([HumanMessage(content="question")])
    chunks = [chunk async for chunk in model.astream([HumanMessage(content="question")])]
    assert isinstance(answer, AIMessage)
    assert [chunk.content for chunk in chunks] == ["an", "swer"]
