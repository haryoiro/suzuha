#!/usr/bin/env bash
set -euo pipefail

mkdir -p /app/tmp
echo "Building suzuha-agent..."
go build -buildvcs=false -o /app/tmp/suzuha-agent ./agent/cmd/suzuha-agent

echo "Starting suzuha-agent..."
exec /app/tmp/suzuha-agent
