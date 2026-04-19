---
applyTo: "spec/**/*"
---

# TypeSpec 開発ルール

> **重要: TypeSpec 定義を追加・修正する前に、必ずこのドキュメント全体を読むこと。**
>
> 読まずに実装すると、命名規則の揺れ・モデルの重複定義・バリデーションの二重実装などが発生します。

## ディレクトリ構成

```text
spec/
├── main.tsp              # service / info / server 定義 + 全 route の import
├── models/               # 共通モデル（エラー、ページネーション等）
│   └── common.tsp
├── routes/               # エンドポイント定義（1 リソース = 1 ファイル）
│   ├── health.tsp
│   ├── memories.tsp
│   └── ...
├── docs/                 # このドキュメント
├── tspconfig.yaml        # @typespec/openapi3 emitter 設定
├── package.json
└── generated/openapi.yaml  # emit 出力（追跡済み）
```

| ディレクトリ | 役割 |
| --- | --- |
| `models/` | 複数 route から参照する共通モデル。ドメイン単位でファイル分割 |
| `routes/` | 個別エンドポイント。1 リソース = 1 ファイル（`memories.tsp` など） |

## ファイルの書き方

### route ファイル

```tsp
// routes/memories.tsp
import "@typespec/http";
import "@typespec/rest";

using TypeSpec.Http;
using TypeSpec.Rest;

namespace SuzuhaAdmin;

model Memory {
  id: string;
  type: string;
  content: string;
  created_at: string;
}

@route("/api/memories")
@tag("Memory")
interface Memories {
  @get op list(...PaginationQuery): PaginatedList<Memory>;
  @get op get(@path id: string): Memory | ErrorResponse;
  @post op create(@body body: Memory): Memory | ErrorResponse;
  @delete op remove(@path id: string): OkResponse | ErrorResponse;
}
```

**必須要素**

- `namespace SuzuhaAdmin;` — 全 route は同じルート namespace に属する
- `@route("/api/...")` — パス prefix
- `@tag("...")` — OpenAPI タグ（UI グルーピング用）
- `interface` で op をグループ化（ogen ハンドラがまとまる）

### op

```tsp
interface Users {
  /** ユーザー一覧を取得。 */
  @get op list(...PaginationQuery): PaginatedList<User>;

  /** プロフィール更新。 */
  @post op update(@path id: string, @body body: UserUpdate): User | ErrorResponse;
}
```

- HTTP メソッドデコレータ（`@get`, `@post`, `@put`, `@delete`）を付ける
- レスポンスは `成功型 | ErrorResponse` の union
- JSDoc コメントは op の説明として openapi に反映される

### model の基本方針

> **原則: 正規化した基本モデルを定義し、派生モデルで形を変える。**
> 同じフィールドを複数モデルに重複定義しない。

```tsp
// 基本モデル（全フィールド）
model User {
  id: string;
  display_name: string;
  role: "owner" | "member" | "guest";
  created_at: string;
}

// 派生モデル
model UserCreate is OmitProperties<User, "id" | "created_at">;
model UserUpdate is OptionalProperties<OmitProperties<User, "id" | "created_at">>;
```

| ユーティリティ | 用途 |
| --- | --- |
| `OmitProperties<T, K>` | 特定フィールドを除外 |
| `PickProperties<T, K>` | 特定フィールドのみ抽出 |
| `OptionalProperties<T>` | 全フィールドをオプショナル化 |

## 共通モデル

`models/common.tsp` に定義済み。新しい route からは自動的に使える：

- `OkResponse { ok: boolean }` — 単純肯定応答
- `ErrorResponse { error: string }` — エラー応答
- `PaginatedList<T>` — `{ data: T[], total: int32 }`
- `PaginationQuery` — `@query offset?: int32; @query limit?: int32`

## デコレータリファレンス

| デコレータ | 用途 |
| --- | --- |
| `@route("/...")` | パス指定（interface にも op にも使える） |
| `@tag("Name")` | OpenAPI タグ |
| `@query` | クエリパラメータ |
| `@path` | パスパラメータ |
| `@body` | リクエストボディ |
| `@format("email")` | バリデーション（email / uri など） |
| `@minLength(n)` / `@maxLength(n)` | 文字列長制約 |
| `@minValue(n)` / `@maxValue(n)` | 数値範囲制約 |
| `@example("...")` | サンプル値 |

## 生成ターゲット（Go + ogen 専用）

- 出力: `spec/generated/openapi.yaml`
- 消費: `agent/internal/admin/api/` に ogen で Go コード生成
- JSON フィールドは **snake_case**（Go struct tag と一致させるため）
- `int32` / `int64` を明示（Go 側の型に直結）

## コミット前チェック

- `pnpm --filter api compile` で `generated/openapi.yaml` を再生成する
- `pnpm --filter api format` で tsp ファイルをフォーマットする
- ogen 生成コードの再生成は `mise run spec` でまとめて実行
