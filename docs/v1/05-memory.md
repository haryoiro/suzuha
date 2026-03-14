# 記憶システム

エージェントの長期記憶は SQLite に保存され、ベクトル埋め込みによるセマンティック検索で取得される。

## 記憶の種類

| type | 用途 |
|------|------|
| `user` | ユーザーに関する事実（好み、職業、特徴など） |
| `world` | 世界の情報（イベント、ニュース、一般的な事実） |
| `tool` | ツール実行結果の記録 |
| `rss` | RSS フィードから取得した記事 |
| `episode` | 会話のエピソード（要約） |
| `self` | 自分自身に関する情報 |

## ストア構造

**パッケージ:** `internal/memory/`

```go
// internal/memory/store.go
type Store interface {
    Save(ctx, Memory) error
    Get(ctx, id) (*Memory, error)
    Search(ctx, query, topK) ([]Memory, error)
    SearchRecent(ctx, query, topK, since) ([]Memory, error)
    SearchByType(ctx, query, type, topK) ([]Memory, error)
    ListByUser(ctx, userID, limit) ([]Memory, error)
    ListByType(ctx, type, limit) ([]Memory, error)
    ListEpisodesByParticipant(ctx, platformUserID, limit) ([]Memory, error)
    ListRecentByType(ctx, type, since, limit) ([]Memory, error)
    Delete(ctx, id) error
}
```

**実装:** `SQLiteStore`（`internal/memory/sqlite.go`）

## ベクトル検索

### 埋め込みワーカー

`store.RunEmbeddingWorker(ctx)` がバックグラウンドで動作し、埋め込みが未生成の記憶に対してベクトルを計算する。

**フロー:**
1. `memory_embeddings` テーブルに存在しない記憶を検出
2. `llm.Client.Embed(ctx, text)` で埋め込みベクトルを取得
3. ベクトルを DB に保存

### セマンティック検索

1. クエリテキストを埋め込みベクトルに変換
2. コサイン類似度で全記憶とのスコアを計算
3. 上位 topK 件を返却

## DB スキーマ

**主要テーブル:**

```sql
-- 記憶本体
CREATE TABLE memories (
    id TEXT PRIMARY KEY,
    type TEXT NOT NULL,
    content TEXT NOT NULL,
    metadata TEXT,  -- JSON
    created_at DATETIME,
    updated_at DATETIME
);

-- ベクトル埋め込み
CREATE TABLE memory_embeddings (
    memory_id TEXT PRIMARY KEY REFERENCES memories(id),
    embedding BLOB NOT NULL  -- float32 配列
);

-- ユーザー情報
CREATE TABLE users (
    id TEXT PRIMARY KEY,
    display_name TEXT,
    role TEXT DEFAULT '',
    is_bot BOOLEAN DEFAULT FALSE,
    closeness REAL DEFAULT 0,
    trust REAL DEFAULT 0,
    interest REAL DEFAULT 0,
    metadata TEXT,
    created_at DATETIME,
    updated_at DATETIME
);

-- プラットフォームリンク
CREATE TABLE user_platform_links (
    user_id TEXT REFERENCES users(id),
    platform TEXT,
    platform_user_id TEXT,
    platform_name TEXT,
    PRIMARY KEY (platform, platform_user_id)
);

-- 好感度イベント
CREATE TABLE affinity_events (
    id TEXT PRIMARY KEY,
    user_id TEXT REFERENCES users(id),
    axis TEXT,      -- 'closeness', 'trust', 'interest'
    delta REAL,
    reason TEXT,
    created_at DATETIME
);

-- チャンネル設定
CREATE TABLE channel_settings (
    channel_id TEXT PRIMARY KEY,
    guild_id TEXT,
    mode TEXT DEFAULT 'active',  -- 'active', 'listen', 'disabled'
    home BOOLEAN DEFAULT FALSE,
    updated_at DATETIME
);

-- チャンネルアクティビティ
CREATE TABLE channel_activity (
    channel_id TEXT PRIMARY KEY,
    last_user_message_at DATETIME
);

-- コンテキストスナップショット
CREATE TABLE context_snapshot (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    messages TEXT,  -- JSON
    updated_at DATETIME
);

-- 会話ログ（ファインチューニングデータ用）
CREATE TABLE conversation_logs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    turn_id TEXT,
    channel_id TEXT,
    role TEXT,
    content TEXT,
    user_id TEXT,
    user_name TEXT,
    message_id TEXT,
    tool_calls TEXT,  -- JSON
    tool_call_id TEXT,
    timestamp DATETIME
);

-- アプリケーション設定
CREATE TABLE app_settings (
    key TEXT PRIMARY KEY,
    value TEXT
);

-- タスク状態永続化
CREATE TABLE task_state (
    task_name TEXT PRIMARY KEY,
    state TEXT  -- JSON
);
```

## コンテキスト永続化

エージェントの会話コンテキスト（LLM に送信するメッセージ履歴）は `context_snapshot` テーブルに保存される。

- **保存タイミング:** Reflect ステージ完了時、コンテキスト圧縮後
- **復元タイミング:** エージェント起動時 (`loadContext()`)
- **形式:** メッセージ配列の JSON

これにより、エージェントの再起動後も会話の文脈が維持される。

## 記憶の利用箇所

| フェーズ | 利用方法 |
|---------|---------|
| Think | セマンティック検索で関連記憶をエフェメラルとして注入 |
| Think | ユーザーに関する記憶をプロフィールに含める |
| Think | 自己認識記憶を注入 |
| Topics | 最近の記憶・独り言を参照して重複回避 |
| Explore | 探索結果を `remember: true` で保存 |
| RSS | 新着記事を RSS タイプで保存 |
| Consolidator | 圧縮時にエピソード記憶を生成 |
| Forget | 類似記憶の検出・マージ |
