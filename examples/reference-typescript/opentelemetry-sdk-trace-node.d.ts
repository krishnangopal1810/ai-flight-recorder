// @opentelemetry/sdk-trace-node 2.9.0 publishes its runtime entrypoint without
// the declaration file named by package.json. Keep this reference-app shim
// narrow until the pinned package restores that file.
declare module "@opentelemetry/sdk-trace-node" {
  import {BasicTracerProvider, type TracerConfig} from "@opentelemetry/sdk-trace-base";

  export class NodeTracerProvider extends BasicTracerProvider {
    constructor(config?: TracerConfig);
    register(): void;
  }
}
