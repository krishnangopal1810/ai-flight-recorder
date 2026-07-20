#!/usr/bin/env bash
set -euo pipefail

kind="${1:-}"
root=$(git rev-parse --show-toplevel)
worktree_id=$(printf '%s' "$root" | shasum -a 256 | cut -c1-10)
export COMPOSE_PROJECT_NAME="flight_${worktree_id}"
http_port=$((18000 + 0x${worktree_id:0:3} % 1000))
token='frt_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA'

case "$kind" in
  logs) docker compose -f deploy/docker-compose.yml logs --no-color --tail "${2:-200}" flight ;;
  metrics) curl --fail --silent --show-error "http://127.0.0.1:${http_port}/metrics" ;;
  traces) curl --fail --silent --show-error -H "Authorization: Bearer ${token}" "http://127.0.0.1:${http_port}/api/v1/capsules?limit=${2:-20}" ;;
  jobs) curl --fail --silent --show-error -H "Authorization: Bearer ${token}" "http://127.0.0.1:${http_port}/api/v1/jobs/${2:?job ID required}" ;;
  *) echo 'usage: scripts/observe.sh <logs [lines]|metrics|traces [limit]|jobs <id>>' >&2; exit 2 ;;
esac
