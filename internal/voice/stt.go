package voice

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

// STT transcribes audio to text.
type STT interface {
	Transcribe(ctx context.Context, pcm []byte, sampleRate int) (string, error)
}

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
	// Convert PCM to WAV in-memory for the multipart upload.
	wav := pcmToWAV(pcm, sampleRate, 1, 16)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "audio.wav")
	if err != nil {
		return "", fmt.Errorf("stt: multipart作成失敗: %w", err)
	}
	if _, err := part.Write(wav); err != nil {
		return "", fmt.Errorf("stt: WAV書き込み失敗: %w", err)
	}
	// Set language to Japanese.
	if err := writer.WriteField("language", "ja"); err != nil {
		return "", fmt.Errorf("stt: フィールド書き込み失敗: %w", err)
	}
	if err := writer.WriteField("response_format", "json"); err != nil {
		return "", fmt.Errorf("stt: フィールド書き込み失敗: %w", err)
	}
	writer.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.baseURL+"/inference", &body)
	if err != nil {
		return "", fmt.Errorf("stt: リクエスト作成失敗: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := w.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("stt: リクエスト失敗: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("stt: ステータス %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("stt: レスポンスパース失敗: %w", err)
	}

	return result.Text, nil
}

// pcmToWAV wraps raw PCM data in a minimal WAV header.
func pcmToWAV(pcm []byte, sampleRate, channels, bitsPerSample int) []byte {
	dataSize := len(pcm)
	byteRate := sampleRate * channels * bitsPerSample / 8
	blockAlign := channels * bitsPerSample / 8

	buf := make([]byte, 44+dataSize)
	copy(buf[0:4], "RIFF")
	le32(buf[4:8], uint32(36+dataSize))
	copy(buf[8:12], "WAVE")
	copy(buf[12:16], "fmt ")
	le32(buf[16:20], 16) // PCM format chunk size
	le16(buf[20:22], 1)  // PCM format
	le16(buf[22:24], uint16(channels))
	le32(buf[24:28], uint32(sampleRate))
	le32(buf[28:32], uint32(byteRate))
	le16(buf[32:34], uint16(blockAlign))
	le16(buf[34:36], uint16(bitsPerSample))
	copy(buf[36:40], "data")
	le32(buf[40:44], uint32(dataSize))
	copy(buf[44:], pcm)
	return buf
}

func le16(b []byte, v uint16) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
}

func le32(b []byte, v uint32) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
}
