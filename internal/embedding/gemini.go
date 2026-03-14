package embedding

import (
	"context"
	"fmt"

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
		return nil, fmt.Errorf("embedding: Gemini API呼び出しに失敗: %w", err)
	}

	if len(resp.Embeddings) == 0 || len(resp.Embeddings[0].Values) == 0 {
		return nil, fmt.Errorf("embedding: 空のレスポンス")
	}

	return resp.Embeddings[0].Values, nil
}

// EmbedBatch uses a single API call for up to maxBatchSize inputs.
// Larger batches are split into chunks.
func (e *GeminiEmbedder) EmbedBatch(ctx context.Context, inputs [][]Part) ([][]float32, error) {
	const maxBatchSize = 100

	results := make([][]float32, len(inputs))
	for start := 0; start < len(inputs); start += maxBatchSize {
		end := start + maxBatchSize
		if end > len(inputs) {
			end = len(inputs)
		}
		chunk := inputs[start:end]

		contents := make([]*genai.Content, len(chunk))
		for i, parts := range chunk {
			contents[i] = partsToContent(parts)
		}

		cfg := &genai.EmbedContentConfig{}
		if e.dims > 0 {
			dim := int32(e.dims)
			cfg.OutputDimensionality = &dim
		}

		resp, err := e.client.Models.EmbedContent(ctx, e.model, contents, cfg)
		if err != nil {
			return nil, fmt.Errorf("embedding: Geminiバッチ呼び出しに失敗: %w", err)
		}

		if len(resp.Embeddings) != len(chunk) {
			return nil, fmt.Errorf("embedding: レスポンス数が不一致 (got %d, want %d)", len(resp.Embeddings), len(chunk))
		}

		for i, emb := range resp.Embeddings {
			results[start+i] = emb.Values
		}
	}
	return results, nil
}

func (e *GeminiEmbedder) Dimensions() int        { return e.dims }
func (e *GeminiEmbedder) Modalities() []Modality  { return []Modality{ModalityText, ModalityImage} }

// partsToContent converts embedding Parts to a genai.Content.
func partsToContent(parts []Part) *genai.Content {
	content := &genai.Content{Role: "user"}
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
