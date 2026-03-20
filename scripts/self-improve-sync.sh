#!/usr/bin/env bash
# self-improve-sync.sh
#
# マージ済みの self-improve PR を検出し、ローカルを同期する。
# cron で定期実行する想定 (例: */5 * * * *)
#
# 処理:
#   1. gh pr list でマージ済み self-improve PR を取得
#   2. git pull で最新を取得 (Air が自動再起動)
#   3. マージ済みブランチをローカル・リモートから削除
#   4. Discord webhook で通知 (設定されている場合)

set -euo pipefail

REPO_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_DIR"

DISCORD_WEBHOOK="${SELF_IMPROVE_DISCORD_WEBHOOK:-}"
LOG_FILE="/tmp/self-improve-sync.log"

log() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] $*" | tee -a "$LOG_FILE"
}

notify_discord() {
    local msg="$1"
    if [[ -n "$DISCORD_WEBHOOK" ]]; then
        curl -s -H "Content-Type: application/json" \
            -d "{\"content\": \"$msg\"}" \
            "$DISCORD_WEBHOOK" >/dev/null 2>&1 || true
    fi
}

# マージ済みの self-improve PR を取得
merged=$(gh pr list --state merged --search "head:self-improve/" --json number,title,headRefName --jq '.[]' 2>/dev/null || echo "")

if [[ -z "$merged" ]]; then
    exit 0
fi

log "マージ済み self-improve PR を検出"

# git pull で最新を取得
current_hash=$(git rev-parse HEAD)
git pull --ff-only origin main 2>/dev/null || {
    log "git pull 失敗 (コンフリクトの可能性)"
    exit 1
}
new_hash=$(git rev-parse HEAD)

if [[ "$current_hash" != "$new_hash" ]]; then
    log "コード更新: $current_hash → $new_hash (Air が自動再起動します)"
fi

# マージ済みブランチを削除
echo "$merged" | jq -r '.headRefName' | while read -r branch; do
    if [[ -z "$branch" ]]; then
        continue
    fi

    pr_title=$(echo "$merged" | jq -r "select(.headRefName == \"$branch\") | .title")
    pr_number=$(echo "$merged" | jq -r "select(.headRefName == \"$branch\") | .number")

    # ローカルブランチ削除
    if git show-ref --verify --quiet "refs/heads/$branch" 2>/dev/null; then
        git branch -d "$branch" 2>/dev/null || true
        log "ローカルブランチ削除: $branch"
    fi

    # リモートブランチ削除
    if git ls-remote --exit-code --heads origin "$branch" >/dev/null 2>&1; then
        git push origin --delete "$branch" 2>/dev/null || true
        log "リモートブランチ削除: $branch"
    fi

    notify_discord "✅ self-improve PR #${pr_number} がマージされました: ${pr_title}\nブランチ \`${branch}\` を削除しました。"
    log "完了: PR #$pr_number ($pr_title)"
done

# stale worktree を掃除
git worktree prune 2>/dev/null || true
