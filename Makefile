SHELL := /bin/bash

.PHONY: bootstrap generate build test integration e2e security docs-check quality verify architecture-check dev-up dev-down observe

bootstrap:
	go mod download
	cd sdk/python && uv sync --all-groups --frozen
	cd sdk/typescript && npm ci
	cd web && npm ci

generate:
	go run ./cmd/flight schema generate --output docs/generated
	go run ./cmd/flight help markdown > docs/generated/cli.md
	cd web && npm run generate:api

build:
	go build ./...
	cd sdk/typescript && npm run build
	cd web && npm run build
	cd sdk/python && uv run python -m compileall -q src

test:
	go test ./...
	cd sdk/python && uv run ruff check src tests && uv run mypy src && uv run pytest
	cd sdk/typescript && npm test
	cd web && npm test

integration:
	go test -tags=integration ./internal/... ./tests/conformance/...

e2e:
	go test -tags=e2e ./tests/e2e/...
	cd web && npm run test:e2e

security:
	go test -tags=security ./tests/security/...

architecture-check:
	./scripts/check-architecture.sh

docs-check:
	./scripts/check-docs.sh

quality:
	./scripts/quality-report.sh

verify: architecture-check docs-check generate build test integration security e2e
	git diff --exit-code -- docs/generated

dev-up:
	./scripts/dev-stack.sh up

dev-down:
	./scripts/dev-stack.sh down

observe:
	./scripts/observe.sh $(QUERY)
