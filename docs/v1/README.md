# suzuha2 v1 ドキュメント

コードベースから直接分析して作成した技術ドキュメント。

## 目次

| # | ドキュメント | 内容 |
|---|------------|------|
| 00 | [overview](00-overview.md) | プロジェクト概要、技術スタック、アーキテクチャ図、ディレクトリ構成 |
| 01 | [pipeline](01-pipeline.md) | 4段パイプライン（Perceive/Think/Act/Reflect）の詳細 |
| 02 | [prompt-assembly](02-prompt-assembly.md) | プロンプト組み立てフロー、情報注入ポイント、メッセージ順序 |
| 03 | [tools](03-tools.md) | ツールインターフェース、全ツール一覧、skip_response、MCP |
| 04 | [features](04-features.md) | Feature システム、Topics/Explore/Affinity/Forget 等 |
| 05 | [memory](05-memory.md) | 記憶システム、ベクトル検索、DB スキーマ、コンテキスト永続化 |
| 06 | [voice](06-voice.md) | 音声チャット（Whisper STT + VOICEVOX TTS + DAVE E2EE） |
| 07 | [device](07-device.md) | 物理デバイス（ESP32 カメラ + サーボ + YOLO） |
| 08 | [admin](08-admin.md) | 管理ダッシュボード（API 一覧、フロントエンドページ） |
| 09 | [config](09-config.md) | 設定ファイル構造、環境変数、Docker 構成、DI |
| 10 | [affinity](10-affinity.md) | 好感度システム（3軸、イベント、利用箇所） |
| 11 | [llm](11-llm.md) | LLM 統合、プロバイダー切り替え、トークン推定、Consolidator |
| 12 | [event-system](12-event-system.md) | イベントバス、Chat Interface、イベントフロー |
