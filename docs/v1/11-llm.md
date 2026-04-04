# LLM 統合

## LLM クライアント

**パッケージ:** `internal/llm/`

OpenAI 互換 API を `any-llm-go` ライブラリ経由で呼び出す。プロバイダー固有の差異はライブラリが吸収する。

### ロールベースプロバイダ管理

Client は複数のロールごとに異なるプロバイダを保持する:

```go
type Client struct {
    roles map[string]roleProvider  // "conversation", "background", "vision", etc.
    // embedding は Embedder インターフェース経由で別管理
}
```

| ロール | 用途 | 例 |
|--------|------|-----|
| `conversation` | 対話応答 | glm-5, gpt-4o |
| `background` | バックグラウンドタスク (memento, diary, wander 等) | glm-4.7 |
| `vision` | 画像認識 (会話モデルが非対応の場合) | gpt-4.1-mini |
| `embedding` | ベクトル埋め込み | gemini-embedding |

### 主要メソッド

```go
// ロール指定で補完
client.For("conversation").Complete(ctx, msgs, tools)
client.For("background").CompleteRaw(ctx, msgs)

// capability 解決 (inline=true: 会話モデルがネイティブ対応)
rc, inline := client.WithCapability("conversation", "vision")

// ロール単位の切り替え
client.SwapRole("conversation", preset)
```

後方互換シムも維持:
- `Complete()` → `For("conversation").Complete()`
- `CompleteRawDefault()` → `For("background").CompleteRaw()`
- `HasVision()` → `WithCapability` で判定
- `DescribeImage()` → `WithCapability("conversation", "vision")` 経由

### サポートプロバイダー

| プロバイダー | api_base |
|-------------|----------|
| OpenAI | `https://api.openai.com/v1` |
| Zhipu (智谱) | `https://open.bigmodel.cn/api/paas/v4` |
| Zhipu CodingPlan | `https://open.bigmodel.cn/api/coding/paas/v4` |
| Qwen (通义千問) | `https://dashscope.aliyuncs.com/compatible-mode/v1` |
| ローカル LLM | `http://host.docker.internal:8000/v1` 等 |

全て OpenAI 互換 API を使用するため、`api_base` を変えるだけで任意のプロバイダーに接続可能。

### ビジョン統合

`WithCapability` で自動解決:

1. **会話モデルが vision 対応** (`capabilities: ["text","vision"]`): 画像を base64 データ URI としてメッセージに直接含める (inline=true)
2. **別途 vision ロール**: `DescribeImage()` で画像をテキスト記述に変換し、テキストとしてメッセージに含める (inline=false)

将来 audio 等のモダリティが追加されても同じパターンで対応可能。

## ProviderRegistry

**パッケージ:** `internal/llm/`

LLM プロバイダ・モデル・ロール割り当てを 3 レイヤーで管理する。旧 PresetStore は廃止。

### 3 レイヤー構造

| レイヤー | ソース | 説明 |
|---------|--------|------|
| Provider | config.yaml | 接続情報 (type, api_key, api_base) |
| Model Catalog | DB | モデルごとの capabilities, max_context |
| Role Assignment | DB | role → provider/model のマッピング |

### API

```
GET    /internal/llm/providers       プロバイダ一覧 (config.yaml 由来)
GET    /internal/llm/models          モデルカタログ一覧
PUT    /internal/llm/roles/{role}    ロールにプロバイダ/モデルを割当
```

### Resolve フロー

```
Resolve("conversation", "vision"):
  1. conversation ロールのモデルの capabilities に "vision" が含まれるか？
     → Yes: Inline=true (そのまま使える)
     → No: vision ロールにフォールバック → Inline=false (別モデル)

フォールバック順: role → "background" → "conversation"
```

## メッセージ構造

```go
type Message struct {
    Role        string      // "system", "user", "assistant", "tool"
    Content     string
    UserID      string      // 発言者のプラットフォーム ID
    UserName    string      // 発言者の表示名
    Channel     string      // チャンネル ID
    ChannelName string
    GuildID     string
    GuildName   string
    MessageID   string      // Discord メッセージ ID
    Timestamp   time.Time
    ImageURLs   []string    // base64 データ URI
    MediaKeys   []string    // MediaStore キー
    ToolCalls   []ToolCall  // アシスタントのツール呼び出し
    ToolCallID  string      // ツール結果の対応 ID
}
```

## Memento (メモリライフサイクル)

**パッケージ:** `internal/memento/`（サブパッケージ: `acquirer/`, `consolidator/`）

メモリの獲得と統合を担う。インプロセスで動作。

### Acquirer (獲得)

**パッケージ:** `internal/memento/acquirer/`

コンテキスト圧縮時に会話からメモリを抽出:

```go
type Acquirer struct { ... }
func (a *Acquirer) Acquire(ctx, *AcquireRequest) (*AcquireResult, error)
```

パイプライン: 既存メモリ取得 → プロンプト構築 → LLM 抽出 → JSON パース → 重複チェック → 保存

### Consolidator (統合)

**パッケージ:** `internal/memento/consolidator/`

定期的にメモリの重複排除・マージを実行:

```go
type Consolidator struct { ... }
func (c *Consolidator) Consolidate(ctx, *ConsolidateOpts) (*ConsolidateResult, error)
```

パイプライン: 全メモリ読み込み → ベクトルクラスタリング → LLM 判定 (keep/merge/skip) → 実行

## トークン推定

CJK 混在テキストに対応した推定ヒューリスティック:

```go
func estimateStringTokens(s string) int {
    for _, r := range s {
        switch {
        case r <= 0x7F:                      tokens += 0.25  // ASCII
        case r >= 0x4E00 && r <= 0x9FFF:     tokens += 1.5   // CJK 漢字
        case r >= 0x3040 && r <= 0x309F:     tokens += 1.0   // ひらがな
        case r >= 0x30A0 && r <= 0x30FF:     tokens += 1.0   // カタカナ
        case r >= 0x3000 && r <= 0x303F:     tokens += 1.0   // CJK 記号
        case r >= 0xFF00 && r <= 0xFFEF:     tokens += 1.0   // 全角 ASCII
        default:                              tokens += 1.5   // その他
        }
    }
}
```

初回 LLM 応答の `Usage.PromptTokens` で実際値と比較し、キャリブレーション係数を EMA 更新する。
