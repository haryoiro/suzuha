package voice

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

// TTS synthesizes text to PCM audio.
type TTS interface {
	// Synthesize converts text to raw PCM audio (16-bit LE, mono, 24kHz).
	Synthesize(ctx context.Context, text string) ([]byte, error)
}

// VoicevoxClient calls a VOICEVOX engine HTTP API for synthesis.
type VoicevoxClient struct {
	baseURL    string
	speakerID  int
	mu         sync.Mutex
	httpClient *http.Client
}

// SetSpeakerID changes the VOICEVOX speaker at runtime.
func (v *VoicevoxClient) SetSpeakerID(id int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.speakerID = id
}

// SpeakerID returns the current speaker ID.
func (v *VoicevoxClient) SpeakerID() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.speakerID
}

// BaseURL returns the VOICEVOX engine URL.
func (v *VoicevoxClient) BaseURL() string {
	return v.baseURL
}

// NewVoicevox creates a VoicevoxClient.
// speakerID selects the voice (e.g. 3 = zundamon normal).
func NewVoicevox(baseURL string, speakerID int) *VoicevoxClient {
	return &VoicevoxClient{
		baseURL:   baseURL,
		speakerID: speakerID,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Synthesize generates speech audio from text via VOICEVOX's two-step API:
// 1. audio_query: text -> synthesis parameters
// 2. synthesis: parameters -> WAV audio
// Returns raw PCM (strips the WAV header).
func (v *VoicevoxClient) Synthesize(ctx context.Context, text string) ([]byte, error) {
	if text == "" {
		return nil, nil
	}

	// Step 1: Generate audio query.
	query, err := v.audioQuery(ctx, text)
	if err != nil {
		return nil, fmt.Errorf("tts: audio_query失敗: %w", err)
	}

	// Step 2: Synthesize audio from query.
	wav, err := v.synthesis(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("tts: synthesis失敗: %w", err)
	}

	// Strip WAV header (44 bytes) to get raw PCM.
	if len(wav) <= 44 {
		return nil, fmt.Errorf("tts: WAVデータが短すぎます: %d bytes", len(wav))
	}
	return wav[44:], nil
}

func (v *VoicevoxClient) audioQuery(ctx context.Context, text string) (json.RawMessage, error) {
	v.mu.Lock()
	sid := v.speakerID
	v.mu.Unlock()
	u := fmt.Sprintf("%s/audio_query?text=%s&speaker=%d",
		v.baseURL, url.QueryEscape(text), sid)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
	if err != nil {
		return nil, err
	}

	resp, err := v.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ステータス %d: %s", resp.StatusCode, string(body))
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(data), nil
}

func (v *VoicevoxClient) synthesis(ctx context.Context, query json.RawMessage) ([]byte, error) {
	v.mu.Lock()
	sid := v.speakerID
	v.mu.Unlock()
	u := fmt.Sprintf("%s/synthesis?speaker=%s",
		v.baseURL, strconv.Itoa(sid))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(query))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := v.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ステータス %d: %s", resp.StatusCode, string(body))
	}

	return io.ReadAll(resp.Body)
}
