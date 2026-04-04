# suzuha Architecture

## System Overview

```
                                    +-----------------+
                                    |   Admin Dashboard  |
                                    |   (Vite, :5173)    |
                                    +--------+--------+
                                             |
                                     REST /api/*
                                             |
+-------------+                     +--------v--------+    +-----------+
|   Discord   |--+                  |  Admin Server   |    |  SearXNG  |
| (discordgo) |  |                  |  (ogen, :8080)  |    | (search)  |
+-------------+  |                  +-----------------+    +-----+-----+
                 |  +-----------+                                |
+-------------+  +->|           |   +-----------------+          |
|   ESP32     |--+  |  Gateway  |   |     Agent       |<---------+
|  (WebSocket)|  |  | (Source   |-->|  (pipeline)     |
+-------------+  |  |  lifecycle|   |                 |    +-----------+
                 |  |  + health)|   |  Perceive       |    |   LLM     |
+-------------+  |  |           |   |  Think          |--->| Providers |
| Web Widget  |--+  |  Event    |   |  Act            |    | (ProviderRegistry)
|  (:5174)    |  |  |   Bus     |   |  Reflect        |    +-----------+
+-------------+  |  +-----------+   +-------+---------+
                 |       ^
+-------------+  |       |
|    CLI      |--+       |
| (stdin/out) |  Sources register
+-------------+  via Gateway
                                            |
                           +----------------+----------------+
                           |                |                |
                    +------v------+  +------v------+  +------v------+
                    |   Memory    |  |  Scheduler  |  |   Tools     |
                    | (SQLite+Vec)|  | (cron tasks)|  | (Registry)  |
                    +------+------+  +------+------+  +------+------+
                           |                |                |
                    +------v------+    +----+----+     +-----+-----+
                    |   Memento   |    | Features|     | MCP/Builtin|
                    | Acquire     |    +----+----+     +-----------+
                    | Consolidate |         |
                    +-------------+    +----+----+----+----+----+----+----+
                                       |    |    |    |    |    |    |
                                     diary topics action forget wander research video vision location
```

## Pipeline Detail

```
Event (Discord/Device/Web)
  |
  v
+--------------------------------------------------+
| PERCEIVE                                         |
|  - Channel filter (active/listen/disabled)       |
|  - User resolution (Discord ID -> internal)      |
|  - Image description (vision model)              |
|  - Channel history injection                     |
|  - Affinity/user profile injection               |
+--------------------------------------------------+
  |
  v
+--------------------------------------------------+
| COMPACT CHECK                                    |
|  - if usage > contextWindowPct -> async compact  |
|    (memento.Acquirer extracts long-term memories) |
+--------------------------------------------------+
  |
  v
+--------------------------------------------------+
| THINK                                            |
|  - Time/location context                         |
|  - Memory search (FTS + Vec + Symbolic)          |
|  - Diary context                                 |
|  - Directive: [RESPOND] / [LISTEN] / [SKIP]      |
+--------------------------------------------------+
  |
  v
+--------------------------------------------------+
| ACT                                              |
|  - LLM completion (conversation role)            |
|  - Tool execution loop:                          |
|      LLM -> tool_calls -> execute -> result      |
|      -> LLM -> ... (until no more tool_calls)    |
|  - Parse [SKIP] / strip directives               |
+--------------------------------------------------+
  |
  v
+--------------------------------------------------+
| RESPOND                                          |
|  - Discord: channel/thread message               |
|  - Device: TTS -> PCM -> WebSocket -> ESP32      |
|  - Web: JSON response                            |
+--------------------------------------------------+
  |
  v
+--------------------------------------------------+
| REFLECT                                          |
|  - Log conversation turn to DB                   |
|  - Persist context snapshot                      |
+--------------------------------------------------+
```

## Source Isolation (Hub-and-Spoke)

```
                      Gateway (Hub)
                    +-----------------+
                    | Register(Source) |
                    | Run(ctx)        |
                    | Status()        |
                    +--------+--------+
                             |
              +--------------+--------------+--------------+
              |              |              |              |
        +-----v------+ +----v-------+ +---v--------+ +---v------+
        | Discord    | | Device     | | Web        | | CLI      |
        | Source     | | Source     | | (sub of    | | Source   |
        | (Run loop) | | (Hub wrap) | |  Device)   | | (stdin)  |
        +-----+------+ +----+-------+ +---+--------+ +---+------+
              |              |              |              |
              +------+-------+------+-------+------+------+
                     |              |              |
                     v              v              v
              +------+------+ +----+-------+ +---+--------+  Workers
              | Discord     | | Device     | | CLI        |  (dynamic)
              | Worker      | | Worker     | | Worker     |
              | Context     | | Context    | | Context    |
              | DrainWin:3s | | DrainWin:2s| | DrainWin:1s|
              | Session:    | | Session:   | | Session:   |
              | Discord     | | Device     | | CLI        |
              +-------------+ +------------+ +------------+
                     |              |              |
              +------v--------------v--------------v------+
              |              Event Bus (fan-in)            |
              |           Shared LLM (role-based)          |
              +-------------------------------------------+
```

## LLM Provider System

```
config.yaml (providers[])
       |
       v
+-------------------------------+
|    ProviderRegistry           |
|  +----------+  +-----------+ |
|  | Provider  |  | Model     | |
|  | (config)  |  | Catalog   | |
|  +----------+  | (DB)      | |
|                +-----------+ |
|  +-----------+               |
|  | Role      |               |
|  | Assignment|               |
|  | (DB)      |               |
|  +-----------+               |
+-------------------------------+
       |
       v
+-------------------------------+
|      llm.Client               |
|  roles: map[string]provider   |
|                               |
|  conversation -> gpt-4o       |
|  background   -> zhipu        |
|  vision       -> gemini-flash |
|  embedding    -> (Embedder)   |
+-------------------------------+
       |
  For("conversation")  ->  agent Act
  For("background")    ->  memento, diary, wander, action
  WithCapability(       ->  vision inline or pipeline
    "conversation",
    "vision")
```

## Device (ESP32) Flow

```
+------------------+          +------------------+
|    ESP32-P4      |          |    Agent         |
|                  |  WebSocket|                  |
|  Microphone -----|--0x01--->|  STT (whisper)   |
|                  |  (audio) |     |            |
|                  |          |     v            |
|                  |          |  event.Bus       |
|                  |          |     |            |
|                  |          |  Agent pipeline  |
|                  |          |     |            |
|                  |          |     v            |
|  Speaker <-------|--0x05----|  TTS (voicevox)  |
|                  |  (audio) |                  |
|                  |          |                  |
|  Camera ---------|--0x02--->|  YOLO detect     |
|                  |  (jpeg)  |  Change detect   |
|                  |          |  Object track    |
|                  |          |                  |
|  OLED Display    |          |                  |
|  Servo Motor <---|--0x03----|  device_command   |
+------------------+          +------------------+
```

## Scheduler & Features

```
Scheduler (cron)
  |
  +-- diary_hourly  (every hour)
  |     Messages -> LLM summarize -> diary_entries
  |
  +-- diary_daily   (1 AM)
  |     Hourly entries -> LLM narrative -> diary_entries
  |
  +-- topics         (every 10 min)
  |     Boredom score -> self_prompt event -> Agent
  |
  +-- schedule       (every 1 min)
  |     Due actions -> direct post or LLM generate -> Discord
  |
  +-- forget         (every 6 hours)
  |     Similar memories -> LLM judge -> keep/merge/delete
  |
  +-- wander         (every 3 hours)
  |     Web search -> evaluate -> follow links -> memorize
  |
  +-- research       (on-demand via tool)
  |     SearXNG search -> fetch pages -> extract -> memorize
  |
  +-- video          (on-demand via tool)
        video_watch: YouTube 等の字幕取得 (external/transcript)
        video_look: フレーム切り出し + VLM 描写 (yt-dlp + ffmpeg)
        Perceive: URL 自動検知 → [動画: "タイトル" (MM:SS)] アノテーション
```

## Memory System

```
+---------------------------------------------------+
|              Memory (SQLite + Vec)                 |
|                                                   |
|  memories table                                   |
|    id, type, content, metadata, keywords, topic,  |
|    persons, event_time, created_at                |
|                                                   |
|  memories_fts (FTS5)                              |
|    Full-text search index                         |
|                                                   |
|  memories_vec (sqlite-vec)                        |
|    Embedding vectors for semantic search          |
|                                                   |
|  Search: FTS + Vec + Symbolic (3-axis)            |
|    FTS: keyword matching                          |
|    Vec: cosine similarity on embeddings           |
|    Symbolic: persons, topic prefix, date range    |
+---------------------------------------------------+
         |                          |
  +------v------+           +------v------+
  |   Memento   |           |   Embedder  |
  |  Acquirer   |           | (Gemini /   |
  |  (extract)  |           |  OpenAI)    |
  |             |           +-------------+
  | Consolidator|
  |  (dedup/    |
  |   merge)    |
  +-------------+
```

## Docker Compose Services

```
+--agent (:8080, :9090)---------+
|  Go binary                     |
|  - Agent pipeline              |
|  - Internal HTTP               |
|  - Admin server                |
|  - Device WebSocket            |
|  - Scheduler                   |
+--------------------------------+

+--searxng-----------------------+
|  Search engine (meta-search)   |
+--------------------------------+

+--voicevox----------------------+
|  TTS engine (Japanese)         |
+--------------------------------+

+--sbv2--------------------------+
|  TTS engine (StyleBertVITS2)   |
+--------------------------------+

+--yolo--------------------------+
|  Object detection (YOLO)       |
+--------------------------------+

+--admin-frontend (:5173)--------+
|  Vite + React (TypeScript)     |
+--------------------------------+

+--widget-frontend (:5174)-------+
|  Web voice chat widget         |
+--------------------------------+

+--langfuse (:3000)--------------+
|  LLM observability             |
|  + worker, db, redis,          |
|    minio, clickhouse           |
+--------------------------------+
```

## Scale

| Layer | Code (hand-written) |
|---|---|
| Go (agent, memory, llm, etc.) | ~24K lines |
| TypeScript/TSX (admin + widget) | ~5.5K lines |
| C (ESP32 firmware) | ~1.5K lines |
| SQL (migrations) | ~360 lines |
| TypeSpec (API definitions) | ~350 lines |
| **Total hand-written** | **~32K lines** |
| Go (ogen generated) | ~38K lines |
