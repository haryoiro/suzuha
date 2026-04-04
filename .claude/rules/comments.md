---
description: コメントの書き方ルール
paths:
  - "**/*.go"
---

## 基本方針

コメントは「なぜ」を書く。「何をしているか」はコードで表現する。

## Exported シンボル

godoc 形式で日本語コメントを付ける。`// {Name} は〜` で始める:

```go
// ProviderRegistry はプロバイダ・モデル・ロール割り当てを管理する。
type ProviderRegistry struct { ... }

// ResolveRole はロール割り当てから RoleSpec を組み立てる。
func (r *ProviderRegistry) ResolveRole(ctx context.Context, role string) (*RoleSpec, error) {
```

## 書かないコメント

- コードを読めばわかること (`// i をインクリメント`)
- 変更を追わないコメント (嘘になるコメント)
- 自分が変更していないコードへのコメント追加

## セクション区切り

ファイル内の論理ブロックは `// --- セクション名 ---` で区切る:

```go
// --- Provider CRUD ---

// --- Role Assignments ---
```

## TODO

`// TODO(user): 内容` 形式。担当者なしの TODO は禁止。
