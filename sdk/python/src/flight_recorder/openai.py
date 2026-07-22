from __future__ import annotations

import importlib
import inspect
from collections.abc import AsyncIterator, Iterator, Mapping
from dataclasses import dataclass
from enum import Enum
from types import TracebackType
from typing import Any, TypeVar, cast, get_args

from opentelemetry import context, trace
from opentelemetry.trace import Status, StatusCode

from .boundary import (
    BoundaryError,
    BoundarySession,
    StreamTermination,
    _LIVE,
    _integration_active,
    _parse_stream_envelope,
    _stream_envelope,
)
from .telemetry import ensure_telemetry

T = TypeVar("T")


class OpenAIIntegrationError(BoundaryError):
    """Stable replay error when an official SDK error cannot be reconstructed safely."""


@dataclass(frozen=True)
class OpenAIOptions:
    logical_key_prefix: str = "openai"
    parent_logical_key: str | None = None
    replay_policy: str = "live"


def wrap_openai(client: T, options: OpenAIOptions | Mapping[str, Any] | None = None) -> T:
    """Explicitly wrap an ``OpenAI`` or ``AsyncOpenAI`` client.

    Only Responses and Chat Completions are intercepted. Unsupported resources are
    forwarded unchanged, so no global monkey patching or transport replacement occurs.
    """
    resolved = _options(options)
    return cast(T, _OpenAIClient(client, resolved))


class _OpenAIClient:
    def __init__(self, client: Any, options: OpenAIOptions) -> None:
        self._client = client
        asynchronous = _is_async_resource(client.responses) or type(client).__name__.startswith(
            "Async"
        )
        self.responses = _OpenAIResource(
            client.responses, "responses", options, asynchronous
        )
        chat = getattr(client, "chat", None)
        self.chat = _ForwardProxy(chat)
        if chat is not None and getattr(chat, "completions", None) is not None:
            setattr(
                self.chat,
                "completions",
                _OpenAIResource(
                    chat.completions,
                    "chat.completions",
                    options,
                    asynchronous,
                ),
            )

    def __getattr__(self, name: str) -> Any:
        return getattr(self._client, name)


class _ForwardProxy:
    def __init__(self, value: Any) -> None:
        self._value = value

    def __getattr__(self, name: str) -> Any:
        return getattr(self._value, name)


class _OpenAIResource:
    def __init__(
        self, resource: Any, surface: str, options: OpenAIOptions, asynchronous: bool
    ) -> None:
        self._resource = resource
        self._surface = surface
        self._options = options
        self._asynchronous = asynchronous

    def __getattr__(self, name: str) -> Any:
        original = getattr(self._resource, name)
        if name not in {"create", "parse", "stream"} or not callable(original):
            return original

        def invoke(*args: Any, **kwargs: Any) -> Any:
            if args:
                return original(*args, **kwargs)
            if _integration_active():
                return original(**kwargs)
            if name == "stream":
                return _DeferredStreamManager(
                    original,
                    kwargs,
                    self._surface,
                    name,
                    self._options,
                    self._asynchronous,
                )
            if self._asynchronous:
                return _call_async(original, kwargs, self._surface, name, self._options)
            return _call_sync(original, kwargs, self._surface, name, self._options)

        return invoke


def _call_sync(
    method: Any,
    parameters: dict[str, Any],
    surface: str,
    operation: str,
    options: OpenAIOptions,
) -> Any:
    span, session, token = _start_call(parameters, surface, operation, options)
    try:
        action = session.action()
        if action is not _LIVE:
            if parameters.get("stream") is True:
                context.detach(cast(Any, token))
                token = None
                return _TrackedSyncStream(None, action, session, span, None, surface)
            result = _decode_response(action, surface, parameters.get("response_format"))
            session.capture_response(action)
            _finish_span(span, token, result=result)
            return result
        effective = _effective_parameters(parameters, session.request)
        result = method(**effective)
        if parameters.get("stream") is True:
            context.detach(cast(Any, token))
            token = None
            return _TrackedSyncStream(result, None, session, span, None, surface)
        encoded = _encode_response(result, surface)
        session.capture_response(encoded)
        _finish_span(span, token, result=result)
        return result
    except BaseException as exc:
        session.capture_response(_error_response(exc, surface))
        _finish_span(span, token, error=exc)
        raise


async def _call_async(
    method: Any,
    parameters: dict[str, Any],
    surface: str,
    operation: str,
    options: OpenAIOptions,
) -> Any:
    span, session, token = _start_call(parameters, surface, operation, options)
    try:
        action = session.action()
        if action is not _LIVE:
            if parameters.get("stream") is True:
                context.detach(cast(Any, token))
                token = None
                return _TrackedAsyncStream(None, action, session, span, None, surface)
            result = _decode_response(action, surface, parameters.get("response_format"))
            session.capture_response(action)
            _finish_span(span, token, result=result)
            return result
        effective = _effective_parameters(parameters, session.request)
        result = method(**effective)
        if inspect.isawaitable(result):
            result = await result
        if parameters.get("stream") is True:
            context.detach(cast(Any, token))
            token = None
            return _TrackedAsyncStream(result, None, session, span, None, surface)
        encoded = _encode_response(result, surface)
        session.capture_response(encoded)
        _finish_span(span, token, result=result)
        return result
    except BaseException as exc:
        session.capture_response(_error_response(exc, surface))
        _finish_span(span, token, error=exc)
        raise


class _DeferredStreamManager:
    def __init__(
        self,
        method: Any,
        parameters: dict[str, Any],
        surface: str,
        operation: str,
        options: OpenAIOptions,
        asynchronous: bool,
    ) -> None:
        self._method = method
        self._parameters = parameters
        self._surface = surface
        self._operation = operation
        self._options = options
        self._asynchronous = asynchronous
        self._manager: Any = None
        self._stream: Any = None

    def __enter__(self) -> Any:
        if self._asynchronous:
            raise TypeError("AsyncOpenAI stream helpers require 'async with'")
        span, session, token = _start_call(
            self._parameters, self._surface, self._operation, self._options
        )
        try:
            action = session.action()
            if action is _LIVE:
                effective = _effective_parameters(self._parameters, session.request)
                self._manager = self._method(**effective)
                source = self._manager.__enter__()
            else:
                source = None
            context.detach(cast(Any, token))
            token = None
            self._stream = _TrackedSyncStream(
                source, None if action is _LIVE else action, session, span, None, self._surface
            )
            return self._stream
        except BaseException as exc:
            session.capture_response(_error_response(exc, self._surface))
            _finish_span(span, token, error=exc)
            raise

    def __exit__(
        self,
        exc_type: type[BaseException] | None,
        exc: BaseException | None,
        traceback: TracebackType | None,
    ) -> bool | None:
        if self._stream is not None:
            self._stream.close()
        if self._manager is not None:
            return self._manager.__exit__(exc_type, exc, traceback)
        return None

    async def __aenter__(self) -> Any:
        if not self._asynchronous:
            raise TypeError("OpenAI stream helpers require 'with'")
        span, session, token = _start_call(
            self._parameters, self._surface, self._operation, self._options
        )
        try:
            action = session.action()
            if action is _LIVE:
                effective = _effective_parameters(self._parameters, session.request)
                self._manager = self._method(**effective)
                source = await self._manager.__aenter__()
            else:
                source = None
            context.detach(cast(Any, token))
            token = None
            self._stream = _TrackedAsyncStream(
                source, None if action is _LIVE else action, session, span, None, self._surface
            )
            return self._stream
        except BaseException as exc:
            session.capture_response(_error_response(exc, self._surface))
            _finish_span(span, token, error=exc)
            raise

    async def __aexit__(
        self,
        exc_type: type[BaseException] | None,
        exc: BaseException | None,
        traceback: TracebackType | None,
    ) -> bool | None:
        if self._stream is not None:
            await self._stream.close()
        if self._manager is not None:
            return await self._manager.__aexit__(exc_type, exc, traceback)
        return None


class _TrackedSyncStream(Iterator[Any]):
    def __init__(
        self,
        source: Any,
        recorded: Any,
        session: BoundarySession,
        span: trace.Span,
        token: object | None,
        surface: str,
    ) -> None:
        self._source = iter(source) if source is not None else None
        self._source_object = source
        self._session = session
        self._span = span
        self._token = token
        self._surface = surface
        self._events: list[dict[str, Any]] = []
        self._finished = False
        self._recorded_events: Iterator[Mapping[str, Any]] | None = None
        self._termination: Mapping[str, Any] | None = None
        self._recorded_envelope: Any = recorded
        self._final: Any = None
        if recorded is not None:
            events, self._termination = _parse_stream_envelope(recorded)
            self._recorded_events = iter(events)
            self._final = recorded.get("final") if isinstance(recorded, Mapping) else None

    def __iter__(self) -> _TrackedSyncStream:
        return self

    def __next__(self) -> Any:
        if self._finished:
            raise StopIteration
        if self._recorded_events is not None:
            try:
                event = next(self._recorded_events)
            except StopIteration:
                self._finish_replay()
                raise
            return _decode_event(event["payload"], self._surface)
        try:
            token = context.attach(trace.set_span_in_context(self._span))
            try:
                assert self._source is not None
                event = next(self._source)
            finally:
                context.detach(token)
        except StopIteration:
            self._finish("completed")
            raise
        except BaseException as exc:
            self._finish("error", exc)
            raise
        self._events.append(
            {"type": _event_name(event), "payload": _encode_event(event, self._surface)}
        )
        return event

    def close(self) -> None:
        if self._finished:
            return
        close = getattr(self._source_object, "close", None)
        if callable(close):
            close()
        self._finish("closed")

    def get_final_response(self) -> Any:
        if self._recorded_events is not None:
            return _decode_response(self._final, self._surface, None)
        callback = getattr(self._source_object, "get_final_response")
        result = callback()
        self._final = _encode_response(result, self._surface)
        return result

    def __getattr__(self, name: str) -> Any:
        return getattr(self._source_object, name)

    def _finish(self, kind: str, error: BaseException | None = None) -> None:
        if self._finished:
            return
        self._finished = True
        final = self._final
        if kind == "completed" and final is None:
            callback = getattr(self._source_object, "get_final_response", None)
            if callable(callback):
                try:
                    final = _encode_response(callback(), self._surface)
                except Exception:
                    final = None
        self._session.capture_response(
            _stream_envelope(
                self._events,
                StreamTermination(kind, type(error).__name__ if error else None),
                final,
            )
        )
        _finish_span(self._span, self._token, error=error)

    def _finish_replay(self) -> None:
        assert self._termination is not None
        kind = self._termination.get("kind")
        if kind == "completed":
            self._finished = True
            self._session.capture_response(self._recorded_envelope)
            _finish_span(self._span, self._token)
            return
        error = OpenAIIntegrationError(
            f"replayed_stream_error: {self._termination.get('error_type', kind)}"
        )
        self._finished = True
        self._session.capture_response(self._recorded_envelope)
        _finish_span(self._span, self._token, error=error)
        raise error


class _TrackedAsyncStream(AsyncIterator[Any]):
    def __init__(
        self,
        source: Any,
        recorded: Any,
        session: BoundarySession,
        span: trace.Span,
        token: object | None,
        surface: str,
    ) -> None:
        self._source = source.__aiter__() if source is not None else None
        self._source_object = source
        self._session = session
        self._span = span
        self._token = token
        self._surface = surface
        self._events: list[dict[str, Any]] = []
        self._finished = False
        self._recorded_events: Iterator[Mapping[str, Any]] | None = None
        self._termination: Mapping[str, Any] | None = None
        self._recorded_envelope: Any = recorded
        self._final: Any = None
        if recorded is not None:
            events, self._termination = _parse_stream_envelope(recorded)
            self._recorded_events = iter(events)
            self._final = recorded.get("final") if isinstance(recorded, Mapping) else None

    def __aiter__(self) -> _TrackedAsyncStream:
        return self

    async def __anext__(self) -> Any:
        if self._finished:
            raise StopAsyncIteration
        if self._recorded_events is not None:
            try:
                event = next(self._recorded_events)
            except StopIteration:
                self._finish_replay()
                raise StopAsyncIteration
            return _decode_event(event["payload"], self._surface)
        try:
            token = context.attach(trace.set_span_in_context(self._span))
            try:
                assert self._source is not None
                event = await self._source.__anext__()
            finally:
                context.detach(token)
        except StopAsyncIteration:
            await self._finish("completed")
            raise
        except BaseException as exc:
            await self._finish("error", exc)
            raise
        self._events.append(
            {"type": _event_name(event), "payload": _encode_event(event, self._surface)}
        )
        return event

    async def close(self) -> None:
        if self._finished:
            return
        close = getattr(self._source_object, "close", None)
        aclose = getattr(self._source_object, "aclose", None)
        if callable(aclose):
            await aclose()
        elif callable(close):
            result = close()
            if inspect.isawaitable(result):
                await result
        await self._finish("closed")

    async def get_final_response(self) -> Any:
        if self._recorded_events is not None:
            return _decode_response(self._final, self._surface, None)
        callback = getattr(self._source_object, "get_final_response")
        result = callback()
        if inspect.isawaitable(result):
            result = await result
        self._final = _encode_response(result, self._surface)
        return result

    def __getattr__(self, name: str) -> Any:
        return getattr(self._source_object, name)

    async def _finish(self, kind: str, error: BaseException | None = None) -> None:
        if self._finished:
            return
        self._finished = True
        final = self._final
        self._session.capture_response(
            _stream_envelope(
                self._events,
                StreamTermination(kind, type(error).__name__ if error else None),
                final,
            )
        )
        _finish_span(self._span, self._token, error=error)

    def _finish_replay(self) -> None:
        assert self._termination is not None
        kind = self._termination.get("kind")
        if kind == "completed":
            self._finished = True
            self._session.capture_response(self._recorded_envelope)
            _finish_span(self._span, self._token)
            return
        error = OpenAIIntegrationError(
            f"replayed_stream_error: {self._termination.get('error_type', kind)}"
        )
        self._finished = True
        self._session.capture_response(self._recorded_envelope)
        _finish_span(self._span, self._token, error=error)
        raise error


def _start_call(
    parameters: dict[str, Any],
    surface: str,
    operation: str,
    options: OpenAIOptions,
) -> tuple[trace.Span, BoundarySession, object | None]:
    ensure_telemetry()
    tracer = trace.get_tracer("ai-flight-recorder.openai", "0.1.0")
    span = tracer.start_span(f"{surface}.{operation}")
    span.set_attribute("openinference.span.kind", "LLM")
    span.set_attribute("gen_ai.operation.name", "chat")
    span.set_attribute("gen_ai.provider.name", "openai")
    model = parameters.get("model")
    if isinstance(model, str):
        span.set_attribute("gen_ai.request.model", model)
    for key in ("temperature", "max_tokens", "max_output_tokens", "top_p"):
        value = parameters.get(key)
        if isinstance(value, (str, int, float, bool)):
            span.set_attribute(f"gen_ai.request.{key}", value)
    token = context.attach(trace.set_span_in_context(span))
    request = {
        "schema_version": "1",
        "integration": "openai",
        "operation": f"{surface}.{operation}",
        "parameters": _json_value(parameters),
    }
    session = BoundarySession(
        f"{options.logical_key_prefix}.{surface}.{operation}",
        request,
        boundary_class="llm",
        side_effect="read_only",
        replay_policy=options.replay_policy,
        parent_logical_key=options.parent_logical_key,
        span=span,
    )
    return span, session, token


def _finish_span(
    span: trace.Span,
    token: object | None,
    *,
    result: Any = None,
    error: BaseException | None = None,
) -> None:
    if result is not None:
        model = getattr(result, "model", None)
        if isinstance(model, str):
            span.set_attribute("gen_ai.response.model", model)
        usage = getattr(result, "usage", None)
        for source, target in (
            ("input_tokens", "input_tokens"),
            ("prompt_tokens", "input_tokens"),
            ("output_tokens", "output_tokens"),
            ("completion_tokens", "output_tokens"),
        ):
            value = getattr(usage, source, None)
            if isinstance(value, int):
                span.set_attribute(f"gen_ai.usage.{target}", value)
    if error is None:
        span.set_status(Status(StatusCode.OK))
    else:
        span.record_exception(error)
        span.set_status(Status(StatusCode.ERROR, type(error).__name__))
    try:
        if token is not None:
            context.detach(cast(Any, token))
    finally:
        span.end()


def _effective_parameters(
    original: dict[str, Any], effective_request: Any
) -> dict[str, Any]:
    if not isinstance(effective_request, Mapping):
        raise BoundaryError("unsafe_replay: OpenAI request override is malformed")
    parameters = effective_request.get("parameters")
    if not isinstance(parameters, Mapping):
        raise BoundaryError("unsafe_replay: OpenAI request override has no parameters")
    result = dict(original)
    for key, value in parameters.items():
        if isinstance(value, Mapping) and value.get("__flight_type__") == "pydantic_schema":
            if key not in result:
                raise BoundaryError(
                    "unsafe_replay: structured response type is unavailable in this process"
                )
            continue
        result[str(key)] = value
    return result


def _encode_response(value: Any, surface: str) -> dict[str, Any]:
    return {
        "schema_version": "1",
        "integration": "openai",
        "kind": "response",
        "surface": surface,
        "type": type(value).__name__,
        "value": _json_value(value),
    }


def _error_response(error: BaseException, surface: str) -> dict[str, Any]:
    return {
        "schema_version": "1",
        "integration": "openai",
        "kind": "error",
        "surface": surface,
        "error_type": type(error).__name__,
    }


def _decode_response(value: Any, surface: str, response_format: Any) -> Any:
    if value is None:
        return None
    if not isinstance(value, Mapping) or value.get("integration") != "openai":
        raise OpenAIIntegrationError("unsafe_replay: recorded OpenAI response is malformed")
    if value.get("kind") == "error":
        raise OpenAIIntegrationError(
            f"replayed_integration_error: {value.get('error_type', 'OpenAIError')}"
        )
    if value.get("surface") != surface or value.get("kind") != "response":
        raise OpenAIIntegrationError("unsafe_replay: recorded OpenAI surface does not match")
    recorded_type = str(value.get("type"))
    model = _response_types().get(recorded_type) or _response_types().get(
        recorded_type.split("[", 1)[0]
    )
    if model is None:
        raise OpenAIIntegrationError(
            f"unsupported_replay_type: OpenAI {value.get('type')} is not registered"
        )
    result = model.model_validate(value.get("value"))
    _restore_parsed_values(result, response_format)
    return result


def _encode_event(value: Any, surface: str) -> dict[str, Any]:
    return {
        "integration": "openai",
        "surface": surface,
        "type": type(value).__name__,
        "value": _json_value(value),
    }


def _decode_event(value: Any, surface: str) -> Any:
    if not isinstance(value, Mapping) or value.get("surface") != surface:
        raise OpenAIIntegrationError("unsafe_replay: recorded OpenAI event is malformed")
    model = _event_types().get(str(value.get("type")))
    if model is None:
        raise OpenAIIntegrationError(
            f"unsupported_replay_type: OpenAI event {value.get('type')} is not registered"
        )
    return model.model_validate(value.get("value"))


def _response_types() -> dict[str, Any]:
    try:
        ChatCompletion = importlib.import_module("openai.types.chat").ChatCompletion
        Response = importlib.import_module("openai.types.responses").Response
    except ImportError as exc:
        raise OpenAIIntegrationError("OpenAI replay requires the 'openai' extra") from exc
    result = {"ChatCompletion": ChatCompletion, "Response": Response}
    try:
        ParsedChatCompletion = importlib.import_module(
            "openai.types.chat.parsed_chat_completion"
        ).ParsedChatCompletion
        result["ParsedChatCompletion"] = ParsedChatCompletion
    except ImportError:
        pass
    try:
        ParsedResponse = importlib.import_module(
            "openai.types.responses.parsed_response"
        ).ParsedResponse
        result["ParsedResponse"] = ParsedResponse
    except ImportError:
        pass
    return result


def _event_types() -> dict[str, Any]:
    result: dict[str, Any] = {}
    try:
        ChatCompletionChunk = importlib.import_module(
            "openai.types.chat"
        ).ChatCompletionChunk
        ResponseStreamEvent = importlib.import_module(
            "openai.types.responses.response_stream_event"
        ).ResponseStreamEvent

        result["ChatCompletionChunk"] = ChatCompletionChunk

        def register(annotation: Any) -> None:
            if inspect.isclass(annotation) and hasattr(annotation, "model_validate"):
                result[annotation.__name__] = annotation
                return
            for item in get_args(annotation):
                register(item)

        register(ResponseStreamEvent)
    except ImportError as exc:
        raise OpenAIIntegrationError("OpenAI replay requires the 'openai' extra") from exc
    return result


def _restore_parsed_values(value: Any, response_format: Any) -> None:
    if response_format is None or not hasattr(response_format, "model_validate"):
        return
    seen: set[int] = set()

    def visit(item: Any) -> None:
        if id(item) in seen:
            return
        seen.add(id(item))
        if hasattr(item, "parsed") and getattr(item, "parsed") is not None:
            parsed = getattr(item, "parsed")
            if not isinstance(parsed, response_format):
                object.__setattr__(item, "parsed", response_format.model_validate(parsed))
        fields = getattr(type(item), "model_fields", None)
        if fields is not None:
            for name in fields:
                child = getattr(item, name, None)
                if isinstance(child, (list, tuple)):
                    for nested in child:
                        visit(nested)
                elif getattr(type(child), "model_fields", None) is not None:
                    visit(child)

    visit(value)


def _json_value(value: Any) -> Any:
    if value is None or isinstance(value, (str, int, float, bool)):
        return value
    if isinstance(value, Enum):
        return _json_value(value.value)
    if isinstance(value, Mapping):
        return {str(key): _json_value(item) for key, item in value.items()}
    if isinstance(value, (list, tuple)):
        return [_json_value(item) for item in value]
    if inspect.isclass(value) and hasattr(value, "model_json_schema"):
        return {
            "__flight_type__": "pydantic_schema",
            "schema": value.model_json_schema(),
        }
    if hasattr(value, "model_dump"):
        return _json_value(value.model_dump(mode="json", warnings=False))
    if hasattr(value, "to_dict"):
        return _json_value(value.to_dict())
    raise BoundaryError(f"validation: OpenAI value {type(value).__name__} is not JSON serializable")


def _event_name(value: Any) -> str:
    event_type = getattr(value, "type", None)
    return event_type if isinstance(event_type, str) else type(value).__name__


def _is_async_resource(resource: Any) -> bool:
    return inspect.iscoroutinefunction(getattr(resource, "create", None))


def _options(value: OpenAIOptions | Mapping[str, Any] | None) -> OpenAIOptions:
    if value is None:
        return OpenAIOptions()
    if isinstance(value, OpenAIOptions):
        return value
    return OpenAIOptions(
        logical_key_prefix=str(value.get("logical_key_prefix", "openai")),
        parent_logical_key=cast(str | None, value.get("parent_logical_key")),
        replay_policy=str(value.get("replay_policy", "live")),
    )
