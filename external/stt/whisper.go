package stt

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"time"
)

// WhisperClient calls a whisper.cpp HTTP server for transcription.
type WhisperClient struct {
	baseURL    string
	httpClient *http.Client
}

// NewWhisper creates a WhisperClient pointing at the given whisper.cpp server URL.
func NewWhisper(baseURL string) *WhisperClient {
	return &WhisperClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Transcribe sends PCM audio (16-bit LE, mono) to whisper.cpp and returns the text.
func (w *WhisperClient) Transcribe(ctx context.Context, pcm []byte, sampleRate int) (string, error) {
	wav := pcmToWAV(pcm, sampleRate, 1, 16)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "audio.wav")
	if err != nil {
		return "", fmt.Errorf("stt/whisper: multipart作成失敗: %w", err)
	}
	if _, err := part.Write(wav); err != nil {
		return "", fmt.Errorf("stt/whisper: WAV書き込み失敗: %w", err)
	}
	if err := writer.WriteField("language", "ja"); err != nil {
		return "", fmt.Errorf("stt/whisper: フィールド書き込み失敗: %w", err)
	}
	if err := writer.WriteField("response_format", "json"); err != nil {
		return "", fmt.Errorf("stt/whisper: フィールド書き込み失敗: %w", err)
	}
	writer.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.baseURL+"/inference", &body)
	if err != nil {
		return "", fmt.Errorf("stt/whisper: リクエスト作成失敗: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := w.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("stt/whisper: リクエスト失敗: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("stt/whisper: ステータス %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("stt/whisper: レスポンスパース失敗: %w", err)
	}

	return result.Text, nil
}
