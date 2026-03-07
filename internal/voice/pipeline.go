package voice

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"sync"

	"github.com/bwmarrin/discordgo"
	"github.com/haryoiro/suzuha/internal/event"
)

// Pipeline bridges voice input/output with the agent's text pipeline.
// It manages voice sessions, STT, and TTS.
type Pipeline struct {
	discordSession *discordgo.Session
	bus            *event.Bus
	stt            STT
	tts            TTS
	logger         *slog.Logger

	mu       sync.Mutex
	sessions map[string]*Session // guildID -> Session
}

// NewPipeline creates a voice pipeline.
func NewPipeline(ds *discordgo.Session, bus *event.Bus, sttClient STT, ttsClient TTS, logger *slog.Logger) *Pipeline {
	return &Pipeline{
		discordSession: ds,
		bus:            bus,
		stt:            sttClient,
		tts:            ttsClient,
		logger:         logger,
		sessions:       make(map[string]*Session),
	}
}

// Join connects to a voice channel and starts the receive pipeline.
func (p *Pipeline) Join(ctx context.Context, guildID, channelID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if existing, ok := p.sessions[guildID]; ok {
		existing.Leave()
		delete(p.sessions, guildID)
	}

	sess := NewSession(p.discordSession, guildID, channelID, p.logger)
	sess.OnSpeech(func(userID string, pcm []byte) {
		p.handleSpeech(ctx, guildID, channelID, userID, pcm)
	})

	if err := sess.Join(ctx); err != nil {
		return err
	}

	p.sessions[guildID] = sess
	return nil
}

// Leave disconnects from the voice channel in the given guild.
func (p *Pipeline) Leave(guildID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	sess, ok := p.sessions[guildID]
	if !ok {
		return fmt.Errorf("voice: ギルド %s のセッションが見つかりません", guildID)
	}

	err := sess.Leave()
	delete(p.sessions, guildID)
	p.logger.Info("voice: VC離脱", "guild", guildID)
	return err
}

// IsConnected returns true if there is an active voice session for the guild.
func (p *Pipeline) IsConnected(guildID string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	sess, ok := p.sessions[guildID]
	return ok && sess.IsConnected()
}

// SpeakText synthesizes text and sends it to the voice channel.
func (p *Pipeline) SpeakText(ctx context.Context, guildID, text string) error {
	p.mu.Lock()
	sess, ok := p.sessions[guildID]
	p.mu.Unlock()
	if !ok {
		return fmt.Errorf("voice: ギルド %s のセッションが見つかりません", guildID)
	}

	pcm, err := p.tts.Synthesize(ctx, text)
	if err != nil {
		return fmt.Errorf("voice: TTS失敗: %w", err)
	}
	if len(pcm) == 0 {
		return nil
	}

	// VOICEVOX outputs 24kHz mono, Discord expects 48kHz stereo.
	pcm48k := resample24kTo48k(pcm)
	stereo := monoToStereo(pcm48k)

	return sess.SendPCM(stereo)
}

// handleSpeech is called when VAD detects a complete speech segment.
// It transcribes the audio and publishes it as a voice event.
func (p *Pipeline) handleSpeech(ctx context.Context, guildID, channelID, userID string, pcm []byte) {
	// Transcribe speech to text.
	// PCM is 48kHz mono from the receiver.
	text, err := p.stt.Transcribe(ctx, pcm, 48000)
	if err != nil {
		p.logger.Error("voice: STT失敗", "user_id", userID, "error", err)
		return
	}

	if text == "" {
		p.logger.Debug("voice: 空の文字起こし結果", "user_id", userID)
		return
	}

	p.logger.Info("voice: 文字起こし完了", "user_id", userID, "text", text)

	// Resolve the user's display name from Discord state.
	userName := userID
	if member, err := p.discordSession.GuildMember(guildID, userID); err == nil {
		if member.Nick != "" {
			userName = member.Nick
		} else if member.User != nil {
			userName = member.User.Username
			if member.User.GlobalName != "" {
				userName = member.User.GlobalName
			}
		}
	}

	// Resolve guild name.
	var guildName string
	if g, err := p.discordSession.State.Guild(guildID); err == nil {
		guildName = g.Name
	}

	// Publish as a voice event to the agent pipeline.
	evt := event.NewMessageEvent("discord", event.MessagePayload{
		Content:     text,
		Channel:     channelID,
		UserID:      userID,
		UserName:    userName,
		IsMention:   true, // VC messages are always directly addressed.
		IsDM:        false,
		IsBot:       false,
		IsVoice:     true,
		GuildID:     guildID,
		GuildName:   guildName,
		ChannelName: "voice",
	})
	p.bus.Publish(evt)
}

// resample24kTo48k doubles the sample rate by linear interpolation.
// Input: 16-bit LE mono PCM at 24kHz. Output: 16-bit LE mono PCM at 48kHz.
func resample24kTo48k(pcm []byte) []byte {
	nSamples := len(pcm) / 2
	if nSamples == 0 {
		return nil
	}
	out := make([]byte, nSamples*4) // 2x samples, 2 bytes each
	for i := 0; i < nSamples; i++ {
		sample := int16(binary.LittleEndian.Uint16(pcm[i*2 : i*2+2]))
		// Write the sample twice (simple nearest-neighbor upsampling).
		binary.LittleEndian.PutUint16(out[i*4:], uint16(sample))
		binary.LittleEndian.PutUint16(out[i*4+2:], uint16(sample))
	}
	return out
}

// monoToStereo duplicates a mono 16-bit LE PCM stream to stereo.
func monoToStereo(mono []byte) []byte {
	nSamples := len(mono) / 2
	stereo := make([]byte, nSamples*4) // 2 channels, 2 bytes each
	for i := 0; i < nSamples; i++ {
		sample := mono[i*2 : i*2+2]
		copy(stereo[i*4:], sample)   // left
		copy(stereo[i*4+2:], sample) // right
	}
	return stereo
}
