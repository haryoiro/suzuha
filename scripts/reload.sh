#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

echo "Restarting suzuha-agent (build + start)..."
docker compose -f container/compose.yaml restart agent

echo "Waiting for agent to become healthy..."
until docker compose -f container/compose.yaml exec -T agent pgrep -f suzuha-agent > /dev/null 2>&1; do
  sleep 1
done

echo "Agent restarted."
