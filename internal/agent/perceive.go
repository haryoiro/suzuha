package agent

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/haryoiro/suzuha/external/transcript"
	"github.com/haryoiro/suzuha/external/twitter"

	"golang.org/x/image/webp"

	channelpkg "github.com/haryoiro/suzuha/internal/channel"
	"github.com/haryoiro/suzuha/internal/event"
	"github.com/haryoiro/suzuha/internal/lib/jtime"
	"github.com/haryoiro/suzuha/internal/lib/textutil"
	"github.com/haryoiro/suzuha/internal/llm"
)

// Perceive is the backward-compatible wrapper that calls PerceiveWith
// with the discord context and a zero DirectiveConfig.
func (a *Agent) Perceive(ctx context.Context, batch []event.Event) *Perception {
	return a.PerceiveWith(ctx, a.contexts[SourceKeyDiscord], batch, DirectiveConfig{})
}

// PerceiveWith ingests all events in the batch into the given context,
// resolving users, describing images, and bootstrapping channel history.
// Returns a Perception summarizing what was observed, or nil if all events
// were filtered out (e.g. disabled channels).
func (a *Agent) PerceiveWith(ctx context.Context, agentCtx *Context, batch []event.Event, dc DirectiveConfig) *Perception {
	// Filter out disabled channels (unless SkipChannelFilter is set).
	if !dc.SkipChannelFilter && a.channelSettings != nil {
		var filtered []event.Event
		for _, evt := range batch {
			chID := evt.Message.Channel
			if chID != "" && !evt.Message.IsDM && a.channelSettings.GetMode(chID) == channelpkg.ModeDisabled {
				a.logger.Debug("無効なチャンネルなのでスルー", "channel", chID)
				continue
			}
			filtered = append(filtered, evt)
		}
		if len(filtered) == 0 {
			return nil
		}
		batch = filtered
	}

	// Ingest all events into context.
	turnStartIdx := agentCtx.Len()
	var lastMsg llm.Message
	var lastEvt event.Event
	var directlyAddressed bool
	for _, evt := range batch {
		msg := a.ingestEventWith(ctx, agentCtx, evt, dc)
		lastMsg = msg
		lastEvt = evt
		if isDirectlyAddressed(evt, a.botID) {
			directlyAddressed = true
		}
	}

	channel := lastEvt.Message.Channel
	isDM := lastEvt.Message.IsDM

	_ = lastMsg // used via field access below
	return &Perception{
		LastMessage:       lastMsg,
		LastEvent:         lastEvt,
		Channel:           channel,
		IsDM:              isDM,
		IsVoice:           lastEvt.Message.IsVoice,
		DirectlyAddressed: directlyAddressed,
		SenderIsBot:       lastEvt.Message.IsBot,
		TurnStartIdx:      turnStartIdx,
	}
}

// ingestEventWith processes a single event: resolves the user, adds the message
// to the given context, and injects channel history. It does NOT trigger LLM completion.
func (a *Agent) ingestEventWith(ctx context.Context, agentCtx *Context, evt event.Event, dc DirectiveConfig) llm.Message {
	msg := eventToMessage(evt)

	a.logger.Info("聞こえた",
		"source", evt.Source, "type", evt.Type,
		"user", msg.UserName, "user_id", msg.UserID,
		"channel", msg.Channel, "content", textutil.TruncateRunes(msg.Content, 100))

	// Resolve user identity (auto-create if not exists).
	if a.users != nil && msg.UserID != "" && msg.UserID != a.botID {
		u, err := a.users.Resolve(ctx, msg.Source, msg.UserID, msg.UserName)
		if err != nil {
			a.logger.Warn("誰かわからなかった", "error", err)
		} else {
			if u.DisplayName != "" {
				msg.UserName = u.DisplayName
				a.logger.Debug("誰かわかった", "display_name", u.DisplayName, "role", u.Role)
			}
			guildID := evt.Message.GuildID
			guildName := evt.Message.GuildName
			channelName := evt.Message.ChannelName
			if guildID != "" {
				if err := a.users.TrackGuildChannel(ctx, u.ID, guildID, guildName, msg.Channel, channelName); err != nil {
					a.logger.Warn("チャンネルの追跡に失敗", "error", err)
				}
			}
		}
	}

	// Track channel activity for topic backoff (non-bot, non-internal messages only).
	if msg.Channel != "" && a.db != nil && msg.UserID != a.botID && evt.Source != event.SourceInternal {
		_, _ = a.db.ExecContext(ctx,
			`INSERT INTO channel_activity (channel_id, last_user_message_at) VALUES (?, ?)
			 ON CONFLICT(channel_id) DO UPDATE SET last_user_message_at = excluded.last_user_message_at`,
			msg.Channel, time.Now())
	}

	// Handle attached images.
	if a.llm != nil {
		if urls := extractImageURLs(evt); len(urls) > 0 {
			// Download, persist to MediaStore, and create data URIs.
			dataURIs, mediaKeys := a.downloadAndPersistImages(ctx, urls, msg.MessageID)
			if len(dataURIs) > 0 {
				msg.ImageURLs = dataURIs
			}
			if len(mediaKeys) > 0 {
				msg.MediaKeys = mediaKeys
			}

			if rc, inline := a.llm.WithCapability("conversation", "vision"); rc != nil {
				if inline {
					// Vision-capable LLM: data URIs already set above.
				} else {
					// Separate VLM: describe images as text for the main LLM.
					descriptions := a.describeImages(ctx, urls)
					if descriptions != "" {
						msg.Content += "\n" + descriptions
					}
				}
			}
		}
	}

	// Annotate video URLs with metadata (title, duration).
	if a.videoMeta != nil {
		msg.Content = annotateVideoURLs(ctx, msg.Content, a.videoMeta, a.logger)
	}

	// Annotate X/Twitter URLs with tweet preview.
	msg.Content = annotateTwitterURLs(ctx, msg.Content, a.logger)

	// Bootstrap channel history if this is a new channel (unless SkipChannelHistory is set).
	if !dc.SkipChannelHistory {
		a.injectChannelHistoryWith(ctx, agentCtx, msg.Channel, msg.Content, msg.Source)
	}

	// Add to context (skip self_prompt — these are injected as ephemeral in Think).
	if evt.Type != event.TypeSelfPrompt {
		agentCtx.Add(msg)
	}

	return msg
}

// eventToMessage converts an event to an llm.Message.
func eventToMessage(evt event.Event) llm.Message {
	m := evt.Message
	role := "user"
	if evt.Source == event.SourceInternal {
		role = "system"
	}
	return llm.Message{
		Role:        role,
		Content:     m.Content,
		UserID:      m.UserID,
		UserName:    m.UserName,
		Source:      evt.Source,
		Channel:     m.Channel,
		ChannelName: m.ChannelName,
		GuildID:     m.GuildID,
		GuildName:   m.GuildName,
		MessageID:   m.MessageID,
		Timestamp:   evt.Timestamp,
	}
}

// extractImageURLs pulls image URLs from an event's typed payload.
func extractImageURLs(evt event.Event) []string {
	return evt.Message.ImageURLs
}

// describeImages calls the VLM for each image URL and returns a combined description.
func (a *Agent) describeImages(ctx context.Context, urls []string) string {
	const maxImages = 4
	if len(urls) > maxImages {
		urls = urls[:maxImages]
	}

	var parts []string
	for i, u := range urls {
		desc, err := a.llm.DescribeImage(ctx, u)
		if err != nil {
			a.logger.Warn("画像が読めなかった", "url", u, "error", err)
			desc = "画像の読み取りに失敗しました"
		}
		parts = append(parts, fmt.Sprintf("[添付画像%d: %s]", i+1, desc))
	}
	return strings.Join(parts, "\n")
}

// downloadAndPersistImages downloads images, saves them to MediaStore,
// and returns both data URIs (for LLM context) and media keys (for memory linking).
func (a *Agent) downloadAndPersistImages(ctx context.Context, urls []string, messageID string) (dataURIs, mediaKeys []string) {
	const maxImages = 4
	const maxBytes = 10 * 1024 * 1024 // 10 MB per image
	if len(urls) > maxImages {
		urls = urls[:maxImages]
	}

	client := &http.Client{Timeout: 15 * time.Second}
	for i, u := range urls {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			a.logger.Warn("画像の取得準備に失敗", "url", u, "error", err)
			continue
		}
		resp, err := client.Do(req)
		if err != nil {
			a.logger.Warn("画像をダウンロードできなかった", "url", u, "error", err)
			continue
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes))
		resp.Body.Close()
		if err != nil || len(data) == 0 {
			a.logger.Warn("画像の読み込みに失敗", "url", u, "error", err)
			continue
		}
		mime := resp.Header.Get("Content-Type")
		if mime == "" || !strings.HasPrefix(mime, "image/") {
			mime = "image/png"
		}
		dataURI := fmt.Sprintf("data:%s;base64,%s", mime, base64.StdEncoding.EncodeToString(data))
		dataURIs = append(dataURIs, dataURI)

		// Convert webp to png for Gemini embedding compatibility.
		if mime == "image/webp" {
			if converted, err := convertWebPToPNG(data); err != nil {
				a.logger.Warn("webp→png変換に失敗、そのまま保存", "error", err)
			} else {
				data = converted
				mime = "image/png"
				// Rebuild data URI with converted data.
				dataURIs[len(dataURIs)-1] = fmt.Sprintf("data:%s;base64,%s", mime, base64.StdEncoding.EncodeToString(data))
			}
		}

		// Persist to MediaStore if available.
		if a.mediaStore != nil {
			ext := extFromMimeType(mime)
			key := fmt.Sprintf("messages/%s/%d%s", messageID, i, ext)
			if err := a.mediaStore.Put(ctx, key, data); err != nil {
				a.logger.Warn("画像の保存に失敗", "key", key, "error", err)
			} else {
				mediaKeys = append(mediaKeys, key)
				a.logger.Debug("画像を保存した", "key", key, "size", len(data))
			}
		}
	}
	return
}

// convertWebPToPNG decodes webp data and re-encodes as PNG.
func convertWebPToPNG(data []byte) ([]byte, error) {
	img, err := webp.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("webp decode: %w", err)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("png encode: %w", err)
	}
	return buf.Bytes(), nil
}

func extFromMimeType(mime string) string {
	switch mime {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	default:
		return ".bin"
	}
}

// injectChannelHistoryWith fetches recent messages for a channel not yet seen
// in the context. Uses the discord_get_history tool if available,
// falling back to recent memory search.
func (a *Agent) injectChannelHistoryWith(ctx context.Context, agentCtx *Context, channelID, messageContent, source string) {
	if channelID == "" {
		return
	}
	if agentCtx.HasChannelHistory(channelID) {
		return
	}
	// context に既にこのチャンネルの会話メッセージがある場合は
	// history 注入をスキップ（重複を防ぐ）。
	if agentCtx.HasMessagesForChannel(channelID) {
		agentCtx.MarkChannelSeen(channelID)
		return
	}
	// Remove stale history (e.g. from DB restore) before injecting fresh one.
	agentCtx.RemoveChannelHistory(channelID)
	agentCtx.MarkChannelSeen(channelID)

	var content string
	if histTool, ok := a.tools.Get("discord_get_history"); ok {
		input, _ := json.Marshal(map[string]any{
			"channel_id": channelID,
			"limit":      10,
		})
		result, err := histTool.Execute(ctx, input)
		if err != nil {
			a.logger.Warn("会話の振り返りに失敗", "channel", channelID, "error", err)
		} else if result != nil && !result.IsError && len(result.Content) > 0 && result.Content[0].Text != "" {
			content = a.formatChannelHistory(ctx, channelID, result.Content[0].Text, source)
		}
	}

	if content == "" && a.memory != nil {
		since := time.Now().Add(-3 * 24 * time.Hour)
		memories, err := a.memory.SearchRecent(ctx, messageContent, 5, since)
		if err != nil {
			a.logger.Debug("関連する記憶が見つからなかった", "error", err)
		}
		if len(memories) > 0 {
			var b strings.Builder
			fmt.Fprintf(&b, "[Recent related memories for channel=%s]\n", channelID)
			for _, m := range memories {
				fmt.Fprintf(&b, "- [%s] %s\n", m.Type, m.Content)
			}
			content = b.String()
		}
	}

	if content != "" {
		agentCtx.Add(llm.Message{
			Role:      "system",
			Content:   content,
			Channel:  channelID,
			Timestamp: jtime.Now(),
		})
		a.logger.Info("最近の会話を振り返った", "channel", channelID, "length", len(content))
	}
}

// formatChannelHistory parses the history tool's JSON output and resolves
// author names via the user store.
func (a *Agent) formatChannelHistory(ctx context.Context, channelID, rawJSON, source string) string {
	type histMsg struct {
		AuthorID string `json:"author_id"`
		Author   string `json:"author"`
		Content  string `json:"content"`
		Time     string `json:"time"`
	}
	var msgs []histMsg
	if err := json.Unmarshal([]byte(rawJSON), &msgs); err != nil {
		return fmt.Sprintf("[Recent history for channel=%s]\n%s", channelID, rawJSON)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "[Recent history for channel=%s]\n", channelID)
	for _, m := range msgs {
		name := m.Author
		if m.AuthorID == a.botID {
			if a.users != nil {
				if u, err := a.users.Resolve(ctx, source, m.AuthorID, m.Author); err == nil && u.DisplayName != "" {
					name = u.DisplayName
				}
			}
			name += " (self)"
		} else if a.users != nil && m.AuthorID != "" {
			if u, err := a.users.Resolve(ctx, source, m.AuthorID, m.Author); err == nil && u.DisplayName != "" {
				name = u.DisplayName
			}
		}
		fmt.Fprintf(&b, "[%s] %s: %s\n", m.Time, name, m.Content)
	}
	return b.String()
}

// annotateVideoURLs はメッセージ中の動画 URL を検知し、メタデータで enrich する。
func annotateVideoURLs(ctx context.Context, text string, meta transcript.MetadataFetcher, logger *slog.Logger) string {
	urls := transcript.ExtractVideoURLs(text)
	if len(urls) == 0 {
		return text
	}

	for _, u := range urls {
		if !meta.Supports(u) {
			continue
		}
		info, err := meta.FetchMetadata(ctx, u)
		if err != nil {
			logger.Debug("video: メタデータ取得失敗", "url", u, "error", err)
			continue
		}
		durMin := int(info.Duration) / 60
		durSec := int(info.Duration) % 60
		annotation := fmt.Sprintf("[動画: %q (%d:%02d) | video_watch で視聴可能] ", info.Title, durMin, durSec)
		text = strings.Replace(text, u, annotation+u, 1)
	}
	return text
}

// annotateTwitterURLs はメッセージ中の X/Twitter URL を検知し、ツイート内容でアノテーションする。
func annotateTwitterURLs(ctx context.Context, text string, logger *slog.Logger) string {
	urls := twitter.ExtractTwitterURLs(text)
	if len(urls) == 0 {
		return text
	}

	fetcher := twitter.NewFxTwitterFetcher()
	for _, u := range urls {
		tweet, err := fetcher.Fetch(ctx, u)
		if err != nil {
			logger.Debug("twitter: ツイート取得失敗", "url", u, "error", err)
			continue
		}
		// ツイートテキストのプレビュー (50 文字まで)
		preview := []rune(tweet.Text)
		if len(preview) > 50 {
			preview = append(preview[:50], []rune("...")...)
		}
		annotation := fmt.Sprintf("[Tweet: @%s「%s」| fetch で詳細取得可能] ", tweet.AuthorID, string(preview))
		text = strings.Replace(text, u, annotation+u, 1)
	}
	return text
}
