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

	resp, err := s.llmClient.CompleteRaw(ctx, []providers.Message{
		{Role: "system", Content: compactSystemPrompt},
		{Role: "user", Content: prompt},
	})
	if err != nil {
		return nil, fmt.Errorf("consolidator: compact llm call: %w", err)
	}

	result := parseCompactResponse(resp.Text, len(req.Messages))

	// Save extracted memories to long-term store (skip duplicates).
	for i := range result.Memories {
		mem := &result.Memories[i]
		if dupID, _ := s.store.IsDuplicate(ctx, mem.Content, mem.Type); dupID != "" {
			s.logger.Debug("consolidator: skip duplicate memory", "existing_id", dupID, "content", mem.Content)
			continue
		}
		if err := s.store.Save(ctx, mem); err != nil {
			s.logger.Warn("consolidator: save memory failed", "error", err)
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

[episode] の使い分け:
- 複数人が関わった会話イベント（盛り上がった話題、一緒に何かした体験）→ episode
- 個人の属性・好み → user
- 出来事のコンテンツには参加者のIDも含めること（検索用）
- 例: [episode participants=123,456 tone=楽しい] 123と456がアニメの話で盛り上がった

IMPORTANT: For [user] memories, always include the user_id of the person the fact is about.
The user_id can be found in message metadata (user_id=... in the message header).
If the fact is about a user whose user_id is not clear, omit the user_id.

AFFINITY:
- [delta] user_id=<platform_user_id> platform=<platform> delta=<+/-float> messages=<comma-separated indices> reason=<日本語で簡潔に>

Rules for AFFINITY:
- Positive interactions (gratitude, enjoyment, warmth, shared interests) increase affinity (+0.1 to +1.0)
- Negative interactions (hostility, rudeness, disrespect) decrease affinity (-0.1 to -1.0)
- Neutral interactions have no affinity entry (omit them)
- Group temporally close positive interactions from the same user into a single delta
- The reason should be concise (under 50 chars, in Japanese)
- Each user who participated should have at most one affinity entry
- The context may include "[User profile: ...]" system messages with affinity history.
  Use this history to detect behavioral contradictions:
  - If a user with negative history shows genuine improvement (apology, kindness), allow a larger positive delta (+0.5 to +1.0)
  - If a user with positive history suddenly becomes hostile, apply a stronger negative delta (-0.5 to -1.0)
  - Consistency matters: sustained positive/negative behavior should reinforce the trend

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

		if strings.HasPrefix(line, "KEEP:") {
			indices := strings.TrimPrefix(line, "KEEP:")
			for _, part := range strings.Split(indices, ",") {
				part = strings.TrimSpace(part)
				var idx int
				if _, err := fmt.Sscanf(part, "%d", &idx); err == nil && idx >= 0 && idx < msgCount {
					result.KeepIndices = append(result.KeepIndices, idx)
				}
			}
			section = ""
			continue
		}

		if strings.HasPrefix(line, "MEMORIES:") {
			section = "memories"
			continue
		}
		if strings.HasPrefix(line, "AFFINITY:") {
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
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return memory.Memory{}, false
	}
	return memory.Memory{Type: memType, Content: content, Metadata: metadata}, true
}

// parseAffinityDelta parses: [delta] user_id=X platform=Y delta=+0.5 messages=1,3,5 reason=...
func parseAffinityDelta(s string) (AffinityDelta, bool) {
	if !strings.HasPrefix(s, "[delta]") {
		return AffinityDelta{}, false
	}
	s = strings.TrimPrefix(s, "[delta]")
	s = strings.TrimSpace(s)

	d := AffinityDelta{}

	// Split into key=value parts. "reason=" may contain spaces, so handle it specially.
	parts := strings.Fields(s)
	for i, part := range parts {
		switch {
		case strings.HasPrefix(part, "user_id="):
			d.PlatformUserID = strings.TrimPrefix(part, "user_id=")
		case strings.HasPrefix(part, "platform="):
			d.Platform = strings.TrimPrefix(part, "platform=")
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
