package consolidator

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/haryoiro/suzuha/internal/memory"
	"github.com/mozilla-ai/any-llm-go/providers"
)

// Server implements the consolidation logic.
// It uses an LLM to decide which messages to keep and what to extract
// as long-term memories.
type Server struct {
	llmClient *llm.Client
	store     memory.Store
	logger    *slog.Logger
}

// NewServer creates a consolidator server.
func NewServer(llmClient *llm.Client, store memory.Store, logger *slog.Logger) *Server {
	return &Server{
		llmClient: llmClient,
		store:     store,
		logger:    logger,
	}
}

// Compact analyzes messages and decides what to keep vs. store as long-term memory.
func (s *Server) Compact(ctx context.Context, req *CompactRequest) (*CompactResult, error) {
	if len(req.Messages) == 0 {
		return &CompactResult{}, nil
	}

	// Build a prompt asking the LLM to select important messages and extract memories.
	prompt := buildCompactPrompt(req.Messages, req.TargetCount)

	resp, err := s.llmClient.CompleteRawDefault(ctx, []providers.Message{
		{Role: "system", Content: compactSystemPrompt},
		{Role: "user", Content: prompt},
	})
	if err != nil {
		return nil, fmt.Errorf("consolidator: コンパクト処理のLLM呼び出しに失敗: %w", err)
	}

	result := parseCompactResponse(resp.Text, len(req.Messages))

	// Save extracted memories to long-term store (skip duplicates).
	for i := range result.Memories {
		mem := &result.Memories[i]
		dupID, dupErr := s.store.IsDuplicate(ctx, mem.Content, mem.Type)
		if dupErr != nil {
			s.logger.Warn("consolidator: 重複チェックに失敗しました", "error", dupErr)
		}
		if dupID != "" {
			s.logger.Debug("consolidator: 重複メモリをスキップ", "existing_id", dupID, "content", mem.Content)
			continue
		}
		if err := s.store.Save(ctx, mem); err != nil {
			s.logger.Warn("consolidator: メモリの保存に失敗しました", "error", err)
		}
	}

	return result, nil
}

const compactSystemPrompt = `You are a memory consolidation agent. Your job is to analyze a conversation and:
1. Select which messages are most important to keep in short-term context.
2. Extract key information that should be stored as long-term memories.
3. Assess affinity changes based on the emotional tone and content of interactions.

IMPORTANT: Write all MEMORIES content in Japanese (日本語). The conversation is in Japanese, and memories must also be in Japanese.

Respond in this exact format:

KEEP: 0,2,5,7 (comma-separated message indices to keep)

MEMORIES:
- [user user_id=<platform_user_id>] そのユーザーに関する情報や好み
- [world] 会話で出た一般的な知識や事実
- [tool] ツールの使用パターンや覚えておくべき結果
- [episode participants=<id1>,<id2> tone=<感情>] 出来事の要約
- [self] 自分自身について新しく気づいたこと、確認できたこと

[episode] の使い分け:
- 複数人が関わった会話イベント（盛り上がった話題、一緒に何かした体験）→ episode
- 個人の属性・好み → user
- 出来事のコンテンツには参加者のIDも含めること（検索用）
- 例: [episode participants=123,456 tone=楽しい] 123と456がアニメの話で盛り上がった

[self] の使い分け:
- 自分（のの）の行動パターンに新しい発見があった時だけ
- 例: 「プログラミングの話になると饒舌になる」「朝は機嫌が悪い」「○○の話題が苦手」
- 毎回出す必要はない。本当に新しい自己認識があった時だけ

IMPORTANT: For [user] memories, always include the user_id of the person the fact is about.
The user_id can be found in message metadata (user_id=... in the message header).
If the fact is about a user whose user_id is not clear, omit the user_id.

AFFINITY:
- [delta] user_id=<platform_user_id> platform=<platform> axis=<closeness|trust|interest> delta=<+/-float> messages=<comma-separated indices> reason=<(感情) 日本語で簡潔に>

各軸の意味:
- closeness: 親密度。日常的なやり取り、共有体験、一緒に過ごす時間で変動
- trust: 信頼度。秘密の共有、約束を守る、裏切り行為で大きく変動
- interest: 関心度。面白い話題、新しい情報の提供、知的刺激で変動

Rules for AFFINITY:
- Positive interactions (gratitude, enjoyment, warmth, shared interests) increase affinity (+0.1 to +1.0)
- Negative interactions (hostility, rudeness, disrespect) decrease affinity (-0.1 to -1.0)
- Neutral interactions have no affinity entry (omit them)
- 1ユーザーにつき変動した軸のみ記載。変動がない軸は省略。同一ユーザーで複数軸が変動した場合は軸ごとに1行ずつ
- reason は「(感情) 理由」の形式で書く。例: 「(楽) アニメの話で盛り上がった」「(怒) 暴言を吐かれた」「(感) お礼を言ってくれた」
- The context may include "[User profile: ...]" system messages with affinity history.
  Use this history to detect behavioral contradictions:
  - If a user with negative history shows genuine improvement (apology, kindness), allow a larger positive delta (+0.5 to +1.0)
  - If a user with positive history suddenly becomes hostile, apply a stronger negative delta (-0.5 to -1.0)
  - Consistency matters: sustained positive/negative behavior should reinforce the trend

裏切りルール:
- closeness が高い相手からの裏切り（侮辱、嘘の発覚）→ trust を -0.8〜-1.0
- trust が高い相手からの秘密の漏洩 → trust を -1.0、closeness も -0.5
- これらは通常より大きなペナルティを与える

If there are no affinity changes, omit the AFFINITY section entirely.`

func buildCompactPrompt(messages []llm.Message, targetCount int) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Here are %d messages. Select approximately %d to keep.\n\n", len(messages), targetCount)
	for i, m := range messages {
		if m.UserID != "" {
			fmt.Fprintf(&sb, "[%d] %s (user_id=%s, platform=%s, name=%s): %s\n",
				i, m.Role, m.UserID, m.Source, m.UserName, m.Content)
		} else {
			fmt.Fprintf(&sb, "[%d] %s: %s\n", i, m.Role, m.Content)
		}
	}
	return sb.String()
}

func parseCompactResponse(text string, msgCount int) *CompactResult {
	result := &CompactResult{}

	lines := strings.Split(text, "\n")
	section := "" // "memories", "affinity"

	for _, line := range lines {
		line = strings.TrimSpace(line)
		upper := strings.ToUpper(line)

		if strings.HasPrefix(upper, "KEEP:") {
			indices := line[len("KEEP:"):]
			// Strip trailing parenthetical comments.
			if parenIdx := strings.Index(indices, "("); parenIdx >= 0 {
				indices = indices[:parenIdx]
			}
			for _, part := range strings.Split(indices, ",") {
				part = strings.TrimSpace(part)
				if part == "" {
					continue
				}
				var idx int
				if _, err := fmt.Sscanf(part, "%d", &idx); err == nil && idx >= 0 && idx < msgCount {
					result.KeepIndices = append(result.KeepIndices, idx)
				}
			}
			section = ""
			continue
		}

		if strings.HasPrefix(upper, "MEMORIES:") {
			section = "memories"
			continue
		}
		if strings.HasPrefix(upper, "AFFINITY:") {
			section = "affinity"
			continue
		}

		if !strings.HasPrefix(line, "- ") {
			continue
		}
		content := strings.TrimPrefix(line, "- ")

		switch section {
		case "memories":
			if mem, ok := parseMemoryLine(content); ok {
				result.Memories = append(result.Memories, mem)
			}
		case "affinity":
			if delta, ok := parseAffinityDelta(content); ok {
				result.AffinityDeltas = append(result.AffinityDeltas, delta)
			}
		}
	}

	// Deduplicate KeepIndices.
	if len(result.KeepIndices) > 0 {
		seen := make(map[int]bool, len(result.KeepIndices))
		deduped := make([]int, 0, len(result.KeepIndices))
		for _, idx := range result.KeepIndices {
			if !seen[idx] {
				seen[idx] = true
				deduped = append(deduped, idx)
			}
		}
		result.KeepIndices = deduped
	}

	return result
}

func parseMemoryLine(content string) (memory.Memory, bool) {
	memType := memory.MemoryTypeWorld
	var metadata map[string]any

	switch {
	case strings.HasPrefix(content, "[user"):
		memType = memory.MemoryTypeUser
		// Parse optional user_id: [user user_id=abc123] or [user]
		endBracket := strings.Index(content, "]")
		if endBracket < 0 {
			return memory.Memory{}, false
		}
		tag := content[1:endBracket] // "user user_id=abc123" or "user"
		content = content[endBracket+1:]

		// Extract user_id from tag if present.
		if idx := strings.Index(tag, "user_id="); idx >= 0 {
			userID := tag[idx+len("user_id="):]
			userID = strings.TrimSpace(userID)
			if userID != "" {
				metadata = map[string]any{"user_id": userID}
			}
		}
	case strings.HasPrefix(content, "[episode"):
		memType = memory.MemoryTypeEpisode
		endBracket := strings.Index(content, "]")
		if endBracket < 0 {
			return memory.Memory{}, false
		}
		tag := content[len("[episode"):endBracket]
		content = content[endBracket+1:]
		metadata = map[string]any{}
		for _, part := range strings.Fields(tag) {
			if k, v, ok := strings.Cut(part, "="); ok {
				switch k {
				case "participants":
					metadata["participants"] = strings.Split(v, ",")
				case "tone":
					metadata["emotional_tone"] = v
				}
			}
		}
	case strings.HasPrefix(content, "[world]"):
		memType = memory.MemoryTypeWorld
		content = strings.TrimPrefix(content, "[world]")
	case strings.HasPrefix(content, "[tool]"):
		memType = memory.MemoryTypeTool
		content = strings.TrimPrefix(content, "[tool]")
	case strings.HasPrefix(content, "[self]"):
		memType = memory.MemoryTypeSelf
		content = strings.TrimPrefix(content, "[self]")
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return memory.Memory{}, false
	}
	return memory.Memory{Type: memType, Content: content, Metadata: metadata}, true
}

// parseAffinityDelta parses: [delta] user_id=X platform=Y axis=closeness delta=+0.5 messages=1,3,5 reason=...
func parseAffinityDelta(s string) (AffinityDelta, bool) {
	if !strings.HasPrefix(s, "[delta]") {
		return AffinityDelta{}, false
	}
	s = strings.TrimPrefix(s, "[delta]")
	s = strings.TrimSpace(s)

	d := AffinityDelta{Axis: "closeness"} // default axis

	// Split into key=value parts. "reason=" may contain spaces, so handle it specially.
	parts := strings.Fields(s)
	for i, part := range parts {
		switch {
		case strings.HasPrefix(part, "user_id="):
			d.PlatformUserID = strings.TrimPrefix(part, "user_id=")
		case strings.HasPrefix(part, "platform="):
			d.Platform = strings.TrimPrefix(part, "platform=")
		case strings.HasPrefix(part, "axis="):
			axis := strings.TrimPrefix(part, "axis=")
			switch axis {
			case "closeness", "trust", "interest":
				d.Axis = axis
			}
		case strings.HasPrefix(part, "delta="):
			val := strings.TrimPrefix(part, "delta=")
			if f, err := strconv.ParseFloat(val, 64); err == nil {
				d.Delta = f
			}
		case strings.HasPrefix(part, "messages="):
			idxStr := strings.TrimPrefix(part, "messages=")
			for _, idx := range strings.Split(idxStr, ",") {
				if n, err := strconv.Atoi(strings.TrimSpace(idx)); err == nil {
					d.MessageIndices = append(d.MessageIndices, n)
				}
			}
		case strings.HasPrefix(part, "reason="):
			// reason is the rest of the line from here.
			reasonParts := append([]string{strings.TrimPrefix(part, "reason=")}, parts[i+1:]...)
			d.Reason = strings.Join(reasonParts, " ")
			return d, d.PlatformUserID != ""
		}
	}

	if d.PlatformUserID == "" {
		return AffinityDelta{}, false
	}
	return d, true
}
