# Session-Pipeline Architecture

## 背景と動機

suzuha は複数のインタラクションソースを持つ:
- Discord テキストチャット
- Discord ボイスチャンネル
- 物理デバイス (ESP32-P4-NANO) の音声対話
- CLI / Admin API
- 内部セルフプロンプト

v1 では全ソースが単一の Agent に単一の Context (会話履歴) を共有していた。
これにより以下の問題が発生:

1. **レイテンシ**: 物理デバイスの音声対話が Discord の drain window (バッチ待ち) を通る
2. **コンテキスト汚染**: Discord の雑談が物理デバイスの会話に混入し、LLM に送るトークンが無駄に増える
3. **密結合**: `Act()` 内に Discord 送信 / Voice TTS / Device TTS の分岐がハードコードされている
4. **拡張困難**: 新しいソースを追加するたびにパイプライン各所に if 分岐が増える

## 解決する問題

- 各ソースが独立した会話コンテキストを持つ
- 物理デバイスの音声対話が即時処理される (drain window をスキップ)
- パイプライン (Perceive → Think → Act → Reflect) がソースを知らない
- 応答ルーティングがパイプラインの外に出る
- Memories, Tools, LLM, SystemPrompt は全ソースで共有

## 設計

### レイヤー構成

```
┌──────────────────────────────────────────────────────┐
│                     Domain (Core)                     │
│                                                       │
│  Pipeline: Perceive → Think → Act → Reflect           │
│  依存: LLM Client, Memory Store, Tool Registry        │
│  ソースの知識: なし                                     │
│                                                       │
└──────────────────────┬───────────────────────────────┘
                       │ Session Interface (Port)
          ┌────────────┼────────────┐
          │            │            │
    ┌─────┴────┐ ┌─────┴────┐ ┌────┴─────┐
    │ Discord  │ │ Device   │ │  CLI     │  Adapter層
    │ Session  │ │ Session  │ │ Session  │
    │          │ │          │ │          │
    │ Context  │ │ Context  │ │ Context  │  各セッションが独自Context
    │ Drain:2s │ │ Drain:0  │ │ Drain:0  │  ソース固有の設定
    │ Out:Chat │ │ Out:TTS  │ │ Out:Stdout│ ソース固有の出力
    └──────────┘ └──────────┘ └──────────┘
```

### Session Interface

```go
// Session は1つの対話セッションを表す。
// 各ソース (Discord, Device, CLI) がこのインターフェースを実装する。
type Session interface {
    // Source はこのセッションのソース識別子を返す ("discord", "device", "cli")
    Source() string

    // Context はこのセッションの会話コンテキスト (会話履歴) を返す
    Context() *Context

    // Send は応答テキストをこのセッションの出力先に送る
    // Discord: テキストチャンネルに送信
    // Device: TTS合成 → ESP32 スピーカーに送信
    // CLI: stdout に出力
    Send(ctx context.Context, text string) error

    // DirectiveConfig はソース固有のパイプライン設定を返す
    // - Directive テンプレート (物理デバイス: 常に応答, Discord: LISTEN/RESPOND)
    // - DrainWindow (物理デバイス: 0, Discord: 2秒)
    // - 応答スキップの可否
    DirectiveConfig() DirectiveConfig
}

type DirectiveConfig struct {
    // ForceRespond が true の場合、skip_response を使わず必ず応答する
    ForceRespond bool
    // DrainWindow はイベントバッチの待ち時間 (0 = 即時処理)
    DrainWindow time.Duration
    // DirectiveTemplate はソース固有の directive テンプレート
    // 空の場合はパイプラインのデフォルト (conversationState ベース) を使う
    DirectiveTemplate string
}
```

### Pipeline

```go
// Pipeline はソースを知らない純粋なエージェントパイプライン。
// Session を通じてコンテキストと出力を操作する。
type Pipeline struct {
    llm    *llm.Client
    memory memory.Store
    users  user.Store
    tools  *tool.Registry
    consol consolidator.Client
    // ...共有リソース
}

// Process は1つのイベントバッチをパイプラインで処理する。
// Session がコンテキストと出力先を提供する。
func (p *Pipeline) Process(ctx context.Context, sess Session, batch []event.Event) error {
    agentCtx := sess.Context()

    // 1. Perceive: agentCtx にメッセージ追加
    perc := p.Perceive(ctx, agentCtx, batch)

    // 2. Compact: agentCtx のトークン使用率チェック
    p.compactIfNeeded(ctx, sess, agentCtx)

    // 3. Think: Memory検索 + directive決定
    thought := p.Think(ctx, agentCtx, perc, sess.DirectiveConfig())
    if thought.ListenMode {
        p.persist(ctx, sess)
        return nil
    }

    // 4. Act: LLM呼び出し + ツール実行
    response, err := p.Act(ctx, agentCtx, perc, thought)
    if err != nil {
        return err
    }

    // 5. Send: Session が出力先にルーティング (パイプラインの外)
    if response != "" {
        if err := sess.Send(ctx, response); err != nil {
            return err
        }
    }

    // 6. Reflect: ログ、永続化
    p.Reflect(ctx, sess, agentCtx, perc)
    return nil
}
```

### Worker (ディスパッチャー)

```go
// Agent はセッション管理 + イベントディスパッチを担当
type Agent struct {
    pipeline *Pipeline
    sessions map[SourceKey]Session
    bus      *event.Bus
}

func (a *Agent) Run(ctx context.Context) error {
    events := a.bus.Subscribe()

    // ソース別チャンネル
    channels := make(map[SourceKey]chan event.Event)
    for key, sess := range a.sessions {
        ch := make(chan event.Event, 16)
        channels[key] = ch
        go a.runWorker(ctx, sess, ch)
    }

    // ディスパッチ: ソースで振り分け
    for {
        select {
        case <-ctx.Done():
            return ctx.Err()
        case evt := <-events:
            key := sourceKeyForEvent(evt)
            if ch, ok := channels[key]; ok {
                ch <- evt
            } else {
                channels[SourceKeyDefault] <- evt
            }
        }
    }
}

func (a *Agent) runWorker(ctx context.Context, sess Session, ch <-chan event.Event) {
    cfg := sess.DirectiveConfig()
    for {
        select {
        case <-ctx.Done():
            return
        case evt := <-ch:
            batch := a.drainBatch(ch, cfg.DrainWindow, evt)
            a.pipeline.Process(ctx, sess, batch)
        }
    }
}
```

## 移行履歴

### Phase 1: Session 導入 + コンテキスト分離 (完了)

- `Session` interface 定義 + DiscordSession / DeviceSession / WebSession 実装
- per-source Context 分離 + DB 永続化
- パイプラインからソース依存の応答ルーティングを Session.Respond() に移動

### Phase 2: ソース固有ロジックの Session への移動 (完了)

- `DirectiveConfig` で drain window, directive template をソース固有に制御
- チャンネルフィルタ、キャッチアップの Session 側スキップ設定

### Phase 3: Hub-and-Spoke Gateway 導入 (完了)

- `internal/gateway/` パッケージ追加: Gateway が全 Source のライフサイクルを管理
- `gateway.Source` interface (Name + Run): 各アダプタが実装
- `Agent.New()` が `[]SourceRegistration` を受け取り、動的にワーカーを生成
- `chat.Sender` interface: `chat.Interface` から Send のみを分離
- `CLISession` + `SourceKeyCLI` 追加: CLI が正しい SourceKey でルーティング
- `GET /internal/gateway/status` ヘルスエンドポイント追加
- 新プラットフォーム追加 = Source 実装 + Session 実装 + SourceRegistration 追加のみ

## 物理アクション (サーボ, 表情, カメラ)

物理デバイスには音声以外のアクションがある:
- **サーボ**: パン/チルトで首を振る
- **表情**: SSD1351 ディスプレイに表示される顔の表情変更
- **カメラ**: キャプチャ要求
- **ボリューム**: スピーカー音量調整

これらは2つの経路で操作される:

### 1. LLM ツールとして (能動的)

LLM が会話の文脈で自発的に使う。既に Tool Registry に登録済み:
- `servo_control`: 話者の方を向く、何かに注目する
- `capture_image`: 視覚情報を取得する
- `face_expression`: 感情を表情で表現する
- `look_at`: VLM と連携して物体を見る

これらは Pipeline の Tool Registry に登録されるので、Session とは独立。
どのセッションの会話でも呼び出せるが、Device が接続されていないときは no-op。

### 2. Session の出力として (受動的)

応答テキストに連動して自動的に:
- TTS 再生中に `FACE_TALKING` 表情に変更
- TTS 完了後に `FACE_NEUTRAL` に戻す
- 感情分析して適切な表情を選ぶ (将来)

これは `DeviceSession.Send()` の内部実装として自然にフィットする:

```go
func (s *DeviceSession) Send(ctx context.Context, text string) error {
    // 1. 話し中の表情に変更
    s.device.SendCommand(map[string]any{"cmd": "face", "expression": FACE_TALKING})

    // 2. TTS 合成 → デバイスに送信
    pcm, rate, _ := s.tts.Synthesize(ctx, text)
    pcm = resampleIfNeeded(pcm, rate, deviceSampleRate)
    s.device.SendTTS(pcm)

    // 3. 再生完了を待って表情を戻す (将来: 感情分析で表情選択)
    s.device.SendCommand(map[string]any{"cmd": "face", "expression": FACE_NEUTRAL})

    return nil
}
```

### 3. DeviceSession 固有のメソッド

Session interface には含めないが、DeviceSession に固有のメソッドとして:

```go
type DeviceSession struct {
    // ...
    device *device.DeviceConn
}

// デバイス固有操作 (ツールから呼ばれる)
func (s *DeviceSession) SetServo(pan, tilt int) error
func (s *DeviceSession) SetFace(expr FaceExpression) error
func (s *DeviceSession) Capture() ([]byte, error)
func (s *DeviceSession) SetVolume(level int) error
```

ツールは `DeviceSession` のメソッドを呼び出す。
Pipeline はツール経由でのみ物理アクションを知る (間接参照)。

### 4. クロスセッション副作用 (Discord → デバイス表情)

Discord の会話内容で物理デバイスの表情が変わるケース:
- Discord で楽しい会話 → デバイスが HAPPY 表情に
- Discord で悲しい話題 → デバイスが SAD 表情に
- Discord で名前を呼ばれた → デバイスが SURPRISED 表情に

これは Session を跨ぐので、Session.Send() の中には入れられない。
**Pipeline の出力フック (SideEffect)** として設計する:

```go
// SideEffect はパイプラインの出力に連動する副作用。
// Session を跨いでデバイスに影響を与える。
type SideEffect interface {
    // AfterResponse はパイプラインが応答を生成した後に呼ばれる。
    // source は応答元セッション、response は応答テキスト。
    AfterResponse(ctx context.Context, source Session, response string) error
}

// DeviceSideEffect はデバイスへの副作用を実装する。
type DeviceSideEffect struct {
    device *device.Hub
    llm    *llm.Client  // 感情分析用 (オプション)
}

func (d *DeviceSideEffect) AfterResponse(ctx context.Context, source Session, response string) error {
    if d.device == nil || !d.device.IsConnected() {
        return nil
    }

    // 方法1: LLM の応答テキストから感情を推定 (軽量分類器 or ルールベース)
    expr := inferEmotion(response)
    d.device.SendCommand(map[string]any{"cmd": "face", "expression": expr})

    return nil
}
```

Pipeline に SideEffect を登録:

```go
type Pipeline struct {
    // ...
    sideEffects []SideEffect
}

func (p *Pipeline) Process(ctx context.Context, sess Session, batch []event.Event) error {
    // ... Perceive → Think → Act ...
    if response != "" {
        sess.Send(ctx, response)
        // 副作用: 全ソースの応答が物理デバイスに影響し得る
        for _, se := range p.sideEffects {
            se.AfterResponse(ctx, sess, response)
        }
    }
    // ... Reflect ...
}
```

これにより:
- Discord Session は物理デバイスの存在を知らない
- Device Session は自分の Send() で表情を直接制御
- DeviceSideEffect が Discord の応答にも反応してデバイスを動かす
- 将来的に他の副作用 (Slack通知, ログ記録等) も同じパターンで追加可能

### 5. セッション間メッセージ伝播

物理デバイスとDiscordの間でメッセージが伝播するケース:

- **Device → Discord**: 物理デバイスで「Discordのみんなに挨拶して」→ ボットがDiscordに投稿
- **Discord → Device**: Discordで重要な通知 → デバイスが音声で読み上げ
- **Device → Discord**: オーナーが物理デバイスに話しかけた内容の要約をDiscordに共有

これは SideEffect とは性質が異なる。SideEffect は「応答に連動する副作用」だが、
伝播は「あるセッションの出力が別セッションの入力になる」。

#### 設計: Broadcast / Bridge パターン

```go
// Bridge はセッション間のメッセージ伝播を管理する。
type Bridge struct {
    sessions map[SourceKey]Session
    rules    []BridgeRule
}

// BridgeRule は伝播ルールを定義する。
type BridgeRule struct {
    From      SourceKey          // 発信元
    To        SourceKey          // 伝播先
    Condition func(Message) bool // 条件 (nil = 常に伝播)
    Transform func(Message) Message // 変換 (nil = そのまま)
}
```

ただし、これは LLM のツールとしても自然に表現できる:

```go
// ツール: discord_send — 任意のチャンネルにメッセージを送る (既存)
// ツール: device_speak — 物理デバイスにTTSで喋らせる (新規)
// ツール: broadcast — 全セッションに同じメッセージを送る (新規)
```

**Phase 1 ではツールベースのアプローチで十分。**
LLM が文脈から判断して適切なツールを呼ぶ。
明示的な Bridge ルールは Phase 3 以降で必要になったら追加。

#### なぜ自動伝播ではなくツールなのか

- LLM が「これは共有すべき」と判断する方が自然
- 全メッセージを自動伝播するとノイズになる
- ツールなら LLM のプロンプトで制御可能 (「重要な情報は discord_send で共有して」)
- 自動伝播ルールはハードコードになりがちで保守性が下がる

## 設計の判断根拠

### なぜ Session なのか

- **Hexagonal Architecture (Ports & Adapters)** の考え方
- Pipeline = Application Core (ソースを知らない)
- Session = Port (インターフェース) + Adapter (ソース固有実装)
- 関心の分離: 「何を考えるか」と「誰と話すか」を分ける

### なぜ EventBus を残すのか

- 既存の Discord / 内部イベント発行コードを変更しなくて済む
- ディスパッチャーパターンで振り分けるだけなので低コスト
- 将来的にイベントソーシングや監査ログに使える

### なぜ Context をセッション内に置くのか

- 各セッションの会話履歴が独立するのが自然
- コンパクションもセッション単位で独立
- MaxTokens もセッションごとに設定可能 (Device は小さくていい)

### catchUpStale はどうなるか

- 各ワーカーが自分のチャンネルだけを見るので混在問題が発生しない
- Discord ワーカーは既存の catchUpStale ロジックをそのまま使える
- Device ワーカーは catchUpStale 不要 (即時処理なので)
