from __future__ import annotations

import dataclasses
import importlib
import inspect
from collections.abc import AsyncIterator, Iterator, Mapping
from dataclasses import dataclass
from typing import Any, TypeVar, cast

from opentelemetry import context, trace
from opentelemetry.trace import Status, StatusCode

from .boundary import (
    BoundarySession,
    StreamTermination,
    _LIVE,
    _integration_scope,
    _parse_stream_envelope,
    _stream_envelope,
)
from .telemetry import ensure_telemetry

T = TypeVar("T")


@dataclass(frozen=True)
class ComponentOptions:
    logical_key: str
    boundary_class: str
    side_effect: str = "read_only"
    replay_policy: str = "substitute"
    sandbox: str | None = None
    parent_logical_key: str | None = None


def wrap_chat_model(
    model: T,
    logical_key: str = "langchain.model",
    *,
    parent_logical_key: str | None = None,
    replay_policy: str = "live",
) -> T:
    """Wrap a LangChain chat model without changing global LangChain behavior."""
    return cast(
        T,
        _RunnableProxy(
            model,
            ComponentOptions(
                logical_key,
                "llm",
                side_effect="read_only",
                replay_policy=replay_policy,
                parent_logical_key=parent_logical_key,
            ),
        ),
    )


def wrap_retriever(
    retriever: T,
    logical_key: str = "langchain.retriever",
    *,
    parent_logical_key: str | None = None,
    replay_policy: str = "substitute",
) -> T:
    """Wrap a LangChain retriever with recorded substitution by default."""
    return cast(
        T,
        _RunnableProxy(
            retriever,
            ComponentOptions(
                logical_key,
                "retriever",
                side_effect="read_only",
                replay_policy=replay_policy,
                parent_logical_key=parent_logical_key,
            ),
        ),
    )


def wrap_tool(
    tool: T,
    logical_key: str = "langchain.tool",
    *,
    side_effect: str = "unknown",
    replay_policy: str = "substitute",
    sandbox: str | None = None,
    parent_logical_key: str | None = None,
) -> T:
    """Wrap a LangChain tool; unknown side effects remain substitute-only by default."""
    return cast(
        T,
        _RunnableProxy(
            tool,
            ComponentOptions(
                logical_key,
                "tool",
                side_effect=side_effect,
                replay_policy=replay_policy,
                sandbox=sandbox,
                parent_logical_key=parent_logical_key,
            ),
        ),
    )


def flight_recorder_middleware(
    *,
    logical_key_prefix: str = "langchain.agent",
    parent_logical_key: str | None = None,
    tool_side_effects: Mapping[str, str] | None = None,
    tool_sandboxes: Mapping[str, str] | None = None,
) -> Any:
    """Return modern LangChain model/tool middleware with fail-closed tool defaults."""
    return _FlightRecorderMiddleware(
        logical_key_prefix,
        parent_logical_key,
        dict(tool_side_effects or {}),
        dict(tool_sandboxes or {}),
    )


class _RunnableProxy:
    def __init__(self, runnable: Any, options: ComponentOptions) -> None:
        self._runnable = runnable
        self._options = options

    def __getattr__(self, name: str) -> Any:
        original = getattr(self._runnable, name)
        if name not in {"invoke", "ainvoke", "stream", "astream"} or not callable(original):
            return original

        def invoke(run_input: Any, config: Any = None, **kwargs: Any) -> Any:
            request = _runnable_request(run_input, config, kwargs)
            if name == "invoke":
                return _component_sync(
                    request,
                    lambda effective: original(
                        _request_input(effective), config=config, **kwargs
                    ),
                    self._options,
                )
            if name == "ainvoke":
                return _component_async(
                    request,
                    lambda effective: original(
                        _request_input(effective), config=config, **kwargs
                    ),
                    self._options,
                )
            if name == "stream":
                return _component_stream(
                    request,
                    lambda effective: original(
                        _request_input(effective), config=config, **kwargs
                    ),
                    self._options,
                )
            return _component_async_stream(
                request,
                lambda effective: original(
                    _request_input(effective), config=config, **kwargs
                ),
                self._options,
            )

        return invoke


@dataclass
class _FlightRecorderMiddleware:
    name_prefix: str
    parent_logical_key: str | None
    tool_side_effects: dict[str, str]
    tool_sandboxes: dict[str, str]
    name: str = "flight_recorder"

    def wrap_model_call(self, request: Any, handler: Any) -> Any:
        return _component_sync(
            _codec_encode(request),
            lambda _effective: handler(request),
            ComponentOptions(
                f"{self.name_prefix}.model",
                "llm",
                replay_policy="live",
                parent_logical_key=self.parent_logical_key,
            ),
        )

    async def awrap_model_call(self, request: Any, handler: Any) -> Any:
        return await _component_async(
            _codec_encode(request),
            lambda _effective: handler(request),
            ComponentOptions(
                f"{self.name_prefix}.model",
                "llm",
                replay_policy="live",
                parent_logical_key=self.parent_logical_key,
            ),
        )

    def wrap_tool_call(self, request: Any, handler: Any) -> Any:
        tool_name = _tool_name(request)
        return _component_sync(
            _codec_encode(request),
            lambda _effective: handler(request),
            self._tool_options(tool_name),
        )

    async def awrap_tool_call(self, request: Any, handler: Any) -> Any:
        tool_name = _tool_name(request)
        return await _component_async(
            _codec_encode(request),
            lambda _effective: handler(request),
            self._tool_options(tool_name),
        )

    def _tool_options(self, tool_name: str) -> ComponentOptions:
        return ComponentOptions(
            f"{self.name_prefix}.tool.{tool_name}",
            "tool",
            side_effect=self.tool_side_effects.get(tool_name, "unknown"),
            replay_policy="substitute",
            sandbox=self.tool_sandboxes.get(tool_name),
            parent_logical_key=self.parent_logical_key,
        )


def _component_sync(request: Any, call: Any, options: ComponentOptions) -> Any:
    span, session, token = _start_component(request, options)
    try:
        action = session.action()
        if action is _LIVE:
            with _integration_scope():
                result = call(session.request)
        else:
            result = _codec_decode(action)
        session.capture_response(_codec_encode(result))
        _finish_component(span, token)
        return result
    except BaseException as exc:
        _finish_component(span, token, exc)
        raise


async def _component_async(request: Any, call: Any, options: ComponentOptions) -> Any:
    span, session, token = _start_component(request, options)
    try:
        action = session.action()
        if action is _LIVE:
            with _integration_scope():
                result = call(session.request)
                if inspect.isawaitable(result):
                    result = await result
        else:
            result = _codec_decode(action)
        session.capture_response(_codec_encode(result))
        _finish_component(span, token)
        return result
    except BaseException as exc:
        _finish_component(span, token, exc)
        raise


def _component_stream(request: Any, call: Any, options: ComponentOptions) -> Iterator[Any]:
    span, session, token = _start_component(request, options)
    try:
        action = session.action()
        if action is _LIVE:
            with _integration_scope():
                source = call(session.request)
            context.detach(cast(Any, token))
            return _LangChainSyncStream(source, None, session, span)
        context.detach(cast(Any, token))
        return _LangChainSyncStream(None, action, session, span)
    except BaseException as exc:
        _finish_component(span, token, exc)
        raise


def _component_async_stream(request: Any, call: Any, options: ComponentOptions) -> AsyncIterator[Any]:
    span, session, token = _start_component(request, options)

    async def start() -> AsyncIterator[Any]:
        try:
            action = session.action()
            if action is _LIVE:
                with _integration_scope():
                    source = call(session.request)
                    if inspect.isawaitable(source):
                        source = await source
                context.detach(cast(Any, token))
                stream = _LangChainAsyncStream(source, None, session, span)
            else:
                context.detach(cast(Any, token))
                stream = _LangChainAsyncStream(None, action, session, span)
            async for event in stream:
                yield event
        except BaseException as exc:
            if span.is_recording():
                _finish_component(span, token, exc)
            raise

    return start()


class _LangChainSyncStream(Iterator[Any]):
    def __init__(self, source: Any, recorded: Any, session: BoundarySession, span: trace.Span):
        self.source = iter(source) if source is not None else None
        self.source_object = source
        self.session = session
        self.span = span
        self.events: list[dict[str, Any]] = []
        self.finished = False
        self.recorded = recorded
        self.replay_events: Iterator[Mapping[str, Any]] | None = None
        self.termination: Mapping[str, Any] | None = None
        if recorded is not None:
            events, self.termination = _parse_stream_envelope(recorded)
            self.replay_events = iter(events)

    def __iter__(self) -> _LangChainSyncStream:
        return self

    def __next__(self) -> Any:
        if self.finished:
            raise StopIteration
        if self.replay_events is not None:
            try:
                event = next(self.replay_events)
            except StopIteration:
                self._finish_replay()
                raise
            return _codec_decode(event["payload"])
        try:
            with _integration_scope():
                assert self.source is not None
                event = next(self.source)
        except StopIteration:
            self._finish("completed")
            raise
        except BaseException as exc:
            self._finish("error", exc)
            raise
        self.events.append({"type": type(event).__name__, "payload": _codec_encode(event)})
        return event

    def close(self) -> None:
        if self.finished:
            return
        callback = getattr(self.source_object, "close", None)
        if callable(callback):
            callback()
        self._finish("closed")

    def _finish(self, kind: str, error: BaseException | None = None) -> None:
        if self.finished:
            return
        self.finished = True
        self.session.capture_response(
            _stream_envelope(
                self.events,
                StreamTermination(kind, type(error).__name__ if error else None),
            )
        )
        _finish_component(self.span, None, error)

    def _finish_replay(self) -> None:
        assert self.termination is not None
        self.finished = True
        self.session.capture_response(self.recorded)
        kind = self.termination.get("kind")
        if kind == "completed":
            _finish_component(self.span, None)
            return
        error = RuntimeError(f"replayed_stream_error: {self.termination.get('error_type', kind)}")
        _finish_component(self.span, None, error)
        raise error


class _LangChainAsyncStream(AsyncIterator[Any]):
    def __init__(self, source: Any, recorded: Any, session: BoundarySession, span: trace.Span):
        self.source = source.__aiter__() if source is not None else None
        self.session = session
        self.span = span
        self.events: list[dict[str, Any]] = []
        self.finished = False
        self.recorded = recorded
        self.replay_events: Iterator[Mapping[str, Any]] | None = None
        self.termination: Mapping[str, Any] | None = None
        if recorded is not None:
            events, self.termination = _parse_stream_envelope(recorded)
            self.replay_events = iter(events)

    def __aiter__(self) -> _LangChainAsyncStream:
        return self

    async def __anext__(self) -> Any:
        if self.finished:
            raise StopAsyncIteration
        if self.replay_events is not None:
            try:
                event = next(self.replay_events)
            except StopIteration:
                self._finish_replay()
                raise StopAsyncIteration
            return _codec_decode(event["payload"])
        try:
            with _integration_scope():
                assert self.source is not None
                event = await self.source.__anext__()
        except StopAsyncIteration:
            self._finish("completed")
            raise
        except BaseException as exc:
            self._finish("error", exc)
            raise
        self.events.append({"type": type(event).__name__, "payload": _codec_encode(event)})
        return event

    def _finish(self, kind: str, error: BaseException | None = None) -> None:
        if self.finished:
            return
        self.finished = True
        self.session.capture_response(
            _stream_envelope(
                self.events,
                StreamTermination(kind, type(error).__name__ if error else None),
            )
        )
        _finish_component(self.span, None, error)

    def _finish_replay(self) -> None:
        assert self.termination is not None
        self.finished = True
        self.session.capture_response(self.recorded)
        kind = self.termination.get("kind")
        if kind == "completed":
            _finish_component(self.span, None)
            return
        error = RuntimeError(f"replayed_stream_error: {self.termination.get('error_type', kind)}")
        _finish_component(self.span, None, error)
        raise error


def _start_component(
    request: Any, options: ComponentOptions
) -> tuple[trace.Span, BoundarySession, object]:
    ensure_telemetry()
    span = trace.get_tracer("ai-flight-recorder.langchain", "0.1.0").start_span(
        options.logical_key
    )
    span.set_attribute("openinference.span.kind", options.boundary_class.upper())
    operation = {
        "llm": "chat",
        "tool": "execute_tool",
        "retriever": "retriever",
    }.get(options.boundary_class, options.boundary_class)
    span.set_attribute("gen_ai.operation.name", operation)
    token = context.attach(trace.set_span_in_context(span))
    session = BoundarySession(
        options.logical_key,
        request,
        boundary_class=options.boundary_class,
        side_effect=options.side_effect,
        replay_policy=options.replay_policy,
        sandbox=options.sandbox,
        parent_logical_key=options.parent_logical_key,
        span=span,
    )
    return span, session, token


def _finish_component(
    span: trace.Span, token: object | None, error: BaseException | None = None
) -> None:
    if error is None:
        span.set_status(Status(StatusCode.OK))
    else:
        span.record_exception(error)
        span.set_status(Status(StatusCode.ERROR, type(error).__name__))
    if token is not None:
        context.detach(cast(Any, token))
    span.end()


def _runnable_request(run_input: Any, config: Any, kwargs: dict[str, Any]) -> dict[str, Any]:
    safe_config = None
    if isinstance(config, Mapping):
        safe_config = {
            key: _codec_encode(config[key])
            for key in ("configurable", "tags", "metadata", "run_name", "recursion_limit")
            if key in config
        }
    return {
        "schema_version": "1",
        "integration": "langchain",
        "input": _codec_encode(run_input),
        "config": safe_config,
        "kwargs": _codec_encode(kwargs),
    }


def _request_input(request: Any) -> Any:
    if not isinstance(request, Mapping) or request.get("integration") != "langchain":
        raise RuntimeError("unsafe_replay: LangChain request override is malformed")
    return _codec_decode(request.get("input"))


def _codec_encode(value: Any) -> Any:
    if value is None or isinstance(value, (str, int, float, bool)):
        return value
    if isinstance(value, Mapping):
        return {str(key): _codec_encode(item) for key, item in value.items()}
    if isinstance(value, (list, tuple)):
        return [_codec_encode(item) for item in value]
    module = type(value).__module__
    name = type(value).__name__
    if module.startswith("langchain_core.messages"):
        return {
            "__flight_type__": "langchain_message",
            "class": name,
            "value": _codec_encode(value.model_dump()),
        }
    if module.startswith("langchain_core.documents"):
        return {
            "__flight_type__": "langchain_document",
            "class": name,
            "value": _codec_encode(value.model_dump()),
        }
    if module.startswith("langgraph.types") and name == "Command":
        payload = (
            dataclasses.asdict(cast(Any, value))
            if dataclasses.is_dataclass(value)
            else vars(value)
        )
        return {
            "__flight_type__": "langgraph_command",
            "class": name,
            "value": _codec_encode(payload),
        }
    if hasattr(value, "model_dump") and module.startswith("langchain"):
        return {
            "__flight_type__": "langchain_model",
            "class": name,
            "value": _codec_encode(value.model_dump()),
        }
    if dataclasses.is_dataclass(value) and module.startswith("langchain"):
        return {
            "__flight_type__": "langchain_dataclass",
            "class": name,
            "value": _codec_encode(dataclasses.asdict(cast(Any, value))),
        }
    raise RuntimeError(f"validation: unsupported LangChain value {module}.{name}")


def _codec_decode(value: Any) -> Any:
    if not isinstance(value, (Mapping, list)):
        return value
    if isinstance(value, list):
        return [_codec_decode(item) for item in value]
    marker = value.get("__flight_type__")
    if marker is None:
        return {str(key): _codec_decode(item) for key, item in value.items()}
    class_name = str(value.get("class"))
    payload = _codec_decode(value.get("value"))
    model = _langchain_types().get((str(marker), class_name))
    if model is None:
        raise RuntimeError(f"unsupported_replay_type: LangChain {class_name} is not registered")
    if hasattr(model, "model_validate"):
        return model.model_validate(payload)
    return model(**payload)


def _langchain_types() -> dict[tuple[str, str], Any]:
    try:
        Document = importlib.import_module("langchain_core.documents").Document
        messages_module = importlib.import_module("langchain_core.messages")
    except ImportError as exc:
        raise RuntimeError("LangChain replay requires the 'langchain' extra") from exc
    messages = [
        getattr(messages_module, name)
        for name in (
            "BaseMessage",
            "AIMessage",
            "AIMessageChunk",
            "ChatMessage",
            "ChatMessageChunk",
            "FunctionMessage",
            "FunctionMessageChunk",
            "HumanMessage",
            "HumanMessageChunk",
            "SystemMessage",
            "SystemMessageChunk",
            "ToolMessage",
            "ToolMessageChunk",
        )
    ]
    result = {("langchain_message", item.__name__): item for item in messages}
    result[("langchain_document", "Document")] = Document
    try:
        Command = importlib.import_module("langgraph.types").Command
        result[("langgraph_command", "Command")] = Command
    except ImportError:
        pass
    try:
        ModelResponse = importlib.import_module(
            "langchain.agents.middleware.types"
        ).ModelResponse
        result[("langchain_dataclass", "ModelResponse")] = ModelResponse
    except ImportError:
        pass
    return result


def _tool_name(request: Any) -> str:
    for candidate in (
        getattr(getattr(request, "tool", None), "name", None),
        getattr(request, "tool_name", None),
    ):
        if isinstance(candidate, str) and candidate:
            return candidate
    tool_call = getattr(request, "tool_call", None)
    if isinstance(tool_call, Mapping) and isinstance(tool_call.get("name"), str):
        return cast(str, tool_call["name"])
    return "unknown"
