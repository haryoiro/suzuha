# 好感度システム

ユーザーとエージェントの関係性を 3 軸で追跡する。

## 3 軸

| 軸 | 意味 | 影響 |
|----|------|------|
| **closeness** (親密度) | どれだけ仲が良いか | ≥ 3.0 で気軽に返答、≤ -1.0 でスキップ |
| **trust** (信頼度) | どれだけ信頼しているか | プロフィールに表示 |
| **interest** (関心度) | どれだけ興味があるか | ≥ 2.0 で詳しい話題のときだけ返答、Topics でのメンション確率 |

## 好感度イベント

好感度の変動は `affinity_events` テーブルに記録される:

```sql
CREATE TABLE affinity_events (
    id TEXT PRIMARY KEY,
    user_id TEXT REFERENCES users(id),
    axis TEXT,      -- 'closeness', 'trust', 'interest'
    delta REAL,     -- 変動量（+/-）
    reason TEXT,    -- 変動理由
    created_at DATETIME
);
```

## 好感度の利用箇所

### Think ステージ（ディレクティブ決定）

| 条件 | ディレクティブ |
|------|---------------|
| closeness ≥ 3.0 | `[LISTEN]` 気軽に返す |
| interest ≥ 2.0 | `[LISTEN]` 詳しい話題のみ |
| closeness ≤ -1.0 | `[LISTEN]` スキップ |

### Think ステージ（プロフィール注入）

各ユーザーの closeness, trust, interest と最近の変動イベント（最大 3 件）がエフェメラルメッセージとして注入される。

### Topics タスク（メンション選択）

退屈度が高い場合、interest を重みとした確率的メンション。高 interest ユーザーがいると退屈度閾値が下がる。

### Consolidator（圧縮時の評価）

コンテキスト圧縮時に Consolidator が好感度デルタを返す。圧縮時の会話内容を分析して:
- 「ユーザーと楽しく会話した → closeness +0.5」
- 「ユーザーが嫌なことを言った → closeness -0.3」

## ユーザー管理

**パッケージ:** `internal/user/`

### ユーザー解決

`users.Resolve(ctx, platform, platformUserID, userName)`:
- 既存ユーザー: `user_platform_links` から検索
- 新規ユーザー: ユーザー作成 + プラットフォームリンク追加

### Bot ID 管理

`userStore.AddBotID(id)`: Bot 自身のプラットフォーム ID を登録。Bot のメッセージは好感度処理をスキップ。

### /affinity スラッシュコマンド

Discord の `/affinity` コマンドで自分の好感度を確認できる（エフェメラルレスポンス = 本人のみ表示）。

```
ユーザーA の好感度

親密度: 3.5
信頼度: 2.0
関心度: 4.0

最近の変動
+0.5 (親密) 面白い話をしてくれた — 01/15
+1.0 (関心) AI について深い議論をした — 01/14
```
