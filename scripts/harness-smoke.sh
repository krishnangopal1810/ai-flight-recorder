#!/usr/bin/env bash
set -euo pipefail

if [[ "${1:-}" == "--help" || "${1:-}" == "-h" ]]; then
  echo 'usage: scripts/harness-smoke.sh'
  echo 'Creates an isolated temporary Git worktree, bootstraps it, starts the stack, queries evidence, runs tests and the live inspector journey, then tears it down.'
  exit 0
fi
if [[ $# -ne 0 ]]; then
  echo 'harness-smoke: no arguments are supported; run scripts/harness-smoke.sh --help' >&2
  exit 2
fi

source_root=$(git rev-parse --show-toplevel)
source "${source_root}/scripts/lib/worktree.sh"
flight_worktree_init
source_project="$COMPOSE_PROJECT_NAME"
run_id="$(date -u +%Y%m%dT%H%M%SZ)-$$"
artifact_root="${FLIGHT_HARNESS_ARTIFACT_ROOT:-${source_root}/.artifacts/harness/${run_id}}"
temporary_root=$(mktemp -d -t flight-harness.XXXXXX)
smoke_root="${temporary_root}/worktree"
step="setup"
failure_classification="harness_capability_gap"

mkdir -p "$artifact_root"
printf '%s\n' 'make harness-check' >"$artifact_root/reproduce.txt"

cleanup() {
  local exit_code=$?
  set +e
  if [[ -d "$smoke_root" && -x "$smoke_root/scripts/dev-stack.sh" ]]; then
    (
      cd "$smoke_root" || exit 0
      if [[ ! -s "$artifact_root/compose.log" ]]; then
        ./scripts/dev-stack.sh logs 300 >"$artifact_root/compose.log" 2>&1
      fi
      ./scripts/dev-stack.sh down >"$artifact_root/teardown.log" 2>&1
    )
  fi
  if [[ -d "$smoke_root" ]]; then
    git -C "$source_root" worktree remove --force "$smoke_root" >>"$artifact_root/teardown.log" 2>&1
  fi
  rmdir "$temporary_root" >>"$artifact_root/teardown.log" 2>&1
  if [[ $exit_code -eq 0 ]]; then
    printf '{"status":"passed","step":"complete","reproduce":"make harness-check"}\n' >"$artifact_root/status.json"
  else
    printf '{"status":"failed","step":"%s","classification":"%s","reproduce":"make harness-check"}\n' "$step" "$failure_classification" >"$artifact_root/status.json"
    echo "harness-smoke: failed during '${step}'; artifacts: ${artifact_root}; reproduce with: make harness-check" >&2
  fi
  exit "$exit_code"
}
trap cleanup EXIT

run_step() {
  local name="$1"
  shift
  step="$name"
  echo "harness-smoke: ${name}"
  "$@" >"$artifact_root/${name}.log" 2>&1 || {
    tail -100 "$artifact_root/${name}.log" >&2 || true
    return 1
  }
}

step="create-worktree"
failure_classification="harness_capability_gap"
git -C "$source_root" worktree add --detach "$smoke_root" HEAD >"$artifact_root/create-worktree.log" 2>&1
git -C "$source_root" diff --binary HEAD >"$artifact_root/worktree.patch"
if [[ -s "$artifact_root/worktree.patch" ]]; then
  git -C "$smoke_root" apply --binary "$artifact_root/worktree.patch"
fi
while IFS= read -r -d '' file; do
  mkdir -p "$smoke_root/$(dirname "$file")"
  cp -p "$source_root/$file" "$smoke_root/$file"
done < <(git -C "$source_root" ls-files --others --exclude-standard -z)

"$source_root/scripts/worktree-env.sh" --json >"$artifact_root/source-worktree.json"
(cd "$smoke_root" && ./scripts/worktree-env.sh --json) >"$artifact_root/smoke-worktree.json"
python3 - "$artifact_root/source-worktree.json" "$artifact_root/smoke-worktree.json" <<'PY'
import json, sys
source, smoke = (json.load(open(path)) for path in sys.argv[1:])
for field in ("worktree_id", "compose_project", "http_port", "otlp_grpc_port", "otlp_http_port", "artifact_root"):
    if source[field] == smoke[field]:
        raise SystemExit(f"harness-smoke: worktree isolation collision for {field}: {source[field]}")
PY

cd "$smoke_root"
failure_classification="environment_or_external_dependency"
run_step bootstrap make bootstrap
failure_classification="harness_capability_gap"
run_step architecture-check make architecture-check
run_step docs-check make docs-check
failure_classification="product_defect"
run_step test make test

step="start-stack"
failure_classification="environment_or_external_dependency"
./scripts/dev-stack.sh up >"$artifact_root/start-stack.log" 2>&1
./scripts/dev-stack.sh ps >"$artifact_root/compose-ps.txt" 2>&1

step="wait-for-fixtures"
failure_classification="product_defect"
deadline=$((SECONDS + 90))
until ./scripts/observe.sh capsules 20 >"$artifact_root/capsules.json" 2>"$artifact_root/capsules.err" && \
  python3 - "$artifact_root/capsules.json" <<'PY'
import json, sys
raise SystemExit(0 if len(json.load(open(sys.argv[1])).get("items", [])) >= 2 else 1)
PY
do
  if (( SECONDS >= deadline )); then
    echo 'harness-smoke: deterministic demo capsules were not seeded within 90s' >&2
    exit 1
  fi
  sleep 2
done

capsule_ids=()
while IFS= read -r capsule_id; do
  capsule_ids+=("$capsule_id")
done < <(python3 - "$artifact_root/capsules.json" <<'PY'
import json, sys
items = json.load(open(sys.argv[1]))["items"]
for item in sorted(items, key=lambda value: value["start_time"])[:2]:
    print(item["capsule_id"])
PY
)
if [[ ${#capsule_ids[@]} -ne 2 ]]; then
  echo 'harness-smoke: expected two deterministic capsule IDs' >&2
  exit 1
fi

source ./scripts/lib/worktree.sh
flight_worktree_init
base_url="http://127.0.0.1:${FLIGHT_HTTP_PORT}"
token='frt_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA'

run_step metrics ./scripts/observe.sh metrics
run_step traces ./scripts/observe.sh traces "${capsule_ids[0]}"
run_step playback go run ./cmd/flight --url "$base_url" --token "$token" --json playback "${capsule_ids[0]}"
run_step diff go run ./cmd/flight --url "$base_url" --token "$token" --json diff "${capsule_ids[0]}" "${capsule_ids[1]}"

step="live-inspector"
failure_classification="product_defect"
(cd web && \
  PLAYWRIGHT_BASE_URL="$base_url" \
  FLIGHT_LIVE_E2E=1 \
  FLIGHT_PLAYWRIGHT_OUTPUT_DIR="$artifact_root/playwright" \
  npx playwright test tests/live-inspector.spec.ts) >"$artifact_root/live-inspector.log" 2>&1 || {
    tail -100 "$artifact_root/live-inspector.log" >&2 || true
    exit 1
  }

run_step reference-replay ./scripts/reference-replay-smoke.sh "$artifact_root/reference-replay"
run_step typescript-reference ./scripts/typescript-reference-smoke.sh \
  "$artifact_root/typescript-reference" "$artifact_root/reference-replay/source-capsule.json"

step="teardown-stack"
failure_classification="harness_capability_gap"
./scripts/dev-stack.sh logs 300 >"$artifact_root/compose.log" 2>&1
./scripts/dev-stack.sh down >"$artifact_root/teardown-stack.log" 2>&1
if docker ps -aq --filter "label=com.docker.compose.project=${COMPOSE_PROJECT_NAME}" | grep -q .; then
  echo 'harness-smoke: teardown left worktree containers behind' >&2
  exit 1
fi
if docker volume ls -q --filter "label=com.docker.compose.project=${COMPOSE_PROJECT_NAME}" | grep -q .; then
  echo 'harness-smoke: teardown left worktree volumes behind' >&2
  exit 1
fi
if [[ "$COMPOSE_PROJECT_NAME" == "$source_project" ]]; then
  echo 'harness-smoke: temporary stack reused the source worktree project' >&2
  exit 1
fi

step="complete"
echo "harness-smoke: passed; retained evidence: $artifact_root"
