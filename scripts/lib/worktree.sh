#!/usr/bin/env bash

flight_worktree_init() {
  local root worktree_id
  root=$(git rev-parse --show-toplevel)
  worktree_id=$(printf '%s' "$root" | shasum -a 256 | cut -c1-10)

  export FLIGHT_REPO_ROOT="$root"
  export FLIGHT_WORKTREE_ID="$worktree_id"
  export COMPOSE_PROJECT_NAME="flight_${worktree_id}"
  export FLIGHT_HTTP_PORT=$((18000 + 0x${worktree_id:0:3} % 1000))
  export FLIGHT_OTLP_GRPC_PORT=$((20000 + 0x${worktree_id:3:3} % 1000))
  export FLIGHT_OTLP_HTTP_PORT=$((22000 + 0x${worktree_id:6:3} % 1000))
  export FLIGHT_ARTIFACT_ROOT="$root/.artifacts"

  # Compose bind sources are resolved by the daemon, not by the local Docker
  # client. Keep the daemon-native default from docker-compose.yml. Rootless or
  # remote engines may set FLIGHT_DOCKER_SOCKET explicitly to a daemon path.
}
