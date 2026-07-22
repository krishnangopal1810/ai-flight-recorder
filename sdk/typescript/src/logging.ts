import type { Logger } from "@opentelemetry/api-logs";
import { OTLPLogExporter } from "@opentelemetry/exporter-logs-otlp-http";
import type { Resource } from "@opentelemetry/resources";
import { BatchLogRecordProcessor, LoggerProvider } from "@opentelemetry/sdk-logs";

export interface OTLPLoggingOptions {
  resource?: Resource;
  endpoint?: string;
  headers?: Record<string, string>;
  scopeName?: string;
  scopeVersion?: string;
}

export interface OTLPLoggingRuntime {
  logger: Logger;
  forceFlush(): Promise<void>;
  shutdown(): Promise<void>;
}

export function createOTLPLogging(options: OTLPLoggingOptions = {}): OTLPLoggingRuntime {
  const exporter = new OTLPLogExporter({
    ...(options.endpoint ? {url: logsEndpoint(options.endpoint)} : {}),
    ...(options.headers ? {headers: options.headers} : {}),
  });
  const processor = new BatchLogRecordProcessor({exporter});
  const provider = new LoggerProvider({
    ...(options.resource ? {resource: options.resource} : {}),
    processors: [processor],
  });
  const logger = provider.getLogger(
    options.scopeName ?? "@ai-flight-recorder/sdk",
    options.scopeVersion,
  );
  return {
    logger,
    forceFlush: () => provider.forceFlush(),
    shutdown: () => provider.shutdown(),
  };
}

function logsEndpoint(endpoint: string): string {
  const normalized = endpoint.replace(/\/$/, "");
  return normalized.endsWith("/v1/logs") ? normalized : `${normalized}/v1/logs`;
}
