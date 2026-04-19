#!/usr/bin/env bash
# depguard の違反一覧を .depguard-baseline.txt に snapshot する。
# strict モード運用の下では出力が常に空 (0 行) であることを期待する。
# 出力行が 0 でなければ回帰として扱い、CI でも同様に失敗させる。
#
# (アーキテクチャ移行中は baseline モード、完了後は strict モードに切り替わる運用)
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
