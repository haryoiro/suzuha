package embedding

import (
	"context"
	"fmt"
	"os"

	"google.golang.org/genai"
)

// GeminiEmbedder implements Embedder using the Google Gemini embedding API.
// Supports text and image parts natively via the genai SDK.
type GeminiEmbedder struct {
	client *genai.Client
	model  string
	dims   int
}

// NewGeminiEmbedder creates a multimodal Embedder using the Gemini API.
func NewGeminiEmbedder(apiKey, model string, dims int) (*GeminiEmbedder, error) {
	client, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("embedding: Geminiクライアントの初期化に失敗: %w", err)
	}
	return &GeminiEmbedder{
		client: client,
		model:  model,
		dims:   dims,
	}, nil
}

func (e *GeminiEmbedder) Embed(ctx context.Context, parts []Part) ([]float32, error) {
	if len(parts) == 0 {
		return nil, fmt.Errorf("embedding: パートが空です")
	}

	content := partsToContent(parts)
	if len(content.Parts) == 0 {
		return nil, fmt.Errorf("embedding: 対応するパートがありません")
	}

	cfg := &genai.EmbedContentConfig{}
	if e.dims > 0 {
		dim := int32(e.dims)
		cfg.OutputDimensionality = &dim
	}

	resp, err := e.client.Models.EmbedContent(ctx, e.model, []*genai.Content{content}, cfg)
	if err != nil {
		// If multimodal input failed, retry with text-only parts.
		hasNonText := false
		var textParts []Part
		for _, p := range parts {
			if p.Modality == ModalityText {
				textParts = append(textParts, p)
			} else {
				hasNonText = true
			}
		}
		if hasNonText && len(textParts) > 0 {
			fmt.Fprintf(os.Stderr, "embedding: multimodal failed, retrying text-only: %v\n", err)
			return e.Embed(ctx, textParts)
		}
		return nil, fmt.Errorf("embedding: Gemini API呼び出しに失敗: %w", err)
	}

	if len(resp.Embeddings) == 0 || len(resp.Embeddings[0].Values) == 0 {
		return nil, fmt.Errorf("embedding: 空のレスポンス")
	}

	return resp.Embeddings[0].Values, nil
}

// EmbedBatch embeds multiple inputs by calling Embed sequentially.
// gemini-embedding-2-preview does not support batchEmbedContents,
// so we fall back to individual embedContent calls.
// Individual failures are skipped (result is nil) rather than aborting the batch.
func (e *GeminiEmbedder) EmbedBatch(ctx context.Context, inputs [][]Part) ([][]float32, error) {
	results := make([][]float32, len(inputs))
	for i, parts := range inputs {
		vec, err := e.Embed(ctx, parts)
		if err != nil {
			// Skip this entry; leave results[i] nil.
			fmt.Fprintf(os.Stderr, "embedding: batch[%d/%d] skipped: %v\n", i, len(inputs), err)
			continue
		}
		results[i] = vec
	}
	return results, nil
}

func (e *GeminiEmbedder) Dimensions() int        { return e.dims }
func (e *GeminiEmbedder) Modalities() []Modality  { return []Modality{ModalityText, ModalityImage} }

// partsToContent converts embedding Parts to a genai.Content.
// Role is intentionally left empty — the embedding API does not require it,
// and setting it can cause INVALID_ARGUMENT in batch calls.
func partsToContent(parts []Part) *genai.Content {
	content := &genai.Content{}
	for _, p := range parts {
		switch p.Modality {
		case ModalityText:
			content.Parts = append(content.Parts, &genai.Part{
				Text: string(p.Data),
			})
		case ModalityImage:
			content.Parts = append(content.Parts, &genai.Part{
				InlineData: &genai.Blob{
					MIMEType: p.MimeType,
					Data:     p.Data,
				},
			})
		}
	}
	return content
}

var _ Embedder = (*GeminiEmbedder)(nil)
