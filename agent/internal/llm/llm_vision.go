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

	// Convert float64 (API response) to float32 (sqlite-vec storage).
	f64 := resp.Data[0].Embedding
	result := make([]float32, len(f64))
	for i, v := range f64 {
		result[i] = float32(v)
	}

	c.logger.Debug("埋め込み完了", "model", c.embeddingModel, "dims", len(result), "elapsed_ms", elapsed.Milliseconds())
	return result, nil
}

// HasVisionCapability returns whether vision is available and whether it's inline.
// Satisfies vision.VisionDescriber interface.
func (c *Client) HasVisionCapability() (available bool, inline bool) {
	rc, inl := c.WithCapability("conversation", "vision")
	return rc != nil, inl
}

// HasVision returns true if vision is available.
// 後方互換シム。
func (c *Client) HasVision() bool {
	avail, _ := c.HasVisionCapability()
	return avail
}

// IsVisionCapable returns true if the active conversation LLM provider supports vision natively.
// 後方互換シム。
func (c *Client) IsVisionCapable() bool {
	_, inline := c.HasVisionCapability()
	return inline
}

// DescribeImage sends an image URL to a vision model and returns a text description.
// 後方互換シム: WithCapability("conversation", "vision") を使用する。
func (c *Client) DescribeImage(ctx context.Context, imageURL string, prompt ...string) (string, error) {
	rc, _ := c.WithCapability("conversation", "vision")
	if rc == nil {
		return "", fmt.Errorf("llm: ビジョンモデルが設定されていません")
	}
	rp := rc.resolve()
	if rp.provider == nil {
		return "", fmt.Errorf("llm: ビジョンモデルが設定されていません")
	}
	prov := rp.provider
	model := rp.model

	textPrompt := "この画像の内容を簡潔に描写してください。"
	if len(prompt) > 0 && prompt[0] != "" {
		textPrompt = prompt[0]
	}

	params := providers.CompletionParams{
		Model: model,
		Messages: []providers.Message{
			{
				Role: "user",
				Content: []providers.ContentPart{
					{Type: "text", Text: textPrompt},
					{Type: "image_url", ImageURL: &providers.ImageURL{URL: imageURL}},
				},
			},
		},
	}

	c.logger.Debug("ビジョンリクエスト", "model", model, "url", imageURL)

	var resp *providers.ChatCompletion
	start := time.Now()

	err := retryOnRateLimit(ctx, c.logger, func() error {
		var callErr error
		resp, callErr = prov.Completion(ctx, params)
		return callErr
	})
	elapsed := time.Since(start)

	if err != nil {
		c.logger.Error("ビジョン補完に失敗しました", "model", model, "elapsed_ms", elapsed.Milliseconds(), "error", err)
		return "", fmt.Errorf("llm: ビジョンに失敗: %w", err)
	}

	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("llm: ビジョン: 空のレスポンス")
	}

	text := resp.Choices[0].Message.ContentString()
	c.logger.Info("ビジョン補完完了", "model", model, "elapsed_ms", elapsed.Milliseconds(), "description_length", len(text))
	return text, nil
}
