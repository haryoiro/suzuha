package consolidator

import (
	"context"
	"fmt"
	"log/slog"
	"path"
	"strings"

	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/haryoiro/suzuha/internal/memory"
	"github.com/mozilla-ai/any-llm-go/providers"
)

// collectMediaKeysByIndex maps message indices to their media keys.
func collectMediaKeysByIndex(msgs []llm.Message) map[int][]string {
	result := make(map[int][]string)
	for i, m := range msgs {
		if len(m.MediaKeys) > 0 {
			result[i] = m.MediaKeys
		}
	}
	return result
}

// attachMediaByIndices links media keys to a memory using image_indices from LLM output.
func attachMediaByIndices(mem *memory.Memory, mediaByIndex map[int][]string) {
	if mem.Metadata == nil || len(mediaByIndex) == 0 {
		return
	}

	indices, ok := mem.Metadata["image_indices"]
	if !ok {
		return
	}

	// Parse indices (may be []int or []any from JSON).
	var idxList []int
	switch v := indices.(type) {
	case []int:
		idxList = v
	case []any:
		for _, item := range v {
			switch n := item.(type) {
			case int:
				idxList = append(idxList, n)
			case float64:
				idxList = append(idxList, int(n))
			}
		}
	}

	// Remove image_indices from metadata (internal use only).
	delete(mem.Metadata, "image_indices")

	seen := make(map[string]bool)
	for _, idx := range idxList {
		for _, key := range mediaByIndex[idx] {
			if !seen[key] {
				seen[key] = true
				mem.Attachments = append(mem.Attachments, memory.Attachment{
					Key:      key,
					Modality: modalityFromKey(key),
					MimeType: mimeFromKey(key),
				})
			}
		}
	}
}

func modalityFromKey(key string) string {
	ext := strings.ToLower(path.Ext(key))
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return "image"
	case ".wav", ".mp3", ".ogg", ".webm":
		return "audio"
	default:
		return "image"
	}
}

func mimeFromKey(key string) string {
	ext := strings.ToLower(path.Ext(key))
	switch ext {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".wav":
		return "audio/wav"
	case ".mp3":
		return "audio/mpeg"
	default:
		return "application/octet-stream"
	}
}

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

	// Build per-message-index media key map.
	mediaByIndex := collectMediaKeysByIndex(req.Messages)

	// Attach media to all candidates first.
	for i := range result.Memories {
		attachMediaByIndices(&result.Memories[i], mediaByIndex)
	}

	// Batch dedup check — single embedding API call for all candidates.
	candidates := make([]memory.DupCandidate, len(result.Memories))
	for i, mem := range result.Memories {
		candidates[i] = memory.DupCandidate{Content: mem.Content, Type: mem.Type}
	}
	dupResults, dupErr := s.store.IsDuplicateBatch(ctx, candidates)
	if dupErr != nil {
		s.logger.Warn("consolidator: バッチ重複チェックに失敗", "error", dupErr)
	}

	// Save non-duplicate memories, reusing embeddings from the dedup check.
	for i := range result.Memories {
		mem := &result.Memories[i]
		if dupResults != nil && dupResults[i].DupID != "" {
			s.logger.Debug("consolidator: 重複メモリをスキップ", "existing_id", dupResults[i].DupID, "content", mem.Content)
			continue
		}
		if dupResults != nil && len(dupResults[i].Embedding) > 0 {
			mem.Embedding = dupResults[i].Embedding
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

IMPORTANT: Write all MEMORIES content in Japanese (日本語). The conversation is in Japanese, and memories must also be in Japanese.

Respond in this exact format:

KEEP: 0,2,5,7 (comma-separated message indices to keep)

MEMORIES:
- [user user_id=<platform_user_id>] そのユーザーに関する情報や好み
- [world] 会話で出た一般的な知識や事実
- [tool] ツールの使用パターンや覚えておくべき結果
- [episode participants=<id1>,<id2> tone=<感情> images=<comma-separated message indices with images>] 出来事の要約
- [self] 自分自身について新しく気づいたこと、確認できたこと

[episode] の使い分け:
- 複数人が関わった会話イベント（盛り上がった話題、一緒に何かした体験）→ episode
- 個人の属性・好み → user
- 出来事のコンテンツには参加者のIDも含めること（検索用）
- 例: [episode participants=123,456 tone=楽しい images=3,7] 123と456がアニメの話で盛り上がった
- images= にはそのエピソードに関連する画像付きメッセージのインデックスを指定（[IMG]マーカー付きのもの）。関連する画像がなければ省略

[self] の使い分け:
- 自分（のの）の行動パターンに新しい発見があった時だけ
- 例: 「プログラミングの話になると饒舌になる」「朝は機嫌が悪い」「○○の話題が苦手」
- 毎回出す必要はない。本当に新しい自己認識があった時だけ

IMPORTANT: For [user] memories, always include the user_id of the person the fact is about.
The user_id can be found in message metadata (user_id=... in the message header).
If the fact is about a user whose user_id is not clear, omit the user_id.`

func buildCompactPrompt(messages []llm.Message, targetCount int) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Here are %d messages. Select approximately %d to keep.\n\n", len(messages), targetCount)
	for i, m := range messages {
		imgTag := ""
		if len(m.MediaKeys) > 0 || len(m.ImageURLs) > 0 {
			imgTag = " [IMG]"
		}
		if m.UserID != "" {
			fmt.Fprintf(&sb, "[%d]%s %s (user_id=%s, platform=%s, name=%s): %s\n",
				i, imgTag, m.Role, m.UserID, m.Source, m.UserName, m.Content)
		} else {
			fmt.Fprintf(&sb, "[%d]%s %s: %s\n", i, imgTag, m.Role, m.Content)
		}
	}
	return sb.String()
}

func parseCompactResponse(text string, msgCount int) *CompactResult {
	result := &CompactResult{}

	lines := strings.Split(text, "\n")
	section := "" // "memories"

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

		if !strings.HasPrefix(line, "- ") {
			continue
		}
		content := strings.TrimPrefix(line, "- ")

		if section == "memories" {
			if mem, ok := parseMemoryLine(content); ok {
				result.Memories = append(result.Memories, mem)
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
				case "images":
					// Parse message indices that have images.
					var indices []int
					for _, s := range strings.Split(v, ",") {
						s = strings.TrimSpace(s)
						var idx int
						if _, err := fmt.Sscanf(s, "%d", &idx); err == nil {
							indices = append(indices, idx)
						}
					}
					if len(indices) > 0 {
						metadata["image_indices"] = indices
					}
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

