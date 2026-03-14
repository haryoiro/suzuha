# エージェントパイプライン

エージェントの中核は `internal/agent/` にある 4 段パイプラインである。イベントを受信してから応答を送信するまでの全処理がここで行われる。

## パイプライン全体フロー

```
Event Bus → Batch (3秒ドレイン) → Perceive → Think → Act → Reflect
                                      ↓          ↓       ↓      ↓
                                  [PipelineHook fired at each stage]
```

各ステージ間で `PipelineHook` が呼ばれる。フック実行はエラーが出てもパイプラインを停止しない（ログのみ）。

## イベントバッチング

`agent.go` の `Run()` メソッドがイベントループ。イベントバスから最初のイベントを受信した後、**3 秒のドレインウィンドウ** 内に到着した追加イベントをまとめてバッチ処理する。新しいイベントが来るたびにタイマーがリセットされるため、連続的なメッセージは 1 バッチにまとまる。

```go
// ドレイン: 3秒間追加イベントを待つ
timer := time.NewTimer(a.drainWindow) // デフォルト 3s
for {
    select {
    case e := <-events:
        batch = append(batch, e)
        timer.Reset(a.drainWindow) // 新イベントでリセット
    case <-timer.C:
        break drain // タイムアウトでバッチ確定
    }
}
```

---

## Stage 1: Perceive（知覚）

**ファイル:** `perceive.go`

イベントバッチを受け取り、LLM が理解できるメッセージ形式に変換する。

### 処理内容

1. **チャンネルフィルタリング**: disabled チャンネルのイベントをスキップ
2. **イベント → メッセージ変換**: 各イベントを `llm.Message` に変換
   - `source == "internal"` → `role: "system"`
   - それ以外 → `role: "user"`
3. **ユーザー解決**: `users.Resolve()` でユーザーを DB に登録/取得。好感度スコア（closeness, trust, interest）を抽出
4. **チャンネルアクティビティ追跡**: `channel_activity.last_user_message_at` を更新
5. **画像処理**:
   - LLM がビジョン対応 → 画像をダウンロードし base64 データ URI に変換（最大 4 枚、10MB/枚）
   - 別途 VLM がある → `VLM.DescribeImage()` でテキスト記述に変換
6. **チャンネル履歴注入**: 初見チャンネルの場合、直近 10 件の履歴を system メッセージとして注入。ツール呼び出し失敗時はセマンティック検索でフォールバック
7. **コンテキスト追加**: メッセージを `Context.Add()` で会話履歴に追加

### 出力: `Perception`

```go
type Perception struct {
    LastMessage       llm.Message  // バッチ最後のメッセージ
    LastEvent         event.Event  // バッチ最後のイベント
    Channel           string       // チャンネル ID
    IsDM              bool         // ダイレクトメッセージか
    IsVoice           bool         // 音声チャンネルからか
    DirectlyAddressed bool         // メンション/DM/CLI/トリガー/セルフプロンプト
    SenderIsBot       bool         // Bot 自身のメッセージか
    MaxCloseness      float64      // バッチ内ユーザーの最大親密度
    MaxInterest       float64      // バッチ内ユーザーの最大関心度
    TurnStartIdx      int          // コンテキスト長（Reflect でのログ用）
}
```

---

## Stage 2: Think（思考）

**ファイル:** `think.go`

エフェメラル（一時的）コンテキストを構築し、応答方針（ディレクティブ）を決定する。

### Part A: エフェメラルコンテキスト構築（並列実行）

4 つの情報源から並列にコンテキストを収集:

1. **記憶コンテキスト**: `memory.Search()` でセマンティック検索（top-3）
2. **ユーザープロフィール**: 直近 10 件の user メッセージから一意ユーザーを抽出し、各ユーザーについて並列に:
   - 好感度スコア + 最近の変動イベント
   - そのユーザーに関する記憶（最大 5 件）
   - 共有エピソード（最大 3 件）
   - サーバー参加状況
3. **自己認識**: `memory.ListByType(MemoryTypeSelf, 3)` で自分自身に関する事実
4. **位置情報**: `locationStore.BuildContextSnippet()` で GPS コンテキスト

### Part B: チャンネルモード確認

- `Listen` モード → `Thought{ListenMode: true}` を返して Act をスキップ
- `Home` チャンネル → 「ここは自分の住処チャンネルです。リラックスして自由に話して。」を注入

### Part C: ディレクティブ決定

会話状態と好感度に基づいて、LLM への指示を決定する:

| 優先度 | 条件 | ディレクティブ |
|--------|------|---------------|
| 1 | セルフプロンプト | `[SELF_PROMPT]` ツールを使って自由に行動 |
| 2 | 直接アドレス（メンション/DM/CLI） | `[RESPOND]` 必ず返答 |
| 3 | アクティブ会話（Bot 発言 <2 分 かつ ≤3 メッセージ） | `[RESPOND]` 会話続行 |
| 4 | 最近の 1-on-1（Bot 発言 <5 分 かつ ≤6 メッセージ） | `[LISTEN]` 続ける価値があれば返答 |
| 5a | 親密度 ≥ 3.0 | `[LISTEN]` 気軽に返す |
| 5b | 関心度 ≥ 2.0 | `[LISTEN]` 詳しい話題のときだけ |
| 5c | 親密度 ≤ -1.0 | `[LISTEN]` スキップ |
| 6 | デフォルト | `[LISTEN]` 本当に付け加える価値があるときだけ |

**会話状態の分析** (`conversationState()`): コンテキスト内の直近 50 メッセージを逆走査し、Bot の最終発言からの経過時間、その後のユーザーメッセージ数、参加ユーザー数を計算。

### 出力: `Thought`

```go
type Thought struct {
    Ephemeral  []llm.Message  // 記憶・プロフィール・位置情報
    Directive  string         // [RESPOND] or [LISTEN] 指示文
    ListenMode bool           // true = Act をスキップ
}
```

---

## Stage 3: Act（行動）

**ファイル:** `act.go`

LLM を呼び出し、ツールを実行し、応答を送信する。

### メッセージ組み立て順序

LLM に送信するメッセージは以下の順序で組み立てられる:

```
1. システムプロンプト (IDENTITY.md + SOUL.md)       ← 最初（固定、圧縮されない）
2. エフェメラルメッセージ (記憶, プロフィール, 位置)  ← ユーザーの理解が先
3. 会話履歴 (コンテキスト内の全メッセージ)           ← 会話の流れ
4. 現在時刻 "[現在時刻: 2024-12-15 14:30:45 (Sun)]"  ← 時間認識
5. ディレクティブ "[RESPOND] あなた宛のメッセージ..."  ← 最後（リーセンシー効果）
```

### コンテキストトリミング

メッセージ総量がコンテキストウィンドウを超える場合:

1. ツール定義のトークン量 + 生成予算 (512) を差し引いて予算計算
2. 予算内に収まらなければ、**先頭（古い）の非 system メッセージから削除**
3. 最低予算: 500 トークン

**トークン推定ヒューリスティック**: 文字種ごとに異なるトークン/文字比率を使用:
- ASCII: 0.25 tok/char
- CJK 漢字: 1.5 tok/char
- ひらがな/カタカナ: 1.0 tok/char

### ツール注入

- 全有効ツールを `registry.AllEnabled()` から取得
- `[RESPOND]` 以外のディレクティブの場合、`skip_response` 仮想ツールを追加

### LLM ツールループ（最大 10 回）

```
for iter := 0; iter < 10; iter++ {
    1. メッセージリスト構築（初回のみフル組み立て、以降は再利用）
    2. トリミング
    3. LLM 呼び出し
    4. 初回: トークン推定のキャリブレーション（EMA: 70%旧 + 30%新）
    5. ツール呼び出しがなければ → 完了
    6. ツール呼び出しあり:
       - アシスタントメッセージをコンテキストに追加
       - 中間テキストがあればチャットに送信
       - 各ツールを実行、結果をコンテキストに追加
       - 全ツールが StopAfter → 早期終了
}
```

### 応答ルーティング

応答の送信先は入力ソースによって分岐:

| ソース | 出力先 |
|--------|--------|
| `device` イベント | `deviceSpeaker.SpeakText()` (ESP32 TTS) |
| VC 接続中ギルド | `voiceSpeaker.SpeakText()` (Discord VC) |
| それ以外 | `chat.Send()` (テキストメッセージ) |

### 応答抑制条件

以下の場合、応答は送信されない:

- `skip_response` ツールが呼ばれた
- 中間テキストと最終テキストの類似度 ≥ 95%（レーベンシュタイン距離）
- サイレントレスポンス（`[SKIP]`, `[SILENT]`）
- チャンネルモード ≠ Active

---

## Stage 4: Reflect（振り返り）

**ファイル:** `reflect.go`

会話ターンのログ記録とコンテキスト永続化を行う。

### 処理内容

1. **会話ログ記録**: `conversation_logs` テーブルに挿入（turn_id = UUID で 1 ターン分をグループ化）
2. **コンテキスト永続化**: 全メッセージを JSON として `context_snapshot` テーブルに保存（再起動後に復元される）
3. **圧縮判定**: `UsageRatio()` が閾値（デフォルト 80%）を超えたら非同期で圧縮開始

### コンテキスト圧縮

圧縮は非同期で実行される（パイプラインをブロックしない）。ミューテックスで同時実行を防止。

1. **Consolidator（LLM ベース）が利用可能な場合**:
   - スナップショットのメッセージ数の半分をターゲットに
   - LLM に「どのメッセージを残すか」を判断させる
   - 好感度デルタ情報も返される → ユーザーの好感度を更新
2. **フォールバック**: 単純に古い半分を切り捨て
3. 圧縮後: チャンネル追跡・ユーザープロフィール追跡をリセット、コンテキストを再永続化

---

## Pipeline Hooks

**ファイル:** `hook.go`

```go
type PipelineHook interface {
    AfterPerceive(ctx, batch, perception) error
    AfterThink(ctx, perception, thought) error
    AfterAct(ctx, perception, thought) error
    AfterReflect(ctx, perception) error
}
```

Feature が `PipelineHook` を実装していれば `agent.AddHook()` で登録される。エラーはログされるが、パイプラインは中断しない。
