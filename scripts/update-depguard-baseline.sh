#!/usr/bin/env bash
# depguard の違反一覧を .depguard-baseline.txt に snapshot する。
# 各 Phase 終了時に実行して、違反が増えていないことを確認する。
#
# 違反数が前回から減っていれば OK、増えていれば差分を調査する。
# Phase 12 で depguard を厳格モードに切り替えて本ファイルを廃止する。
set -euo pipefail
cd "$(dirname "$0")/.."

out=".depguard-baseline.txt"

echo "depguard snapshot を生成中…"
docker compose -f container/compose.yaml exec -T -w /app/agent agent \
  /go/bin/golangci-lint run \
    --enable-only depguard \
    --issues-exit-code 0 \
    ./... > "$out.tmp"

# golangci-lint のサマリ行 ("0 issues." / "N issues:" / "* linter: count") は
# 除外し、実際のファイル:行の違反だけを抜く。
grep -E '^[^:]+\.go:[0-9]+:' "$out.tmp" | sort > "$out" || true
rm -f "$out.tmp"

count=$(wc -l < "$out" | tr -d ' ')
echo "depguard 違反: $count 件 (.depguard-baseline.txt)"

if [ "$count" != "0" ]; then
  echo "---"
  head -20 "$out"
fi
