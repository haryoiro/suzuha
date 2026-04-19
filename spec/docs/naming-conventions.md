---
applyTo: "spec/**/*"
---

# 命名規則

## ルート（エンドポイント）

- **複数形 + kebab-case** を使用する

```
# Good
/api/memories
/api/channel-settings
/api/scheduled-actions

# Bad
/api/memory
/api/channelSettings
/api/channel_settings
```

## API 要素

| 要素 | ケース | 例 |
| --- | --- | --- |
| ルート | kebab-case | `/api/memories`, `/api/channel-settings` |
| JSON フィールド | **snake_case** | `user_id`, `created_at` |
| クエリパラメータ | snake_case | `offset`, `limit`, `channel_id` |

> **JSON が snake_case なのは、Go 側の struct tag と一致させるため。**
> TS クライアント主体のプロジェクトでは camelCase を採ることが多いので混同しないこと。

## ネストしたリソース

```
/api/users/{id}/affinity
/api/channels/{id}/settings
```

## アクション系エンドポイント

動詞を許容する：

```
/api/agent/reload
/api/scheduler/{name}/trigger
```

## TypeSpec 要素

| 要素 | ケース | 例 |
| --- | --- | --- |
| namespace | PascalCase | `SuzuhaAdmin` |
| model | PascalCase | `Memory`, `UserUpdate`, `PaginatedList` |
| op（操作） | camelCase | `list`, `update`, `getById` |
| interface | PascalCase（複数形） | `Memories`, `Users`, `Tools` |
| ファイル名 | kebab-case | `channel-settings.tsp`, `scheduled-actions.tsp` |
| `@route` | 複数形 kebab-case | `/api/memories` |
| `@tag` | 単数形 PascalCase | `Memory`, `User` |

## モデル派生の命名

基本モデルから派生させた場合：

| 派生元 | 用途 | 命名例 |
| --- | --- | --- |
| `User` | 作成入力（id/created_at 除く） | `UserCreate` |
| `User` | 更新入力（全部 optional） | `UserUpdate` |
| `User` | 一覧用サマリ | `UserSummary` |

## 禁止

- `interface` 名の単数形（`User` ではなく `Users`）
- `op` 名の PascalCase（`GetProfile` ではなく `getProfile`）
- JSON フィールドの camelCase（Go 互換性のため snake_case 固定）
- `utils`, `helpers`, `common` という名前の interface や model
