package memento

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/haryoiro/suzuha/internal/memory"
)

// extractedMemory はLLMが返すJSON構造体。
type extractedMemory struct {
	Type          string   `json:"type"`
	Content       string   `json:"content"`
	Keywords      []string `json:"keywords"`
	Topic         string   `json:"topic"`
	Persons       []string `json:"persons"`
	EventTime     *string  `json:"event_time"`
	EmotionalTone string   `json:"emotional_tone,omitempty"`
	ImageIndices  []int    `json:"image_indices,omitempty"`
}

// parseExtractedMemories はLLMのJSON出力をMemoryオブジェクトにパースする。
func parseExtractedMemories(raw string) ([]memory.Memory, error) {
	raw = stripJSONFence(raw)

	var items []extractedMemory
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return nil, fmt.Errorf("acquire: JSON解析に失敗: %w (raw: %s)", err, truncate(raw, 200))
	}

	var memories []memory.Memory
	for _, item := range items {
		if item.Content == "" {
			continue
		}

		mem := memory.Memory{
			Type:     toMemoryType(item.Type),
			Content:  strings.TrimSpace(item.Content),
			Keywords: item.Keywords,
			Topic:    item.Topic,
			Persons:  item.Persons,
		}

		// event_time が指定されていればパースする。
		if item.EventTime != nil && *item.EventTime != "" {
			if t, err := time.Parse(time.RFC3339, *item.EventTime); err == nil {
				mem.EventTime = &t
			}
		}

		// タイプ固有のメタデータを格納する。
		meta := make(map[string]any)

		// エピソード用の emotional_tone をメタデータに保持する。
		if item.EmotionalTone != "" {
			meta["emotional_tone"] = item.EmotionalTone
		}

		// メディア添付用の image_indices を格納する（acquirer.go で使用）。
		if len(item.ImageIndices) > 0 {
			meta["image_indices"] = item.ImageIndices
		}

		if len(meta) > 0 {
			mem.Metadata = meta
		}

		memories = append(memories, mem)
	}
	return memories, nil
}

func toMemoryType(s string) memory.MemoryType {
	switch s {
	case "user":
		return memory.MemoryTypeUser
	case "world":
		return memory.MemoryTypeWorld
	case "tool":
		return memory.MemoryTypeTool
	case "episode":
		return memory.MemoryTypeEpisode
	case "self":
		return memory.MemoryTypeSelf
	default:
		return memory.MemoryTypeWorld
	}
}
