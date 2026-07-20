#!/usr/bin/env bash
set -euo pipefail

action="${1:-}"
case "$action" in
  up|down|ps|logs) ;;
  *) echo 'usage: scripts/dev-stack.sh <up|down|ps|logs>' >&2; exit 2 ;;
esac

root=$(git rev-parse --show-toplevel)
worktree_id=$(printf '%s' "$root" | shasum -a 256 | cut -c1-10)
export COMPOSE_PROJECT_NAME="flight_${worktree_id}"
export FLIGHT_HTTP_PORT=$((18000 + 0x${worktree_id:0:3} % 1000))
export FLIGHT_OTLP_GRPC_PORT=$((20000 + 0x${worktree_id:3:3} % 1000))
export FLIGHT_OTLP_HTTP_PORT=$((22000 + 0x${worktree_id:6:3} % 1000))
docker_host=$(docker context inspect --format '{{.Endpoints.docker.Host}}' 2>/dev/null || true)
if [[ "$docker_host" == unix://* ]]; then
  export FLIGHT_DOCKER_SOCKET="${docker_host#unix://}"
fi
docker compose -f deploy/docker-compose.yml "$action"
