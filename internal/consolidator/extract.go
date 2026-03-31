package consolidator

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/haryoiro/suzuha/internal/memory"
)

// extract は完全な抽出パイプラインを実行する: コンテキスト取得 → プロンプト → LLM → パース → メディア添付。
func (s *Server) extract(ctx context.Context, msgs []llm.Message) ([]memory.Memory, error) {
	// 重複排除コンテキスト用に最近の既存メモリを取得する。
	existing := s.fetchRecentMemories(ctx)

	// プロンプトを構築する。
	systemPrompt := buildSystemPrompt(s.config.Extraction.Rules)
	userPrompt := buildCompactPrompt(msgs, existing)

	// LLMを呼び出す。
	resp, err := s.llmClient.CompleteRawDefault(ctx, []llm.RawMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	})
	if err != nil {
		return nil, fmt.Errorf("consolidator: LLM呼び出しに失敗: %w", err)
	}

	// JSON出力をパースする。
	memories, err := parseExtractedMemories(resp.Text)
	if err != nil {
		// レガシーフォールバック: 構造化フィールド（Keywords/Topic/Persons/EventTime）は全て欠落する。
		// このパスの発動頻度が高い場合はプロンプトの見直しが必要。
		s.logger.Error("consolidator: JSON解析に失敗、レガシーフォールバック発動（構造化フィールド欠落）",
			"error", err, "response_prefix", truncate(resp.Text, 100))
		result := parseLegacyCompactResponse(resp.Text)
		memories = result.Memories
	}

	// インデックスに基づいてメディアを添付する。
	mediaByIndex := collectMediaKeysByIndex(msgs)
	for i := range memories {
		attachMediaByIndices(&memories[i], mediaByIndex)
	}

	return memories, nil
}

// fetchRecentMemories は重複排除コンテキスト用にストアから最近のメモリを取得する。
func (s *Server) fetchRecentMemories(ctx context.Context) []memory.Memory {
	cfg := s.config.Extraction
	if cfg.RecentMemoryLimit <= 0 {
		return nil
	}

	since := time.Now().Add(-cfg.RecentMemoryWindow)
	mems, err := s.store.ListRecent(ctx, since, cfg.RecentMemoryLimit)
	if err != nil {
		s.logger.Debug("consolidator: 既存メモリの取得に失敗", "error", err)
		return nil
	}
	return mems
}

// collectMediaKeysByIndex はメッセージインデックスをメディアキーにマッピングする。
func collectMediaKeysByIndex(msgs []llm.Message) map[int][]string {
	result := make(map[int][]string)
	for i, m := range msgs {
		if len(m.MediaKeys) > 0 {
			result[i] = m.MediaKeys
		}
	}
	return result
}

// attachMediaByIndices はLLM出力の image_indices を使ってメディアキーをメモリに紐付ける。
func attachMediaByIndices(mem *memory.Memory, mediaByIndex map[int][]string) {
	if mem.Metadata == nil || len(mediaByIndex) == 0 {
		return
	}

	indices, ok := mem.Metadata["image_indices"]
	if !ok {
		return
	}

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

// parseLegacyCompactResponse はフォールバックとして旧テキスト形式をパースする。
func parseLegacyCompactResponse(text string) *CompactResult {
	result := &CompactResult{}

	lines := strings.Split(text, "\n")
	inMemories := false

	for _, line := range lines {
		line = strings.TrimSpace(line)

		if strings.HasPrefix(strings.ToUpper(line), "MEMORIES:") {
			inMemories = true
			continue
		}

		if !inMemories || !strings.HasPrefix(line, "- ") {
			continue
		}
		content := strings.TrimPrefix(line, "- ")
		if mem, ok := parseLegacyMemoryLine(content); ok {
			result.Memories = append(result.Memories, mem)
		}
	}

	return result
}

func parseLegacyMemoryLine(content string) (memory.Memory, bool) {
	memType := memory.MemoryTypeWorld
	var metadata map[string]any

	switch {
	case strings.HasPrefix(content, "[user"):
		memType = memory.MemoryTypeUser
		endBracket := strings.Index(content, "]")
		if endBracket < 0 {
			return memory.Memory{}, false
		}
		tag := content[1:endBracket]
		content = content[endBracket+1:]
		if idx := strings.Index(tag, "user_id="); idx >= 0 {
			userID := strings.TrimSpace(tag[idx+len("user_id="):])
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
