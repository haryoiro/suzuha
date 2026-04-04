# suzuha2 開発ガイド

## ビルド環境 (Mandatory)

このプロジェクトはDockerコンテナ内でビルド・実行される。ホストマシンにはsqlite3ヘッダやGoのCGO依存が入っていない。

- `go build`, `go test`, `go vet` は **`docker compose -f container/compose.yaml exec agent` 経由で実行する**
- ホストで直接実行しないこと (CGO依存でビルドが通らない)
- `sqlite3` コマンドも同様にコンテナ内で実行する
- **ビルドタグ**: `-tags fts5` と `-buildvcs=false` を常に付ける

```bash
# ビルド
docker compose -f container/compose.yaml exec agent go build -buildvcs=false -tags fts5 ./...

# テスト
docker compose -f container/compose.yaml exec agent go test -tags fts5 ./...

# sqlite3
docker compose -f container/compose.yaml exec agent sqlite3 /data/memory.db
```

## Docker構成

- `docker compose -f container/compose.yaml up` で agent, admin, searxng, admin-frontend が起動
- agent は Air によるホットリロード対応
- llama.cpp server は別途 `docker run` で起動 (コンテナ名: `llama-qwen3-5`, ポート: 8000)

## コミットルール (Mandatory)

lefthook + commitlint で強制される:
- **Conventional Commits**: `type(scope): 日本語の説明`
- **type**: feat, fix, docs, style, refactor, perf, test, build, ci, chore, revert
- **header**: 72文字以内
- **description**: 日本語必須 (ひらがな・カタカナ・漢字を1文字以上含む)
- **body/footer**: 200文字/行以内

## LLMプロバイダ切り替え

3層分離アーキテクチャ (Provider / Model / Role):

```bash
# ロール切り替え
curl -X PUT http://localhost:9090/internal/llm/roles/conversation \
  -H "Content-Type: application/json" \
  -d '{"provider": "zhipu", "model": "glm-4.7"}'

# モデルカタログ更新
curl -X POST http://localhost:9090/internal/llm/models/refresh
```

---

## アーキテクチャ規約 (Mandatory)

### レイヤー構成

```
cmd/suzuha-agent/       ← エントリポイント + DI配線のみ
internal/
├── adapter/            ← プロトコル変換 (外部世界との接続)
├── agent/              ← ドメイン (パイプラインロジック)
├── memento/            ← ドメイン (記憶ライフサイクル)
├── feature/            ← アプリケーション (ユースケース, scheduler.Feature)
├── llm/                ← インフラ (LLMクライアント + ProviderRegistry)
├── memory/             ← インフラ (永続化)
├── event/              ← インフラ (イベントバス)
├── tool/               ← インフラ (ツールレジストリ)
├── scheduler/          ← インフラ (cron + Feature contract)
├── voice/              ← インフラ (音声処理)
├── mcp/                ← インフラ (MCP ツールブリッジ)
├── observe/            ← インフラ (ログ, トレーシング)
├── chat/               ← インフラ (Interface 定義のみ)
├── channel/            ← インフラ (チャンネル設定)
├── user/               ← インフラ (ユーザー永続化)
├── config/             ← インフラ (設定読み込み)
├── admin/              ← インターフェース (管理HTTP API)
└── lib/                ← 純粋ユーティリティ (crypto, jtime, textutil)
external/               ← 外部サービス SDK (stt, tts, detect, embedding, search, transcript, twitter)
```

### 依存方向ルール (Mandatory)

import は **上から下にのみ** 許可。逆方向・同レイヤー間の import は禁止。

```
cmd/         → 全レイヤー (DI配線のため)
adapter/     → infra, domain, external
feature/     → infra, domain, external (feature 間の import は禁止)
domain/      → infra の interface のみ (具体型を import しない)
infra/       → external, lib
external/    → lib のみ
lib/         → 標準ライブラリのみ
```

#### アンチパターン (Never)

- adapter/ が他の adapter/ を import する
- feature/ が他の feature/ を import する
- domain/ (agent/, memento/) が adapter/ を import する
- domain/ が external/ を直接 import する
- lib/ が internal/ の他パッケージを import する
- external/ が internal/ を import する

### パッケージ配置ルール

| 新しいコードの種類 | 配置先 | 例 |
|---|---|---|
| 外部プロトコルアダプタ (WebSocket, Discord, CLI) | `internal/adapter/{name}/` | adapter/device/, adapter/discord/ |
| 自己完結した機能 (Tools + Tasks) | `internal/feature/{name}/` | feature/vision/, feature/diary/ |
| ドメインロジック (パイプライン, 記憶) | `internal/agent/` or `internal/memento/` | agent/prompt/, memento/acquirer/ |
| ストレージ, クライアント, レジストリ | `internal/{name}/` | memory/, llm/, tool/ |
| 外部サービスHTTPクライアント | `external/{name}/` | external/stt/, external/tts/ |
| 共有ユーティリティ (暗号, 時刻, テキスト) | `internal/lib/{name}/` | lib/crypto/, lib/jtime/ |

### Feature パターン (Mandatory)

新しい機能は `scheduler.Feature` interface を実装する:

```go
// internal/feature/{name}/feature.go
type Feature struct { /* 依存注入 */ }

func (f *Feature) Name() string                        { return "{name}" }
func (f *Feature) Setup(ctx context.Context, db *sql.DB) error { return nil }
func (f *Feature) Tools() []tool.Tool                  { return nil }
func (f *Feature) Tasks() []scheduler.CronTask         { return nil }
```

- Feature.Tools() で返したツールは自動登録される
- Feature.Tasks() で返したタスクは自動登録される
- Feature 間の import は禁止。共有が必要なら infra 層に interface を定義する

### Interface 定義ルール (Mandatory)

**Consumer-side interface** パターンを使う:

```go
// 良い: consumer 側で必要なメソッドだけ定義
// internal/feature/forget/feature.go
type consolidator interface {
    Consolidate(ctx context.Context, opts *consolidator.ConsolidateOpts) (*consolidator.ConsolidateResult, error)
}

// 悪い: provider パッケージの interface を直接 import
import "github.com/haryoiro/suzuha/internal/memento/consolidator"
```

- interface は **使う側** で定義する (Go の implicit interface を活用)
- provider パッケージに大きな interface を定義しない
- adapter/ と feature/ が infra/ の具体型に依存する場合は、consumer-side interface を定義して間接参照する

### 命名規則

| 対象 | 規則 | 例 |
|---|---|---|
| パッケージ名 | 単数形, 短い, 小文字 | `vision`, `diary`, `user` |
| ファイル名 | snake_case, パッケージ名を繰り返さない | `tracker.go`, `change.go` (NOT `vision_tracker.go`) |
| Feature 構造体 | `Feature` (パッケージ名で修飾される) | `vision.Feature`, `diary.Feature` |
| Tool 構造体 | `{動詞}{名詞}Tool` or private | `servoTool`, `GetLocation` |
| Consumer interface | 小文字 (private), メソッド1-2個 | `consolidator`, `completer` |
| コンストラクタ | `New` or `New{Type}` | `New()`, `NewStore()` |
| テストファイル | `{対象}_test.go` | `acquirer_test.go` |

### その他の規約

- **エラー処理**: エラーは上位に伝播する。ログはトップレベル (main, handler) でのみ出力。中間層では `fmt.Errorf("context: %w", err)` でラップ。
- **Context**: 最初の引数に `ctx context.Context` を渡す。Context に設定や依存を詰めない。
- **ファイルサイズ**: 1ファイル 500行以下を目安にする。超えたら分割を検討。
- **テスト**: パッケージ内テスト (`package xxx`) を基本とする。外部テスト (`package xxx_test`) はインテグレーション用。
- **未使用コード**: 使われていないコードは即削除する。「将来使うかも」で残さない。
- **コメント**: ロジックが自明でない箇所にのみ日本語でコメント。全ての exported シンボルに godoc コメント。

---

## 自己改善ワークフロー (Self-Improve)

suzuha2 が Discord の `#self-improve` チャンネル (ID: `1484450828302680154`) に改善リクエストを投稿する。
Claude Code (Discord plugin 経由) がリクエストを受け取り、コード変更を行う。

### 手順

1. suzuha2 からのリクエストが `#self-improve` チャンネルに届く
2. **git worktree** を使い、Air の監視外で作業する:
   ```bash
   git worktree add /tmp/suzuha-wt-<name> -b self-improve/<name>
   ```
3. worktree 内でコードを変更・テスト:
   ```bash
   cd /tmp/suzuha-wt-<name>
   docker compose -f container/compose.yaml exec agent go build -buildvcs=false -tags fts5 ./...
   ```
4. 変更をコミット:
   ```bash
   git add -A && git commit -m "self-improve: <内容>"
   ```
5. worktree を削除 (ブランチは残す):
   ```bash
   git worktree remove /tmp/suzuha-wt-<name>
   ```
6. チャンネルにブランチ名と変更内容を報告
7. PR を作成:
   ```bash
   git push origin self-improve/<name>
   gh pr create --base main --head self-improve/<name> \
     --title "self-improve: <内容>" \
     --body "suzuha2からの自己改善リクエスト"
   ```
8. **絶対にマージしないこと** — オーナーがレビュー後にマージする

### 重要
- Air は `cmd/suzuha-agent/` と `internal/` の `.go` / `.yaml` を監視している
- worktree (`/tmp/`) での変更は再起動を引き起こさない
