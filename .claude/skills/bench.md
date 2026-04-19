# suzuha-bench: 会話品質ベンチマーク

suzuha エージェントの会話品質を評価するベンチマークスキル。

## 使い方

```bash
/bench                    # 全シナリオ実行 + 評価
/bench casual_chat        # 特定シナリオのみ
/bench --skip-eval        # 応答生成のみ (評価スキップ)
/bench --snapshot         # 新しいスナップショットを取得してから実行
```

## 前提条件

- ParadeDB (`suzuha-db`) が起動中
- ベンチ用 DB `suzuha_bench` が作成済み
- `bench/snapshots/baseline.dump` が存在
- `.suzuha/IDENTITY.md` が存在

## 実行手順

### 1. 初回セットアップ (1 回だけ)

```bash
# ベンチ用 DB を作成
docker compose -f container/compose.yaml exec suzuha-db \
  psql -U suzuha -c "CREATE DATABASE suzuha_bench;"

# 本番データのスナップショットを取得
docker compose -f container/compose.yaml exec suzuha-db \
  pg_dump -U suzuha --format=custom suzuha > bench/snapshots/baseline.dump
```

### 2. ベンチ実行

```bash
docker compose -f container/compose.yaml exec agent \
  go run ./cmd/suzuha-bench \
    -config config.yaml \
    -scenarios bench/scenarios/ \
    -snapshot bench/snapshots/baseline.dump \
    -bench-db "postgres://suzuha:suzuha@suzuha-db:5432/suzuha_bench?sslmode=disable" \
    -identity .suzuha/IDENTITY.md \
    -output bench/results.json
```

### 3. スナップショット更新 (本番データが変わった時)

```bash
# スナップショット取得
docker compose -f container/compose.yaml exec suzuha-db \
  pg_dump -U suzuha --format=custom suzuha > bench/snapshots/baseline.dump

# ベンチ用 DB に復元 + 匿名化
docker compose -f container/compose.yaml exec suzuha-db \
  pg_restore --dbname=suzuha_bench --no-owner --clean --if-exists bench/snapshots/baseline.dump
docker compose -f container/compose.yaml exec suzuha-db \
  psql -U suzuha -d suzuha_bench -f /app/scripts/anonymize_bench_db.sql
```

## シナリオ構成

| シナリオ | 件数 | 評価対象 |
|---------|------|---------|
| `casual_chat.yaml` | 5 | 日常会話の自然さ |
| `topics_self_prompt.yaml` | 5 | セルフプロンプト品質 |
| `memory_recall.yaml` | 5 | 記憶の活用と文脈理解 |
| `boundary.yaml` | 5 | キャラ崩壊しやすい境界ケース |

## シナリオの追加

`bench/scenarios/` に YAML を追加:

```yaml
name: scenario_name
description: シナリオの説明

inject_logs:
  - role: user
    user_name: "はりょ"
    content: "事前に注入する会話"
  - role: assistant
    content: "エージェントの応答"

cases:
  - id: case_id
    prompt: "テストプロンプト"
    source: discord  # or "internal"
    expect: "評価基準 (LLM-as-Judge に渡す)"
```

## 評価基準

`claude -p` が 3 軸で 1-5 点評価:

- **関連性 (relevance)**: プロンプトに対する適切さ
- **自然さ (naturalness)**: 日本語・口語体の自然さ
- **キャラ一貫性 (character)**: IDENTITY.md の設定への準拠

## 結果ファイル

`bench/results.json` に JSON 配列で出力:

```json
[
  {
    "scenario": "casual_chat",
    "case_id": "casual_greeting",
    "prompt": "おはよう",
    "response": "おはよ",
    "expect": "短い挨拶。過剰に元気すぎない",
    "scores": {
      "relevance": 5,
      "naturalness": 5,
      "character": 5,
      "reasoning": "短く自然な挨拶で、キャラ設定に合致"
    }
  }
]
```

## ファイル構成

```
bench/
├── scenarios/          # YAML シナリオ定義
├── snapshots/          # pg_dump スナップショット (gitignored)
└── results.json        # 評価結果 (gitignored)
cmd/suzuha-bench/
└── main.go             # ベンチ CLI
internal/bench/
├── scenario.go         # シナリオ読み込み
├── runner.go           # Agent 構築 + パイプライン実行 + 応答キャプチャ
└── evaluator.go        # claude -p による LLM-as-Judge 評価
```
