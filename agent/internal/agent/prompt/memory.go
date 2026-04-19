package prompt

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"strings"

	"github.com/haryoiro/suzuha/external/embedding"
	"github.com/haryoiro/suzuha/internal/lib/jtime"
	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/haryoiro/suzuha/internal/memory"
)

func parseDataURI(uri string) ([]byte, string) {
	if !strings.HasPrefix(uri, "data:") {
		return nil, ""
	}
	commaIdx := strings.Index(uri, ",")
	if commaIdx < 0 {
		return nil, ""
	}
	header := uri[5:commaIdx]
	mime := strings.TrimSuffix(header, ";base64")
	data, err := base64.StdEncoding.DecodeString(uri[commaIdx+1:])
	if err != nil {
		return nil, ""
	}
	return data, mime
}

func modalityFromMime(mime string) embedding.Modality {
	switch {
	case strings.HasPrefix(mime, "image/"):
		return embedding.ModalityImage
	case strings.HasPrefix(mime, "audio/"):
		return embedding.ModalityAudio
	default:
		return embedding.ModalityText
	}
}

type MemoryProvider struct {
	Memory memory.Store
	Media  memory.MediaStore
	Logger *slog.Logger
}

func (p *MemoryProvider) ProvideContext(ctx context.Context, req Request) Block {
	if p.Memory == nil {
		return Block{}
	}

	filter := memory.SymbolicFilter{}
	for _, pt := range req.Participants {
		filter.PersonIDs = append(filter.PersonIDs, pt.UserID)
	}

	memories, err := p.Memory.SearchWithContext(ctx, req.Query, 5, filter)
	if err != nil {
		p.Logger.Debug("思い出せなかった", "error", err)
	}

	for _, dataURI := range req.ImageURLs {
		data, mime := parseDataURI(dataURI)
		if data == nil {
			continue
		}
		parts := []embedding.Part{{
			Modality: modalityFromMime(mime),
			Data:     data,
			MimeType: mime,
		}}
		mediaResults, err := p.Memory.SearchByParts(ctx, parts, 5)
		if err != nil {
			p.Logger.Debug("画像の記憶を探せなかった", "error", err)
			continue
		}
		seen := make(map[string]bool, len(memories))
		for _, m := range memories {
			seen[m.ID] = true
		}
		for _, m := range mediaResults {
			if !seen[m.ID] {
				memories = append(memories, m)
				seen[m.ID] = true
			}
		}
	}

	if len(memories) == 0 {
		return Block{}
	}

	var textParts []string
	for _, m := range memories {
		label := string(m.Type)
		if len(m.Persons) > 0 {
			label += " persons=" + strings.Join(m.Persons, ",")
		}
		if m.Topic != "" {
			label += " topic=" + m.Topic
		}
		if m.Metadata != nil {
			if tone, ok := m.Metadata["emotional_tone"].(string); ok && tone != "" {
				label += " tone=" + tone
			}
		}
		date := jtime.In(m.CreatedAt).Format("2006-01-02")
		if m.EventTime != nil {
			date = jtime.In(*m.EventTime).Format("2006-01-02")
		}
		textParts = append(textParts, fmt.Sprintf("- [%s] %s (%s)", label, m.Content, date))
	}
	textContent := "[関連する記憶]\n" + strings.Join(textParts, "\n")

	var attachedImages []string
	if p.Media != nil {
		for _, m := range memories {
			for _, att := range m.Attachments {
				if att.Modality != "image" {
					continue
				}
				data, err := p.Media.Get(ctx, att.Key)
				if err != nil {
					p.Logger.Debug("記憶の画像を読めなかった", "key", att.Key, "error", err)
					continue
				}
				attachedImages = append(attachedImages, fmt.Sprintf("data:%s;base64,%s",
					att.MimeType, base64.StdEncoding.EncodeToString(data)))
			}
		}
	}

	if len(attachedImages) > 0 {
		return Block{Background: []llm.Message{{
			Role:      "user",
			Content:   "[記憶 (画像付き)]\n" + textContent,
			ImageURLs: attachedImages,
			Timestamp: jtime.Now(),
		}}}
	}

	return Block{Background: []llm.Message{{
		Role:      "system",
		Content:   textContent,
		Timestamp: jtime.Now(),
	}}}
}
