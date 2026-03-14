# suzuha2 プロジェクト概要

## これは何か

suzuha2 は **自律型 Discord Bot エージェント** である。LLM を頭脳として、Discord 上でユーザーと会話し、ツールを使い、記憶を蓄え、自発的に行動する。物理ボディ（ESP32）と接続して「目」と「首」を持つこともできる。

## 主な特徴

- **4 段パイプライン**: Perceive → Think → Act → Reflect
- **長期記憶**: SQLite + ベクトル埋め込みによるセマンティック検索
- **好感度システム**: closeness / trust / interest の 3 軸でユーザーとの関係を追跡
- **自発的行動**: 退屈度ベースの独り言、RSS 巡回、ウェブ探索
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
┌─────────────────────────────────────────────────────────┐
│                    Discord / CLI                         │
│                    (chat.Interface)                       │
└──────────────┬───────────────────────┬──────────────────┘
               │ events                │ responses
               ▼                       ▲
┌──────────────────────────────────────────────────────────┐
│                     Event Bus                            │
└──────────────┬───────────────────────────────────────────┘
               │ subscribe
               ▼
┌──────────────────────────────────────────────────────────┐
│                  Agent Pipeline                          │
│  ┌──────────┐  ┌──────────┐  ┌──────┐  ┌──────────┐    │
│  │ Perceive │→ │  Think   │→ │ Act  │→ │ Reflect  │    │
│  └──────────┘  └──────────┘  └──────┘  └──────────┘    │
│       │             │            │           │           │
│  ユーザー解決   記憶検索      LLM呼出    会話ログ       │
│  画像処理      プロフィール   ツール実行  コンテキスト永続 │
│  履歴注入      ディレクティブ 応答ルーティング 圧縮判定  │
└──────────────────────────────────────────────────────────┘
       │              │            │
       ▼              ▼            ▼
┌──────────┐  ┌──────────┐  ┌──────────┐
│  Users   │  │  Memory  │  │   LLM    │
│ (SQLite) │  │ (SQLite) │  │ (API)    │
└──────────┘  └──────────┘  └──────────┘

┌──────────────────────────┐  ┌───────────────────┐
│     Scheduler            │  │   Admin Server    │
│  topics, rss, explore,   │  │   (React SPA)     │
│  affinity, forget, ...   │  │                   │
└──────────────────────────┘  └───────────────────┘

┌──────────────┐  ┌───────────┐  ┌──────────────┐
│ Voice (VC)   │  │  Device   │  │  Location    │
│ Whisper+VOX  │  │  ESP32    │  │  Overland    │
└──────────────┘  └───────────┘  └──────────────┘
```

## ディレクトリ構成

```
suzuha2/
├── cmd/suzuha-agent/       # エントリーポイント (main.go, providers.go)
├── internal/
│   ├── agent/              # 4段パイプライン (perceive/think/act/reflect/context/hook)
│   ├── admin/              # 管理ダッシュボード HTTP サーバー (ogen + handler)
│   ├── affinity/           # 好感度評価タスク
│   ├── channel/            # チャンネル設定・アクティビティ追跡
│   ├── chat/               # チャットインターフェース (discord/, cli/)
│   ├── config/             # YAML 設定読み込み
│   ├── consolidator/       # コンテキスト圧縮 (LLM ベース)
│   ├── device/             # 物理デバイス (ESP32 WebSocket, サーボ, カメラ, YOLO)
│   ├── dyntools/           # 動的スクリプトツール (/data/tools/)
│   ├── event/              # イベントバス
│   ├── explore/            # ウェブ探索 (SearXNG + LLM 評価)
│   ├── forget/             # 記憶重複削除
│   ├── jtime/              # タイムゾーン対応時刻ユーティリティ
│   ├── llm/                # LLM クライアント (OpenAI 互換, 埋め込み, ビジョン)
│   ├── location/           # GPS 位置追跡 (Overland)
│   ├── mcp/                # MCP (Model Context Protocol) ツールサーバー
│   ├── memory/             # 長期記憶ストア (SQLite + ベクトル検索)
│   ├── notification/       # 通知ミドルウェア (静寂時間, チャンネル設定)
│   ├── observe/            # メトリクス, ログ, リングバッファ
│   ├── rss/                # RSS フィード巡回
│   ├── schedule/           # スケジュールアクション (予約投稿)
│   ├── scheduler/          # cron スケジューラー
│   ├── tool/               # ツールインターフェース + builtin/ (Discord, fetch, python, user_profile)
│   ├── topics/             # 独り言 (退屈度ベース)
│   ├── user/               # ユーザー管理 + 好感度
│   └── voice/              # 音声パイプライン (VAD, STT, TTS, Session)
├── web/admin/              # React SPA (Vite + Ant Design)
├── firmware/               # ESP32 ファームウェア (ESP-IDF)
├── yolo/                   # YOLO 物体検出サーバー
├── api/                    # TypeSpec API 仕様
├── .suzuha/                # プロンプトファイル (IDENTITY.md, SOUL.md) [gitignored]
├── docker-compose.yaml     # Docker 構成
└── config.yaml             # アプリケーション設定 [gitignored]
```
