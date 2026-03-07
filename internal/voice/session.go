package voice

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/disgoorg/disgo/discord"
	botgateway "github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/disgo/voice"
	"github.com/disgoorg/godave/golibdave"
	"github.com/disgoorg/snowflake/v2"
	"github.com/hraban/opus"
)

// Session manages a single Discord voice channel connection using disgo's
// voice.Conn (DAVE E2EE support) while bridging events from discordgo's gateway.
type Session struct {
	discordSession *discordgo.Session
	guildID        string
	channelID      string
	logger         *slog.Logger

	conn voice.Conn

	// onSpeech is called when a user finishes speaking (after VAD).
	onSpeech func(userID string, pcm []byte)

	// Per-user VAD instances.
	vadMu sync.Mutex
	vads  map[string]*VAD

	// Opus decoder (shared, Discord sends mono 48kHz).
	decoderMu sync.Mutex
	decoder   *opus.Decoder

	// Opus encoder for sending audio (48kHz stereo).
	encoderMu sync.Mutex
	encoder   *opus.Encoder

	// discordgo event handler cleanup functions.
	cleanupHandlers []func()
}

// NewSession creates a voice session for the given guild/channel.
func NewSession(s *discordgo.Session, guildID, channelID string, logger *slog.Logger) *Session {
	return &Session{
		discordSession: s,
		guildID:        guildID,
		channelID:      channelID,
		logger:         logger,
		vads:           make(map[string]*VAD),
	}
}

// OnSpeech sets the callback invoked when a complete speech segment is detected.
// The callback receives the speaker's Discord user ID and raw PCM audio (16-bit LE, mono, 48kHz).
func (s *Session) OnSpeech(fn func(userID string, pcm []byte)) {
	s.onSpeech = fn
}

// Join connects to the voice channel using disgo's DAVE-capable voice connection.
func (s *Session) Join(ctx context.Context) error {
	// Create Opus decoder (48kHz, mono).
	dec, err := opus.NewDecoder(48000, 1)
	if err != nil {
		return fmt.Errorf("voice: Opusデコーダ作成失敗: %w", err)
	}
	s.decoder = dec

	// Create Opus encoder (48kHz, stereo) for sending audio.
	enc, err := opus.NewEncoder(48000, 2, opus.AppVoIP)
	if err != nil {
		return fmt.Errorf("voice: Opusエンコーダ作成失敗: %w", err)
	}
	s.encoder = enc

	guildSF, err := snowflake.Parse(s.guildID)
	if err != nil {
		return fmt.Errorf("voice: guild_id パース失敗: %w", err)
	}
	channelSF, err := snowflake.Parse(s.channelID)
	if err != nil {
		return fmt.Errorf("voice: channel_id パース失敗: %w", err)
	}
	userSF, err := snowflake.Parse(s.discordSession.State.User.ID)
	if err != nil {
		return fmt.Errorf("voice: user_id パース失敗: %w", err)
	}

	// voiceStateUpdateFunc sends a voice state update via discordgo's gateway.
	voiceStateUpdateFunc := func(ctx context.Context, guildID snowflake.ID, channelID *snowflake.ID, selfMute bool, selfDeaf bool) error {
		var chID string
		if channelID != nil {
			chID = channelID.String()
		}
		s.logger.Debug("voice: sending voice state update", "guild", guildID, "channel", chID)
		return s.discordSession.ChannelVoiceJoinManual(guildID.String(), chID, selfMute, selfDeaf)
	}

	// Create the disgo voice connection with DAVE support.
	s.conn = voice.NewConn(guildSF, userSF, voiceStateUpdateFunc, func() {}, // removeFunc
		voice.WithConnLogger(s.logger),
		voice.WithConnDaveSessionCreateFunc(golibdave.NewSession),
	)

	// Register discordgo handlers to forward voice events to disgo's conn.
	removeVSU := s.discordSession.AddHandler(func(_ *discordgo.Session, vs *discordgo.VoiceStateUpdate) {
		if vs.GuildID != s.guildID {
			return
		}
		s.conn.HandleVoiceStateUpdate(convertVoiceStateUpdate(vs))
	})
	removeVSrv := s.discordSession.AddHandler(func(_ *discordgo.Session, vs *discordgo.VoiceServerUpdate) {
		if vs.GuildID != s.guildID {
			return
		}
		s.conn.HandleVoiceServerUpdate(convertVoiceServerUpdate(vs))
	})
	s.cleanupHandlers = append(s.cleanupHandlers, removeVSU, removeVSrv)

	// Open the voice connection (triggers voice state update and waits for ready).
	openCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := s.conn.Open(openCtx, channelSF, false, false); err != nil {
		s.cleanup()
		return fmt.Errorf("voice: VC参加失敗: %w", err)
	}

	// Set up opus frame receiver AFTER connection is open (UDP must be ready).
	s.conn.SetOpusFrameReceiver(&opusReceiver{session: s})

	s.logger.Info("voice: VC参加完了 (DAVE E2EE)", "guild", s.guildID, "channel", s.channelID)
	return nil
}

// Leave disconnects from the voice channel.
func (s *Session) Leave() error {
	if s.conn != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.conn.Close(ctx)
	}
	s.cleanup()
	return nil
}

func (s *Session) cleanup() {
	for _, fn := range s.cleanupHandlers {
		fn()
	}
	s.cleanupHandlers = nil
}

// SendPCM encodes PCM to Opus and sends it to the voice channel.
// PCM must be 16-bit LE, stereo (2ch), 48kHz.
func (s *Session) SendPCM(pcm []byte) error {
	if s.conn == nil {
		return fmt.Errorf("voice: VCに接続されていません")
	}

	// Set speaking flag.
	ctx := context.Background()
	if err := s.conn.SetSpeaking(ctx, voice.SpeakingFlagMicrophone); err != nil {
		s.logger.Warn("voice: speaking flag 設定失敗", "error", err)
	}

	// 20ms frame at 48kHz stereo = 960 samples * 2 channels = 1920 int16 samples = 3840 bytes.
	const frameSamples = 960
	const frameBytes = frameSamples * 2 * 2 // stereo, 16-bit

	s.encoderMu.Lock()
	defer s.encoderMu.Unlock()

	udp := s.conn.UDP()
	for offset := 0; offset+frameBytes <= len(pcm); offset += frameBytes {
		// Convert bytes to int16 slice.
		frame := make([]int16, frameSamples*2) // stereo
		for i := range frame {
			frame[i] = int16(binary.LittleEndian.Uint16(pcm[offset+i*2:]))
		}

		// Encode to Opus.
		opusBuf := make([]byte, 1000)
		n, err := s.encoder.Encode(frame, opusBuf)
		if err != nil {
			return fmt.Errorf("voice: Opusエンコード失敗: %w", err)
		}

		// Send via disgo's UDP connection.
		if _, err := udp.Write(opusBuf[:n]); err != nil {
			return fmt.Errorf("voice: Opus送信失敗: %w", err)
		}

		// Pace at 20ms per frame to avoid flooding.
		time.Sleep(20 * time.Millisecond)
	}

	// Clear speaking flag.
	_ = s.conn.SetSpeaking(ctx, 0)
	return nil
}

// IsConnected returns true if currently connected to a voice channel.
func (s *Session) IsConnected() bool {
	return s.conn != nil && s.conn.ChannelID() != nil
}

// opusReceiver implements voice.OpusFrameReceiver to handle incoming audio.
type opusReceiver struct {
	session *Session
}

func (r *opusReceiver) ReceiveOpusFrame(userID snowflake.ID, packet *voice.Packet) error {
	if len(packet.Opus) == 0 {
		return nil
	}

	uid := userID.String()

	// Decode Opus to PCM (mono, 48kHz).
	pcmBuf := make([]int16, 960)
	r.session.decoderMu.Lock()
	n, err := r.session.decoder.Decode(packet.Opus, pcmBuf)
	r.session.decoderMu.Unlock()
	if err != nil {
		r.session.logger.Debug("voice: Opusデコード失敗", "error", err)
		return nil
	}
	pcmBuf = pcmBuf[:n]

	// Convert int16 to bytes (16-bit LE).
	mono := int16ToBytes(pcmBuf)

	// Feed to per-user VAD.
	r.session.vadMu.Lock()
	vad, exists := r.session.vads[uid]
	if !exists {
		vad = NewVAD()
		r.session.vads[uid] = vad
	}
	r.session.vadMu.Unlock()

	result := vad.Process(mono, time.Now())
	if result.SpeechEnded && r.session.onSpeech != nil {
		r.session.logger.Info("voice: 発話検出", "user_id", uid, "audio_bytes", len(result.Audio))
		r.session.onSpeech(uid, result.Audio)
	}
	return nil
}

func (r *opusReceiver) CleanupUser(userID snowflake.ID) {
	uid := userID.String()
	r.session.vadMu.Lock()
	delete(r.session.vads, uid)
	r.session.vadMu.Unlock()
}

func (r *opusReceiver) Close() {}

// convertVoiceStateUpdate converts a discordgo VoiceStateUpdate to disgo's gateway event.
func convertVoiceStateUpdate(vs *discordgo.VoiceStateUpdate) botgateway.EventVoiceStateUpdate {
	evt := botgateway.EventVoiceStateUpdate{
		VoiceState: discord.VoiceState{
			GuildID:   mustParseSnowflake(vs.GuildID),
			UserID:    mustParseSnowflake(vs.UserID),
			SessionID: vs.SessionID,
			GuildDeaf: vs.Deaf,
			GuildMute: vs.Mute,
			SelfDeaf:  vs.SelfDeaf,
			SelfMute:  vs.SelfMute,
			SelfVideo: vs.SelfVideo,
			Suppress:  vs.Suppress,
		},
	}
	if vs.ChannelID != "" {
		chID := mustParseSnowflake(vs.ChannelID)
		evt.VoiceState.ChannelID = &chID
	}
	return evt
}

// convertVoiceServerUpdate converts a discordgo VoiceServerUpdate to disgo's gateway event.
func convertVoiceServerUpdate(vs *discordgo.VoiceServerUpdate) botgateway.EventVoiceServerUpdate {
	evt := botgateway.EventVoiceServerUpdate{
		Token:   vs.Token,
		GuildID: mustParseSnowflake(vs.GuildID),
	}
	if vs.Endpoint != "" {
		evt.Endpoint = &vs.Endpoint
	}
	return evt
}

func mustParseSnowflake(id string) snowflake.ID {
	n, _ := strconv.ParseUint(id, 10, 64)
	return snowflake.ID(n)
}

// int16ToBytes converts an int16 slice to little-endian bytes.
func int16ToBytes(samples []int16) []byte {
	buf := make([]byte, len(samples)*2)
	for i, s := range samples {
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(s))
	}
	return buf
}
