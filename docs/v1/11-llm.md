# LLM 統合

## LLM クライアント

**パッケージ:** `internal/llm/`

OpenAI 互換 API を `any-llm-go` ライブラリ経由で呼び出す。プロバイダー固有の差異はライブラリが吸収する。

### 主要メソッド

```go
type Client struct {
    // Complete: ツール定義付きの chat completion
    Complete(ctx, messages []Message, tools []tool.Tool) (*Response, error)

    // CompleteRawDefault: ツールなし、プレーンメッセージでの completion
    // (explore, consolidator 等の内部用)
    CompleteRawDefault(ctx, messages []providers.Message) (*Response, error)

    // Embed: テキストをベクトル埋め込みに変換
    Embed(ctx, text string) ([]float32, error)

    // DescribeImage: VLM で画像を記述
    DescribeImage(ctx, imageURL string) (string, error)

    // SwapProvider: ランタイムでプロバイダー切り替え
    SwapProvider(provider, model, apiKey, apiBase string, maxCtx int, vision bool) error

    // ProviderInfo: 現在のプロバイダー情報
    ProviderInfo() (provider, model, apiBase string, vision bool)

    // HasVision/IsVisionCapable: ビジョン機能の有無
    HasVision() bool
    IsVisionCapable() bool

    // MaxContextTokens: 最大コンテキストトークン数
    MaxContextTokens() int
}
```

### サポートプロバイダー

| プロバイダー | api_base |
|-------------|----------|
| OpenAI | `https://api.openai.com/v1` |
| Zhipu (智谱) | `https://open.bigmodel.cn/api/paas/v4` |
| Qwen (通义千问) | `https://dashscope.aliyuncs.com/compatible-mode/v1` |
| ローカル LLM | `http://host.docker.internal:8000/v1` 等 |

全て OpenAI 互換 API を使用するため、`api_base` を変えるだけで任意のプロバイダーに接続可能。

### ビジョン統合

2 つのモード:

1. **LLM 自体がビジョン対応** (`vision: true`): 画像を base64 データ URI としてメッセージに直接含める
2. **別途 VLM** (`vision` セクション): `DescribeImage()` で画像をテキスト記述に変換し、テキストとしてメッセージに含める

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
    ToolCalls   []ToolCall  // アシスタントのツール呼び出し
    ToolCallID  string      // ツール結果の対応 ID
}
```

## レスポンス構造

```go
type Response struct {
    Text         string      // テキスト応答
    Reasoning    string      // 推論過程（対応モデルのみ）
    ToolCalls    []ToolCall
    FinishReason string
    Usage        Usage       // トークン使用量
}

type Usage struct {
    PromptTokens     int
    CompletionTokens int
    TotalTokens      int
}
```

## プロバイダー切り替えフロー

```
1. PUT /internal/llm {"preset": "local-qwen"}
2. プリセット解決 → provider, model, api_base, max_tokens, vision
3. API キー解決:
   a. プリセットに api_key があれば使用
   b. 同じ provider + api_base のプリセットから探す
   c. 同じ provider のプリセットから探す
   d. デフォルト config から同 provider なら使用
4. llmClient.SwapProvider() でクライアント切り替え
5. agentContext.SetMaxTokens() でコンテキストサイズ更新
6. 使用率 > 50% なら ForceCompact()
7. app_settings に永続化
```

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

## Consolidator

**パッケージ:** `internal/consolidator/`

コンテキスト圧縮に LLM を使用するモジュール。以前は gRPC で別プロセスだったが、現在はインプロセス。

### 圧縮リクエスト

```go
type CompactRequest struct {
    Messages    []llm.Message
    TargetCount int  // 目標メッセージ数（通常は半分）
}

type CompactResult struct {
    KeepIndices    []int           // 残すメッセージのインデックス
    AffinityDeltas []AffinityDelta // 好感度変動
}
```

LLM に会話履歴を送り、「どのメッセージを残すべきか」「ユーザーの好感度をどう変更すべきか」を判断させる。
