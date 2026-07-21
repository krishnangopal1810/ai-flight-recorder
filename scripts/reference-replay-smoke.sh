#!/usr/bin/env bash
set -euo pipefail

if [[ "${1:-}" == "--help" || "${1:-}" == "-h" ]]; then
  echo 'usage: scripts/reference-replay-smoke.sh <artifact-directory>'
  echo 'Records the Python reference agent, proves five-boundary controlled replay, and proves an unmatched boundary cannot fall back to a live dependency.'
  exit 0
fi
if [[ $# -ne 1 ]]; then
  echo 'reference-replay-smoke: expected one artifact directory; run scripts/reference-replay-smoke.sh --help' >&2
  exit 2
fi

source "$(dirname "$0")/lib/worktree.sh"
flight_worktree_init
cd "$FLIGHT_REPO_ROOT"

artifact_root="$1"
mkdir -p "$artifact_root"
artifact_root=$(cd "$artifact_root" && pwd)
token='frt_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA'
base_url="http://127.0.0.1:${FLIGHT_HTTP_PORT}"
compose_network="${COMPOSE_PROJECT_NAME}_default"
image_tag="flight-reference-python-${FLIGHT_WORKTREE_ID}:local"
expected_gateway="${COMPOSE_PROJECT_NAME}-flight:latest"

cleanup() {
  local exit_code=$?
  set +e
  docker image rm "$image_tag" >"$artifact_root/image-cleanup.log" 2>&1
  exit "$exit_code"
}
trap cleanup EXIT

authorized_curl() {
  curl --fail --silent --show-error --connect-timeout 3 --max-time 30 \
    -H "Authorization: Bearer ${token}" "$@"
}

wait_for_job() {
  local job_id="$1" output="$2" state deadline
  deadline=$((SECONDS + 120))
  while true; do
    authorized_curl "$base_url/api/v1/jobs/$job_id" >"$output.tmp"
    state=$(python3 - "$output.tmp" <<'PY'
import json, sys
print(json.load(open(sys.argv[1]))["state"])
PY
)
    case "$state" in
      completed|failed|blocked|inconclusive)
        mv "$output.tmp" "$output"
        return 0
        ;;
    esac
    if (( SECONDS >= deadline )); then
      mv "$output.tmp" "$output"
      echo "reference-replay-smoke: job $job_id did not finish within 120s (state=$state)" >&2
      return 1
    fi
    sleep 1
  done
}

create_replay() {
  local source_id="$1" model="$2" substitute_llm="$3" output="$4"
  local request_output="${output%.json}-request.json"
  python3 - "$source_id" "$model" "$substitute_llm" >"$request_output" <<'PY'
import json, sys
request = {
    "source_capsule_id": sys.argv[1],
    "substitute_llm": sys.argv[3] == "true",
    "allow_sandboxed_side_effects": False,
}
if sys.argv[2]:
    request["model"] = sys.argv[2]
print(json.dumps(request, separators=(",", ":")))
PY
  authorized_curl -H 'Content-Type: application/json' \
    --data-binary @"$request_output" \
    "$base_url/api/v1/replays" >"$output"
}

authorized_curl "$base_url/api/v1/capsules?project=reference-replay&limit=100" >"$artifact_root/capsules-before.json"

"$FLIGHT_REPO_ROOT/scripts/run-with-timeout.py" 240 -- \
  docker build --tag "$image_tag" --file examples/reference-python/Dockerfile . \
  >"$artifact_root/image-build.log" 2>&1
docker image inspect --format '{{.Id}}' "$image_tag" >"$artifact_root/image-id.txt"
image_id=$(tr -d '\n' <"$artifact_root/image-id.txt")
if [[ ! "$image_id" =~ ^sha256:[0-9a-f]{64}$ ]]; then
  echo "reference-replay-smoke: OCI runtime returned a non-immutable image ID: $image_id" >&2
  exit 1
fi
if ! docker network inspect "$compose_network" >"$artifact_root/compose-network.json" 2>&1; then
  echo "reference-replay-smoke: worktree stack network $compose_network is unavailable; start the stack first" >&2
  exit 1
fi

printf '{"policy_id":"refund"}' | \
  "$FLIGHT_REPO_ROOT/scripts/run-with-timeout.py" 60 -- \
  docker run --rm -i --network "$compose_network" \
    -e OTEL_EXPORTER_OTLP_ENDPOINT=http://flight:4318 \
    -e "FLIGHT_OTLP_TOKEN=$token" \
    -e "FLIGHT_REFERENCE_IMAGE=$image_id" \
    -e FLIGHT_RECORDING_FIXTURE=1 \
    "$image_tag" >"$artifact_root/source-stdout.json" 2>"$artifact_root/source-stderr.log"

deadline=$((SECONDS + 30))
while true; do
  authorized_curl "$base_url/api/v1/capsules?project=reference-replay&limit=100" >"$artifact_root/capsules-after.json"
  if source_id=$(python3 - "$artifact_root/capsules-before.json" "$artifact_root/capsules-after.json" <<'PY'
import json, sys
before = {item["capsule_id"] for item in json.load(open(sys.argv[1])).get("items", [])}
after = [item for item in json.load(open(sys.argv[2])).get("items", []) if item["capsule_id"] not in before]
if len(after) != 1:
    raise SystemExit(1)
print(after[0]["capsule_id"])
PY
  ); then
    break
  fi
  if (( SECONDS >= deadline )); then
    echo 'reference-replay-smoke: the source reference capsule was not finalized within 30s' >&2
    exit 1
  fi
  sleep 1
done
printf '%s\n' "$source_id" >"$artifact_root/source-capsule-id.txt"
authorized_curl "$base_url/api/v1/capsules/$source_id" >"$artifact_root/source-capsule.json"
python3 - "$artifact_root/source-capsule.json" "$image_id" <<'PY'
import json, sys
capsule = json.load(open(sys.argv[1]))
assert capsule["project_id"] == "reference-replay", capsule
assert capsule["service_name"] == "python-reference-agent", capsule
assert capsule["terminal_status"] == "ok", capsule
assert capsule["completeness"]["replay"]["state"] == "ready", capsule["completeness"]
assert capsule["provenance"]["entrypoint"]["image"] == sys.argv[2], capsule["provenance"]
assert capsule["provenance"]["entrypoint"]["command"] == ["python", "/app/agent.py"]
assert capsule["provenance"]["entrypoint"]["model_hosts"] == ["fake-deps"]
expected = ["retriever.policy", "mcp.policy", "tool.eligibility", "api.policy", "llm.answer"]
assert [boundary["logical_key"] for boundary in capsule["boundaries"]] == expected, capsule["boundaries"]
assert [boundary["replay_policy"] for boundary in capsule["boundaries"]] == ["substitute", "substitute", "substitute", "substitute", "live"]
assert all(boundary["parent_logical_key"] == "agent.answer" for boundary in capsule["boundaries"])
assert all(boundary["response"]["state"] == "captured" for boundary in capsule["boundaries"])
assert len(capsule["model_configuration"]["tool_definitions"]) == 1, capsule["model_configuration"]
assert capsule["model_configuration"]["tool_definitions"][0]["state"] == "captured"
root = next(item for item in capsule["operations"] if item["logical_key"] == "agent.answer")
assert root["attributes"]["flight.reference.model_callback_invoked"] == "true", root
for kind in ["retrieval", "mcp", "tool", "api"]:
    assert root["attributes"][f"flight.reference.{kind}_callback_invoked"] == "true", root
PY

create_replay "$source_id" "" false "$artifact_root/exact-job-created.json"
exact_job_id=$(python3 - "$artifact_root/exact-job-created.json" <<'PY'
import json, sys
print(json.load(open(sys.argv[1]))["id"])
PY
)
wait_for_job "$exact_job_id" "$artifact_root/exact-job.json"
authorized_curl "$base_url/api/v1/jobs/$exact_job_id/events" >"$artifact_root/exact-job-events.sse"
exact_candidate_id=$(python3 - "$artifact_root/exact-job.json" "$source_id" "$image_id" "$expected_gateway" "$artifact_root/exact-job-events.sse" <<'PY'
import json, sys
job = json.load(open(sys.argv[1]))
assert job["state"] == "completed", job
assert job["request"]["gateway_image"] == sys.argv[4], job["request"]
assert job["request"]["allowed_hosts"] == ["fake-deps"], job["request"]
assert job["request"]["plan"]["plan_digest"].startswith("sha256:"), job["request"]
result = job["result"]
assert result["source_capsule_id"] == sys.argv[2], result
assert result["candidate_image"] == sys.argv[3], result
substituted = ["api.policy", "mcp.policy", "retriever.policy", "tool.eligibility"]
assert result["substitutions"] == substituted, result
assert result["live_boundaries"] == ["llm.answer"], result
assert result["outcome"] == "exact", result
assert result["exit_code"] == 0, result
assert len(result["trials"]) == 1 and result["trials"][0]["outcome"] == "exact", result
events = []
for line in open(sys.argv[5]):
    if line.startswith("data: "):
        events.append(json.loads(line.removeprefix("data: ")))
domain_events = [event for event in events if "stage" not in event]
stage_events = [event for event in events if "stage" in event]
assert len(domain_events) == 3, events
assert domain_events[0]["plan_digest"].startswith("sha256:"), domain_events[0]
assert domain_events[0]["substitutions"] == substituted, domain_events[0]
assert domain_events[0]["live_boundaries"] == ["llm.answer"], domain_events[0]
assert domain_events[1]["outcome"] == "exact", domain_events[1]
assert domain_events[2]["outcome"] == "exact", domain_events[2]
required_stages = {"preflight", "preparing_candidate", "building_image", "starting_isolated_application", "replaying_recorded_dependencies", "capturing_candidate", "comparing_executions", "complete"}
assert required_stages.issubset({event["stage"] for event in stage_events}), stage_events
candidate = result["candidate_capsule_id"]
assert result["trials"][0]["candidate_capsule_id"] == candidate, result
print(candidate)
PY
)
printf '%s\n' "$exact_candidate_id" >"$artifact_root/exact-candidate-id.txt"
authorized_curl "$base_url/api/v1/capsules/$exact_candidate_id" >"$artifact_root/exact-candidate.json"
python3 - "$artifact_root/source-capsule.json" "$artifact_root/exact-candidate.json" "$source_id" <<'PY'
import json, sys
source, candidate = (json.load(open(path)) for path in sys.argv[1:3])
assert candidate["source_capsule_id"] == sys.argv[3], candidate
assert candidate["terminal_status"] == "ok", candidate
expected = ["retriever.policy", "mcp.policy", "tool.eligibility", "api.policy", "llm.answer"]
assert [item["logical_key"] for item in candidate["boundaries"]] == expected, candidate["boundaries"]
def root_output(capsule):
    return next(item["output"]["digest"] for item in capsule["operations"] if item["logical_key"] == "agent.answer")
assert root_output(source) == root_output(candidate), (root_output(source), root_output(candidate))
assert [item["response"]["digest"] for item in source["boundaries"]] == [item["response"]["digest"] for item in candidate["boundaries"]]
assert source["model_configuration"]["tool_definitions"] == candidate["model_configuration"]["tool_definitions"]
root = next(item for item in candidate["operations"] if item["logical_key"] == "agent.answer")
assert root["attributes"]["flight.reference.model_callback_invoked"] == "true", root
for kind in ["retrieval", "mcp", "tool", "api"]:
    assert root["attributes"][f"flight.reference.{kind}_callback_invoked"] == "false", root
PY
authorized_curl -H 'Content-Type: application/json' \
  --data "{\"baseline_capsule_id\":\"$source_id\",\"candidate_capsule_id\":\"$exact_candidate_id\"}" \
  "$base_url/api/v1/diffs" >"$artifact_root/exact-diff.json"
python3 - "$artifact_root/exact-diff.json" <<'PY'
import json, sys
diff = json.load(open(sys.argv[1]))
assert all(item["kind"] == "provenance_changed" for item in (diff.get("observations") or [])), diff
PY

create_replay "$source_id" "substitute-only" true "$artifact_root/all-substituted-job-created.json"
all_substituted_job_id=$(python3 - "$artifact_root/all-substituted-job-created.json" <<'PY'
import json, sys
print(json.load(open(sys.argv[1]))["id"])
PY
)
wait_for_job "$all_substituted_job_id" "$artifact_root/all-substituted-job.json"
authorized_curl "$base_url/api/v1/jobs/$all_substituted_job_id/events" >"$artifact_root/all-substituted-job-events.sse"
all_substituted_candidate_id=$(python3 - "$artifact_root/all-substituted-job.json" "$source_id" <<'PY'
import json, sys
job = json.load(open(sys.argv[1]))
assert job["state"] == "completed", job
assert job["request"]["allowed_hosts"] == [], job["request"]
assert job["request"]["plan"]["substitute_llm"] is True, job["request"]
result = job["result"]
assert result["source_capsule_id"] == sys.argv[2], result
assert result["substitutions"] == ["api.policy", "llm.answer", "mcp.policy", "retriever.policy", "tool.eligibility"], result
assert result.get("live_boundaries", []) == [], result
assert result["outcome"] == "exact", result
assert result["exit_code"] == 0, result
assert len(result["trials"]) == 1 and result["trials"][0]["outcome"] == "exact", result
print(result["candidate_capsule_id"])
PY
)
printf '%s\n' "$all_substituted_candidate_id" >"$artifact_root/all-substituted-candidate-id.txt"
authorized_curl "$base_url/api/v1/capsules/$all_substituted_candidate_id" >"$artifact_root/all-substituted-candidate.json"
python3 - "$artifact_root/source-capsule.json" "$artifact_root/all-substituted-candidate.json" "$source_id" <<'PY'
import json, sys
source, candidate = (json.load(open(path)) for path in sys.argv[1:3])
assert candidate["source_capsule_id"] == sys.argv[3], candidate
assert candidate["terminal_status"] == "ok", candidate
assert [item["logical_key"] for item in candidate["boundaries"]] == ["retriever.policy", "mcp.policy", "tool.eligibility", "api.policy", "llm.answer"], candidate["boundaries"]
assert [item["response"]["digest"] for item in source["boundaries"]] == [item["response"]["digest"] for item in candidate["boundaries"]]
assert source["model_configuration"]["tool_definitions"] == candidate["model_configuration"]["tool_definitions"]
root = next(item for item in candidate["operations"] if item["logical_key"] == "agent.answer")
assert root["attributes"]["flight.reference.model_callback_invoked"] == "false", root
for kind in ["retrieval", "mcp", "tool", "api"]:
    assert root["attributes"][f"flight.reference.{kind}_callback_invoked"] == "false", root
PY

create_replay "$source_id" "mismatch-request" false "$artifact_root/mismatch-job-created.json"
mismatch_job_id=$(python3 - "$artifact_root/mismatch-job-created.json" <<'PY'
import json, sys
print(json.load(open(sys.argv[1]))["id"])
PY
)
wait_for_job "$mismatch_job_id" "$artifact_root/mismatch-job.json"
authorized_curl "$base_url/api/v1/jobs/$mismatch_job_id/events" >"$artifact_root/mismatch-job-events.sse"
mismatch_candidate_id=$(python3 - "$artifact_root/mismatch-job.json" <<'PY'
import json, sys
job = json.load(open(sys.argv[1]))
assert job["state"] == "failed", job
assert job["error"]["code"] == "build", job
result = job["result"]
assert result["exit_code"] == 17, result
assert result["outcome"] == "divergent", result
assert result["substitutions"] == ["api.policy", "mcp.policy", "retriever.policy", "tool.eligibility"], result
assert result["live_boundaries"] == ["llm.answer"], result
assert len(result["trials"]) == 1 and result["trials"][0]["exit_code"] == 17, result
print(result["candidate_capsule_id"])
PY
)
printf '%s\n' "$mismatch_candidate_id" >"$artifact_root/mismatch-candidate-id.txt"
authorized_curl "$base_url/api/v1/capsules/$mismatch_candidate_id" >"$artifact_root/mismatch-candidate.json"
mismatch_output_digest=$(python3 - "$artifact_root/mismatch-candidate.json" "$source_id" <<'PY'
import json, sys
capsule = json.load(open(sys.argv[1]))
assert capsule["source_capsule_id"] == sys.argv[2], capsule
assert capsule["terminal_status"] == "error", capsule
root = next(item for item in capsule["operations"] if item["logical_key"] == "agent.answer")
assert root["status"] == "error", root
print(root["output"]["digest"])
PY
)
authorized_curl "$base_url/api/v1/capsules/$mismatch_candidate_id/payloads/$mismatch_output_digest" >"$artifact_root/mismatch-output.json"
python3 - "$artifact_root/mismatch-output.json" <<'PY'
import json, sys
output = json.load(open(sys.argv[1]))
assert output["live_callback_invoked"] is False, output
assert "unmatched_boundary" in output["error"], output
assert "live fallback is disabled" in output["error"], output
PY

printf '%s\n' \
  "source_capsule_id=$source_id" \
  "exact_job_id=$exact_job_id" \
  "exact_candidate_capsule_id=$exact_candidate_id" \
  "all_substituted_job_id=$all_substituted_job_id" \
  "all_substituted_candidate_capsule_id=$all_substituted_candidate_id" \
  "mismatch_job_id=$mismatch_job_id" \
  "mismatch_candidate_capsule_id=$mismatch_candidate_id" \
  >"$artifact_root/summary.txt"
echo "reference-replay-smoke: passed; retained evidence: $artifact_root"
