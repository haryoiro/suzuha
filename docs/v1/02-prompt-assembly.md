# プロンプト組み立てフロー

エージェントが LLM を呼び出す際、複数のソースから情報を収集してプロンプトを組み立てる。このドキュメントではその全フローと注入ポイントを記述する。

## プロンプトの全体構造

LLM に送信されるメッセージリストの順序:

```
┌─────────────────────────────────────────────────────────────┐
│ 1. System Prompt（固定）                                     │
│    IDENTITY.md + SOUL.md を結合したもの                       │
│    圧縮されない。再起動でも永続。                              │
├─────────────────────────────────────────────────────────────┤
│ 2. Ephemeral: Memory Context（一時）                         │
│    "Relevant memories:\n- [type] content\n..."               │
│    セマンティック検索 top-3 の結果                             │
├─────────────────────────────────────────────────────────────┤
│ 3. Ephemeral: Location Context（一時）                       │
│    GPS 位置情報スニペット（有効時のみ）                        │
├─────────────────────────────────────────────────────────────┤
│ 4. Ephemeral: User Profiles（一時、ユーザーごと）             │
│    各ユーザーの好感度・最近の変動・記憶・エピソード・サーバー   │
├─────────────────────────────────────────────────────────────┤
│ 5. Ephemeral: Self-awareness（一時）                         │
│    "Self-awareness:\n  - fact\n  - fact"                     │
│    自分自身に関する記憶（最大 3 件）                           │
├─────────────────────────────────────────────────────────────┤
│ 6. Channel History（初見チャンネルのみ）                      │
│    "[Recent history for channel=ID]\n..."                    │
│    直近 10 件のメッセージ履歴                                 │
├─────────────────────────────────────────────────────────────┤
│ 7. Conversation History（永続、圧縮対象）                     │
│    全チャンネルの会話履歴（user/assistant/tool メッセージ）    │
├─────────────────────────────────────────────────────────────┤
│ 8. Current Time（一時）                                      │
│    "[現在時刻: 2024-12-15 14:30:45 (Sun)]"                   │
├─────────────────────────────────────────────────────────────┤
│ 9. Directive（一時、最後）                                    │
│    "[RESPOND] あなた宛のメッセージです。必ず返答してください。" │
│    or "[LISTEN] チャンネルの会話です。skip_response ツール..."  │
└─────────────────────────────────────────────────────────────┘
```

### 設計意図

- **エフェメラルが会話履歴の前**: LLM がユーザーの背景を理解してから会話を読むため
- **ディレクティブが最後**: LLM のリーセンシーバイアス（最後に読んだ情報に強く影響される）を利用して応答方針を徹底させるため
- **システムプロンプトが最初**: 圧縮やトリミングの対象外として固定するため

## 情報源と注入ポイント

### 1. システムプロンプト

**ソース:** `.suzuha/IDENTITY.md` + `.suzuha/SOUL.md`（config.yaml の `agent.prompt_dir` で指定）

**読み込みタイミング:**
- 起動時: `config.loadPromptFiles()` で読み込み、`Config.Agent.SystemPrompt` に格納
- ランタイム: `POST /internal/reload-prompt` で再読み込み → `agent.ReloadPrompt()`

**格納:**
- `Context.systemPrompt` に設定。圧縮やトリミングの対象外。
- `Context.MessagesWithSystem()` で先頭に system メッセージとして付加

### 2. 記憶コンテキスト

**ソース:** `memory.SQLiteStore.Search(query, topK=3)`

**検索クエリ:** 最後のユーザーメッセージの content

**フォーマット:**
```
Relevant memories:
- [user] ユーザーAはプログラマーで Python が得意
- [world] 昨日のイベントでは30人が参加した
- [episode] ユーザーBとの会話で新しいプロジェクトについて話した
```

### 3. ユーザープロフィール

**ソース:** 複数の DB テーブル（並列クエリ）

各ユーザーについて以下を収集:
- `users.Resolve()` → display_name, closeness, trust, interest
- `users.GetAffinity(userID, limit=3)` → 最近の好感度変動
- `memory.ListByUser(userID, 5)` → そのユーザーに関する既知事実
- `memory.ListEpisodesByParticipant(platformUserID, 3)` → 共有エピソード
- `users.GetUserGuilds(userID)` → サーバー参加状況

**フォーマット例:**
```
[User: ユーザーA]
  Closeness: 3.5, Trust: 2.0, Interest: 4.0
  Recent affinity:
    +0.5 (closeness): 面白い話をしてくれた (01/15)
    +1.0 (interest): AI について深い議論をした (01/14)
  Known facts:
    - Python が得意
    - 東京在住
  Shared episodes:
    - 新プロジェクトについて議論した
  Servers:
    テストサーバー: #general, #random
```

### 4. 自己認識

**ソース:** `memory.ListByType(MemoryTypeSelf, 3)`

自分自身に関する記憶。プロフィール構築と並列で取得。

### 5. 位置情報

**ソース:** `location.Store.BuildContextSnippet()`

Overland アプリから受信した GPS データ。有効化されている場合のみ注入。

### 6. チャンネル履歴

**ソース:** `discord_get_history` ツール（10 件） → フォールバック: `memory.SearchRecent(3件, 3日)`

初見チャンネルの場合のみ注入。同チャンネルの 2 回目以降はスキップ（Context.seenChannels で追跡）。

### 7. 現在時刻

**フォーマット:** `[現在時刻: 2024-12-15 14:30:45 (Sun)]`

Act ステージでメッセージリスト構築時に動的に追加。

### 8. ディレクティブ

**生成元:** Think ステージの会話状態分析

ディレクティブの詳細は `01-pipeline.md` の Think ステージを参照。

## セルフプロンプト

独り言（topics タスク）が生成するセルフプロンプトは特殊な経路で注入される:

```
[ふと意識が浮かぶ]

今: 2024-12-15 14:30
退屈レベル: 65 / 100（そこそこ暇）

私は自由

最近の記憶の断片:
- ...

最近見つけた記事:
- ...

何をするかは自分で決める 何もしなくてもいい
```

セルフプロンプトのディレクティブ:
```
[SELF_PROMPT] 自分の内なる思考です。以下のツールを自由に組み合わせて遊んでください: <tool-names>
```

## ツール定義の注入

LLM にはツール定義も送信される。Act ステージで全有効ツール + 条件付き `skip_response` が注入される。

ツール定義のトークン量はコンテキスト予算から差し引かれる:
```
toolTokens = Σ(name + description + schema + 20) for each tool
messageBudget = maxTokens - toolTokens - 512(generation budget)
```

## トークンキャリブレーション

初回の LLM 応答で実際の `Usage.PromptTokens` を取得し、推定値との比率を EMA で更新:
```
new_calibration = old_calibration * 0.7 + (actual / estimated) * 0.3
```

以降のトリミング判断に使用され、推定精度が徐々に向上する。
