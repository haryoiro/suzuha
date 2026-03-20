package voice

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

// DeepgramClient calls the Deepgram pre-recorded transcription API.
type DeepgramClient struct {
	apiKey     string
	model      string
	httpClient *http.Client
}

// NewDeepgram creates a DeepgramClient with the given API key and model.
func NewDeepgram(apiKey, model string) *DeepgramClient {
	if model == "" {
		model = "nova-3"
	}
	return &DeepgramClient{
		apiKey: apiKey,
		model:  model,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Transcribe sends PCM audio (16-bit LE, mono) to Deepgram and returns the text.
func (d *DeepgramClient) Transcribe(ctx context.Context, pcm []byte, sampleRate int) (string, error) {
	wav := pcmToWAV(pcm, sampleRate, 1, 16)

	url := fmt.Sprintf("https://api.deepgram.com/v1/listen?model=%s&language=ja", d.model)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(wav))
	if err != nil {
		return "", fmt.Errorf("stt/deepgram: リクエスト作成失敗: %w", err)
	}
	req.Header.Set("Authorization", "Token "+d.apiKey)
	req.Header.Set("Content-Type", "audio/wav")

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("stt/deepgram: リクエスト失敗: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("stt/deepgram: レスポンス読み取り失敗: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("stt/deepgram: ステータス %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Metadata struct {
			Duration float64 `json:"duration"`
		} `json:"metadata"`
		Results struct {
			Channels []struct {
				Alternatives []struct {
					Transcript string  `json:"transcript"`
					Confidence float64 `json:"confidence"`
				} `json:"alternatives"`
			} `json:"channels"`
		} `json:"results"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("stt/deepgram: レスポンスパース失敗: %w", err)
	}

	if len(result.Results.Channels) > 0 && len(result.Results.Channels[0].Alternatives) > 0 {
		alt := result.Results.Channels[0].Alternatives[0]
		// Log empty results for debugging.
		if alt.Transcript == "" {
			log.Printf("stt/deepgram: 空結果 (duration=%.2fs, wav_bytes=%d)", result.Metadata.Duration, len(wav))
		}
		return alt.Transcript, nil
	}

	return "", nil
}
