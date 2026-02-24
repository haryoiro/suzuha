---
applyTo: "**"
paths: "internal/observe/**,internal/admin/handler/metrics*"
---

# メトリクス（observe パッケージ）

## 概要

アプリケーションメトリクスを SQLite に永続化する仕組み。
Agent プロセスがメトリクスを書き込み、Admin プロセスが SQLite を直接クエリして管理画面に表示する。

以前は Prometheus を使っていたが、Docker 再起動でリセットされる問題があったため SQLite-backed に移行した。

## メトリクス型

すべて `*sql.DB` を受け取り、`ON CONFLICT` による upsert で原子的に更新する。

| 型 | 用途 | 主要メソッド |
|----|------|-------------|
| `SQLCounter` | 累積カウンター | `Inc()`, `Add(float64)` |
| `SQLGauge` | 現在値 | `Set(float64)` |
| `SQLHistogram` | 分布（バケット + sum + count） | `Observe(float64)` |
| `SQLCounterVec` | ラベル付きカウンター群 | `WithLabelValues(...string) → *SQLCounter` |

呼び出し側のコードは Prometheus 時代と同じシグネチャ（`Add`, `Inc`, `Set`, `Observe`, `WithLabelValues`）を維持しているため、`internal/llm` や `internal/agent` は変更不要だった。

## テーブル設計

### metrics テーブル

```sql
CREATE TABLE metrics (
  name       TEXT NOT NULL,
  labels     TEXT NOT NULL DEFAULT '{}',  -- JSON (ソート済みキー)
  value      REAL NOT NULL DEFAULT 0,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (name, labels)
);
```

- Counter/Gauge: `(name, '{}')` の 1 行。Add は `value = value + ?`、Set は `value = ?`
- CounterVec: labels が `{"status":"success","tool":"fetch"}` のように JSON 化（キーはソート済みで決定的）
- Histogram の sum/count: `(name + "_sum", '{}')` と `(name + "_count", '{}')` の行

### metric_histogram_buckets テーブル

```sql
CREATE TABLE metric_histogram_buckets (
  name  TEXT NOT NULL,
  le    REAL NOT NULL,
  count INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (name, le)
);
```

- バケット境界: `{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}`（Prometheus DefBuckets 互換）
- `Observe(v)` → `le >= v` のすべてのバケットの count を +1（累積ヒストグラム）

## 登録済みメトリクス

`observe/metrics.go` の `NewMetrics(db)` で一括生成:

| 名前 | 型 | ラベル | 説明 |
|------|-----|--------|------|
| `suzuha_llm_latency_seconds` | Histogram | — | LLM API レイテンシ |
| `suzuha_llm_tokens_input_total` | Counter | — | 入力トークン累計 |
| `suzuha_llm_tokens_output_total` | Counter | — | 出力トークン累計 |
| `suzuha_embedding_latency_seconds` | Histogram | — | Embedding API レイテンシ |
| `suzuha_context_window_usage_ratio` | Gauge | — | コンテキストウィンドウ使用率 |
| `suzuha_tool_calls_total` | CounterVec | tool, status | ツール呼び出し回数 |
| `suzuha_events_total` | CounterVec | source, type | イベント処理数 |
| `suzuha_memory_writes_total` | Counter | — | メモリ書き込み回数 |

## 管理画面での読み取り

`admin/handler/metrics.go` の `ServeJSON` が `metrics` + `metric_histogram_buckets` テーブルを直接クエリ。
`suzuha_` プレフィックスのメトリクスのみ返す。ヒストグラムは `_sum`/`_count` 行 + buckets テーブルから組み立てる。

JSON レスポンス形状:

```json
{
  "metrics": [
    { "name": "suzuha_llm_tokens_input_total", "value": 1500, "type": "counter" },
    { "name": "suzuha_llm_latency_seconds", "sum": 12.5, "count": 5,
      "buckets": [{"le": 0.1, "count": 2}, ...], "type": "histogram" }
  ]
}
```

## ファイル構成

```
internal/observe/
  sqlite_metrics.go       # SQLCounter, SQLGauge, SQLHistogram, SQLCounterVec
  sqlite_metrics_test.go  # 各型のテスト + 永続性テスト
  metrics.go              # Metrics 構造体 + NewMetrics(db)
  log.go                  # slog 関連ユーティリティ

internal/memory/migrations/
  00010_metrics.sql        # metrics, metric_histogram_buckets テーブル作成
```
