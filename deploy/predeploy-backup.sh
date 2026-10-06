#!/bin/sh
set -eu

container=${SWARM_CONTAINER:-swarm_relay}
timestamp=$(date -u +%Y%m%dT%H%M%SZ)
was_running=$(docker inspect --format '{{.State.Running}}' "$container")

restart() {
  if [ "$was_running" = "true" ]; then
    docker start "$container" >/dev/null
  fi
}

trap restart EXIT INT TERM

if [ "$was_running" = "true" ]; then
  docker stop "$container" >/dev/null
fi

docker run --rm --volumes-from "$container" alpine:3.22 \
  tar -czf "/app/backups/predeploy-$timestamp.tar.gz" -C /app db public/.well-known

restart
was_running=false
trap - EXIT INT TERM
