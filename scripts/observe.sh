#!/usr/bin/env bash
set -euo pipefail

source "$(dirname "$0")/lib/worktree.sh"
flight_worktree_init
cd "$FLIGHT_REPO_ROOT"

kind="${1:-}"
token='frt_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA'
base="http://127.0.0.1:${FLIGHT_HTTP_PORT}"

case "$kind" in
  logs) docker compose -f deploy/docker-compose.yml logs --no-color --tail "${2:-200}" flight ;;
  metrics) curl --fail --silent --show-error "$base/metrics" ;;
  capsules) curl --fail --silent --show-error -H "Authorization: Bearer ${token}" "$base/api/v1/capsules?limit=${2:-20}" ;;
  traces)
    capsule_id="${2:?observe: capsule ID required for traces; run make observe QUERY=capsules first}"
    curl --fail --silent --show-error -H "Authorization: Bearer ${token}" "$base/api/v1/capsules/${capsule_id}"
    ;;
  jobs)
    job_id="${2:?observe: job ID required; create a replay first}"
    curl --fail --silent --show-error -H "Authorization: Bearer ${token}" "$base/api/v1/jobs/${job_id}"
    ;;
  job-events)
    job_id="${2:?observe: job ID required; create a replay first}"
    curl --fail --silent --show-error --no-buffer -H "Authorization: Bearer ${token}" "$base/api/v1/jobs/${job_id}/events"
    ;;
  --help|-h|help) echo 'usage: scripts/observe.sh <logs [lines]|metrics|capsules [limit]|traces <capsule-id>|jobs <job-id>|job-events <job-id>>' ;;
  *) echo 'observe: expected logs, metrics, capsules, traces, jobs, or job-events; run scripts/observe.sh --help' >&2; exit 2 ;;
esac
