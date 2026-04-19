---
description: レイヤー構成、依存方向、パッケージ配置のルール
---

## レイヤーと依存方向

import は上→下のみ。逆方向・同レイヤー間は禁止。

```
cmd/         → di, + アプリ固有のランタイム配線
di/          → 全レイヤー (横断的な DI 配線の集約先)
adapter/     → infra, domain, external
feature/     → infra, domain, external (feature間 import 禁止)
domain/      → infra の interface のみ
infra/       → external, lib
external/    → lib のみ
lib/         → 標準ライブラリのみ
```

## パッケージ配置

Go コードは `agent/` ディレクトリ配下:

| 種類 | 配置先 |
|---|---|
| プロトコルアダプタ | `agent/internal/adapter/{name}/` |
| ゲートウェイ (ライフサイクル管理) | `agent/internal/gateway/` |
| 機能 (Tools + Tasks) | `agent/internal/feature/{name}/` |
| ドメイン | `agent/internal/agent/` or `agent/internal/memento/` |
| インフラ | `agent/internal/{name}/` |
| DI 配線 (全パッケージ横断) | `agent/internal/di/` |
| 外部サービス SDK | `agent/external/{name}/` |
| ユーティリティ | `agent/internal/lib/{name}/` |

各 package は自前の `Package(i do.Injector)` で primitive を登録し、
複数 package をまたぐ配線 (agent 構築、scheduler、admin server 等) は
`internal/di` に集約する。cmd はこれを `do.New(di.Packages(cfgPath)...)`
で呼び出すだけに留める。

## Feature パターン

新機能は `scheduler.Feature` を実装。Feature.Tools() と Feature.Tasks() で自動登録。

## Never

- adapter が他の adapter を import
- feature が他の feature を import
- domain が adapter や external を直接 import
- `utils`, `helpers`, `common` パッケージ名を使う
