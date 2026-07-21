# AI Flight Recorder

AI Flight Recorder captures an agent run as an immutable, policy-safe capsule, plays its evidence back without executing code, and replays a candidate inside an isolated OCI network to identify the first meaningful divergence.

The V1 repository includes the Go collector/service/CLI, native Python and TypeScript SDKs, React inspector, SQLite/filesystem and PostgreSQL/S3-compatible storage, deterministic bundles, isolated replay, evaluators, and experimental Git bisect.

## Quickstart

Prerequisites are Go 1.25+, Node.js 22, [uv](https://docs.astral.sh/uv/), Git, and Docker. Podman is supported by the replay runtime when configured, while the bundled development stack uses Docker Compose.

```sh
make bootstrap
make verify
make dev-up
```

The worktree-local stack starts Flight Recorder, PostgreSQL, MinIO, and deterministic fake dependencies, then runs the instrumented refund-review agent in `examples/investigation-agent`. The agent records an eligible refund that was incorrectly denied, performs a real isolated replay with a policy-grounded prompt, and captures a successful candidate comparison through the normal product APIs. The inspector has no demo mode or example-specific behavior; these runs appear like any other `refund-operations` project. Discover the derived port with `./scripts/dev-stack.sh ps`, then open the mapped HTTP URL. The development bearer token is intentionally fixed and loopback-only:

```text
frt_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA
```

Query the running system with:

```sh
make observe QUERY=metrics
make observe QUERY=traces
./scripts/dev-stack.sh logs
make dev-down
```

For an embedded local service without Compose:

```sh
go run ./cmd/flight serve
```

Then initialize and record a replay-aware application:

```sh
go run ./cmd/flight init
go run ./cmd/flight record -- python app.py
go run ./cmd/flight capsule list --json
```

In the inspector, the workflow is **Add a run → Inspect trace → Change one value → Replay pipeline → Compare runs**. Select a tool or model operation in the trace to edit its recorded output; request-aware model boundaries also allow model-input edits. The replay keeps recorded tools offline and opens the resulting before/after comparison automatically.

## Repository map

- [Product requirements](ai-flight-recorder-prd.md)
- [Architecture](ARCHITECTURE.md)
- [Agent map](AGENTS.md)
- [Documentation index](docs/README.md)
- [Generated API, schema, CLI, and database references](docs/generated/index.md)
- [Operations and recovery](docs/OPERATIONS.md)
- [Release process](docs/RELEASING.md)
- [AFR acceptance matrix](docs/ACCEPTANCE.md)

`make verify` is the blocking local and CI gate. Generated contracts are checked in; change their source, run `make generate`, and commit the result together.
