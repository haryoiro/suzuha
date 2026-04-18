package voice

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
)

// StreamWatcher monitors Discord voice channel streams (screen shares)
// and periodically captures preview images.
type StreamWatcher struct {
	session    *discordgo.Session
	logger     *slog.Logger
	httpClient *http.Client

	mu      sync.Mutex
	streams map[string]*activeStream // stream_key -> stream info

	onPreview func(guildID string, jpeg []byte) // callback when preview captured
}

type activeStream struct {
	GuildID   string
	ChannelID string
	UserID    string
	StreamKey string
}

// NewStreamWatcher creates a StreamWatcher.
func NewStreamWatcher(session *discordgo.Session, logger *slog.Logger) *StreamWatcher {
	return &StreamWatcher{
		session: session,
		logger:  logger,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		streams: make(map[string]*activeStream),
	}
}

// OnPreview sets the callback for when a stream preview is captured.
func (sw *StreamWatcher) OnPreview(fn func(guildID string, jpeg []byte)) {
	sw.onPreview = fn
}

// Start begins watching for voice state changes and capturing previews.
func (sw *StreamWatcher) Start() {
	sw.session.AddHandler(func(_ *discordgo.Session, vs *discordgo.VoiceStateUpdate) {
		sw.handleVoiceState(vs)
	})

	// Periodic preview capture loop.
	go func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			sw.captureAll()
		}
	}()
}

func (sw *StreamWatcher) handleVoiceState(vs *discordgo.VoiceStateUpdate) {
	sw.mu.Lock()
	defer sw.mu.Unlock()

	userID := vs.UserID
	// Don't watch our own streams.
	if userID == sw.session.State.User.ID {
		return
	}

	if vs.SelfStream {
		key := fmt.Sprintf("guild:%s:%s:%s", vs.GuildID, vs.ChannelID, userID)
		if _, exists := sw.streams[key]; !exists {
			sw.streams[key] = &activeStream{
				GuildID:   vs.GuildID,
				ChannelID: vs.ChannelID,
				UserID:    userID,
				StreamKey: key,
			}
			sw.logger.Info("stream: 配信検出", "user", userID, "key", key)
		}
	} else {
		// User stopped streaming — remove all their streams in this guild.
		for key, s := range sw.streams {
			if s.UserID == userID && s.GuildID == vs.GuildID {
				delete(sw.streams, key)
				sw.logger.Info("stream: 配信終了", "user", userID, "key", key)
			}
		}
	}
}

func (sw *StreamWatcher) captureAll() {
	sw.mu.Lock()
	keys := make([]*activeStream, 0, len(sw.streams))
	for _, s := range sw.streams {
		keys = append(keys, s)
	}
	sw.mu.Unlock()

	for _, s := range keys {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		jpeg, err := sw.fetchPreview(ctx, s.StreamKey)
		cancel()
		if err != nil {
			sw.logger.Debug("stream: プレビュー取得失敗", "key", s.StreamKey, "error", err)
			continue
		}
		if sw.onPreview != nil && len(jpeg) > 0 {
			sw.onPreview(s.GuildID, jpeg)
		}
	}
}

// fetchPreview calls the undocumented Discord stream preview endpoint.
func (sw *StreamWatcher) fetchPreview(ctx context.Context, streamKey string) ([]byte, error) {
	url := fmt.Sprintf("https://discord.com/api/v10/streams/%s/preview", streamKey)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return nil, fmt.Errorf("voice: プレビューリクエスト作成に失敗: %w", err)
	}
	req.Header.Set("Authorization", sw.session.Token)

	resp, err := sw.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("voice: プレビューAPI呼び出しに失敗: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("voice: プレビューURLのデコードに失敗: %w", err)
	}
	if result.URL == "" {
		return nil, fmt.Errorf("empty preview URL")
	}

	imgReq, err := http.NewRequestWithContext(ctx, http.MethodGet, result.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("voice: 画像リクエスト作成に失敗: %w", err)
	}
	imgResp, err := sw.httpClient.Do(imgReq)
	if err != nil {
		return nil, fmt.Errorf("voice: プレビュー画像取得に失敗: %w", err)
	}
	defer imgResp.Body.Close()

	return io.ReadAll(imgResp.Body)
}

// ActiveStreams returns the number of currently active streams.
func (sw *StreamWatcher) ActiveStreams() int {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	return len(sw.streams)
}
