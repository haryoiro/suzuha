package research

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Article holds a summary of a Wikipedia article.
type Article struct {
	Title   string
	Extract string
	URL     string
}

var wikiHTTP = &http.Client{Timeout: 10 * time.Second}

// RandomArticle fetches a random article summary from Japanese Wikipedia.
func RandomArticle(ctx context.Context) (*Article, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://ja.wikipedia.org/api/rest_v1/page/random/summary", nil)
	if err != nil {
		return nil, fmt.Errorf("wikipedia: リクエストの作成に失敗: %w", err)
	}
	req.Header.Set("User-Agent", "suzuha-bot/1.0")

	resp, err := wikiHTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("wikipedia: 取得に失敗: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("wikipedia: ステータス %d", resp.StatusCode)
	}

	var body struct {
		Title       string `json:"title"`
		Extract     string `json:"extract"`
		ContentURLs struct {
			Desktop struct {
				Page string `json:"page"`
			} `json:"desktop"`
		} `json:"content_urls"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("wikipedia: デコードに失敗: %w", err)
	}

	return &Article{
		Title:   body.Title,
		Extract: body.Extract,
		URL:     body.ContentURLs.Desktop.Page,
	}, nil
}
