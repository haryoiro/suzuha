package voice

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/bwmarrin/discordgo"
	"github.com/haryoiro/suzuha/internal/adapter/stt"
	"github.com/haryoiro/suzuha/internal/adapter/tts"
	"github.com/haryoiro/suzuha/internal/event"
)

// Pipeline bridges voice input/output with the agent's text pipeline.
// It manages voice sessions, STT, and TTS.
type Pipeline struct {
	discordSession *discordgo.Session
	bus            *event.Bus
	stt            stt.STT
	tts            tts.TTS
	logger         *slog.Logger

	mu       sync.Mutex
	sessions map[string]*Session // guildID -> Session
}

// NewPipeline creates a voice pipeline.
func NewPipeline(ds *discordgo.Session, bus *event.Bus, sttClient stt.STT, ttsClient tts.TTS, logger *slog.Logger) *Pipeline {
	p := &Pipeline{
		discordSession: ds,
		bus:            bus,
		stt:            sttClient,
		tts:            ttsClient,
		logger:         logger,
		sessions:       make(map[string]*Session),
	}
	return p
}

// SetSpeakerID changes the VOICEVOX speaker ID at runtime.
func (p *Pipeline) SetSpeakerID(id int) {
	if vc, ok := p.tts.(*tts.VoicevoxClient); ok {
		vc.SetSpeakerID(id)
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
	p.logger.Info("ボイスチャンネルから出た", "guild", guildID)
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

	pcm, sampleRate, err := p.tts.Synthesize(ctx, text)
	if err != nil {
		return fmt.Errorf("voice: TTS失敗: %w", err)
	}
	if len(pcm) == 0 {
		return nil
	}

	// Discord expects 48kHz stereo.
	pcm48k := ResamplePCM(pcm, sampleRate, 48000)
	stereo := monoToStereo(pcm48k)

	return sess.SendPCM(stereo)
}

// SpeakStream は文チャネルから逐次 TTS → 音声送信する。
// TTS 合成と音声送信を並行させ、BeginSpeaking の準備時間も TTS と重ねる:
//
//	[synth N]─────→[send N]
//	          └→[synth N+1]─→[send N+1]
//	[BeginSpeaking] (並行準備)
func (p *Pipeline) SpeakStream(ctx context.Context, guildID string, sentences <-chan string) error {
	p.mu.Lock()
	sess, ok := p.sessions[guildID]
	p.mu.Unlock()
	if !ok {
		for range sentences {
		}
		return fmt.Errorf("voice: ギルド %s のセッションが見つかりません", guildID)
	}

	synthCtx, cancelSynth := context.WithCancel(ctx)
	defer cancelSynth()

	// 1 文先行合成: send 中に次の文を用意する。
	pcmQueue := make(chan []byte, 1)

	go func() {
		defer close(pcmQueue)
		for sentence := range sentences {
			if synthCtx.Err() != nil {
				for range sentences {
				}
				return
			}
			pcm, sampleRate, err := p.tts.Synthesize(synthCtx, sentence)
			if err != nil {
				p.logger.Warn("ストリーミング TTS 失敗、スキップ", "error", err, "sentence", sentence)
				continue
			}
			if len(pcm) == 0 {
				continue
			}
			pcm48k := ResamplePCM(pcm, sampleRate, 48000)
			stereo := monoToStereo(pcm48k)
			select {
			case pcmQueue <- stereo:
			case <-synthCtx.Done():
				for range sentences {
				}
				return
			}
		}
	}()

	// BeginSpeaking は TTS 合成と並行で走らせる (DAVE 待機と無音プリアンブルを合成時間に隠す)。
	if err := sess.BeginSpeaking(); err != nil {
		cancelSynth()
		go func() {
			for range pcmQueue {
			}
		}()
		return fmt.Errorf("voice: speaking 開始失敗: %w", err)
	}
	defer sess.EndSpeaking()

	for stereo := range pcmQueue {
		if err := sess.SendPCMChunk(stereo); err != nil {
			cancelSynth()
			go func() {
				for range pcmQueue {
				}
			}()
			return fmt.Errorf("voice: PCM チャンク送信失敗: %w", err)
		}
	}
	return nil
}

// handleSpeech is called when VAD detects a complete speech segment.
// It transcribes the audio and publishes it as a voice event.
func (p *Pipeline) handleSpeech(ctx context.Context, guildID, channelID, userID string, pcm []byte) {
	// Transcribe speech to text.
	// PCM is 48kHz mono from the receiver.
	durationMs := len(pcm) * 1000 / (48000 * 2)
	p.logger.Debug("声を聞き取り中", "user_id", userID, "pcm_bytes", len(pcm), "duration_ms", durationMs)

	// Normalize volume before STT (Discord voice can be very quiet).
	pcm = NormalizePCM(pcm, 16000)

	text, err := p.stt.Transcribe(ctx, pcm, 48000)
	if err != nil {
		p.logger.Error("声の聞き取りに失敗", "user_id", userID, "error", err)
		return
	}

	text = strings.TrimSpace(text)
	if text == "" || isWhisperHallucination(text) {
		p.logger.Debug("空耳だったみたい", "user_id", userID, "text", text)
		return
	}

	p.logger.Info("声を聞き取れた", "user_id", userID, "text", text)

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

// whisperHallucinations contains phrases that Whisper commonly hallucinates
// when given silence or noise input.
var whisperHallucinations = []string{
	"ありがとうございました",
	"ご視聴ありがとうございました",
	"チャンネル登録お願いします",
	"おやすみなさい",
	"お疲れ様でした",
	"よろしくお願いします",
	"ではまた",
}

// isWhisperHallucination returns true if the text matches a known Whisper hallucination.
func isWhisperHallucination(text string) bool {
	normalized := strings.TrimRight(text, "。、！!.… \n")
	for _, h := range whisperHallucinations {
		if normalized == h {
			return true
		}
	}
	return false
}
