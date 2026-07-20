#!/usr/bin/env bash
set -euo pipefail

go_tests=$(go test ./... >/dev/null 2>&1 && echo pass || echo fail)
python_sdk=$(cd sdk/python && uv run ruff check src tests ../../examples/reference-python >/dev/null 2>&1 && uv run mypy src ../../examples/reference-python >/dev/null 2>&1 && uv run pytest -q >/dev/null 2>&1 && echo pass || echo fail)
typescript_sdk=$(cd sdk/typescript && npm test -- --run >/dev/null 2>&1 && echo pass || echo fail)
inspector=$(cd web && npm test -- --run >/dev/null 2>&1 && echo pass || echo fail)
contracts=$(go test -tags=integration ./tests/conformance/... >/dev/null 2>&1 && echo pass || echo fail)
security=$(make --no-print-directory security >/dev/null 2>&1 && echo pass || echo fail)
docs=$(./scripts/check-docs.sh >/dev/null 2>&1 && echo pass || echo fail)
architecture=$(./scripts/check-architecture.sh >/dev/null 2>&1 && echo pass || echo fail)
compose=$(docker compose -f deploy/docker-compose.yml config --quiet >/dev/null 2>&1 && echo pass || echo fail)
harness=$(./scripts/harness-check.sh --static >/dev/null 2>&1 && echo pass || echo fail)
printf '{"go":"%s","python_sdk":"%s","typescript_sdk":"%s","inspector":"%s","conformance":"%s","security":"%s","docs":"%s","architecture":"%s","compose":"%s","harness_static":"%s"}\n' "$go_tests" "$python_sdk" "$typescript_sdk" "$inspector" "$contracts" "$security" "$docs" "$architecture" "$compose" "$harness"
test "$go_tests" = pass
test "$python_sdk" = pass
test "$typescript_sdk" = pass
test "$inspector" = pass
test "$contracts" = pass
test "$security" = pass
test "$docs" = pass
test "$architecture" = pass
test "$compose" = pass
test "$harness" = pass
