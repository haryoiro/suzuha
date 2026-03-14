# 好感度（Affinity）システム

## 概要

suzuha はユーザーごとに好感度スコア（`affinity`）を管理する。
好感度はやり取りの内容に基づいて Consolidator が自動的に評価・更新する。
Agent は好感度を直接操作しない。

## スコア

| フィールド | 型 | 説明 |
|---|---|---|
| `users.affinity` | `REAL` | 累積好感度スコア。初期値 `0.0`。上限・下限なし |
| `affinity_events.delta` | `REAL` | 1回の変動量。`-1.0` ～ `+1.0` |

## 変動タイミング

好感度はコンテキスト圧縮（Compact）時にのみ変動する。

```
Agent: コンテキスト使用率 > 80%
  → Consolidator.Compact(messages) を gRPC で呼び出し
  → Consolidator が LLM でメッセージを分析
  → AffinityDeltas を返却
  → Agent が affinity_events に記録 + users.affinity を更新
```

リアルタイムのメッセージ処理中には変動しない。

## 変動基準

Consolidator の LLM プロンプトで以下のルールを定義している。

### 上昇（+0.1 ～ +1.0）

| delta | トリガー | 例 |
|---|---|---|
| +0.1 ～ +0.3 | 軽い好意・日常的なポジティブ | 軽い挨拶、普通の会話、些細なお礼 |
| +0.3 ～ +0.5 | 明確な好意・感謝 | 「ありがとう」「助かった」、共通の話題で盛り上がる |
| +0.5 ～ +0.8 | 強い好意・信頼 | 個人的な相談、深い会話、繰り返しの感謝 |
| +0.8 ～ +1.0 | 非常に強い好意 | 強い信頼の表明、長時間の親密な会話 |

### 下降（-0.1 ～ -1.0）

| delta | トリガー | 例 |
|---|---|---|
| -0.1 ～ -0.3 | 軽い不快 | 素っ気ない態度、無関心 |
| -0.3 ～ -0.5 | 明確な敵意 | 暴言、侮辱、意図的な無礼 |
| -0.5 ～ -1.0 | 強い敵意 | 繰り返しの攻撃、悪意のある行為 |

### 変動なし（省略）

- 通常の事務的なやり取り
- 感情的な要素がないメッセージ
- システムメッセージ、ツール実行結果

## グルーピングルール

- 時系列で近い同一ユーザーのポジティブなやり取りは **1つの delta にまとめる**
- 各ユーザーにつき、1回の Compact で **最大1つ** の AffinityDelta
- `reason` は50文字以内の簡潔な説明

## 矛盾検出ルール

Consolidator は Compact 時にユーザーの affinity 履歴をコンテキスト内の `[User profile: ...]` メッセージから参照できる。
過去の行動パターンと現在の行動に矛盾がある場合、delta の幅を大きく調整する。

| 状況 | 調整 |
|---|---|
| 負の履歴 → 改善（謝罪、親切） | 通常より大きい正の delta（+0.5 ～ +1.0） |
| 正の履歴 → 急な敵意 | 通常より大きい負の delta（-0.5 ～ -1.0） |
| 一貫した正/負の行動 | トレンドを強化する方向で delta を適用 |

これにより、好感度が下がりすぎた場合でも行動の改善で回復できる。

## データモデル

### `affinity_events` テーブル

```sql
CREATE TABLE affinity_events (
    id              TEXT PRIMARY KEY,
    user_id         TEXT NOT NULL REFERENCES users(id),
    delta           REAL NOT NULL,
    reason          TEXT NOT NULL DEFAULT '',
    interaction_ids TEXT,       -- 関連メッセージ ID の JSON 配列
    group_start     DATETIME,  -- やり取りの開始時刻
    group_end       DATETIME,  -- やり取りの終了時刻
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

### `users.affinity` の更新

`affinity_events` への INSERT と `users.affinity += delta` は **1トランザクション** で原子的に実行。

```go
// user.Store.UpdateAffinity(ctx, evt)
// 1. INSERT INTO affinity_events
// 2. UPDATE users SET affinity = affinity + ? WHERE id = ?
// 両方を同一 Tx で実行
```

## Consolidator LLM プロンプト（抜粋）

```
AFFINITY:
- [delta] user_id=<platform_user_id> platform=<platform> delta=<+/-float>
         messages=<comma-separated indices> reason=<brief explanation>

Rules for AFFINITY:
- Positive interactions (gratitude, enjoyment, warmth, shared interests) → +0.1 to +1.0
- Negative interactions (hostility, rudeness, disrespect) → -0.1 to -1.0
- Neutral interactions → omit
- Group temporally close positive interactions from the same user into a single delta
- reason should be concise (under 50 chars)
- Each user: at most one affinity entry per Compact
```

## ユーザープロフィール注入

ユーザーがコンテキストに初めて登場したとき、Agent が自動的に背景情報を注入する。

```
User X がメッセージ送信
  → コンテキストに User X のプロフィールがある？
  → なければ DB から取得して system メッセージとして注入:
    1. users テーブル → affinity スコア、role、display_name
    2. affinity_events → 直近5件の変動履歴 + reason
    3. memories (type=user) → その人に関する長期記憶（最大3件）
  → Compact 後にリセット → 次回登場時に再注入
```

注入例:
```
[User profile: はりょ (ID=abc123) role=member affinity=1.50]
Recent affinity history:
  +0.3: 楽しい雑談 (2026-02-23)
  -0.5: 暴言 (2026-02-20)
Known facts:
  - プログラミングが趣味
  - Goが好き
```

## フロー図

```
User message → Agent context に追加
                    │
                    ▼
            ユーザープロフィール注入（未注入なら）
                    │
                    ▼
            コンテキスト使用率チェック
                    │
              < 80% │ ≥ 80%
                │       │
                ▼       ▼
             通常応答   Consolidator.Compact(messages)
                           │
                           ▼
                    LLM が分析（プロフィール情報を参照）:
                    - KEEP: 保持するメッセージ
                    - MEMORIES: 長期記憶に抽出
                    - AFFINITY: 好感度変化（矛盾検出あり）
                           │
                           ▼
                    Agent が適用:
                    - context.KeepOnly(indices)
                    - context.ResetInjectedUsers()
                    - memory.Save(memories)
                    - user.UpdateAffinity(deltas)
```

## 行動への反映

好感度スコアは以下の場面で行動に影響する。

### 応答ディレクティブ

`responseDirective()` が affinity に基づいてディレクティブを変える:
- affinity >= 3.0: 仲の良い人の発言として、リアクションや相槌を含む多段階の反応を促す
- affinity <= -1.0: スルー寄りのディレクティブ
- その他: 標準的な `[LISTEN]` ディレクティブ

### 口調・距離感

SOUL.md に affinity 値域ごとの振る舞いガイドラインを記載。LLM が User profile の affinity スコアを見て口調を自然に調整する。

### Topics メンション

`selectMentionTarget()` が affinity に基づいてメンション閾値を動的に調整:
- affinity >= 5.0 → boredom 30 からメンション可能（通常は 50）
- affinity >= 3.0 → boredom 40 からメンション可能

### 軽量評価タスク

`affinity_eval` CronTask が短い会話（Compact 未到達）でも好感度を評価する:
- チャンネル非活性を検知 → context_snapshot から直近の会話を取得 → LLM で軽量評価
- 設定: `inactivity_minutes`（デフォルト 15 分）
