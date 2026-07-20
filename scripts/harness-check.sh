#!/usr/bin/env bash
set -euo pipefail

mode="${1:---full}"
case "$mode" in
  --full|--static) ;;
  --help|-h)
    echo 'usage: scripts/harness-check.sh [--full|--static]'
    echo '  --full    run static contracts and the fresh-worktree smoke journey'
    echo '  --static  run deterministic repository checks without starting services'
    exit 0
    ;;
  *)
    echo 'harness-check: expected --full or --static; run scripts/harness-check.sh --help' >&2
    exit 2
    ;;
esac

root=$(git rev-parse --show-toplevel)
cd "$root"

./scripts/check-docs.sh
./scripts/check-architecture.sh

python3 - <<'PY'
from pathlib import Path
import re

agents = Path("AGENTS.md").read_text()
makefile = Path("Makefile").read_text()
targets = set(re.findall(r"^([a-z][a-z0-9-]*):", makefile, re.MULTILINE))
required_targets = {
    "help", "bootstrap", "harness-check", "generate", "build", "test",
    "integration", "security", "e2e", "quality", "verify", "architecture-check",
    "docs-check", "dev-up", "dev-down", "observe",
}
errors = []
for target in sorted(required_targets):
    if target not in targets:
        errors.append(f"Makefile: missing documented target '{target}'; add an actionable recipe")
    if target != "help" and f"make {target}" not in agents:
        errors.append(f"AGENTS.md: missing discoverable command 'make {target}'")

acceptance = Path("docs/ACCEPTANCE.md").read_text()
test_sources = "\n".join(path.read_text() for path in Path(".").rglob("*_test.go"))
for number in range(1, 16):
    afr_id = f"AFR-T{number:02d}"
    if afr_id not in acceptance:
        errors.append(f"docs/ACCEPTANCE.md: missing {afr_id}")
    if afr_id not in test_sources and afr_id.replace("-", "") not in test_sources:
        errors.append(f"tests: missing deterministic executable named {afr_id}")

quality = Path("docs/QUALITY_SCORE.md").read_text().splitlines()
for line in quality:
    if line.startswith("|") and re.search(r"\|\s*A[-+]?\s*\|", line) and "`" not in line:
        errors.append("docs/QUALITY_SCORE.md: every grade must link to an executable command")

playwright = Path("web/playwright.config.ts").read_text()
for evidence in ['trace: "on"', 'screenshot: "on"', 'video: "on"', "FLIGHT_PLAYWRIGHT_OUTPUT_DIR"]:
    if evidence not in playwright:
        errors.append(f"web/playwright.config.ts: missing retained UI evidence setting {evidence}")
if "testInfo.attach" not in Path("web/tests/inspector.spec.ts").read_text():
    errors.append("web/tests/inspector.spec.ts: passing journeys must attach a DOM snapshot")
if not Path("web/tests/live-inspector.spec.ts").exists():
    errors.append("web/tests/live-inspector.spec.ts: missing live-stack inspector journey")

if errors:
    raise SystemExit("\n".join(errors))
PY

./scripts/dev-stack.sh --help >/dev/null
./scripts/observe.sh --help >/dev/null
./scripts/worktree-env.sh --help >/dev/null
go run ./cmd/flight --help >/dev/null
for command in record replay diff playback bisect capsule evaluator token serve; do
  go run ./cmd/flight "$command" --help >/dev/null
done

generated=$(mktemp -d -t flight-generated.XXXXXX)
trap 'rm -rf "$generated"' EXIT
go run ./cmd/flight schema generate --output "$generated/docs" >/dev/null
go run ./cmd/flight help markdown >"$generated/docs/cli.md"
cp docs/generated/index.md "$generated/docs/index.md"
diff -ru docs/generated "$generated/docs" >/dev/null || {
  echo 'harness-check: generated documentation drifted; run make generate and commit the result' >&2
  diff -ru docs/generated "$generated/docs" || true
  exit 1
}
(cd web && npx openapi-typescript ../api/openapi.yaml -o "$generated/openapi.ts" >/dev/null)
cmp -s web/src/generated/openapi.ts "$generated/openapi.ts" || {
  echo 'harness-check: generated inspector API types drifted; run make generate and commit the result' >&2
  exit 1
}

if [[ "$mode" == "--full" ]]; then
  if ! docker info >/dev/null 2>&1; then
    echo 'harness-check: Docker or Podman is required for the fresh-worktree smoke journey; start the configured engine and rerun make harness-check' >&2
    exit 1
  fi
  ./scripts/harness-smoke.sh
fi

echo "harness-check: ${mode#--} repository and agent workflow contracts passed"
