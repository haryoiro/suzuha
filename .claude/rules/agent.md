---
applyTo: "**"
paths: "internal/agent/**"
---

# エージェント

## handleEvent フロー

`agent.go` の `handleEvent()` は以下の順序で処理する。

1. **Event → Message 変換** — `event.Event` を `llm.Message` に変換
2. **User identity 解決** — `users.Resolve(ctx, source, platformUserID, userName)` で内部ユーザー特定・自動作成
3. **チャンネル履歴ブートストラップ** — `injectChannelHistory()` で未見チャンネルの直近会話を注入
4. **コンテキストに追加** — `ctx.Add(msg)` で短期記憶に追加
5. **ユーザープロファイル注入** — 初回のみ system メッセージとしてユーザー情報を注入
6. **コンテキスト圧縮チェック** — 使用率が `contextWindowPct`（デフォルト80%）超なら consolidator に圧縮要求。不通時は `TruncateOldest` でフォールバック
7. **長期記憶注入** — `memory.Search` で関連記憶を検索し system メッセージとして注入
8. **応答ディレクティブ決定** — `responseDirective()` で `[RESPOND]` or `[LISTEN]` を生成
9. **LLM completion** — `completeWithTools()` でツールループ（最大10回）。ディレクティブは初回のみ一時的 system メッセージとして注入（コンテキストに永続化しない）
10. **アシスタント応答をコンテキストに追加**
11. **応答送信 or スキップ** — `isSilentResponse()` が `[SKIP]` や空テキストを検出したらスキップ

## コンテキスト管理

`context.go` の `Context` struct:
- `messages []llm.Message` — 短期記憶本体
- `injectedUsers map[string]bool` — プロファイル注入済みユーザー追跡
- `seenChannels map[string]bool` — 履歴ブートストラップ済みチャンネル追跡
- トークン推定: 4文字 ≈ 1トークン（`EstimatedTokens()`）
- `UsageRatio()` で使用率を計算し圧縮判定に使用

圧縮時のリセット:
- `ResetInjectedUsers()` + `ResetSeenChannels()` を consolidator 圧縮・truncate 両方で呼ぶ

## 応答判定

`interest.go`:
- `ShouldRespond(msg)` — DM/メンション/CLI/トリガー → true。通常チャンネルメッセージも true（ディレクティブに委ねる）
- `isDirectlyAddressed(msg)` — DM, `<@botID>` メンション, CLI source, トリガーを検出

ディレクティブ（`agent.go` の `responseDirective()`）:
- 直接アドレス → `[RESPOND]` タグ付き指示（必ず応答）
- それ以外 → `[LISTEN]` タグ付き指示（応答不要なら `[SKIP]` を返す）
- 一時的 system メッセージとして LLM に渡し、コンテキストには保存しない

## チャンネル履歴ブートストラップ

`injectChannelHistory(ctx, channelID, content, source)`:
1. `channelID` が空 or `seenChannels` に存在 → スキップ
2. `tools.Get("discord_get_history")` でツール取得 → なければスキップ（CLI 等、プラットフォーム非依存）
3. ツール実行で直近10件取得 → `formatChannelHistory()` でユーザー名解決（`users.Resolve(ctx, source, ...)`）
4. 取得失敗 or 0件 → `memory.SearchRecent(ctx, content, 5, 3日前)` でDBフォールバック
5. 結果を system メッセージとして注入
6. `ctx.MarkChannelSeen(channelID)`

## ForceCompact

`ForceCompact()` — 管理画面から呼び出し可能な公開メソッド。即座にコンテキスト圧縮を実行。
