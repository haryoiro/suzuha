# アーキテクチャ改善ロードマップ

suzuha の設計ゴールは「アシスタントではなく、人間として接せる自立型 Agent」。
インフラ・データ層は堅実に構築されている。次のフェーズでは **記憶の質** と **行動への反映** に注力する。

本ドキュメントは各改善項目の実装方針を記録する。

---

## フェーズ 1: 好感度を行動に反映する

**目的**: 蓄積済みの affinity データが応答の質・態度を変えるようにする。

### 1-A. システムプロンプトに affinity 解釈ルールを追加

**現状**: `injectUserProfile`（`agent.go:452-538`）で `affinity=X.XX` を LLM に渡している。
しかし「この数値をどう解釈すべきか」の指示がないため、LLM は数値を無視しがち。

**方針**: `.suzuha/SOUL.md`（現在空）に affinity ベースの振る舞いガイドラインを記述する。

```
# 好感度と距離感

ユーザープロフィールに affinity スコアが含まれている。
この数値はそのユーザーとの関係性の深さを表す。

- affinity < 0: 苦手。最低限の対応。敬語寄り。自分からは話しかけない
- affinity 0 付近: まだよく知らない人。普通に接する
- affinity 1〜2: 顔見知り。気軽に話せるけど、まだ距離がある
- affinity 3〜4: 仲良し。タメ口混じり、冗談が増える。ツッコミも入れる
- affinity 5+: 大好き。甘えが出る。からかう。心配する。長い会話も苦じゃない

自然にふるまう。affinity が高いからといって突然ベタベタしない。
数値を意識するというより、「この人との距離感」として体に染み付いてる感覚。
```

**変更箇所**:
- `.suzuha/SOUL.md` にガイドラインを追記

**依存**: なし。最小コストで最大効果。

### 1-B. 応答ディレクティブに affinity を反映

**現状**: `responseDirective()`（`agent.go:673-680`）は `isDirectlyAddressed()` のみで
`[RESPOND]` / `[LISTEN]` を二値判定。affinity に関係なく全員同じ扱い。

**方針**: `[LISTEN]` ディレクティブの文面に affinity 情報を含め、LLM の判断材料を増やす。

```go
// interest.go — responseDirective を拡張

func responseDirective(evt event.Event, botID string, affinity float64) string {
    if isDirectlyAddressed(evt, botID) {
        return "[RESPOND] あなた宛のメッセージです。必ず返答してください。"
    }
    switch {
    case affinity >= 3.0:
        return "[LISTEN] 仲の良い人の会話です。気になったら気軽に混ざって。" +
            "反応だけしたいなら discord_react ツールでリアクションを付けてもいい。" +
            "参加しないなら `[SKIP]` とだけ返してください。"
    case affinity <= -1.0:
        return "[LISTEN] チャンネルの会話です。特に自分に関係なければスルーでいい。" +
            "`[SKIP]` とだけ返してください。"
    default:
        return "[LISTEN] チャンネルの会話です。会話に混ざりたいときは返答し、" +
            "そうでなければ `[SKIP]` とだけ返してください。"
    }
}
```

**変更箇所**:
- `internal/agent/interest.go` — `responseDirective` のシグネチャ変更
- `internal/agent/agent.go:192` — affinity を取得して渡す

**取得方法**: `handleEvent` 内でユーザー解決済み（line 139）なので、`u.Affinity` をそのまま使える。
ただし現状 `u` のスコープが `if` ブロック内に閉じているため、変数のスコープを広げる必要がある。

```go
// agent.go handleEvent 内

var userAffinity float64 // デフォルト 0

// 2. Resolve user identity
if a.users != nil && msg.UserID != "" && msg.UserID != a.botID {
    u, err := a.users.Resolve(ctx, msg.Source, msg.UserID, msg.UserName)
    if err != nil {
        a.logger.Warn("user resolve failed", "error", err)
    } else {
        if u.DisplayName != "" {
            msg.UserName = u.DisplayName
        }
        userAffinity = u.Affinity
        // ... guild tracking ...
    }
}

// ...

// 8. Determine response directive
directive := responseDirective(evt, a.botID, userAffinity)
```

**依存**: なし。

### 1-C. タイピングインジケーター

**現状**: LLM 呼び出し中、Discord 側では何も表示されない。応答が来るまで無反応に見える。

**方針**: `completeWithTools` の開始前に Discord の `ChannelTyping` API を呼ぶ。

```go
// agent.go handleEvent 内、LLM 呼び出し直前

// 8.5. Show typing indicator.
if typer, ok := a.chat.(interface{ Typing(ctx context.Context, ch string) }); ok {
    typer.Typing(ctx, channel)
}
```

Discord の Typing インジケーターは約 10 秒で消えるため、長い tool loop 中は再送が必要。
`completeWithTools` の各イテレーション冒頭で再送する。

**変更箇所**:
- `internal/chat/discord.go` — `Typing(ctx, channel)` メソッドを追加
  - `discordgo.Session.ChannelTyping(channelID)` を呼ぶだけ
- `internal/agent/agent.go` — `completeWithTools` 内で呼び出し
- Optional Interface パターン（既存の `chat.Replier` と同じ手法）で実装

**依存**: なし。

---

## フェーズ 2: 応答判定の多段階化

**目的**: 「返すか返さないか」の二択から、人間らしいグラデーションのある反応へ。

### 2-A. [LISTEN] ディレクティブの拡張

**現状**: `[RESPOND]` / `[LISTEN]` → `[SKIP]` の二値。
`discord_react` ツールは存在するが、`[LISTEN]` のディレクティブに言及がないため
LLM が自発的にリアクションを選ぶことが少ない。

**方針**: ディレクティブ文面を拡張し、LLM に多様な反応の選択肢を明示する。
これは 1-B の `responseDirective` 拡張と同時に行う。

ディレクティブの拡張内容:

```
[LISTEN] チャンネルの会話です。以下の選択肢から自然な反応を選んでください:
- 会話に混ざりたいなら返答する
- リアクションだけつけたいなら discord_react ツールを使う
- 一言だけ相槌を打つ（「わかる」「それな」「草」くらいの短さ）
- 関心がなければ [SKIP] とだけ返す
```

**新しいタグは不要**。LLM に選択肢を提示するだけで、既存の `discord_react` ツールと
テキスト応答の仕組みをそのまま使える。

**`[SKIP]` 判定の拡張不要**:
- リアクション → ツール呼び出しが発生 → テキスト応答は空 or `[SKIP]` → 既存ロジックで処理
- 短い相槌 → 通常のテキスト応答として送信される

**変更箇所**: 1-B と統合。`responseDirective` の文面のみ。

### 2-B. 応答長のバリエーション

**現状**: IDENTITY.md に「短くいく。1〜2文でいけるならそれでOK」と書いてあるが、
常に一定の長さで返してしまう傾向がある。

**方針**: SOUL.md に応答長のバリエーションについてのガイドラインを追加。

```
# 応答の長さ

いつも同じ長さで返さない。
- だいたいは短い。1文で済むなら1文
- テンション上がったら長くなることもある。それは自然
- 「うん」「あー」「草」みたいな一言だけの時もある
- 考えて長く話す時もある。でもそれは本当に語りたい時だけ
```

**変更箇所**: `.suzuha/SOUL.md`

**依存**: 1-A と同時に SOUL.md に書く。

---

## フェーズ 3: エピソード記憶

**目的**: 「前にこの話したよね」を可能にする。共有体験を参照することで関係性が深まる。

### 3-A. `episode` メモリタイプの追加

**現状**: `user` / `world` / `tool` / `rss` の 4 タイプ。
`Memory.Metadata` は `map[string]any` なので追加フィールドは自由。

**方針**: `MemoryTypeEpisode` を追加し、consolidator が会話の「出来事」を記録する。

```go
// memory/store.go
const (
    MemoryTypeUser    MemoryType = "user"
    MemoryTypeWorld   MemoryType = "world"
    MemoryTypeTool    MemoryType = "tool"
    MemoryTypeRSS     MemoryType = "rss"
    MemoryTypeEpisode MemoryType = "episode"
)
```

Episode 記憶の Metadata 構造:

```json
{
  "participants": ["user_abc", "user_def"],
  "emotional_tone": "楽しい",
  "topic_tags": ["アニメ", "進撃の巨人"],
  "channel_id": "123456789"
}
```

**DB マイグレーション**: 不要。`memories.type` は TEXT 型で制約なし。

### 3-B. Consolidator プロンプトの拡張

**現状**: `compactSystemPrompt`（`consolidator/server.go:67-103`）は
`[user]` / `[world]` / `[tool]` の 3 フォーマットのみ。

**方針**: `[episode]` フォーマットを追加。

```
MEMORIES:
- [user user_id=<id>] ユーザーに関する情報
- [world] 一般的な知識や事実
- [tool] ツールの使用パターン
- [episode participants=<id1>,<id2> tone=<感情> tags=<tag1>,<tag2>] 出来事の要約

[episode] の使い分け:
- 複数人が関わった会話イベント → episode
- 個人の属性・好み → user
- 出来事 = 「何が起きたか」、個人属性 = 「その人がどういう人か」
- 例: 「アニメの話で盛り上がった」→ episode
        「○○さんはアニメ好き」→ user（episodeから自然に導出される）
```

**パーサー拡張**: `consolidator/server.go` の `parseCompactResponse` に `[episode ...]` の
パースロジックを追加。`participants` / `tone` / `tags` を Metadata に格納。

```go
// consolidator/server.go parseCompactResponse 内

case strings.HasPrefix(line, "[episode"):
    mem, err := parseEpisodeMemory(line)
    if err != nil {
        continue
    }
    result.Memories = append(result.Memories, *mem)
```

```go
func parseEpisodeMemory(line string) (*memory.Memory, error) {
    // "[episode participants=id1,id2 tone=楽しい tags=アニメ,進撃の巨人] 内容"
    // ヘッダ部分を解析して Metadata に格納
    closeBracket := strings.Index(line, "]")
    if closeBracket < 0 {
        return nil, fmt.Errorf("no closing bracket")
    }
    header := line[1:closeBracket]
    content := strings.TrimSpace(line[closeBracket+1:])

    metadata := map[string]any{}
    // participants=id1,id2 → metadata["participants"] = ["id1", "id2"]
    // tone=楽しい → metadata["emotional_tone"] = "楽しい"
    // tags=アニメ,進撃の巨人 → metadata["topic_tags"] = ["アニメ", "進撃の巨人"]
    for _, part := range strings.Fields(header) {
        if k, v, ok := strings.Cut(part, "="); ok {
            switch k {
            case "participants":
                metadata["participants"] = strings.Split(v, ",")
            case "tone":
                metadata["emotional_tone"] = v
            case "tags":
                metadata["topic_tags"] = strings.Split(v, ",")
            }
        }
    }

    return &memory.Memory{
        Type:     memory.MemoryTypeEpisode,
        Content:  content,
        Metadata: metadata,
    }, nil
}
```

### 3-C. エピソード記憶の検索と注入

**現状**: `injectMemories`（`agent.go:540-562`）は `Search()` で top-3 を取得し、
タイプに関係なく全文検索 + ベクトル検索のハイブリッドで返す。

**方針**: ユーザープロフィール注入時に、そのユーザーが参加したエピソード記憶も注入する。

```go
// agent.go injectUserProfile 内、Known facts セクションの後に追加

// Fetch episode memories involving this user.
if a.memory != nil {
    episodes, err := a.memory.SearchByType(ctx,
        u.ID, // ユーザーIDで検索（participants に含まれる）
        memory.MemoryTypeEpisode, 3)
    if err == nil && len(episodes) > 0 {
        content += "Shared episodes:\n"
        for _, e := range episodes {
            content += fmt.Sprintf("  - %s (%s)\n",
                e.Content,
                e.CreatedAt.Format("2006-01-02"))
        }
    }
}
```

**問題点**: `SearchByType` はコンテンツに対する検索であり、Metadata 内の
`participants` 配列を直接検索できない。

**対応案**: episode 記憶のコンテンツに参加者 ID を含めるか、
Metadata JSON 検索用の新しいメソッドを追加する。

**案 A: コンテンツに参加者 ID を埋め込む**（シンプル、推奨）
Consolidator がエピソード記憶を生成するとき、コンテンツの末尾に参加者 ID を含める。
FTS でユーザー ID を検索すればヒットする。

```
[episode participants=user_abc,user_def tone=楽しい tags=アニメ] user_abc と user_def がアニメの話で盛り上がった
```

→ `SearchByType(ctx, "user_abc", MemoryTypeEpisode, 3)` でヒット。

**案 B: `SearchByMetadata` メソッドを追加**（正確だが工数大）

```go
// memory/store.go に追加
SearchByMetadata(ctx context.Context, key string, value string, memType MemoryType, limit int) ([]Memory, error)
```

SQLite の JSON 関数 `json_each()` で Metadata 内の配列を検索する。

```sql
SELECT m.* FROM memories m, json_each(m.metadata, '$.participants') j
WHERE j.value = ? AND m.type = ?
ORDER BY m.updated_at DESC LIMIT ?
```

**初期実装は案 A を推奨**。案 B は episode 記憶が増えて検索精度が問題になったら検討。

---

## フェーズ 4: 自発的コミュニケーションの動機改善

**目的**: 「暇だから話す」ではなく「話したいことがあるから話す」へ。

### 4-A. Topics に RSS ネタを注入

**現状**: `generateMuttering`（`topics/task.go:312-376`）は直近の会話記憶と過去の
独り言のみを参照。RSS で得た情報は使われていない。

**方針**: 最近の RSS 記憶を取得し、独り言の種として LLM に渡す。

```go
// topics/task.go Execute 内、recentMemories 取得の直後

// 3.1. Fetch recent RSS discoveries as conversation seeds.
rssMemories := fetchRecentRSS(ctx, cc, 5)
```

```go
// generateMuttering 内のプロンプト組み立てに追加

if len(rssMemories) > 0 {
    sb.WriteString("最近見つけた面白い記事・ニュース（気になったら話題にしてもいい）:\n")
    for _, m := range rssMemories {
        fmt.Fprintf(&sb, "- %s\n", truncateStr(m.Content, 120))
    }
    sb.WriteString("\n")
}
```

```go
func fetchRecentRSS(ctx context.Context, cc *scheduler.CronContext, limit int) []memory.Memory {
    since := time.Now().Add(-24 * time.Hour)
    mems, err := cc.Memory.SearchRecent(ctx, "", limit, since)
    if err != nil {
        return nil
    }
    var rss []memory.Memory
    for _, m := range mems {
        if m.Type == memory.MemoryTypeRSS {
            rss = append(rss, m)
        }
    }
    return rss
}
```

**注意**: `SearchRecent` は query が空の場合に全件返すかどうかを確認する必要がある。
空文字列だと FTS がスキップされ、ベクトル検索もできないため、
`SearchByType` に `since` パラメータを追加するか、直接 SQL で取得する方が確実。

**代替実装**: `cc.Memory` の `Store` インターフェースを経由せず、
`cc.DB` で直接クエリする（Topics は既に `cc.DB` を使っている箇所がある）。

```go
func fetchRecentRSS(ctx context.Context, db *sql.DB, limit int) []rssItem {
    rows, err := db.QueryContext(ctx, `
        SELECT content FROM memories
        WHERE type = 'rss'
          AND created_at > datetime('now', '-24 hours')
        ORDER BY created_at DESC
        LIMIT ?`, limit)
    // ...
}
```

**変更箇所**:
- `internal/topics/task.go` — `fetchRecentRSS` 追加、`generateMuttering` のシグネチャと
  プロンプト拡張、`Execute` から呼び出し

### 4-B. affinity ベースのメンション頻度調整

**現状**: `selectMentionTarget`（`topics/task.go:405-427`）は affinity > 0 の
ユーザーから affinity 加重ランダムで選択。メンション確率は boredom のみに依存。

**方針**: affinity が特に高いユーザーには、boredom が低めでもメンションする可能性を持たせる。

```go
// topics/task.go

// mentionBoredomMin を affinity に応じて下げる
func adjustedMentionBoredomMin(maxAffinity float64) float64 {
    // affinity 5+ なら boredom 30 からメンション可能（通常は 50）
    if maxAffinity >= 5.0 {
        return 30.0
    }
    if maxAffinity >= 3.0 {
        return 40.0
    }
    return mentionBoredomMin // デフォルト 50.0
}
```

**変更箇所**:
- `internal/topics/task.go` — `selectMentionTarget` の閾値を動的に

---

## フェーズ 5: 好感度変動タイミングの拡張

**目的**: 短い会話でも好感度が動くようにする。

### 5-A. 軽量 Affinity 評価タスク

**現状**: 好感度は Compact 時（コンテキスト 80% 到達）にのみ変動する。
短い会話（数往復で終わる）ではコンテキストが 80% に到達せず、好感度が一切動かない。

**方針**: 新しい `CronTask` として「会話終了検知 + 軽量 affinity 評価」を実装する。

#### 設計

```
affinityEvalTask (新規 CronTask)
  - cron: "*/10 * * * *"  (10分ごと)
  - 処理:
    1. channel_activity から「最終メッセージから N 分経過」のチャンネルを検出
    2. context_snapshot から直近の会話メッセージを取得
    3. 「前回評価以降の新しいメッセージ」があるか確認
    4. あれば、LLM に軽量な affinity 評価のみを依頼（KEEP/MEMORIES は不要）
    5. 結果を affinity_events に記録
```

#### 軽量プロンプト

```
以下の短い会話から、各ユーザーに対する好感度の変化を評価してください。

フォーマット:
- [delta] user_id=<id> platform=<platform> delta=<+/-float> reason=<簡潔に>

変化がなければ「変化なし」とだけ返してください。
```

**変更箇所**:
- `internal/affinity/` — 新パッケージ（feature.go + task.go）
- `internal/scheduler/feature.go` — Feature として登録
- `cmd/suzuha-consolidator/main.go` — Feature 登録に追加

#### 重複評価の防止

`affinity_events.group_end` を参照し、「最後の affinity 評価以降」のメッセージのみを対象にする。
あるいは `task_state` テーブルに `last_evaluated_at` を保存する。

```go
type persistedState struct {
    LastEvaluatedAt time.Time `json:"last_evaluated_at"`
}
```

#### LLM コスト考慮

- この CronTask は短い会話のみが対象なので、入力トークンは少ない
- affinity 評価のみ（KEEP/MEMORIES なし）なので、出力も短い
- 「変化がなければ省略」のルールで、LLM が空回りするケースを減らす
- 10 分間隔 + 「新メッセージなし → スキップ」で無駄な呼び出しを最小化

### 5-B. context_snapshot からの会話取得

**現状**: `context_snapshot` は agent プロセスが持つ。consolidator プロセスから
直接読むことは可能（同じ SQLite DB を WAL モードで共有）。

**問題**: `context_snapshot` には system メッセージ（プロフィール注入、記憶注入）も含まれる。
affinity 評価には user/assistant メッセージのみが必要。

```go
// 取得時にフィルタリング
var msgs []llm.Message
json.Unmarshal(data, &msgs)

var userMsgs []llm.Message
for _, m := range msgs {
    if m.Role == "user" || m.Role == "assistant" {
        userMsgs = append(userMsgs, m)
    }
}
```

---

## フェーズ 6: 好感度の多軸化

**目的**: 単一 affinity スコアから、closeness / trust / interest の 3 軸へ。

### 6-A. DB マイグレーション

```sql
-- migrations/00015_affinity_axes.sql
ALTER TABLE users ADD COLUMN closeness REAL NOT NULL DEFAULT 0.0;
ALTER TABLE users ADD COLUMN trust     REAL NOT NULL DEFAULT 0.0;
ALTER TABLE users ADD COLUMN interest  REAL NOT NULL DEFAULT 0.0;

-- 既存データの移行: affinity → closeness に転記
UPDATE users SET closeness = affinity;

-- affinity_events に軸情報を追加
ALTER TABLE affinity_events ADD COLUMN axis TEXT NOT NULL DEFAULT 'closeness';
```

### 6-B. 型の拡張

```go
// user/user.go
type User struct {
    // ... 既存フィールド ...
    Affinity  float64 // 後方互換（= closeness のエイリアス）
    Closeness float64
    Trust     float64
    Interest  float64
}

type AffinityEvent struct {
    // ... 既存フィールド ...
    Axis  string // "closeness" | "trust" | "interest"
}
```

### 6-C. Consolidator プロンプトの拡張

```
AFFINITY:
- [delta] user_id=<id> platform=<platform> axis=<closeness|trust|interest> delta=<+/-float> reason=<簡潔に>

各軸の意味:
- closeness: 親密度。日常的なやり取り、共有体験、一緒に過ごす時間で変動
- trust: 信頼度。秘密の共有、約束を守る、裏切り行為で大きく変動
- interest: 関心度。面白い話題、新しい情報の提供、知的刺激で変動

1ユーザーにつき変動した軸のみ記載。変動がない軸は省略。
```

### 6-D. 行動への反映

各軸が影響する範囲:

| 軸 | 影響 |
|---|---|
| closeness | 口調（敬語↔タメ口）、冗談の度合い、甘え |
| trust | 深い話をするか、本音を言うか、弱みを見せるか |
| interest | 自分から話しかけるか、相手の話題を覚えて掘り下げるか |

**responseDirective での使い分け**:
- `interest` が高い → `[LISTEN]` でも反応しやすい
- `closeness` が低い → 反応しても丁寧な距離感

**Topics でのメンション選択**:
- `interest` でメンション対象を選ぶ（現在は総合 affinity）
- `closeness` でメンションの口調を調整

### 6-E. 裏切りの重み付け

closeness が高い状態での攻撃は trust を大きく下げる。
Consolidator プロンプトに追加:

```
特別ルール:
- closeness が高い相手からの裏切り（侮辱、嘘の発覚）→ trust を -0.8〜-1.0
- trust が高い相手からの秘密の漏洩 → trust を -1.0、closeness も -0.5
```

**依存**: フェーズ 1〜5 の完了後。大規模な変更のため、1 軸で十分な行動変化が得られてから着手。

---

## フェーズ 7: 人格の一貫性

**目的**: 長い会話でも人格がドリフトしないようにする。

### 7-A. Self-reflection 記憶

**方針**: 新しい記憶タイプ `self` を追加し、のの自身の自己認識を保存する。

```go
const MemoryTypeSelf MemoryType = "self"
```

内容例:
- 「私はこういう時イラッとする」
- 「こういう話題になるとテンション上がる」
- 「プログラミングの話は得意で饒舌になりがち」

生成タイミング: Compact 時に Consolidator が判断。
「この会話で自分（のの）の新しい一面が見えた」場合にのみ生成。

Consolidator プロンプトに追加:

```
- [self] 自分自身について新しく気づいたこと、確認できたこと
  例: 「プログラミングの話になると饒舌になる」「朝は機嫌が悪い」
  毎回出す必要はない。自分の行動パターンに新しい発見があった時だけ
```

注入: システムプロンプトと一緒に、最近の self 記憶を LLM に渡す。

---

## フェーズ 8: 小さな改善

### 8-A. affinity_events.emotional_tone

**現状**: `affinity_events.reason` にテキストで理由を記録。
感情的なトーン情報は reason に含まれることもあるが、構造化されていない。

**方針**: `reason` フィールドの記述をリッチにすることで対応する。
新しいカラム追加は行わない（Consolidator プロンプトの reason 指示を調整するだけ）。

```
- reason は「感情 + 理由」の形式で書く
  例: 「(楽) アニメの話で盛り上がった」「(怒) 暴言を吐かれた」「(感) お礼を言ってくれた」
```

### 8-B. 誤字・言い淀み

**方針**: SOUL.md に注記を追加。

```
# テキストの自然さ

完璧な文章を書こうとしない。
- 「〜だけど」「〜だし」みたいな口語体
- 「えーと」「あー」みたいな言い淀み（たまに）
- 打ち間違いを直さないこともある（稀に）
```

---

## 実装順序サマリー

| 順 | フェーズ | 内容 | 主な変更 | 工数 |
|---|---|---|---|---|
| 1 | 1-A | SOUL.md に affinity 解釈ルール | `.suzuha/SOUL.md` | 極小 |
| 2 | 1-B + 2-A | ディレクティブ拡張 | `interest.go`, `agent.go` | 小 |
| 3 | 1-C | タイピングインジケーター | `chat/discord.go`, `agent.go` | 極小 |
| 4 | 2-B + 8-B | SOUL.md に応答長・自然さガイドライン | `.suzuha/SOUL.md` | 極小 |
| 5 | 4-A | Topics に RSS ネタ注入 | `topics/task.go` | 小 |
| 6 | 3-A〜C | エピソード記憶 | `store.go`, `consolidator`, `agent.go` | 中 |
| 7 | 4-B | affinity ベースのメンション調整 | `topics/task.go` | 小 |
| 8 | 5-A〜B | 軽量 affinity 評価タスク | 新パッケージ `affinity/` | 中 |
| 9 | 6-A〜E | 好感度多軸化 | DB, `user/`, `consolidator`, `agent` | 大 |
| 10 | 7-A | Self-reflection 記憶 | `store.go`, `consolidator` | 中 |
