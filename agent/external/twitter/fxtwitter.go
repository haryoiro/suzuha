package twitter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const fxTwitterBase = "https://api.fxtwitter.com"

// FxTwitterFetcher は FxTwitter API で X の投稿を取得する。
// API キー不要、無料。
type FxTwitterFetcher struct {
	client *http.Client
}

// NewFxTwitterFetcher は FxTwitterFetcher を作成する。
func NewFxTwitterFetcher() *FxTwitterFetcher {
	return &FxTwitterFetcher{
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

// Supports は X/Twitter URL のみサポート。
func (f *FxTwitterFetcher) Supports(url string) bool {
	return IsTwitterURL(url)
}

// Fetch は FxTwitter API で投稿を取得する。
func (f *FxTwitterFetcher) Fetch(ctx context.Context, rawURL string) (*Tweet, error) {
	tweetID := ExtractTweetID(rawURL)
	if tweetID == "" {
		return nil, fmt.Errorf("twitter: ツイート ID を抽出できません: %s", rawURL)
	}

	// FxTwitter API: /i/status/{id} (i はプレースホルダ、IDで解決される)
	apiURL := fmt.Sprintf("%s/i/status/%s", fxTwitterBase, tweetID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("twitter: リクエスト作成に失敗: %w", err)
	}
	req.Header.Set("User-Agent", "suzuha-bot/1.0")

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("twitter: API リクエストに失敗: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("twitter: レスポンス読み取りに失敗: %w", err)
	}

	var apiResp fxTwitterResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("twitter: JSON パースに失敗: %w", err)
	}

	if apiResp.Code != 200 || apiResp.Tweet == nil {
		return nil, fmt.Errorf("twitter: ツイートが見つかりません (code=%d, id=%s)", apiResp.Code, tweetID)
	}

	return apiResp.Tweet.toTweet(), nil
}

type fxTwitterResponse struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Tweet   *fxTwitterTweet `json:"tweet"`
}

type fxTwitterTweet struct {
	ID        string          `json:"id"`
	URL       string          `json:"url"`
	Text      string          `json:"text"`
	Author    fxTwitterAuthor `json:"author"`
	Replies   int             `json:"replies"`
	Retweets  int             `json:"retweets"`
	Likes     int             `json:"likes"`
	CreatedAt string          `json:"created_at"`
	Media     *fxTwitterMedia `json:"media"`
}

type fxTwitterAuthor struct {
	Name       string `json:"name"`
	ScreenName string `json:"screen_name"`
}

type fxTwitterMedia struct {
	Photos []fxTwitterPhoto `json:"photos"`
	Videos []fxTwitterVideo `json:"videos"`
}

type fxTwitterPhoto struct {
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

type fxTwitterVideo struct {
	URL       string  `json:"url"`
	Thumbnail string  `json:"thumbnail_url"`
	Duration  float64 `json:"duration"`
}

func (t *fxTwitterTweet) toTweet() *Tweet {
	tweet := &Tweet{
		ID:         t.ID,
		Text:       t.Text,
		AuthorName: t.Author.Name,
		AuthorID:   t.Author.ScreenName,
		CreatedAt:  t.CreatedAt,
		Likes:      t.Likes,
		Retweets:   t.Retweets,
		Replies:    t.Replies,
		URL:        t.URL,
	}

	if t.Media != nil {
		for _, p := range t.Media.Photos {
			tweet.Images = append(tweet.Images, p.URL)
		}
		if len(t.Media.Videos) > 0 {
			tweet.VideoURL = t.Media.Videos[0].URL
		}
	}

	return tweet
}
