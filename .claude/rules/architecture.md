---
description: レイヤー構成、依存方向、パッケージ配置のルール
---

## レイヤーと依存方向

import は上→下のみ。逆方向・同レイヤー間は禁止。

```
cmd/         → 全レイヤー (DI配線)
adapter/     → infra, domain, external
feature/     → infra, domain, external (feature間 import 禁止)
domain/      → infra の interface のみ
infra/       → external, lib
external/    → lib のみ
lib/         → 標準ライブラリのみ
```

## パッケージ配置

| 種類 | 配置先 |
|---|---|
| プロトコルアダプタ | `internal/adapter/{name}/` |
| ゲートウェイ (ライフサイクル管理) | `internal/gateway/` |
| 機能 (Tools + Tasks) | `internal/feature/{name}/` |
| ドメイン | `internal/agent/` or `internal/memento/` |
| インフラ | `internal/{name}/` |
| 外部サービス SDK | `external/{name}/` |
| ユーティリティ | `internal/lib/{name}/` |

## Feature パターン

新機能は `scheduler.Feature` を実装。Feature.Tools() と Feature.Tasks() で自動登録。

## Never

- adapter が他の adapter を import
- feature が他の feature を import
- domain が adapter や external を直接 import
- `utils`, `helpers`, `common` パッケージ名を使う
