# 物理デバイスシステム

ESP32 マイコンボードをカメラ + サーボ付きの物理ボディとして使用し、エージェントに「目」と「首」と「表情」を与える。

## アーキテクチャ

```
┌──────────────┐     WebSocket      ┌──────────────────┐
│   ESP32      │ ←────────────────→ │   Agent Server   │
│  Camera +    │  JPEG frames       │   /ws/device     │
│  Servo +     │  commands          │                  │
│  Display     │                    │   device.Hub     │
└──────────────┘                    └────────┬─────────┘
                                             │
                                    ┌────────┴─────────┐
                                    │     YOLO Server  │
                                    │   物体検出       │
                                    └────────┬─────────┘
                                             │
                                    ┌────────┴─────────┐
                                    │  Change Detector │
                                    │  変化検出 → VLM  │
                                    └──────────────────┘
```

## バックエンド

リファクタリングにより、デバイス関連のコードは 2 つのパッケージに分割されている:

- **`internal/adapter/device/`** — WebSocket アダプタ（薄いレイヤー）
- **`internal/feature/vision/`** — ビジョン機能（tracker, change detection, stream, tools）

### Hub（`internal/adapter/device/`）

デバイスとの WebSocket 接続を管理する中央ハブ。

**主な機能:**
- WebSocket 接続管理（1 デバイスのみ接続可能）
- JPEG フレームの受信・保存
- コマンド送信（サーボ、キャプチャ、表情）
- TTS 音声合成 → WAV データのデバイスへの送信

### FrameStore（`internal/feature/vision/stream.go`）

最新のカメラフレームを保持。HTTP 経由でフレーム画像を配信。

- `GET /internal/device/frame`: 最新 JPEG フレーム
- `GET /internal/device/detections`: SSE で物体検出結果をストリーム

### Change Detector（`internal/feature/vision/change.go`）

連続するフレーム間の差分を検出し、大きな変化があった場合に VLM で画像を記述してイベントバスに発行。

### Object Tracker（`internal/feature/vision/`）

YOLO サーバーに JPEG フレームを送信し、検出結果（ラベル、信頼度、バウンディングボックス）を取得・追跡。

## デバイスツール（`internal/feature/vision/tools.go`）

| ツール名 | 説明 | パラメータ |
|---------|------|-----------|
| `body_turn_head` | 首を動かす | `pan` (0-180), `tilt` (0-180) |
| `body_blink` | スナップショット撮影 | なし |
| `body_expression` | 表情変更 | `expression` (0-7) |
| `body_look` | 視界認識 | なし |

### 表情 ID

| ID | 表情 |
|----|------|
| 0 | 通常 |
| 1 | 嬉しい |
| 2 | 悲しい |
| 3 | 驚き |
| 4 | 怒り |
| 5 | 眠い |
| 6 | 考え中 |
| 7 | 喋り中 |

### `body_look` の動作

1. デバイスにキャプチャコマンド送信
2. 新しいフレームを 2 秒待機（フォールバック: キャッシュフレーム）
3. LLM がビジョン対応 → 画像を直接 ToolResult に添付
4. 別途 VLM → `VLM.DescribeImage()` でテキスト記述

## ファームウェア（`firmware/`）

ESP-IDF v5.5.3 ベース。ESP32 / ESP32-P4 をターゲットとする。

**主な機能:**
- カメラ (OV2640/OV5640) からの JPEG キャプチャ
- WebSocket クライアント（サーバーへの常時接続）
- サーボ制御（PWM、pan/tilt）
- LCD ディスプレイ（表情表示）
- WAV 音声再生（I2S DAC）
- Wi-Fi 自動接続

**Docker ビルド:**
```bash
docker compose --profile firmware run firmware-esp32
```

## WebSocket プロトコル

### デバイス → サーバー

- **JPEG フレーム**: バイナリメッセージ（JPEG データそのまま）
- **ステータス**: JSON `{"type": "status", ...}`

### サーバー → デバイス

- **コマンド**: JSON
  ```json
  {"cmd": "servo", "pan": 90, "tilt": 90}
  {"cmd": "capture"}
  {"cmd": "face", "expression": 1}
  ```
- **WAV 音声**: バイナリメッセージ（WAV データ、TTS 結果）

## デバイスからのイベント

デバイスからの入力はイベントバスに `source: "device"` として発行される。Agent 側では:
- `source == "device"` → 応答は `deviceSpeaker.SpeakText()` 経由で TTS → WAV → WebSocket でデバイスに送信

## 設定

```yaml
# 環境変数
YOLO_URL: "http://yolo:8002"  # YOLO サーバー URL
```

ホームチャンネル（デバイスイベントのルーティング先）は DB の `channel_settings` から `home = 1` のチャンネルを自動検出。
