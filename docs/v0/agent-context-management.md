# エージェント コンテキスト管理

## 全体アーキテクチャ

```mermaid
graph TB
    subgraph External["外部入力"]
        Discord["Discord"]
        CLI["CLI"]
    end

    subgraph AgentProcess["suzuha-agent プロセス"]
        Chat["chat.Interface<br/>(Discord/CLI)"]
        Bus["event.Bus<br/>(buffered ch 128)"]
        Agent["Agent"]
        Ctx["Context<br/>(短期メモリ)"]
        LLM["llm.Client"]
        Tools["tool.Registry"]
        UserStore["user.Store"]
    end

    subgraph ConsolidatorProcess["suzuha-consolidator プロセス"]
        GRPC["gRPC Server"]
        Consolidator["Consolidator"]
        ConsolLLM["llm.Client"]
    end

    subgraph Storage["共有ストレージ (SQLite WAL)"]
        MemDB[("memories<br/>+ FTS5<br/>+ vec")]
        UserDB[("users<br/>platform_links<br/>affinity_events")]
    end

    Discord --> Chat
    CLI --> Chat
    Chat -->|Publish Event| Bus
    Bus -->|Subscribe| Agent
    Agent --> Ctx
    Agent --> LLM
    Agent --> Tools
    Agent --> UserStore
    Agent -.->|gRPC Compact| GRPC
    GRPC --> Consolidator
    Consolidator --> ConsolLLM
    Consolidator -->|Save memories| MemDB
    Agent -->|Search memories| MemDB
    UserStore --> UserDB
    Agent -->|Send response| Chat
```

## イベント処理パイプライン

ユーザー入力からレスポンスまでの全フロー。

```mermaid
flowchart TD
    Start(["Discord/CLI メッセージ受信"])

    Start --> EventConvert["eventToMessage()<br/>Event → llm.Message に変換"]
    EventConvert --> Resolve["users.Resolve()<br/>ユーザー検索/自動作成<br/>表示名をメッセージに反映"]
    Resolve --> AddCtx["ctx.Add(msg)<br/>短期メモリに追加"]
    AddCtx --> InjectProfile{"ユーザープロファイル<br/>未注入?"}

    InjectProfile -->|Yes| DoInject["injectUserProfile()<br/>system messageとして注入<br/>injectedUsersにマーク"]
    InjectProfile -->|No| ShouldResp
    DoInject --> ShouldResp

    ShouldResp{"ShouldRespond()?"}
    ShouldResp -->|"CLI / DM / Mention"| CheckWindow
    ShouldResp -->|"無視"| End(["処理終了"])

    CheckWindow{"UsageRatio()<br/>> contextWindowPct<br/>(default 0.8)?"}
    CheckWindow -->|Yes| Compact["compact()"]
    CheckWindow -->|No| InjectMem
    Compact --> InjectMem

    InjectMem["injectMemories()<br/>長期メモリ検索 (top 3)<br/>system messageとして注入"]
    InjectMem --> ToolLoop["completeWithTools()"]
    ToolLoop --> AddResp["ctx.Add(assistant message)"]
    AddResp --> Send["chat.Send(channel, text)"]
    Send --> End
```

## ツール実行ループ

`completeWithTools()` 内の最大10回のツール実行ループ。

```mermaid
flowchart TD
    Start(["completeWithTools() 開始"])
    Start --> Call["llm.Complete(messages, tools)"]
    Call --> HasTools{"ToolCalls<br/>あり?"}

    HasTools -->|No| Return(["Response を返却"])
    HasTools -->|Yes| CheckIter{"反復回数<br/>< 10?"}

    CheckIter -->|No| Return
    CheckIter -->|Yes| AddAssist["assistant message を<br/>context に追加<br/>(tool_calls メタデータ付き)"]

    AddAssist --> ExecLoop["各 ToolCall を実行"]

    subgraph ToolExec["ツール実行"]
        ExecLoop --> GetTool["registry.Get(name)"]
        GetTool --> Execute["tool.Execute(ctx, args)"]
        Execute --> AddResult["tool result message を<br/>context に追加<br/>(role=tool, ToolCallID付き)"]
    end

    AddResult --> Call
```

## コンテキストウィンドウ管理

短期メモリの圧縮と長期メモリへの退避フロー。

```mermaid
flowchart TD
    Trigger(["UsageRatio > 0.8"])
    Trigger --> CalcTarget["target = len(messages) / 2"]
    CalcTarget --> HasConsol{"Consolidator<br/>接続あり?"}

    HasConsol -->|Yes| GRPCCall["gRPC CompactRequest 送信<br/>(全messages + targetCount)"]
    HasConsol -->|No| Fallback["ctx.TruncateOldest(target)<br/>古いメッセージを単純削除"]

    GRPCCall --> ConsolProcess

    subgraph ConsolProcess["Consolidator 処理"]
        BuildPrompt["LLMプロンプト構築<br/>全メッセージをインデックス付きで列挙"]
        BuildPrompt --> LLMCall["LLM 呼び出し<br/>重要度判定 + 情報抽出"]
        LLMCall --> Parse["レスポンス解析<br/>KEEP / MEMORIES / AFFINITY"]
    end

    Parse --> ApplyKeep["ctx.KeepOnly(keepIndices)<br/>重要メッセージのみ保持"]
    Parse --> SaveMem["store.Save(memory)<br/>抽出事実を長期メモリに保存"]
    Parse --> ApplyAffinity["applyAffinityDeltas()<br/>ユーザー親密度を更新"]

    Fallback --> Done(["圧縮完了"])
    ApplyKeep --> Done
    SaveMem --> Done
    ApplyAffinity --> Done
```

## メモリ検索・保存フロー

### ハイブリッド検索 (FTS5 + sqlite-vec KNN + RRF)

```mermaid
flowchart TD
    Query["クエリ文字列"]
    Query --> FTSBranch & VecBranch

    subgraph FTSBranch["FTS5 キーワード検索"]
        LenCheck{"len >= 3?"}
        LenCheck -->|Yes| FTS["FTS5 trigram MATCH"]
        LenCheck -->|No| LIKE["LIKE マッチング"]
        FTS --> FTSResults["FTS 結果リスト<br/>(rank順)"]
        LIKE --> FTSResults
    end

    subgraph VecBranch["ベクター類似検索"]
        EmbedQuery["embedFn(query)<br/>→ float32[1024]"]
        EmbedQuery --> HasEmbed{"embedding<br/>取得成功?"}
        HasEmbed -->|Yes| KNN["sqlite-vec KNN<br/>MATCH + cosine distance"]
        HasEmbed -->|No| VecSkip["スキップ"]
        KNN --> VecResults["vec 結果リスト<br/>(distance順)"]
    end

    FTSResults --> Merge
    VecResults --> Merge
    VecSkip -.->|"FTSのみ"| FTSOnly["FTS結果を返却"]

    subgraph Merge["RRF マージ"]
        RRF["Reciprocal Rank Fusion<br/>score = Σ 1/(60 + rank)"]
        RRF --> Sort["スコア降順ソート"]
        Sort --> TopN["上位N件を返却"]
    end
```

### 保存フロー

```mermaid
flowchart LR
    subgraph Save["保存 (Consolidator)"]
        MemObj["Memory{Type, Content}"]
        MemObj --> GenEmbed["embedFn(content)<br/>→ float32[1024]"]
        GenEmbed --> InsertMain["memories テーブル<br/>INSERT OR REPLACE"]
        InsertMain --> InsertFTS["memories_fts 更新"]
        InsertFTS --> InsertVec["memories_vec 更新<br/>(SerializeFloat32 バイナリ)"]
    end
```

### グレースフルデグラデーション

```mermaid
flowchart TD
    Start["searchInternal()"]
    Start --> RunBoth["FTS検索 + vec検索 を実行"]
    RunBoth --> BothOK{"両方成功?"}

    BothOK -->|Yes| RRF["RRF マージ → 返却"]
    BothOK -->|No| OnlyFTS{"FTS のみ成功?"}

    OnlyFTS -->|Yes| ReturnFTS["FTS 結果を返却"]
    OnlyFTS -->|No| OnlyVec{"vec のみ成功?"}

    OnlyVec -->|Yes| LoadVec["loadMemoriesByIDs<br/>→ vec結果を返却"]
    OnlyVec -->|No| Error["エラー返却"]
```

## データモデル関係図

```mermaid
erDiagram
    Agent ||--|| Context : "保持"
    Agent ||--|| LLMClient : "使用"
    Agent ||--|| ToolRegistry : "使用"
    Agent ||--|| MemoryStore : "検索"
    Agent ||--|| UserStore : "参照"
    Agent ||--|| EventBus : "購読"
    Agent ||--|| ChatInterface : "送受信"
    Agent ||..|| ConsolidatorClient : "gRPC (optional)"

    Context {
        Message[] messages
        int maxTokens
        map injectedUsers
    }

    Message {
        string Role
        string Content
        string UserID
        string UserName
        string Source
        string Channel
        string MessageID
        time Timestamp
        ToolCall[] ToolCalls
    }

    User {
        string ID
        string DisplayName
        string Role
        float64 Affinity
        map Metadata
    }

    PlatformLink {
        string UserID
        string Platform
        string PlatformUserID
        string PlatformName
    }

    Memory {
        string ID
        string Type
        string Content
        float32[] Embedding
        map Metadata
    }

    AffinityEvent {
        string UserID
        float64 Delta
        string Reason
        string[] InteractionIDs
        time GroupStart
        time GroupEnd
    }

    User ||--o{ PlatformLink : "リンク"
    User ||--o{ AffinityEvent : "履歴"
    MemoryStore ||--o{ Memory : "管理"
    UserStore ||--o{ User : "管理"
    UserStore ||--o{ PlatformLink : "管理"
    Context ||--o{ Message : "保持"
```

## Consolidator 圧縮プロトコル

Consolidator が LLM に送るプロンプトと応答のフォーマット。

```mermaid
sequenceDiagram
    participant A as Agent
    participant G as gRPC
    participant C as Consolidator
    participant L as LLM

    A->>G: CompactRequest<br/>{Messages[], TargetCount}
    G->>C: Compact()

    C->>L: System: "You are a memory consolidation agent..."
    Note over C,L: メッセージをインデックス付きで送信<br/>[0] user (Alice): hello<br/>[1] assistant: Hi!<br/>[2] user (Alice): how are you?

    L-->>C: KEEP: 0,2,5,7<br/>MEMORIES:<br/>- [user] Aliceは非公式な会話を好む<br/>- [world] 今日は天気がいい<br/>AFFINITY:<br/>- delta=+0.5 user_id=123 reason=positive

    C->>C: parseCompactResponse()
    C->>C: store.Save(memories)

    C-->>G: CompactResult<br/>{KeepIndices, Memories, AffinityDeltas}
    G-->>A: Response

    A->>A: ctx.KeepOnly(keepIndices)
    A->>A: applyAffinityDeltas()
```

## スレッドセーフティ

```mermaid
graph LR
    subgraph Mutex["sync.RWMutex"]
        CtxMu["Context.mu<br/>messages アクセス"]
        RegMu["Registry.mu<br/>tools アクセス"]
    end

    subgraph SQLite["SQLite WAL モード"]
        Write["書き込み<br/>(Consolidator)"]
        Read["読み取り<br/>(Agent)"]
        Write -.->|"並行可"| Read
    end

    subgraph Channel["Buffered Channel"]
        BusCh["event.Bus.ch<br/>cap=128<br/>single consumer"]
    end
```
