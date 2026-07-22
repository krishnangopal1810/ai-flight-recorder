import { context, Span, SpanStatusCode, trace } from "@opentelemetry/api";
import {
	BoundaryOptions,
	BoundarySession,
	integrationActive,
	liveBoundary,
	parseStreamEnvelope,
	StreamEnvelope,
} from "./index.js";
import { ensureTelemetry } from "./telemetry.js";

export interface OpenAIOptions {
	logicalKeyPrefix?: string;
	parentLogicalKey?: string;
	replayPolicy?: "substitute" | "live" | "block";
}

export class OpenAIIntegrationError extends Error {}

export function wrapOpenAI<T extends object>(client: T, options: OpenAIOptions = {}): T {
	const responses = wrapResource((client as Record<string, unknown>).responses as object, "responses", options);
	const chat = (client as Record<string, unknown>).chat as Record<string, unknown> | undefined;
	const chatProxy = chat ? new Proxy(chat, {
		get(target, property, receiver): unknown {
			if (property === "completions") return wrapResource(target.completions as object, "chat.completions", options);
			return Reflect.get(target, property, receiver);
		},
	}) : undefined;
	return new Proxy(client, {
		get(target, property, receiver): unknown {
			if (property === "responses") return responses;
			if (property === "chat" && chatProxy) return chatProxy;
			return Reflect.get(target, property, receiver);
		},
	});
}

function wrapResource(resource: object, surface: string, options: OpenAIOptions): object {
	return new Proxy(resource, {
		get(target, property, receiver): unknown {
			const original = Reflect.get(target, property, receiver) as unknown;
			if (!["create", "parse", "stream"].includes(String(property)) || typeof original !== "function") return original;
			return (parameters: Record<string, unknown>, ...rest: unknown[]): unknown => {
				if (integrationActive()) return original.call(target, parameters, ...rest);
				const operation = String(property);
				if (operation === "stream") return trackedStream(() => original.call(target, parameters, ...rest), parameters, surface, operation, options);
				if (parameters?.stream === true) return startCall(parameters, surface, operation, options).then(started => {
					if (started.action !== liveBoundary) return new OpenAIStream(undefined, started.action, started.session, started.span, surface);
					const source = context.with(trace.setSpan(context.active(), started.span), () => original.call(target, effectiveParameters(parameters, started.session.request), ...rest));
					return Promise.resolve(source).then(value => new OpenAIStream(value, undefined, started.session, started.span, surface));
				});
				return valueCall(effective => original.call(target, effective, ...rest), parameters, surface, operation, options);
			};
		},
	});
}

async function valueCall(
	call: (effective: Record<string, unknown>) => unknown,
	parameters: Record<string, unknown>,
	surface: string,
	operation: string,
	options: OpenAIOptions,
): Promise<unknown> {
	const started = await startCall(parameters, surface, operation, options);
	try {
		if (started.action !== liveBoundary) {
			const value = decodeResponse(started.action, surface);
			started.session.capture(started.action);
			finishSpan(started.span, undefined, value);
			return value;
		}
		const effective = effectiveParameters(parameters, started.session.request);
		const result = await context.with(trace.setSpan(context.active(), started.span), () => call(effective));
		const encoded = encodeResponse(result, surface);
		started.session.capture(encoded);
		finishSpan(started.span, undefined, result);
		return result;
	} catch (error) {
		started.session.capture(errorResponse(error, surface));
		finishSpan(started.span, error);
		throw error;
	}
}

function trackedStream(call: () => unknown, parameters: Record<string, unknown>, surface: string, operation: string, options: OpenAIOptions): OpenAIStream {
	const source = startCall(parameters, surface, operation, options).then(started => {
		if (started.action !== liveBoundary) return {started, source: undefined};
		return Promise.resolve(context.with(trace.setSpan(context.active(), started.span), () => call())).then(value => ({started, source: value}));
	});
	return new OpenAIStream(source, undefined, undefined, undefined, surface);
}

class OpenAIStream implements AsyncIterable<unknown> {
	private listeners: Array<[string, (...args: unknown[]) => void]> = [];
	private finalValue: unknown;
	constructor(
		private sourceOrStart: unknown,
		private recorded: unknown,
		private session: BoundarySession | undefined,
		private span: Span | undefined,
		private surface: string,
	) {}

	on(name: string, listener: (...args: unknown[]) => void): this {
		this.listeners.push([name, listener]);
		void this.attachListeners();
		return this;
	}

	async finalResponse(): Promise<unknown> { return this.getFinalResponse(); }
	async getFinalResponse(): Promise<unknown> {
		if (this.finalValue !== undefined) return this.finalValue;
		const source = await this.resolveSource();
		for (const name of ["finalResponse", "getFinalResponse"]) {
			const callback = source && typeof source === "object" ? (source as Record<string, unknown>)[name] : undefined;
			if (typeof callback === "function") {
				this.finalValue = await callback.call(source);
				return this.finalValue;
			}
		}
		if (this.recorded && typeof this.recorded === "object" && "final" in this.recorded) return decodeResponse((this.recorded as StreamEnvelope).final, this.surface);
		return undefined;
	}

	async done(): Promise<void> { for await (const _event of this) { /* drain */ } }

	async *[Symbol.asyncIterator](): AsyncIterator<unknown> {
		const source = await this.resolveSource();
		if (!this.session || !this.span) throw new OpenAIIntegrationError("OpenAI stream did not initialize");
		if (this.recorded !== undefined) {
			const envelope = parseStreamEnvelope(this.recorded);
			for (const event of envelope.events) yield decodeEvent(event.payload, this.surface);
			this.session.capture(this.recorded);
			if (envelope.termination.kind === "completed") { finishSpan(this.span); return; }
			const error = new OpenAIIntegrationError(`replayed_stream_error: ${envelope.termination.error_type ?? envelope.termination.kind}`);
			finishSpan(this.span, error);
			throw error;
		}
		const events: Array<{type: string; payload: unknown}> = [];
		let termination: StreamEnvelope["termination"] = {kind: "closed"};
		try {
			for await (const event of source as AsyncIterable<unknown>) {
				events.push({type: eventType(event), payload: encodeEvent(event, this.surface)});
				yield event;
			}
			termination = {kind: "completed"};
		} catch (error) {
			termination = {kind: "error", error_type: error instanceof Error ? error.name : "unknown"};
			throw error;
		} finally {
			if (termination.kind === "completed") {
				try { this.finalValue = await this.getFinalResponse(); } catch { this.finalValue = undefined; }
			}
			const envelope: StreamEnvelope = {schema_version: "1", kind: "stream", events, termination};
			if (this.finalValue !== undefined) envelope.final = encodeResponse(this.finalValue, this.surface);
			this.session.capture(envelope);
			finishSpan(this.span, termination.kind === "error" ? new OpenAIIntegrationError(termination.error_type) : undefined);
		}
	}

	private async resolveSource(): Promise<unknown> {
		if (this.session) return this.sourceOrStart;
		const resolved = await this.sourceOrStart as {started: Awaited<ReturnType<typeof startCall>>; source: unknown};
		this.session = resolved.started.session;
		this.span = resolved.started.span;
		if (resolved.started.action !== liveBoundary) this.recorded = resolved.started.action;
		this.sourceOrStart = resolved.source;
		return resolved.source;
	}

	private async attachListeners(): Promise<void> {
		const source = await this.resolveSource();
		const on = source && typeof source === "object" ? (source as Record<string, unknown>).on : undefined;
		if (typeof on === "function") for (const [name, listener] of this.listeners.splice(0)) on.call(source, name, listener);
	}
}

async function startCall(parameters: Record<string, unknown>, surface: string, operation: string, options: OpenAIOptions): Promise<{span: Span; session: BoundarySession; action: unknown}> {
	await ensureTelemetry();
	const span = trace.getTracer("ai-flight-recorder.openai", "0.1.0").startSpan(`${surface}.${operation}`);
	span.setAttribute("openinference.span.kind", "LLM");
	span.setAttribute("gen_ai.operation.name", "chat");
	span.setAttribute("gen_ai.provider.name", "openai");
	if (typeof parameters.model === "string") span.setAttribute("gen_ai.request.model", parameters.model);
	for (const key of ["temperature", "max_tokens", "max_output_tokens", "top_p"]) if (["string", "number", "boolean"].includes(typeof parameters[key])) span.setAttribute(`gen_ai.request.${key}`, parameters[key] as string | number | boolean);
	const request = {schema_version: "1", integration: "openai", operation: `${surface}.${operation}`, parameters: jsonValue(parameters)};
	const boundaryOptions: BoundaryOptions = {logicalKey: `${options.logicalKeyPrefix ?? "openai"}.${surface}.${operation}`, boundaryClass: "llm", sideEffect: "read_only", replayPolicy: options.replayPolicy ?? "live", parentLogicalKey: options.parentLogicalKey};
	const session = new BoundarySession(request, boundaryOptions, span);
	return {span, session, action: session.action()};
}

function effectiveParameters(original: Record<string, unknown>, request: unknown): Record<string, unknown> {
	if (request === undefined) return original;
	if (!request || typeof request !== "object" || Array.isArray(request) || !((request as Record<string, unknown>).parameters) || typeof (request as Record<string, unknown>).parameters !== "object") throw new OpenAIIntegrationError("unsafe_replay: OpenAI request override is malformed");
	const encoded = (request as {parameters: Record<string, unknown>}).parameters;
	const result = {...original};
	for (const [key, value] of Object.entries(encoded)) {
		if (!(key in original)) { result[key] = value; continue; }
		const originalValue = original[key];
		result[key] = JSON.stringify(jsonValue(originalValue)) === JSON.stringify(value) ? originalValue : value;
	}
	return result;
}

function encodeResponse(value: unknown, surface: string): Record<string, unknown> { return {schema_version: "1", integration: "openai", kind: "response", surface, value: jsonValue(value)}; }
function decodeResponse(value: unknown, surface: string): unknown {
	if (!value || typeof value !== "object" || Array.isArray(value)) throw new OpenAIIntegrationError("unsafe_replay: recorded OpenAI response is malformed");
	const encoded = value as Record<string, unknown>;
	if (encoded.kind === "error") throw new OpenAIIntegrationError(`replayed_integration_error: ${encoded.error_type ?? "OpenAIError"}`);
	if (encoded.integration !== "openai" || encoded.surface !== surface || encoded.kind !== "response") throw new OpenAIIntegrationError("unsafe_replay: recorded OpenAI surface does not match");
	return encoded.value;
}
function errorResponse(error: unknown, surface: string): Record<string, unknown> { return {schema_version: "1", integration: "openai", kind: "error", surface, error_type: error instanceof Error ? error.name : "unknown"}; }
function encodeEvent(value: unknown, surface: string): Record<string, unknown> { return {integration: "openai", surface, value: jsonValue(value)}; }
function decodeEvent(value: unknown, surface: string): unknown {
	if (!value || typeof value !== "object" || (value as Record<string, unknown>).surface !== surface) throw new OpenAIIntegrationError("unsafe_replay: recorded OpenAI event is malformed");
	return (value as Record<string, unknown>).value;
}
function eventType(value: unknown): string { return value && typeof value === "object" && "type" in value && typeof (value as {type?: unknown}).type === "string" ? (value as {type: string}).type : value && typeof value === "object" ? value.constructor.name : typeof value; }
function jsonValue(value: unknown): unknown {
	if (value === null || ["string", "number", "boolean"].includes(typeof value)) return value;
	if (Array.isArray(value)) return value.map(jsonValue);
	if (value && typeof value === "object") {
		if ("toJSON" in value && typeof (value as {toJSON?: unknown}).toJSON === "function") return jsonValue((value as {toJSON(): unknown}).toJSON());
		const result: Record<string, unknown> = {};
		for (const [key, item] of Object.entries(value)) if (item !== undefined && typeof item !== "function") result[key] = jsonValue(item);
		return result;
	}
	throw new OpenAIIntegrationError(`validation: OpenAI value ${typeof value} is not JSON serializable`);
}
function finishSpan(span: Span, error?: unknown, result?: unknown): void {
	if (result && typeof result === "object") {
		const model = (result as Record<string, unknown>).model;
		if (typeof model === "string") span.setAttribute("gen_ai.response.model", model);
	}
	if (error) { span.recordException(error as Error); span.setStatus({code: SpanStatusCode.ERROR, message: error instanceof Error ? error.name : "unknown"}); }
	else span.setStatus({code: SpanStatusCode.OK});
	span.end();
}
