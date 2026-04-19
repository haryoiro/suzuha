#!/usr/bin/env bash
# PostToolUse hook: Edit/Write が spec/*/*.tsp を変更したら自動で
# TypeSpec コンパイル → openapi.yaml 後処理 → ogen 生成 を走らせる。
#
# stdin で Claude Code から以下の JSON を受け取る:
#   { "tool_name": "Edit", "tool_input": { "file_path": "..." }, ... }
#
# 非該当ファイルは静かに exit 0。

set -uo pipefail

payload=$(cat)
file_path=$(printf '%s' "$payload" | jq -r '.tool_input.file_path // empty')

# spec/admin/**/*.tsp または spec/control/**/*.tsp のみ対象。
case "$file_path" in
  */spec/admin/*.tsp|*/spec/control/*.tsp)
    ;;
  *)
    exit 0
    ;;
esac

cd "${CLAUDE_PROJECT_DIR:-$(pwd)}"

log() { printf '[regen-spec] %s\n' "$*" >&2; }

log "TypeSpec changed: $file_path → regenerating"

if ! pnpm --filter api compile >/tmp/regen-spec-tsp.log 2>&1; then
  cat /tmp/regen-spec-tsp.log >&2
  log "FAILED: tsp compile"
  exit 1
fi

if ! docker compose -f container/compose.yaml exec -T -w /app/agent agent go generate ./... >/tmp/regen-spec-ogen.log 2>&1; then
  cat /tmp/regen-spec-ogen.log >&2
  log "FAILED: go generate (ogen)"
  exit 1
fi

# docker 内の root が書き出すので chown し直す。
docker compose -f container/compose.yaml exec -T agent chown -R "$(id -u):$(id -g)" /app/agent/internal/api/ >/dev/null 2>&1 || true

log "OK"
