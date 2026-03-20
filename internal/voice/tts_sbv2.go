package voice

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// SBV2Client calls a Style-Bert-VITS2 API server for synthesis.
type SBV2Client struct {
	baseURL    string
	model      string
	style      string
	httpClient *http.Client
}

// NewSBV2 creates an SBV2Client.
func NewSBV2(baseURL, model, style string) *SBV2Client {
	if style == "" {
		style = "Neutral"
	}
	return &SBV2Client{
		baseURL: baseURL,
		model:   model,
		style:   style,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// Synthesize generates speech audio from text via the SBV2 /voice endpoint.
// Returns raw PCM and the sample rate parsed from the WAV header.
func (s *SBV2Client) Synthesize(ctx context.Context, text string) ([]byte, int, error) {
	if text == "" {
		return nil, 0, nil
	}

	params := url.Values{
		"text":       {text},
		"language":   {"JP"},
		"auto_split": {"true"},
	}
	if s.model != "" {
		params.Set("model_name", s.model)
	}
	if s.style != "" {
		params.Set("style", s.style)
	}

	u := s.baseURL + "/voice?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("tts/sbv2: リクエスト作成失敗: %w", err)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("tts/sbv2: リクエスト失敗: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, 0, fmt.Errorf("tts/sbv2: ステータス %d: %s", resp.StatusCode, string(body))
	}

	wav, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, fmt.Errorf("tts/sbv2: レスポンス読み取り失敗: %w", err)
	}

	sr := wavSampleRate(wav)
	if sr == 0 {
		return nil, 0, fmt.Errorf("tts/sbv2: WAVヘッダ不正: %d bytes", len(wav))
	}

	pcm := wavPCM(wav)
	if pcm == nil {
		return nil, 0, fmt.Errorf("tts/sbv2: WAVデータが短すぎます: %d bytes", len(wav))
	}

	return pcm, sr, nil
}
