package voice

import (
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
	session *discordgo.Session
	logger  *slog.Logger

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
		jpeg, err := sw.fetchPreview(s.StreamKey)
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
func (sw *StreamWatcher) fetchPreview(streamKey string) ([]byte, error) {
	// POST /streams/{stream_key}/preview returns {"url": "https://..."}
	url := fmt.Sprintf("https://discord.com/api/v10/streams/%s/preview", streamKey)

	req, err := http.NewRequest(http.MethodPost, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", sw.session.Token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
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
		return nil, err
	}
	if result.URL == "" {
		return nil, fmt.Errorf("empty preview URL")
	}

	// Download the preview image.
	imgResp, err := http.Get(result.URL)
	if err != nil {
		return nil, err
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
