package embedding

import (
	"context"
	"fmt"
	"strings"
)

// TextEmbedClient is the interface for text-only embedding API clients.
// llm.Client satisfies this via its Embed method.
type TextEmbedClient interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// TextOnlyEmbedder adapts a TextEmbedClient to the Embedder interface.
// It extracts text from parts and delegates to the underlying client.
// Non-text parts are silently ignored.
type TextOnlyEmbedder struct {
	client TextEmbedClient
	dims   int
}

// NewTextOnlyEmbedder creates an Embedder wrapping a text-only client.
func NewTextOnlyEmbedder(client TextEmbedClient, dims int) *TextOnlyEmbedder {
	return &TextOnlyEmbedder{client: client, dims: dims}
}

func (e *TextOnlyEmbedder) Embed(ctx context.Context, parts []Part) ([]float32, error) {
	var texts []string
	for _, p := range parts {
		if p.Modality == ModalityText && len(p.Data) > 0 {
			texts = append(texts, string(p.Data))
		}
	}
	if len(texts) == 0 {
		return nil, fmt.Errorf("embedding: テキストパートがありません")
	}
	return e.client.Embed(ctx, strings.Join(texts, "\n"))
}

// EmbedBatch falls back to sequential Embed calls for text-only providers.
func (e *TextOnlyEmbedder) EmbedBatch(ctx context.Context, inputs [][]Part) ([][]float32, error) {
	results := make([][]float32, len(inputs))
	for i, parts := range inputs {
		vec, err := e.Embed(ctx, parts)
		if err != nil {
			return nil, fmt.Errorf("embedding: バッチの%d番目で失敗: %w", i, err)
		}
		results[i] = vec
	}
	return results, nil
}

func (e *TextOnlyEmbedder) Dimensions() int        { return e.dims }
func (e *TextOnlyEmbedder) Modalities() []Modality { return []Modality{ModalityText} }

var _ Embedder = (*TextOnlyEmbedder)(nil)
