package llm

import (
	"context"
	"fmt"
	"time"

	"github.com/mozilla-ai/any-llm-go/providers"
)

// HasVisionCapability returns whether vision is available and whether it's inline.
// Satisfies vision.VisionDescriber interface.
func (c *Client) HasVisionCapability() (available bool, inline bool) {
	rc, inl := c.WithCapability("conversation", "vision")
	return rc != nil, inl
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
