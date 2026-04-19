# suzuha プロジェクト概要

## これは何か

suzuha は **自律型 Discord Bot エージェント** である。LLM を頭脳として、Discord 上でユーザーと会話し、ツールを使い、記憶を蓄え、自発的に行動する。物理ボディ（ESP32）と接続して「目」と「首」を持つこともできる。

## 主な特徴

- **4 段パイプライン**: Perceive → Think → Act → Reflect
- **長期記憶**: SQLite + ベクトル埋め込みによるセマンティック検索
- **好感度システム**: closeness / trust / interest の 3 軸でユーザーとの関係を追跡
- **自発的行動**: 退屈度ベースの独り言、ウェブ探索
- **音声チャット**: Whisper (STT) + VOICEVOX (TTS) で Discord VC に参加
- **物理エージェント**: ESP32 カメラ + サーボで「見る」「首を振る」「表情を変える」
- **MCP アプリ**: MCP プロトコル対応ツールサーバーを動的にインストール
- **管理ダッシュボード**: React SPA で記憶・ユーザー・ツール・プロンプト等を管理
- **LLM プロバイダー切り替え**: ランタイムで OpenAI / Zhipu / Qwen / ローカル LLM を切り替え

## 技術スタック

| レイヤー | 技術 |
|---------|------|
| 言語 | Go (バックエンド), TypeScript/React (フロントエンド), C (ファームウェア) |
| LLM | OpenAI 互換 API（any-llm-go ライブラリ経由） |
| DB | SQLite (CGO, mattn/go-sqlite3) |
| チャット | discordgo + disgo (DAVE E2EE 対応) |
| 音声 | Whisper.cpp (STT), VOICEVOX (TTS), hraban/opus (コーデック) |
| 物理 | ESP-IDF v5.5.3, ESP32 / ESP32-P4 |
| ビジョン | YOLO (物体検出), VLM (画像記述) |
| 検索 | SearXNG (メタ検索エンジン) |
| DI | samber/do/v2 |
| ビルド | Docker Compose, Air (ホットリロード) |
| フロントエンド | Vite, Ant Design, ogen (OpenAPI コード生成) |

## アーキテクチャ図

```
┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐
│ Discord  │  │  Device  │  │   Web    │  │   CLI    │
│ (Source) │  │ (Source) │  │ (Source) │  │ (Source) │
└────┬─────┘  └────┬─────┘  └────┬─────┘  └────┬─────┘
     │             │             │             │
     └──────┬──────┴──────┬──────┘             │
            │             │                    │
     ┌──────▼─────────────▼────────────────────▼──────┐
     │              Gateway (Hub)                       │
     │  Source lifecycle + health monitoring            │
     │  GET /internal/gateway/status                   │
     └──────────────────┬─────────────────────────────┘
                        │ events
                        ▼
     ┌──────────────────────────────────────────────────┐
     │                  Event Bus                       │
     └──────────────────┬─────────────────────────────┘
                        │ subscribe
                        ▼
     ┌──────────────────────────────────────────────────┐
     │        Agent (dynamic per-source workers)        │
     │  ┌──────────┐  ┌──────────┐  ┌──────┐  ┌──────┐│
     │  │ Perceive │→ │  Think   │→ │ Act  │→ │Reflect││
     │  └──────────┘  └──────────┘  └──────┘  └──────┘│
     │       │             │            │          │    │
     │  ユーザー解決   記憶検索      LLM呼出   会話ログ │
     │  画像処理      プロフィール   ツール実行 永続化   │
     └──────────────────────────────────────────────────┘
          │              │            │
          ▼              ▼            ▼
     ┌──────────┐  ┌──────────┐  ┌──────────┐
     │  Users   │  │  Memory  │  │   LLM    │
     │ (SQLite) │  │ (SQLite) │  │ (API)    │
     └──────────┘  └──────────┘  └──────────┘

     ┌──────────────────────────┐  ┌───────────────────┐
     │     Scheduler            │  │   Admin Server    │
     │  topics, explore,        │  │   (React SPA)     │
     │  forget, schedule, ...   │  │                   │
     └──────────────────────────┘  └───────────────────┘

     ┌──────────────┐  ┌───────────┐  ┌──────────────┐
     │ Voice (VC)   │  │  Device   │  │  Location    │
     │ Whisper+VOX  │  │  ESP32    │  │  Overland    │
     └──────────────┘  └───────────┘  └──────────────┘
```

## ディレクトリ構成

```
suzuha/
├── cmd/suzuha-agent/       # エントリーポイント (main.go, providers.go)
├── internal/
│   ├── adapter/            # プロトコルアダプタ
│   │   ├── cli/            # CLI アダプタ (stdin/stdout)
│   │   ├── device/         # ESP32 WebSocket アダプタ (薄いレイヤー)
│   │   └── discord/        # Discord アダプタ (discordgo)
│   ├── admin/              # 管理ダッシュボード HTTP サーバー (ogen + handler)
│   ├── agent/              # 4段パイプライン (perceive/think/act/reflect/context/hook)
│   │   └── prompt/         # プロンプト組み立て
│   ├── channel/            # チャンネル設定・アクティビティ追跡
│   ├── chat/               # チャットインターフェース定義 (Sender, Replier, IDSender, Typer, VoiceSpeaker)
│   ├── gateway/            # Gateway: 全アダプタのライフサイクル管理 + ヘルス追跡
│   ├── config/             # YAML 設定読み込み
│   ├── event/              # イベントバス
│   ├── feature/            # 自己完結的な Feature モジュール
│   │   ├── action/         # スケジュールアクション (予約投稿)
│   │   ├── diary/          # 日記 (hourly/daily)
│   │   ├── forget/         # 記憶重複削除
│   │   ├── location/       # GPS 位置追跡 (Overland)
│   │   ├── research/       # ウェブ検索 (SearXNG + ページ取得)
│   │   ├── topics/         # 独り言 (退屈度ベース)
│   │   ├── video/          # 動画理解 (字幕取得, フレーム VLM)
│   │   ├── vision/         # ビジョン機能 (tracker, change detection, stream, tools)
│   │   └── wander/         # 好奇心探索 (SearXNG + LLM 評価)
│   ├── lib/                # 汎用ユーティリティ
│   ├── llm/                # LLM クライアント + ProviderRegistry
│   ├── mcp/                # MCP (Model Context Protocol) ツールサーバー
│   ├── memento/            # メモリライフサイクル
│   │   ├── acquirer/       # メモリ獲得
│   │   └── consolidator/   # メモリ統合
│   ├── memory/             # 長期記憶ストア (SQLite + ベクトル検索)
│   ├── observe/            # メトリクス, ログ, Langfuse
│   │   └── langfuse/
│   ├── scheduler/          # cron スケジューラー + Feature contract
│   ├── tool/               # ツールインターフェース + builtin/
│   ├── user/               # ユーザー管理 + 好感度
│   └── voice/              # 音声パイプライン (VAD, STT, TTS, Session)
├── external/               # サードパーティサービスアダプタ
├── admin/                  # React SPA (Vite + Ant Design)
├── widget/                 # WebSocket voice widget (React + Vite)
├── firmware/               # ESP32 ファームウェア (ESP-IDF)
├── api/                    # TypeSpec API 仕様
├── .suzuha/                # プロンプトファイル (IDENTITY.md, SOUL.md) [gitignored]
├── container/              # Docker 構成 (compose.yaml, Dockerfile 等)
└── config.yaml             # アプリケーション設定 [gitignored]
```
