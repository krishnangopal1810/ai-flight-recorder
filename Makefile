SHELL := /bin/bash

.PHONY: help bootstrap harness-check harness-static generate build test integration e2e security docs-check quality verify architecture-check dev-up dev-down observe

help:
	@echo 'AI Flight Recorder development commands:'
	@echo '  make bootstrap          Install all pinned project dependencies'
	@echo '  make harness-check      Validate the agent harness in a fresh isolated worktree'
	@echo '  make generate           Rebuild checked-in schemas, CLI docs, and inspector types'
	@echo '  make build              Build Go, Python, TypeScript, and inspector artifacts'
	@echo '  make test               Run unit, SDK, and inspector tests'
	@echo '  make integration        Run backend and OTLP conformance contracts'
	@echo '  make security           Run privacy and live OCI isolation probes'
	@echo '  make e2e                Run bundle and browser journeys'
	@echo '  make quality            Emit the machine-readable quality report'
	@echo '  make verify             Run every blocking repository gate'
	@echo '  make dev-up/dev-down    Start or tear down this worktree stack'
	@echo '  make observe QUERY=...  Query logs, metrics, capsules, traces, or job events'

bootstrap:
	go mod download
	cd sdk/python && uv sync --all-groups --all-extras --frozen
	cd sdk/typescript && npm ci && npm run build
	cd examples/reference-typescript && npm ci --install-links
	cd web && npm ci

harness-check:
	./scripts/harness-check.sh --full

harness-static:
	./scripts/harness-check.sh --static

generate:
	go run ./cmd/flight schema generate --output docs/generated
	go run ./cmd/flight help markdown > docs/generated/cli.md
	cd web && npm run generate:api

build:
	go build ./...
	cd sdk/typescript && npm run build
	cd examples/reference-typescript && npm run build
	cd web && npm run build
	cd sdk/python && uv run python -m compileall -q src

test:
	go test ./...
	cd sdk/python && uv run ruff check src tests ../../examples/reference-python ../../examples/investigation-agent && uv run mypy src ../../examples/reference-python && uv run mypy src ../../examples/investigation-agent && uv run pytest
	cd sdk/typescript && npm test
	cd examples/reference-typescript && npm test
	cd web && npm test

integration:
	go test -tags=integration ./internal/... ./tests/conformance/...

e2e:
	go test -tags=e2e ./tests/e2e/...
	cd web && npm run test:e2e

security:
	@if docker info >/dev/null 2>&1; then docker build --tag ai-flight-recorder:local --file deploy/Dockerfile . && ./scripts/playback-offline-smoke.sh; fi
	go test -count=1 -tags=security ./tests/security/...

architecture-check:
	./scripts/check-architecture.sh

docs-check:
	./scripts/check-docs.sh

quality:
	./scripts/quality-report.sh

verify: harness-check architecture-check docs-check generate build test integration security e2e quality
	./scripts/harness-check.sh --static

dev-up:
	./scripts/dev-stack.sh up

dev-down:
	./scripts/dev-stack.sh down

observe:
	./scripts/observe.sh $(QUERY)
