package acquire

import (
	"context"
	"fmt"
	"log/slog"
	"path"
	"strings"
	"time"

	"github.com/haryoiro/suzuha/internal/adapter/store/memory"
	"github.com/haryoiro/suzuha/internal/domain/message"
	"github.com/haryoiro/suzuha/internal/lib/textutil"
	portllm "github.com/haryoiro/suzuha/internal/port/llm"
	portmem "github.com/haryoiro/suzuha/internal/port/memory"
)

// Acquirer は会話コンテキストから長期メモリを抽出する。
type Acquirer struct {
	llm    portllm.Completer
	store  memory.Store
	config Config
	logger *slog.Logger
}

// NewAcquirer は Acquirer を作成する。
func NewAcquirer(llm portllm.Completer, store memory.Store, cfg Config, logger *slog.Logger) *Acquirer {
	return &Acquirer{llm: llm, store: store, config: cfg, logger: logger}
}

// Acquire は指定されたメッセージから長期メモリを抽出する。
func (a *Acquirer) Acquire(ctx context.Context, req *portmem.AcquireRequest) (*portmem.AcquireResult, error) {
	if len(req.Messages) == 0 {
		return &portmem.AcquireResult{}, nil
	}

	memories, err := a.extract(ctx, req.Messages)
	if err != nil {
		return nil, fmt.Errorf("acquire: 抽出に失敗: %w", err)
	}

	candidates := make([]memory.DupCandidate, len(memories))
	for i, mem := range memories {
		candidates[i] = memory.DupCandidate{Content: mem.Content, Type: mem.Type}
	}
	dupResults, dupErr := a.store.IsDuplicateBatch(ctx, candidates)
	if dupErr != nil {
		a.logger.Warn("acquire: バッチ重複チェックに失敗", "error", dupErr)
	}

	result := &portmem.AcquireResult{}
	for i := range memories {
		mem := &memories[i]
		if dupResults != nil && dupResults[i].DupID != "" {
			a.logger.Debug("acquire: 重複メモリをスキップ", "existing_id", dupResults[i].DupID, "content", mem.Content)
			continue
		}
		if dupResults != nil && len(dupResults[i].Embedding) > 0 {
			mem.Embedding = dupResults[i].Embedding
		}
		if err := a.store.Save(ctx, mem); err != nil {
			a.logger.Warn("acquire: メモリの保存に失敗", "error", err)
		}
		result.Memories = append(result.Memories, *mem)
	}

	return result, nil
}

func (a *Acquirer) extract(ctx context.Context, msgs []message.Message) ([]memory.Memory, error) {
	existing := a.fetchRecentMemories(ctx)
	systemPrompt := buildSystemPrompt(a.config.Rules)
	userPrompt := buildCompactPrompt(msgs, existing)

	resp, err := a.llm.CompleteRaw(ctx, []portllm.RawMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	})
	if err != nil {
		return nil, fmt.Errorf("acquire: LLM呼び出しに失敗: %w", err)
	}

	memories, err := parseExtractedMemories(resp.Text)
	if err != nil {
		a.logger.Error("acquire: JSON解析に失敗、レガシーフォールバック発動（構造化フィールド欠落）",
			"error", err, "response_prefix", textutil.TruncateRunes(resp.Text, 100))
		result := parseLegacyCompactResponse(resp.Text)
		memories = result.Memories
	}

	mediaByIndex := collectMediaKeysByIndex(msgs)
	for i := range memories {
		attachMediaByIndices(&memories[i], mediaByIndex)
	}

	return memories, nil
}

func (a *Acquirer) fetchRecentMemories(ctx context.Context) []memory.Memory {
	cfg := a.config
	if cfg.RecentMemoryLimit <= 0 {
		return nil
	}
	since := time.Now().Add(-cfg.RecentMemoryWindow)
	mems, err := a.store.ListRecent(ctx, since, cfg.RecentMemoryLimit)
	if err != nil {
		a.logger.Debug("acquire: 既存メモリの取得に失敗", "error", err)
		return nil
	}
	return mems
}

func collectMediaKeysByIndex(msgs []message.Message) map[int][]string {
	result := make(map[int][]string)
	for i, m := range msgs {
		if len(m.MediaKeys) > 0 {
			result[i] = m.MediaKeys
		}
	}
	return result
}

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

func parseLegacyCompactResponse(text string) *portmem.AcquireResult {
	result := &portmem.AcquireResult{}
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
