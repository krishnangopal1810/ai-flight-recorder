import { Span, SpanStatusCode, trace } from "@opentelemetry/api";
import { Document } from "@langchain/core/documents";
import {
	AIMessage,
	AIMessageChunk,
	ChatMessage,
	ChatMessageChunk,
	FunctionMessage,
	FunctionMessageChunk,
	HumanMessage,
	HumanMessageChunk,
	SystemMessage,
	SystemMessageChunk,
	ToolMessage,
	ToolMessageChunk,
} from "@langchain/core/messages";
import {
	BoundaryOptions,
	BoundarySession,
	liveBoundary,
	parseStreamEnvelope,
	StreamEnvelope,
	withIntegration,
} from "./index.js";
import { ensureTelemetry } from "./telemetry.js";

export interface ComponentOptions {
	logicalKey: string;
	boundaryClass: "llm" | "retriever" | "tool";
	sideEffect?: "read_only" | "mutating" | "unknown";
	replayPolicy?: "substitute" | "live" | "block";
	sandbox?: string;
	parentLogicalKey?: string;
}

export function wrapChatModel<T extends object>(model: T, options: Partial<ComponentOptions> = {}): T {
	return wrapRunnable(model, {logicalKey: options.logicalKey ?? "langchain.model", boundaryClass: "llm", sideEffect: "read_only", replayPolicy: options.replayPolicy ?? "live", parentLogicalKey: options.parentLogicalKey});
}

export function wrapRetriever<T extends object>(retriever: T, options: Partial<ComponentOptions> = {}): T {
	return wrapRunnable(retriever, {logicalKey: options.logicalKey ?? "langchain.retriever", boundaryClass: "retriever", sideEffect: "read_only", replayPolicy: options.replayPolicy ?? "substitute", parentLogicalKey: options.parentLogicalKey});
}

export function wrapTool<T extends object>(tool: T, options: Partial<ComponentOptions> = {}): T {
	return wrapRunnable(tool, {logicalKey: options.logicalKey ?? "langchain.tool", boundaryClass: "tool", sideEffect: options.sideEffect ?? "unknown", replayPolicy: options.replayPolicy ?? "substitute", sandbox: options.sandbox, parentLogicalKey: options.parentLogicalKey});
}

export function flightRecorderMiddleware(options: {
	logicalKeyPrefix?: string;
	parentLogicalKey?: string;
	toolSideEffects?: Record<string, "read_only" | "mutating" | "unknown">;
	toolSandboxes?: Record<string, string>;
} = {}): Record<string, unknown> {
	const prefix = options.logicalKeyPrefix ?? "langchain.agent";
	return {
		name: "flight_recorder",
		wrapModelCall: async (request: unknown, handler: (request: unknown) => Promise<unknown>) => componentValue(codecEncode(request), () => handler(request), {logicalKey: `${prefix}.model`, boundaryClass: "llm", sideEffect: "read_only", replayPolicy: "live", parentLogicalKey: options.parentLogicalKey}),
		wrapToolCall: async (request: unknown, handler: (request: unknown) => Promise<unknown>) => {
			const name = toolName(request);
			return componentValue(codecEncode(request), () => handler(request), {logicalKey: `${prefix}.tool.${name}`, boundaryClass: "tool", sideEffect: options.toolSideEffects?.[name] ?? "unknown", replayPolicy: "substitute", sandbox: options.toolSandboxes?.[name], parentLogicalKey: options.parentLogicalKey});
		},
	};
}

function wrapRunnable<T extends object>(runnable: T, options: ComponentOptions): T {
	return new Proxy(runnable, {
		get(target, property, receiver): unknown {
			const original = Reflect.get(target, property, receiver) as unknown;
			if (!["invoke", "stream", "batch"].includes(String(property)) || typeof original !== "function") return original;
			if (property === "batch") return original.bind(target);
			return (input: unknown, config?: unknown, ...rest: unknown[]): unknown => {
				const request = runnableRequest(input, config, rest);
				if (property === "stream") return componentStream(request, effective => original.call(target, requestInput(effective), config, ...rest), options);
				return componentValue(request, effective => original.call(target, requestInput(effective), config, ...rest), options);
			};
		},
	});
}

async function componentValue(request: unknown, call: (effective: unknown) => unknown, options: ComponentOptions): Promise<unknown> {
	const {span, session} = await startComponent(request, options);
	try {
		const action = session.action();
		const result = action === liveBoundary ? await withIntegration(() => call(session.request)) : codecDecode(action);
		session.capture(codecEncode(result));
		finishSpan(span);
		return result;
	} catch (error) {
		finishSpan(span, error);
		throw error;
	}
}

async function componentStream(request: unknown, call: (effective: unknown) => unknown, options: ComponentOptions): Promise<AsyncIterable<unknown>> {
	const {span, session} = await startComponent(request, options);
	try {
		const action = session.action();
		const source = action === liveBoundary ? await withIntegration(() => call(session.request)) : undefined;
		return new LangChainStream(source, action === liveBoundary ? undefined : action, session, span);
	} catch (error) {
		finishSpan(span, error);
		throw error;
	}
}

class LangChainStream implements AsyncIterable<unknown> {
	constructor(private source: unknown, private recorded: unknown, private session: BoundarySession, private span: Span) {}

	async *[Symbol.asyncIterator](): AsyncIterator<unknown> {
		if (this.recorded !== undefined) {
			const envelope = parseStreamEnvelope(this.recorded);
			for (const event of envelope.events) yield codecDecode(event.payload);
			this.session.capture(this.recorded);
			if (envelope.termination.kind === "completed") { finishSpan(this.span); return; }
			const error = new Error(`replayed_stream_error: ${envelope.termination.error_type ?? envelope.termination.kind}`);
			finishSpan(this.span, error);
			throw error;
		}
		const events: Array<{type: string; payload: unknown}> = [];
		let termination: StreamEnvelope["termination"] = {kind: "closed"};
		try {
			for await (const event of this.source as AsyncIterable<unknown>) {
				events.push({type: eventType(event), payload: codecEncode(event)});
				yield event;
			}
			termination = {kind: "completed"};
		} catch (error) {
			termination = {kind: "error", error_type: error instanceof Error ? error.name : "unknown"};
			throw error;
		} finally {
			this.session.capture({schema_version: "1", kind: "stream", events, termination} satisfies StreamEnvelope);
			finishSpan(this.span, termination.kind === "error" ? new Error(termination.error_type) : undefined);
		}
	}
}

async function startComponent(request: unknown, options: ComponentOptions): Promise<{span: Span; session: BoundarySession}> {
	await ensureTelemetry();
	const span = trace.getTracer("ai-flight-recorder.langchain", "0.1.0").startSpan(options.logicalKey);
	span.setAttribute("openinference.span.kind", options.boundaryClass.toUpperCase());
	span.setAttribute("gen_ai.operation.name", options.boundaryClass === "llm" ? "chat" : options.boundaryClass === "tool" ? "execute_tool" : "retriever");
	const boundaryOptions: BoundaryOptions = {...options};
	return {span, session: new BoundarySession(request, boundaryOptions, span)};
}

function runnableRequest(input: unknown, config: unknown, rest: unknown[]): Record<string, unknown> {
	const safeConfig: Record<string, unknown> = {};
	if (config && typeof config === "object" && !Array.isArray(config)) for (const key of ["configurable", "tags", "metadata", "runName", "recursionLimit"]) if (key in config) safeConfig[key] = codecEncode((config as Record<string, unknown>)[key]);
	return {schema_version: "1", integration: "langchain", input: codecEncode(input), config: safeConfig, args: codecEncode(rest)};
}

function requestInput(request: unknown): unknown {
	if (!request || typeof request !== "object" || Array.isArray(request) || (request as Record<string, unknown>).integration !== "langchain") throw new Error("unsafe_replay: LangChain request override is malformed");
	return codecDecode((request as Record<string, unknown>).input);
}

function codecEncode(value: unknown): unknown {
	if (value === null || ["string", "number", "boolean"].includes(typeof value)) return value;
	if (Array.isArray(value)) return value.map(codecEncode);
	if (value instanceof Document) return {__flight_type__: "langchain_document", class: "Document", value: {pageContent: value.pageContent, metadata: codecEncode(value.metadata), id: value.id}};
	if (value && typeof value === "object" && typeof (value as {_getType?: unknown})._getType === "function") {
		const message = value as {_getType(): string; toDict?: () => {data?: unknown}};
		return {__flight_type__: "langchain_message", class: value.constructor.name, value: codecEncode(message.toDict?.().data ?? Object.fromEntries(Object.entries(value).filter(([, item]) => typeof item !== "function")))};
	}
	if (value && typeof value === "object") return Object.fromEntries(Object.entries(value).filter(([, item]) => item !== undefined && typeof item !== "function").map(([key, item]) => [key, codecEncode(item)]));
	throw new Error(`validation: unsupported LangChain value ${typeof value}`);
}

function codecDecode(value: unknown): unknown {
	if (Array.isArray(value)) return value.map(codecDecode);
	if (!value || typeof value !== "object") return value;
	const encoded = value as Record<string, unknown>;
	if (!encoded.__flight_type__) return Object.fromEntries(Object.entries(encoded).map(([key, item]) => [key, codecDecode(item)]));
	const payload = codecDecode(encoded.value) as Record<string, unknown>;
	if (encoded.__flight_type__ === "langchain_document" && encoded.class === "Document") return new Document(payload as {pageContent: string; metadata?: Record<string, unknown>; id?: string});
	const Message = messageTypes[String(encoded.class)];
	if (encoded.__flight_type__ === "langchain_message" && Message) return new Message(payload as never);
	throw new Error(`unsupported_replay_type: LangChain ${String(encoded.class)} is not registered`);
}

const messageTypes: Record<string, new (...args: never[]) => unknown> = {AIMessage, AIMessageChunk, ChatMessage, ChatMessageChunk, FunctionMessage, FunctionMessageChunk, HumanMessage, HumanMessageChunk, SystemMessage, SystemMessageChunk, ToolMessage, ToolMessageChunk};
function toolName(request: unknown): string {
	if (!request || typeof request !== "object") return "unknown";
	const item = request as Record<string, unknown>;
	const tool = item.tool as Record<string, unknown> | undefined;
	if (typeof tool?.name === "string") return tool.name;
	const call = item.toolCall as Record<string, unknown> | undefined;
	return typeof call?.name === "string" ? call.name : "unknown";
}
function eventType(value: unknown): string { return value && typeof value === "object" ? value.constructor.name : typeof value; }
function finishSpan(span: Span, error?: unknown): void { if (error) { span.recordException(error as Error); span.setStatus({code: SpanStatusCode.ERROR, message: error instanceof Error ? error.name : "unknown"}); } else span.setStatus({code: SpanStatusCode.OK}); span.end(); }
