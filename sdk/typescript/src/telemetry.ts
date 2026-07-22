import { trace } from "@opentelemetry/api";

export interface TelemetryRuntime {
	provider: {forceFlush?: () => Promise<void>; shutdown?: () => Promise<void>};
	owned: boolean;
	forceFlush(): Promise<void>;
	shutdown(): Promise<void>;
}

let runtime: TelemetryRuntime | undefined;

export async function configureTelemetry(options: {
	endpoint?: string;
	headers?: Record<string, string>;
	serviceName?: string;
} = {}): Promise<TelemetryRuntime> {
	if (runtime) return runtime;
	const current = trace.getTracerProvider() as unknown as {constructor?: {name?: string}; forceFlush?: () => Promise<void>; shutdown?: () => Promise<void>};
	if (current.constructor?.name !== "ProxyTracerProvider") {
		runtime = makeRuntime(current, false);
		return runtime;
	}
	const [{NodeTracerProvider}, {BatchSpanProcessor}, {OTLPTraceExporter}, resources] = await Promise.all([
		import("@opentelemetry/sdk-trace-node"),
		import("@opentelemetry/sdk-trace-base"),
		import("@opentelemetry/exporter-trace-otlp-http"),
		import("@opentelemetry/resources"),
	]);
	const resourceAttributes = parseResourceAttributes();
	resourceAttributes["service.name"] ??= options.serviceName ?? "ai-application";
	const exporter = new OTLPTraceExporter({
		url: options.endpoint ?? traceEndpoint(),
		headers: options.headers ?? traceHeaders(),
	});
	const provider = new NodeTracerProvider({
		resource: resources.resourceFromAttributes(resourceAttributes),
		spanProcessors: [new BatchSpanProcessor(exporter)],
	});
	provider.register();
	runtime = makeRuntime(provider, true);
	process.once("beforeExit", () => { void runtime?.shutdown(); });
	return runtime;
}

export async function ensureTelemetry(): Promise<TelemetryRuntime | undefined> {
	if (!captureEnvironment()) return undefined;
	try { return await configureTelemetry(); }
	catch (error) {
		process.stderr.write(JSON.stringify({level: "warning", code: "flight_sdk_metadata", operation: "configure_telemetry", error_type: error instanceof Error ? error.name : "unknown"}) + "\n");
		return undefined;
	}
}

function makeRuntime(provider: {forceFlush?: () => Promise<void>; shutdown?: () => Promise<void>}, owned: boolean): TelemetryRuntime {
	return {
		provider,
		owned,
		async forceFlush(): Promise<void> { await provider.forceFlush?.(); },
		async shutdown(): Promise<void> { if (owned) await provider.shutdown?.(); },
	};
}

function captureEnvironment(): boolean {
	if (process.env.FLIGHT_PROJECT_ID) return true;
	if (["1", "true", "yes"].includes((process.env.FLIGHT_CAPTURE ?? "").toLowerCase())) return true;
	return parseResourceAttributes()["flight.capture"]?.toLowerCase() === "true";
}

function parseResourceAttributes(): Record<string, string> {
	const result: Record<string, string> = {};
	for (const item of (process.env.OTEL_RESOURCE_ATTRIBUTES ?? "").split(",")) {
		const separator = item.indexOf("=");
		if (separator < 1) continue;
		result[decodeURIComponent(item.slice(0, separator).trim())] = decodeURIComponent(item.slice(separator + 1).trim());
	}
	return result;
}

function traceEndpoint(): string | undefined {
	if (process.env.OTEL_EXPORTER_OTLP_TRACES_ENDPOINT) return process.env.OTEL_EXPORTER_OTLP_TRACES_ENDPOINT;
	const base = process.env.OTEL_EXPORTER_OTLP_ENDPOINT?.replace(/\/$/, "");
	if (!base) return undefined;
	return base.endsWith("/v1/traces") ? base : `${base}/v1/traces`;
}

function traceHeaders(): Record<string, string> | undefined {
	const encoded = process.env.OTEL_EXPORTER_OTLP_TRACES_HEADERS ?? process.env.OTEL_EXPORTER_OTLP_HEADERS;
	if (!encoded) return undefined;
	const result: Record<string, string> = {};
	for (const item of encoded.split(",")) {
		const separator = item.indexOf("=");
		if (separator < 1) continue;
		result[decodeURIComponent(item.slice(0, separator).trim())] = decodeURIComponent(item.slice(separator + 1).trim());
	}
	return result;
}

export const telemetryTesting = {reset(): void { runtime = undefined; }};
