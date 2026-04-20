package llm

import (
	"context"
	"fmt"
	"time"

	"github.com/mozilla-ai/any-llm-go/providers"
)

// Embed generates an embedding vector for the given text.
// Returns nil, nil if no embedding model is configured.
func (c *Client) Embed(ctx context.Context, text string) ([]float32, error) {
	if c.embeddingModel == "" || c.embeddingProv == nil {
		return nil, nil
	}

	ep, ok := c.embeddingProv.(providers.EmbeddingProvider)
	if !ok {
		return nil, fmt.Errorf("llm: 埋め込みプロバイダ %q は埋め込みをサポートしていません", c.embeddingProv.Name())
	}

	params := providers.EmbeddingParams{
		Model: c.embeddingModel,
		Input: text,
	}
	if c.embeddingDims > 0 {
		dims := c.embeddingDims
		params.Dimensions = &dims
	}

	c.logger.Debug("埋め込みリクエスト", "model", c.embeddingModel, "text_length", len(text))

	var resp *providers.EmbeddingResponse
	start := time.Now()

	err := retryOnRateLimit(ctx, c.logger, func() error {
		var callErr error
		resp, callErr = ep.Embedding(ctx, params)
		return callErr
	})
	elapsed := time.Since(start)

	if err != nil {
		c.logger.Error("埋め込みに失敗しました", "model", c.embeddingModel, "elapsed_ms", elapsed.Milliseconds(), "error", err)
		return nil, fmt.Errorf("llm: 埋め込みに失敗: %w", err)
	}

	if len(resp.Data) == 0 {
		c.logger.Warn("埋め込みの空レスポンスを受信しました", "model", c.embeddingModel)
		return nil, fmt.Errorf("llm: 埋め込みの空レスポンス")
	}

	f64 := resp.Data[0].Embedding
	result := make([]float32, len(f64))
	for i, v := range f64 {
		result[i] = float32(v)
	}

	c.logger.Debug("埋め込み完了", "model", c.embeddingModel, "dims", len(result), "elapsed_ms", elapsed.Milliseconds())
	return result, nil
}
