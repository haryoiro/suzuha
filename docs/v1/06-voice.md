# 音声チャットシステム

Discord VC（ボイスチャンネル）での音声対話を実現する。STT（音声→テキスト）と TTS（テキスト→音声）を組み合わせて、テキストパイプラインを経由して応答する。

## アーキテクチャ

```
Discord VC
    │
    │ Opus フレーム (48kHz mono)
    ▼
┌──────────┐     ┌──────┐     ┌────────────┐
│ Session  │ ──→ │ VAD  │ ──→ │ handleSpeech│
│ (disgo)  │     │ /user│     │            │
│ DAVE E2EE│     └──────┘     └────┬───────┘
└──────────┘                       │
                                   │ PCM 48kHz mono
                                   ▼
                            ┌──────────┐
                            │ Whisper  │ STT
                            │ (HTTP)   │
                            └────┬─────┘
                                 │ テキスト
                                 ▼
                          ┌─────────────┐
                          │ Event Bus   │ → Agent Pipeline → 応答テキスト
                          └─────────────┘
                                                  │
                                                  ▼
                            ┌──────────┐    ┌──────────┐
                            │ VOICEVOX │ →  │ Session  │ → Discord VC
                            │ (HTTP)   │    │ SendPCM  │
                            └──────────┘    └──────────┘
                            24kHz mono       48kHz stereo
```

## コンポーネント

### Pipeline（`internal/voice/pipeline.go`）

音声パイプラインの統括。セッション管理、STT/TTS の橋渡し。

```go
type Pipeline struct {
    discordSession *discordgo.Session
    bus            *event.Bus
    stt            STT
    tts            TTS
    sessions       map[string]*Session  // guildID -> Session
}
```

**主要メソッド:**
- `Join(ctx, guildID, channelID)` - VC に参加
- `Leave(guildID)` - VC を退出
- `SpeakText(ctx, guildID, text)` - テキストを音声で発話
- `IsConnected(guildID)` - 接続状態確認
- `SetSpeakerID(id)` - VOICEVOX の話者変更

### Session（`internal/voice/session.go`）

単一の Discord VC 接続を管理。disgo の `voice.Conn` を使用し、DAVE (Discord Audio/Video E2EE) に対応。

**特徴:**
- discordgo のゲートウェイイベントを disgo の voice.Conn に転送するブリッジ構成
- Opus エンコード/デコード（hraban/opus）
- ユーザーごとの VAD インスタンス

**接続フロー:**
1. Opus デコーダ/エンコーダ作成（48kHz）
2. disgo の `voice.NewConn()` で DAVE 対応接続を作成
3. discordgo のイベントハンドラを登録（VoiceStateUpdate, VoiceServerUpdate）
4. `conn.Open()` で接続（30 秒タイムアウト）
5. `conn.SetOpusFrameReceiver()` で受信パイプラインを設定

**送信フロー（SendPCM）:**
1. DAVE 鍵の準備を待機（最大 10 秒）
2. 追加の DAVE エポック収束待ち（2 秒）
3. Speaking フラグ設定
4. プライミング用サイレンスフレーム送信（15 フレーム）
5. 20ms ごとに Opus エンコード → UDP 送信
6. DAVE 鍵遷移エラー時はリトライ
7. 末尾サイレンスフレーム送信、Speaking フラグ解除

### VAD（`internal/voice/vad.go`）

Voice Activity Detection。ユーザーごとに発話区間を検出する。

- エネルギーベースの簡易 VAD
- 発話開始/終了を検出し、完全な発話セグメントの PCM を返す

### STT（`internal/voice/stt.go`）

Whisper.cpp サーバーとの HTTP 通信。

```go
type STT interface {
    Transcribe(ctx context.Context, pcm []byte, sampleRate int) (string, error)
}
```

**Whisper ハルシネーション対策:**
無音/ノイズ入力時に Whisper が出力しがちな定型フレーズをフィルタリング:
- 「ありがとうございました」
- 「ご視聴ありがとうございました」
- 「チャンネル登録お願いします」
- 「おやすみなさい」
- etc.

### TTS（`internal/voice/tts.go`）

VOICEVOX エンジンとの HTTP 通信。

```go
type TTS interface {
    Synthesize(ctx context.Context, text string) ([]byte, error)
}
```

**VOICEVOX 2 段階 API:**
1. `POST /audio_query?text=...&speaker=N` → 合成パラメータ (JSON)
2. `POST /synthesis?speaker=N` → WAV 音声

出力は 24kHz mono PCM。Discord への送信前に 48kHz stereo に変換。

### オーディオ変換

```
VOICEVOX (24kHz mono) → resample24kTo48k() → monoToStereo() → SendPCM (48kHz stereo)
```

- `resample24kTo48k`: 各サンプルを 2 回書き込み（最近傍アップサンプリング）
- `monoToStereo`: 各サンプルを L/R に複製

## 音声イベントの Agent への流れ

音声チャンネルからのメッセージは以下の特別な属性を持つ:

```go
event.NewMessageEvent("discord", event.MessagePayload{
    Content:   transcribedText,
    IsVoice:   true,
    IsMention: true,  // VC メッセージは常に直接アドレス扱い
})
```

Agent 側では:
- `IsMention: true` → `DirectlyAddressed: true` → `[RESPOND]` ディレクティブ
- 応答テキストは `voiceSpeaker.SpeakText()` 経由で VC に送信

## 設定

```yaml
voice:
  enabled: true
  whisper_url: "http://whisper:8001"
  voicevox_url: "http://voicevox:50021"
  speaker_id: 3  # zundamon normal
  allowed_channels: []  # 空 = 全チャンネル許可
```

## Docker 構成

whisper と voicevox は `voice` プロファイルで起動:

```bash
docker compose --profile voice up
```

- `whisper`: whisper.cpp サーバー（CUDA 対応、large-v3 モデル）
- `voicevox`: VOICEVOX エンジン（CUDA 対応）
